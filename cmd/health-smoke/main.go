//go:build windows

// Command health-smoke checks environment repair with a fresh official Nginx
// runtime. It never opens the user's profile or saves a site/hosts entry.
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"phptools/internal/acceptance"
	"phptools/internal/desktop"
	"phptools/internal/model"
	"phptools/internal/services"
	"phptools/internal/store"
)

type check struct {
	Name    string `json:"name"`
	Details string `json:"details"`
}

type report struct {
	Started   string                         `json:"started"`
	Completed string                         `json:"completed"`
	Root      string                         `json:"root"`
	Checks    []check                        `json:"checks"`
	Reports   map[string]*model.HealthReport `json:"reports"`
	Error     string                         `json:"error,omitempty"`
}

func main() {
	root := flag.String("root", "", "new disposable runtime (default: system temporary directory); must not already exist")
	source := flag.String("source", "", "required read-only source directory containing an installed official Nginx package")
	flag.Parse()
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	if err := run(ctx, *root, *source); err != nil {
		fmt.Fprintln(os.Stderr, "HEALTH ACCEPTANCE FAILED:", err)
		os.Exit(1)
	}
}

func run(parent context.Context, baseRoot, source string) (runErr error) {
	if !filepath.IsAbs(source) {
		return errors.New("-source must explicitly name an absolute read-only official Nginx package directory")
	}
	source, err := filepath.EvalSymlinks(filepath.Clean(source))
	if err != nil {
		return fmt.Errorf("resolve read-only source: %w", err)
	}
	reportPath, err := filepath.Abs(filepath.Join("build", "qa", "health-acceptance.json"))
	if err != nil {
		return err
	}
	temporary, err := acceptance.NewRoot(baseRoot, "health-acceptance")
	if err != nil {
		return err
	}
	root := temporary.Path
	reportAllowed := !pathWithin(root, reportPath)
	r := report{Started: model.Now(), Root: root, Checks: []check{}, Reports: map[string]*model.HealthReport{}}
	record := func(name, details string) {
		r.Checks = append(r.Checks, check{Name: name, Details: details})
		fmt.Println("PASS", name, details)
	}
	defer func() {
		if !reportAllowed {
			return
		}
		r.Completed = model.Now()
		if runErr != nil {
			r.Error = runErr.Error()
		}
		data, marshalErr := json.MarshalIndent(r, "", "  ")
		if marshalErr == nil {
			marshalErr = os.MkdirAll(filepath.Dir(reportPath), 0700)
		}
		if marshalErr == nil {
			marshalErr = os.WriteFile(reportPath, data, 0600)
		}
		runErr = errors.Join(runErr, marshalErr)
		if runErr == nil {
			fmt.Println("HEALTH ACCEPTANCE PASSED", len(r.Checks), "checks; report:", reportPath)
		}
	}()
	defer func() { runErr = errors.Join(runErr, temporary.Cleanup()) }()
	if !reportAllowed {
		return errors.New("acceptance report must be outside the disposable runtime")
	}
	if pathWithin(source, root) || pathWithin(root, source) {
		return errors.New("read-only source and disposable runtime must not overlap")
	}
	ctx, cancel := context.WithTimeout(parent, 3*time.Minute)
	defer cancel()
	if err := ctx.Err(); err != nil {
		return err
	}
	db, err := store.Open(root)
	if err != nil {
		return err
	}
	defer func() {
		if db != nil {
			runErr = errors.Join(runErr, db.Close())
		}
	}()
	pkg := model.Package{ID: "nginx-1.31.7-windows", Kind: "nginx", Version: "1.31.7", Arch: "x64", Verified: true, Path: filepath.Join(root, "packages", "nginx-1.31.7"), InstalledAt: model.Now()}
	if err := os.CopyFS(pkg.Path, os.DirFS(source)); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := db.Put("package", pkg.ID, pkg); err != nil {
		return err
	}
	record("isolated runtime", root+"; copied official Nginx from "+source)
	m := services.New(root, func(i model.Instance) error { return db.Put("instance", i.ID, i) }, func(string) (string, error) { return "", errors.New("Nginx must not request credentials") })
	port, err := freePort()
	if err != nil {
		return err
	}
	i, err := m.Create(ctx, pkg, model.InstanceInput{Name: "Health acceptance Nginx", PackageID: pkg.ID, Port: port})
	if err != nil {
		return err
	}
	managerCleanup := true
	defer func() {
		if managerCleanup {
			current := m.Inspect(i)
			if current.State != "stopped" {
				stopCtx, stopCancel := context.WithTimeout(context.Background(), 40*time.Second)
				defer stopCancel()
				_, stopErr := m.Stop(stopCtx, current)
				runErr = errors.Join(runErr, stopErr)
			}
		}
	}()
	config, err := os.ReadFile(i.ConfigPath)
	if err != nil {
		return err
	}
	config = append(config, []byte("\n# acceptance: preserve this manual configuration comment\n")...)
	if err := os.WriteFile(i.ConfigPath, config, 0600); err != nil {
		return err
	}
	i, err = m.Start(ctx, pkg, i)
	if err != nil {
		return err
	}
	stale := i
	i, err = m.Stop(ctx, i)
	if err != nil {
		return err
	}
	if i.State != "stopped" || i.PID != 0 || portOpen(port) {
		return errors.New("initial managed Nginx did not stop cleanly")
	}
	managerCleanup = false
	if err := db.Put("instance", stale.ID, stale); err != nil {
		return err
	}
	record("real stale PID fixture", fmt.Sprintf("managed PID %d stopped before restoring only stale metadata", stale.PID))
	dir := filepath.Dir(filepath.Dir(i.ConfigPath))
	for _, name := range []string{"logs", "temp"} {
		path := filepath.Join(dir, name)
		if !strings.HasPrefix(filepath.Clean(path), root+string(os.PathSeparator)) {
			return errors.New("runtime directory escaped isolated root")
		}
		if err := os.Rename(path, filepath.Join(dir, "acceptance-saved-"+name)); err != nil {
			return err
		}
	}
	if err := db.Close(); err != nil {
		return err
	}
	db = nil
	profile := filepath.Join(root, "acceptance-profile")
	if err := os.Mkdir(profile, 0700); err != nil {
		return err
	}
	settings, _ := json.Marshal(map[string]string{"root": root})
	if err := os.WriteFile(filepath.Join(profile, "settings.json"), settings, 0600); err != nil {
		return err
	}
	a, err := desktop.New(profile, desktop.Hooks{})
	if err != nil {
		return err
	}
	defer func() {
		if a != nil {
			runErr = errors.Join(runErr, a.Quit())
		}
	}()
	snapshot, err := a.GetState()
	if err != nil {
		return err
	}
	r.Reports["startup"] = snapshot.HealthReport
	if snapshot.HealthReport == nil || snapshot.HealthReport.Trigger != "startup" || snapshot.HealthReport.Full || !hasItem(snapshot.HealthReport, i.ID+":process", "repaired") {
		return errors.New("startup did not publish a lightweight stale-process repair report")
	}
	if len(snapshot.Instances) != 1 || snapshot.Instances[0].PID != 0 || snapshot.Instances[0].State != "stopped" {
		return errors.New("startup did not clear the stale stopped PID")
	}
	record("automatic startup repair", "startup report repaired the confirmed stale PID without starting a service")
	for _, name := range []string{"logs", "temp"} {
		info, statErr := os.Stat(filepath.Join(dir, name))
		if statErr != nil || !info.IsDir() || !hasItem(snapshot.HealthReport, i.ID+":"+name, "repaired") {
			return fmt.Errorf("startup did not repair missing %s directory", name)
		}
	}
	if portOpen(port) {
		return errors.New("startup repair unexpectedly started a service")
	}
	record("automatic runtime-directory repair", "missing logs/temp recreated; original directories preserved; service stayed stopped")
	if err := checkConfigPreserved(i.ConfigPath, config); err != nil {
		return err
	}
	record("manual configuration preserved on startup", i.ConfigPath)
	taskID, err := a.StartInstance(i.ID)
	if err != nil {
		return err
	}
	snapshot, err = awaitTask(ctx, a, taskID)
	if err != nil {
		return err
	}
	if len(snapshot.Instances) != 1 || snapshot.Instances[0].State != "running" {
		return errors.New("managed start task did not leave Nginx running")
	}
	activePID := snapshot.Instances[0].PID
	if err := expectHTTP(port, "/__phptools", "PHPTools is running"); err != nil {
		return err
	}
	record("desktop managed start", fmt.Sprintf("PID %d serves the managed endpoint on port %d", activePID, port))
	snapshot, err = fullCheck(ctx, a)
	if err != nil {
		return err
	}
	r.Reports["full"] = snapshot.HealthReport
	for _, code := range []string{"config-test", "port", "ready"} {
		if !hasItem(snapshot.HealthReport, i.ID+":"+code, "ok") {
			return fmt.Errorf("full manual check did not pass %s", code)
		}
	}
	record("full manual environment task", "manual/full report passed real Nginx syntax, all listening ports, and readiness")
	if err := checkConfigPreserved(i.ConfigPath, config); err != nil {
		return err
	}
	record("manual configuration preserved during full check", "configuration bytes stayed identical")
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		return err
	}
	externalPort := listener.Addr().(*net.TCPAddr).Port
	server := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = io.WriteString(w, "external acceptance listener") }), ReadHeaderTimeout: time.Second}
	go func() { _ = server.Serve(listener) }()
	defer server.Close()
	siteConf := filepath.Join(root, "sites", "acceptance-manual.conf")
	conflictConfig := []byte(fmt.Sprintf("# acceptance-owned temporary site\nserver { listen 127.0.0.1:%d; server_name acceptance.invalid; location / { return 200 'acceptance'; } }\n", externalPort))
	if err := os.WriteFile(siteConf, conflictConfig, 0600); err != nil {
		return err
	}
	defer func() {
		if err := os.Remove(siteConf); err != nil && !errors.Is(err, os.ErrNotExist) {
			runErr = errors.Join(runErr, err)
		}
	}()
	snapshot, err = fullCheck(ctx, a)
	if err != nil {
		return err
	}
	r.Reports["external-port-conflict"] = snapshot.HealthReport
	if !hasItem(snapshot.HealthReport, i.ID+":config-test", "error") {
		return errors.New("occupied included-site port did not produce a configuration/port error")
	}
	foundPort := false
	for _, item := range snapshot.HealthReport.Items {
		if item.TargetID == i.ID && item.Status == "error" && strings.Contains(item.Message, strconv.Itoa(externalPort)) {
			foundPort = true
		}
	}
	if !foundPort {
		return errors.New("port conflict report omitted the occupied port")
	}
	record("external port conflict detected", fmt.Sprintf("included manual site port %d reported with a repair suggestion", externalPort))
	if snapshot.Instances[0].PID != activePID || snapshot.Instances[0].State != "running" {
		return errors.New("health check modified or stopped the managed Nginx")
	}
	if err := expectHTTP(port, "/__phptools", "PHPTools is running"); err != nil {
		return err
	}
	if err := expectHTTP(externalPort, "/", "external acceptance listener"); err != nil {
		return err
	}
	if err := checkConfigPreserved(siteConf, conflictConfig); err != nil {
		return err
	}
	record("port conflict left both services alive", "managed PID unchanged; external listener responds; custom site configuration preserved")
	invalidConfig := []byte("# acceptance-owned invalid syntax\nserver { acceptance_unknown_directive on; }\n")
	if err := os.WriteFile(siteConf, invalidConfig, 0600); err != nil {
		return err
	}
	snapshot, err = fullCheck(ctx, a)
	if err != nil {
		return err
	}
	r.Reports["invalid-syntax"] = snapshot.HealthReport
	if !hasItem(snapshot.HealthReport, i.ID+":config-test", "error") {
		return errors.New("invalid included configuration syntax was not reported")
	}
	if err := checkConfigPreserved(siteConf, invalidConfig); err != nil {
		return err
	}
	if err := expectHTTP(port, "/__phptools", "PHPTools is running"); err != nil {
		return err
	}
	record("invalid syntax detected safely", "report explains syntax failure; invalid file and running Nginx left intact")
	if err := os.Remove(siteConf); err != nil {
		return err
	}
	snapshot, err = fullCheck(ctx, a)
	if err != nil {
		return err
	}
	r.Reports["recovery"] = snapshot.HealthReport
	if !hasItem(snapshot.HealthReport, i.ID+":config-test", "ok") || !hasItem(snapshot.HealthReport, i.ID+":ready", "ok") {
		return errors.New("full health check did not recover after removing only the acceptance fixture")
	}
	if err := checkConfigPreserved(i.ConfigPath, config); err != nil {
		return err
	}
	record("manual fixture correction rechecked", "configuration and readiness returned to OK; original custom configuration retained")
	if err := a.Quit(); err != nil {
		return err
	}
	a = nil
	if portOpen(port) {
		return errors.New("managed port still accepts connections after App.Quit")
	}
	if current := m.Inspect(snapshot.Instances[0]); current.State != "stopped" || current.PID != 0 {
		return fmt.Errorf("managed process still present after App.Quit: %+v", current)
	}
	if err := expectHTTP(externalPort, "/", "external acceptance listener"); err != nil {
		return err
	}
	if err := server.Close(); err != nil {
		return err
	}
	record("clean shutdown", "App.Quit stopped the managed process; managed port closed; independent listener was untouched")
	return nil
}

func pathWithin(parent, path string) bool {
	relative, err := filepath.Rel(parent, path)
	return err == nil && relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator)) && !filepath.IsAbs(relative)
}

func freePort() (int, error) {
	l, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		return 0, err
	}
	port := l.Addr().(*net.TCPAddr).Port
	return port, l.Close()
}

func portOpen(port int) bool {
	conn, err := net.DialTimeout("tcp4", net.JoinHostPort("127.0.0.1", strconv.Itoa(port)), 300*time.Millisecond)
	if conn != nil {
		_ = conn.Close()
	}
	return err == nil
}

func expectHTTP(port int, path, expected string) error {
	client := &http.Client{Timeout: 2 * time.Second, Transport: &http.Transport{Proxy: nil, DisableKeepAlives: true}}
	response, err := client.Get(fmt.Sprintf("http://127.0.0.1:%d%s", port, path))
	if err != nil {
		return err
	}
	defer response.Body.Close()
	data, err := io.ReadAll(io.LimitReader(response.Body, 4096))
	if err != nil {
		return err
	}
	if response.StatusCode != http.StatusOK || string(data) != expected {
		return fmt.Errorf("port %d response: HTTP %d %q", port, response.StatusCode, data)
	}
	return nil
}

func checkConfigPreserved(path string, expected []byte) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	if !bytes.Equal(data, expected) {
		return fmt.Errorf("health check modified custom configuration %s", path)
	}
	return nil
}

func hasItem(report *model.HealthReport, id, status string) bool {
	if report == nil {
		return false
	}
	for _, item := range report.Items {
		if item.ID == id && item.Status == status {
			return true
		}
	}
	return false
}

func awaitTask(ctx context.Context, a *desktop.App, id string) (model.Snapshot, error) {
	timer := time.NewTicker(100 * time.Millisecond)
	defer timer.Stop()
	for {
		snapshot, err := a.GetState()
		if err != nil {
			return snapshot, err
		}
		for _, task := range snapshot.Tasks {
			if task.ID == id {
				switch task.State {
				case "completed":
					return snapshot, nil
				case "failed", "cancelled":
					return snapshot, fmt.Errorf("task %s %s: %s", task.Action, task.State, task.Error)
				}
			}
		}
		select {
		case <-ctx.Done():
			return snapshot, ctx.Err()
		case <-timer.C:
		}
	}
}

func fullCheck(ctx context.Context, a *desktop.App) (model.Snapshot, error) {
	id, err := a.CheckEnvironment()
	if err != nil {
		return model.Snapshot{}, err
	}
	snapshot, err := awaitTask(ctx, a, id)
	if err == nil && (snapshot.HealthReport == nil || !snapshot.HealthReport.Full || snapshot.HealthReport.Trigger != "manual") {
		err = errors.New("completed environment task did not publish a full manual report")
	}
	return snapshot, err
}
