//go:build windows

package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"phptools/internal/acceptance"
	"phptools/internal/model"
	"phptools/internal/services"
	"phptools/internal/sites"
	"phptools/internal/store"
)

type validationReport struct {
	Started    string   `json:"started"`
	Completed  string   `json:"completed"`
	TestRoot   string   `json:"testRoot"`
	Cleaned    bool     `json:"cleaned"`
	Checks     []string `json:"checks"`
	PlanSHA256 string   `json:"planSHA256"`
	Error      string   `json:"error,omitempty"`
}

func validatePlan(ctx context.Context, sourceRoot, reportPath string, plan, existing []model.Site, sourceInstances []model.Instance, packages []model.Package) (runErr error) {
	temporary, err := acceptance.NewRoot("", "phpstudy-sites")
	if err != nil {
		return err
	}
	digest, err := siteDigest(plan)
	if err != nil {
		return errors.Join(err, temporary.Cleanup())
	}
	report := validationReport{Started: model.Now(), TestRoot: temporary.Path, PlanSHA256: digest}
	defer func() {
		cleanupErr := temporary.Cleanup()
		runErr = errors.Join(runErr, cleanupErr)
		_, statErr := os.Stat(temporary.Path)
		report.Cleaned = cleanupErr == nil && errors.Is(statErr, os.ErrNotExist)
		report.Completed = model.Now()
		if runErr != nil {
			report.Error = runErr.Error()
		}
		data, marshalErr := json.MarshalIndent(report, "", "  ")
		if marshalErr == nil {
			marshalErr = os.MkdirAll(filepath.Dir(reportPath), 0700)
		}
		if marshalErr == nil {
			marshalErr = os.WriteFile(reportPath, data, 0600)
		}
		runErr = errors.Join(runErr, marshalErr)
	}()
	root := temporary.Path
	db, err := store.Open(root)
	if err != nil {
		return err
	}
	defer func() { runErr = errors.Join(runErr, db.Close()) }()
	instances := map[string]model.Instance{}
	save := func(instance model.Instance) error { instances[instance.ID] = instance; return nil }
	manager := services.New(root, save, nil)
	defer func() {
		for _, kind := range []string{"nginx", "php"} {
			for _, instance := range instances {
				if instance.Kind != kind {
					continue
				}
				stopCtx, cancel := context.WithTimeout(context.Background(), 35*time.Second)
				_, stopErr := manager.Stop(stopCtx, instance)
				cancel()
				runErr = errors.Join(runErr, stopErr)
			}
		}
	}()
	selected := map[string]model.Package{}
	for _, pkg := range packages {
		if pkg.Kind != "nginx" && !(pkg.Kind == "php" && strings.HasPrefix(pkg.Version, "8.0.")) {
			continue
		}
		relative, err := filepath.Rel(filepath.Join(sourceRoot, "packages"), pkg.Path)
		if err != nil || filepath.IsAbs(relative) || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
			return errors.New("read-only package source escaped the destination package directory")
		}
		destination := filepath.Join(root, "packages", pkg.ID)
		if err := os.CopyFS(destination, os.DirFS(pkg.Path)); err != nil {
			return err
		}
		pkg.Path = destination
		selected[pkg.Kind] = pkg
	}
	if selected["nginx"].ID == "" || selected["php"].ID == "" {
		return errors.New("independent Nginx and PHP 8.0 packages are required")
	}
	port, err := availablePort()
	if err != nil {
		return err
	}
	nginxPkg := selected["nginx"]
	nginx, err := manager.Create(ctx, nginxPkg, model.InstanceInput{Name: "site migration syntax", PackageID: nginxPkg.ID, Port: port})
	if err != nil {
		return err
	}
	instances[nginx.ID] = nginx
	mainConfig, err := os.ReadFile(nginx.ConfigPath)
	if err != nil {
		return err
	}
	mainConfig, err = sourceHTTPSettings(mainConfig)
	if err != nil {
		return err
	}
	if err = os.WriteFile(nginx.ConfigPath, mainConfig, 0600); err != nil {
		return err
	}
	all := append(append([]model.Site{}, existing...), plan...)
	if _, err = sites.Apply(root, all, sourceInstances); err != nil {
		return err
	}
	if err = nginxSyntax(ctx, nginx); err != nil {
		return fmt.Errorf("full migration nginx -t: %w", err)
	}
	report.Checks = append(report.Checks, fmt.Sprintf("Nginx accepted all %d imported and existing site configurations", len(all)))
	if err := checkPathInfo(ctx, root, manager, selected["php"], nginxPkg, nginx, instances, &report); err != nil {
		return err
	}
	fmt.Printf("Isolated site checks passed; report: %s\n", reportPath)
	return nil
}

func availablePort() (int, error) {
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		return 0, err
	}
	port := listener.Addr().(*net.TCPAddr).Port
	return port, listener.Close()
}

// nginx -t checks syntax without binding production ports or controlling any
// process. Manager.ValidateConfig also checks occupied ports and is unsuitable
// while phpStudy still owns the source listeners.
func nginxSyntax(ctx context.Context, instance model.Instance) error {
	dir := filepath.Dir(filepath.Dir(instance.ConfigPath))
	command := exec.CommandContext(ctx, instance.Executable, "-p", filepath.ToSlash(dir)+"/", "-c", filepath.ToSlash(instance.ConfigPath), "-t")
	command.Dir = dir
	command.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: 0x08000000}
	output, err := command.CombinedOutput()
	if err != nil {
		return fmt.Errorf("Nginx syntax: %w: %s", err, output)
	}
	return nil
}

func sourceHTTPSettings(data []byte) ([]byte, error) {
	const marker = "  # Imported phpStudy request limits and hostname capacity\n"
	const settings = marker + "  client_max_body_size 50m;\n  client_body_buffer_size 60k;\n  client_body_timeout 60;\n  client_header_buffer_size 64k;\n  client_header_timeout 60;\n  large_client_header_buffers 4 64k;\n  send_timeout 60;\n  server_names_hash_bucket_size 256;\n"
	text := strings.ReplaceAll(string(data), "\r\n", "\n")
	if strings.Count(text, settings) == 1 {
		return []byte(text), nil
	}
	if strings.Count(text, "http {\n") != 1 || strings.Contains(text, marker) {
		return nil, errors.New("unexpected destination http block; review main configuration")
	}
	for _, key := range []string{"client_max_body_size", "client_body_buffer_size", "client_body_timeout", "client_header_buffer_size", "client_header_timeout", "large_client_header_buffers", "send_timeout", "server_names_hash_bucket_size"} {
		if strings.Contains(text, key) {
			return nil, fmt.Errorf("destination already specifies %s; review it before migration", key)
		}
	}
	return []byte(strings.Replace(text, "http {\n", "http {\n"+settings, 1)), nil
}
