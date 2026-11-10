//go:build windows

// Command rewritecheck tests real Nginx/PHP routing with small entry fixtures.
// It does not install or claim to test complete PHP frameworks.
package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"net"
	"os"
	"os/signal"
	"path/filepath"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"time"

	"phptools/internal/acceptance"
	"phptools/internal/model"
	"phptools/internal/services"
	"phptools/internal/sites"
	"phptools/internal/store"
)

type result struct {
	Name    string `json:"name"`
	Details string `json:"details"`
	Passed  bool   `json:"passed"`
}

type report struct {
	Started                string                `json:"started"`
	Completed              string                `json:"completed"`
	Root                   string                `json:"root"`
	SourceRoot             string                `json:"sourceRoot"`
	Scope                  string                `json:"scope"`
	Notes                  []string              `json:"notes,omitempty"`
	Checks                 []result              `json:"checks"`
	Presets                []model.RewritePreset `json:"presets"`
	ProductionBefore       []processInfo         `json:"productionBefore"`
	ProductionAfter        []processInfo         `json:"productionAfter"`
	TestProcessesRemaining []processInfo         `json:"testProcessesRemaining"`
	Error                  string                `json:"error,omitempty"`
}

func main() {
	root := flag.String("root", "", "new isolated runtime; defaults to a system temporary directory and is removed after the run")
	sourceRoot := flag.String("source-root", "", "required read-only source of installed PHP 8.0 and Nginx packages")
	reportPath := flag.String("report", `build\qa\rewrite-acceptance.json`, "JSON report output path")
	flag.Parse()
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt)
	defer cancel()
	if err := run(ctx, *root, *sourceRoot, *reportPath); err != nil {
		fmt.Fprintln(os.Stderr, "REWRITE ACCEPTANCE FAILED:", err)
		os.Exit(1)
	}
}

func run(ctx context.Context, root, sourceRoot, reportPath string) (runErr error) {
	if strings.TrimSpace(sourceRoot) == "" {
		return errors.New("-source-root is required; select an existing PHPTools runtime as a read-only package source")
	}
	sourceRoot, err := filepath.Abs(sourceRoot)
	if err != nil {
		return err
	}
	reportPath, err = filepath.Abs(reportPath)
	if err != nil {
		return err
	}
	if root != "" {
		root, err = filepath.Abs(root)
		if err != nil {
			return err
		}
		if within(root, sourceRoot) || within(sourceRoot, root) || within(`E:\PHPTools`, root) || within(root, `E:\PHPTools`) {
			return errors.New("rewrite test, source, and E:\\PHPTools production roots must be separate")
		}
	}
	if within(sourceRoot, reportPath) {
		return errors.New("rewrite report must be outside the read-only source runtime")
	}
	testRoot, err := acceptance.NewRoot(root, "rewrite")
	if err != nil {
		return err
	}
	root = testRoot.Path
	if within(root, sourceRoot) || within(sourceRoot, root) || within(`E:\PHPTools`, root) || within(root, `E:\PHPTools`) {
		return errors.Join(errors.New("rewrite test, source, and E:\\PHPTools production roots must be separate"), testRoot.Cleanup())
	}
	if within(root, reportPath) {
		return errors.Join(errors.New("rewrite report must be outside the temporary runtime"), testRoot.Cleanup())
	}
	presets := sites.RewritePresets()
	r := report{Started: model.Now(), Root: root, SourceRoot: sourceRoot, Scope: "Real Nginx + PHP 8.0 HTTP checks using lightweight index.php fixtures; no full framework installations, no hosts edits, no production service operations.", Checks: []result{}, Presets: presets}
	r.Notes = []string{"HTTP fixtures use short rN.test hosts. The current services template does not set server_names_hash_bucket_size; this test does not modify that setting or verify long hostname capacity."}
	record := func(name, details string) {
		r.Checks = append(r.Checks, result{Name: name, Details: details, Passed: true})
		fmt.Println("PASS", name, details)
	}
	defer func() {
		r.Completed = model.Now()
		if runErr != nil {
			r.Error = runErr.Error()
		}
		b, err := json.MarshalIndent(r, "", "  ")
		if err == nil {
			err = os.MkdirAll(filepath.Dir(reportPath), 0700)
		}
		if err == nil {
			err = os.WriteFile(reportPath, b, 0600)
		}
		runErr = errors.Join(runErr, err)
	}()
	defer func() { runErr = errors.Join(runErr, testRoot.Cleanup()) }()
	db, err := store.Open(root)
	if err != nil {
		return err
	}
	defer func() { runErr = errors.Join(runErr, db.Close()) }()
	if len(presets) == 0 {
		return errors.New("rewrite preset catalog is empty")
	}
	hostsBefore, err := hostsDigest()
	if err != nil {
		return err
	}
	r.ProductionBefore, err = snapshot(ctx, `E:\PHPTools`)
	if err != nil {
		return err
	}
	defer func() {
		verifyCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		remaining, err := snapshot(verifyCtx, root)
		r.TestProcessesRemaining = remaining
		if err != nil {
			runErr = errors.Join(runErr, err)
		} else if len(remaining) != 0 {
			runErr = errors.Join(runErr, fmt.Errorf("%d rewrite test processes remain", len(remaining)))
		} else {
			record("process cleanup", "zero executables remain beneath the isolated rewrite root")
		}
		r.ProductionAfter, err = snapshot(verifyCtx, `E:\PHPTools`)
		if err != nil {
			runErr = errors.Join(runErr, err)
		} else if !reflect.DeepEqual(r.ProductionBefore, r.ProductionAfter) {
			runErr = errors.Join(runErr, errors.New("E:\\PHPTools process snapshot changed during acceptance; inspect concurrent production activity"))
		} else {
			record("production isolation", fmt.Sprintf("E:\\PHPTools process identities unchanged (%d processes)", len(r.ProductionAfter)))
		}
		after, err := hostsDigest()
		if err != nil {
			runErr = errors.Join(runErr, err)
		} else if after != hostsBefore {
			runErr = errors.Join(runErr, errors.New("hosts content changed during acceptance"))
		} else {
			record("hosts isolation", "system hosts SHA256 unchanged: "+after)
		}
	}()
	selected, err := sourcePackages(sourceRoot)
	if err != nil {
		return err
	}
	for n, pkg := range selected {
		copied, count, err := copyPackage(root, pkg)
		if err != nil {
			return err
		}
		selected[n] = copied
		if err = db.Put("package", copied.ID, copied); err != nil {
			return err
		}
		record("copy "+pkg.ID, fmt.Sprintf("%d verified files copied into %s", count, copied.Path))
	}
	instances := map[string]model.Instance{}
	save := func(i model.Instance) error { instances[i.ID] = i; return db.Put("instance", i.ID, i) }
	m := services.New(root, save, nil)
	defer func() {
		owned := make([]model.Instance, 0, len(instances))
		for _, i := range instances {
			owned = append(owned, i)
		}
		sort.Slice(owned, func(a, b int) bool { return owned[a].Kind == "nginx" && owned[b].Kind != "nginx" })
		for _, i := range owned {
			stopCtx, cancel := context.WithTimeout(context.Background(), 40*time.Second)
			next, err := m.Stop(stopCtx, i)
			cancel()
			if err != nil {
				runErr = errors.Join(runErr, err)
				continue
			}
			current := m.Inspect(next)
			if current.PID != 0 || current.State != "stopped" {
				runErr = errors.Join(runErr, fmt.Errorf("%s not stopped: %+v", i.Name, current))
				continue
			}
			record("stop "+i.Kind, "graceful/owned stop completed; persisted PID=0; Inspect=stopped")
		}
	}()
	var php, nginx model.Instance
	var nginxPkg model.Package
	port := 24080
	for _, pkg := range selected {
		port, err = freePort(port)
		if err != nil {
			return err
		}
		i, err := m.Create(ctx, pkg, model.InstanceInput{Name: "rewrite acceptance " + pkg.Kind, PackageID: pkg.ID, Port: port})
		port++
		if err != nil {
			return err
		}
		if err = save(i); err != nil {
			return err
		}
		if pkg.Kind == "php" {
			php, err = m.Start(ctx, pkg, i)
			instances[php.ID] = php
			if err != nil {
				return err
			}
			record("start PHP", pkg.Version+" on 127.0.0.1:"+strconv.Itoa(php.Port))
		} else {
			nginx = i
			nginxPkg = pkg
		}
	}
	if php.ID == "" || nginx.ID == "" {
		return errors.New("PHP and Nginx instances are required")
	}
	var allSites []model.Site
	for ordinal, preset := range presets {
		s, err := fixtureSite(root, preset, php, nginx.Port, ordinal)
		if err != nil {
			return err
		}
		s, err = sites.Validate(s, allSites, []model.Instance{php, nginx})
		if err != nil {
			return err
		}
		allSites = append(allSites, s)
		rollback, err := sites.Apply(root, allSites, []model.Instance{php})
		if err != nil {
			return err
		}
		if err = m.ValidateConfig(ctx, nginxPkg, nginx); err != nil {
			_ = rollback()
			return fmt.Errorf("nginx -t preset %s: %w", preset.ID, err)
		}
		record("nginx -t "+preset.ID, "real nginx.exe accepted generated site configuration")
		if err = db.Put("site", s.ID, s); err != nil {
			return err
		}
	}
	nginx, err = m.Start(ctx, nginxPkg, nginx)
	instances[nginx.ID] = nginx
	if err != nil {
		return err
	}
	record("start Nginx", nginxPkg.Version+" on 127.0.0.1:"+strconv.Itoa(nginx.Port))
	for n, preset := range presets {
		if err = checkHTTP(ctx, preset, allSites[n], record); err != nil {
			return err
		}
	}
	return nil
}

func within(root, path string) bool {
	relative, err := filepath.Rel(filepath.Clean(root), filepath.Clean(path))
	return err == nil && relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator)) && !filepath.IsAbs(relative)
}

func hostsDigest() (string, error) {
	b, err := os.ReadFile(filepath.Join(os.Getenv("SystemRoot"), "System32", "drivers", "etc", "hosts"))
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:]), nil
}

func freePort(start int) (int, error) {
	for port := start; port <= 65000; port++ {
		listener, err := net.Listen("tcp4", net.JoinHostPort("127.0.0.1", strconv.Itoa(port)))
		if err == nil {
			_ = listener.Close()
			return port, nil
		}
	}
	return 0, errors.New("no free local test port")
}
