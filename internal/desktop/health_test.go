package desktop

import (
	"bytes"
	"context"
	"errors"
	"net"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"phptools/internal/model"
	"phptools/internal/sites"
	"phptools/internal/store"
)

// These component files are intentionally not executable. Lightweight checks
// must work without launching them, and unsafe identity checks must stop first.
func desktopHealthFixture(t *testing.T, a *App, kind string) (model.Package, model.Instance) {
	t.Helper()
	pkg := model.Package{ID: kind + "-health-fixture", Kind: kind, Version: "8.4.8", Path: filepath.Join(a.data.Settings.Root, "packages", kind+"-health-fixture")}
	name := map[string]string{"php": "php-cgi.exe", "nginx": "nginx.exe"}[kind]
	if name == "" {
		t.Fatalf("unsupported health fixture kind %q", kind)
	}
	if err := os.MkdirAll(pkg.Path, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(pkg.Path, name), []byte("health fixture: never execute"), 0600); err != nil {
		t.Fatal(err)
	}
	if kind == "nginx" {
		pkg.Version = "1.31.7"
		if err := os.MkdirAll(filepath.Join(pkg.Path, "conf"), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(pkg.Path, "conf", "fastcgi_params"), []byte("fastcgi_param REQUEST_METHOD $request_method;\n"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	if err := a.db.Put("package", pkg.ID, pkg); err != nil {
		t.Fatal(err)
	}
	a.data.Packages = append(a.data.Packages, pkg)
	i, err := a.manager.Create(context.Background(), pkg, model.InstanceInput{Name: kind + " health fixture", PackageID: pkg.ID, Port: 19991})
	if err != nil {
		t.Fatal(err)
	}
	if err := a.saveInstance(i); err != nil {
		t.Fatal(err)
	}
	return pkg, i
}

func desktopHealthItem(t *testing.T, items []model.HealthItem, id string) model.HealthItem {
	t.Helper()
	for _, item := range items {
		if item.ID == id {
			return item
		}
	}
	t.Fatalf("health item %q not found in %+v", id, items)
	return model.HealthItem{}
}

func TestStartupHealthRepairsRuntimeDirectoriesAndPersistsStoppedState(t *testing.T) {
	a := testApp(t)
	_, i := desktopHealthFixture(t, a, "nginx")
	i.State, i.Error = "error", "stale failure"
	if err := a.saveInstance(i); err != nil {
		t.Fatal(err)
	}
	configBefore, err := os.ReadFile(i.ConfigPath)
	if err != nil {
		t.Fatal(err)
	}
	runtimeDirs := []string{filepath.Dir(i.LogPath), filepath.Join(filepath.Dir(filepath.Dir(i.ConfigPath)), "temp")}
	for _, dir := range runtimeDirs {
		if err := os.Remove(dir); err != nil {
			t.Fatal(err)
		}
	}
	if err := a.db.Close(); err != nil {
		t.Fatal(err)
	}
	a.db = nil
	b, err := New(a.configDir, Hooks{})
	if err != nil {
		t.Fatal(err)
	}
	defer b.db.Close()
	report := b.data.HealthReport
	if report == nil || report.Trigger != "startup" || report.Full || report.CompletedAt == "" {
		t.Fatalf("missing completed lightweight startup report: %+v", report)
	}
	for _, code := range []string{"process", "logs", "temp"} {
		if item := desktopHealthItem(t, report.Items, i.ID+":"+code); item.Status != "repaired" {
			t.Fatalf("expected a reported %s repair, got %+v", code, item)
		}
	}
	for _, dir := range runtimeDirs {
		if info, err := os.Stat(dir); err != nil || !info.IsDir() {
			t.Fatalf("runtime directory was not restored: %s %v", dir, err)
		}
	}
	stored, err := store.List[model.Instance](b.db, "instance")
	if err != nil || len(stored) != 1 || stored[0].State != "stopped" || stored[0].Error != "" || stored[0].PID != 0 {
		t.Fatalf("repaired state was not persisted without a service start: %+v %v", stored, err)
	}
	configAfter, err := os.ReadFile(i.ConfigPath)
	if err != nil || !bytes.Equal(configBefore, configAfter) {
		t.Fatalf("startup rewrote configuration: %v", err)
	}
	if _, err := os.Stat(filepath.Join(filepath.Dir(filepath.Dir(i.ConfigPath)), "process.json")); !os.IsNotExist(err) {
		t.Fatalf("startup created a service process record: %v", err)
	}
	if err := b.checkEnvironment(context.Background(), "manual", false, ""); err != nil {
		t.Fatal(err)
	}
	for _, item := range b.data.HealthReport.Items {
		if item.Status == "repaired" {
			t.Fatalf("repeat check repeated a repair: %+v", item)
		}
	}
}

func TestEnvironmentCheckQueueDeduplicatesCancelsAndCanRunAgain(t *testing.T) {
	a := testApp(t)
	var cancelledID string
	func() {
		a.opMu.Lock()
		defer a.opMu.Unlock()
		var err error
		cancelledID, err = a.CheckEnvironment()
		if err != nil || cancelledID == "" {
			t.Fatalf("could not queue health check: %q %v", cancelledID, err)
		}
		if _, err := a.CheckEnvironment(); err == nil {
			t.Fatal("accepted a duplicate queued health check")
		}
		if err := a.CancelTask(cancelledID); err != nil {
			t.Fatal(err)
		}
	}()
	a.wg.Wait()
	if task := a.data.Tasks[0]; task.ID != cancelledID || task.State != "cancelled" || task.Progress == 100 {
		t.Fatalf("cancelled check was presented as completed: %+v", task)
	}
	report := a.data.HealthReport
	if report == nil || report.Trigger != "manual" || !report.Full {
		t.Fatalf("queued check did not publish its report: %+v", report)
	}
	if item := desktopHealthItem(t, report.Items, "environment:cancelled"); item.Status != "warning" || !strings.Contains(item.Message, "未完成") {
		t.Fatalf("cancelled report lost its incomplete warning: %+v", item)
	}
	completedID, err := a.CheckEnvironment()
	if err != nil || completedID == cancelledID {
		t.Fatalf("could not start a fresh check after cancellation: %q %v", completedID, err)
	}
	a.wg.Wait()
	if task := a.data.Tasks[0]; task.ID != completedID || task.State != "completed" || task.Progress != 100 {
		t.Fatalf("full check did not finish through the task queue: %+v", task)
	}
	report = a.data.HealthReport
	if report == nil || !report.Full || report.Trigger != "manual" || report.StartedAt == "" || report.CompletedAt == "" {
		t.Fatalf("completed manual report is incomplete: %+v", report)
	}
	for _, item := range report.Items {
		if item.ID == "environment:cancelled" {
			t.Fatal("fresh report retained an old cancellation warning")
		}
	}
}

func TestEnvironmentCheckCancellationKeepsCompletedRepairsAndSkipsRemainingOnes(t *testing.T) {
	a := testApp(t)
	_, first := desktopHealthFixture(t, a, "php")
	_, second := desktopHealthFixture(t, a, "nginx")
	for _, i := range []model.Instance{first, second} {
		if err := os.Remove(filepath.Dir(i.LogPath)); err != nil {
			t.Fatal(err)
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	progressUpdates := 0
	a.hooks.Notify = func() {
		progressUpdates++
		if progressUpdates == 2 {
			cancel()
		}
	}
	a.opMu.Lock()
	err := a.checkEnvironment(ctx, "manual", false, "cancel-fixture")
	a.opMu.Unlock()
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled check returned %v", err)
	}
	report := a.data.HealthReport
	if item := desktopHealthItem(t, report.Items, first.ID+":logs"); item.Status != "repaired" {
		t.Fatalf("completed repair was lost: %+v", item)
	}
	if _, err := os.Stat(filepath.Dir(first.LogPath)); err != nil {
		t.Fatalf("completed repair was undone on cancellation: %v", err)
	}
	if _, err := os.Stat(filepath.Dir(second.LogPath)); !os.IsNotExist(err) {
		t.Fatalf("remaining directory was repaired after cancellation: %v", err)
	}
	if item := desktopHealthItem(t, report.Items, "environment:cancelled"); item.Status != "warning" {
		t.Fatalf("partial report was presented as complete: %+v", item)
	}
}

func TestSiteHealthReportsProblemsWithoutCreatingOrOverwritingConfiguration(t *testing.T) {
	runtimeRoot := t.TempDir()
	if err := os.Mkdir(filepath.Join(runtimeRoot, "conf"), 0700); err != nil {
		t.Fatal(err)
	}
	projectRoot := t.TempDir()
	base := model.Site{ID: "site-health", Name: "Health site", Domain: "health.test", Root: projectRoot, Port: 8085, Enabled: true}
	for _, test := range []struct {
		name, code, status string
		modify             func(*model.Site)
		content            []byte
	}{
		{name: "missing-project", code: "directory", status: "error", modify: func(s *model.Site) { s.Root = filepath.Join(projectRoot, "missing") }},
		{name: "missing-php-reference", code: "directory", status: "error", modify: func(s *model.Site) { s.PHPInstanceID = "missing-php" }},
		{name: "missing-config", code: "config", status: "error"},
		{name: "hand-edited-config", code: "config", status: "warning", content: []byte("# Generated by PHPTools: site-health\n# keep my manual edits\n")},
		{name: "matching-config", code: "config", status: "ok", content: []byte(sites.Render(runtimeRoot, base, nil))},
	} {
		t.Run(test.name, func(t *testing.T) {
			site := base
			if test.modify != nil {
				test.modify(&site)
			}
			path := filepath.Join(runtimeRoot, "conf", site.ID+".conf")
			if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
				t.Fatal(err)
			}
			if test.content != nil {
				if err := os.WriteFile(path, test.content, 0600); err != nil {
					t.Fatal(err)
				}
			}
			items := checkSiteHealth(runtimeRoot, site, []model.Site{site}, nil, nil)
			if item := desktopHealthItem(t, items, "site:"+site.ID+":"+test.code); item.Status != test.status {
				t.Fatalf("unexpected site check result: %+v", item)
			}
			after, err := os.ReadFile(path)
			if test.content == nil {
				if !os.IsNotExist(err) {
					t.Fatalf("site check created a configuration: %v", err)
				}
			} else if err != nil || !bytes.Equal(after, test.content) {
				t.Fatalf("site check overwrote existing content: %q %v", after, err)
			}
		})
	}
}

func TestEnvironmentCheckRefusesUnrecordedLivePIDWithoutChangingInstance(t *testing.T) {
	a := testApp(t)
	_, i := desktopHealthFixture(t, a, "php")
	i.PID, i.State = os.Getpid(), "running"
	if err := a.saveInstance(i); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Dir(i.LogPath)); err != nil {
		t.Fatal(err)
	}
	configBefore, err := os.ReadFile(i.ConfigPath)
	if err != nil {
		t.Fatal(err)
	}
	a.opMu.Lock()
	err = a.checkEnvironment(context.Background(), "manual", true, "")
	a.opMu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	report := a.data.HealthReport
	if item := desktopHealthItem(t, report.Items, i.ID+":process"); item.Status != "error" {
		t.Fatalf("untrusted live PID was accepted: %+v", item)
	}
	stored, err := store.List[model.Instance](a.db, "instance")
	if err != nil || len(stored) != 1 || !reflect.DeepEqual(stored[0], i) {
		t.Fatalf("untrusted live PID metadata was rewritten: %+v %v", stored, err)
	}
	if _, err := os.Stat(filepath.Dir(i.LogPath)); !os.IsNotExist(err) {
		t.Fatalf("directory was repaired despite ambiguous process ownership: %v", err)
	}
	configAfter, err := os.ReadFile(i.ConfigPath)
	if err != nil || !bytes.Equal(configBefore, configAfter) {
		t.Fatalf("untrusted process check overwrote configuration: %v", err)
	}
	for _, item := range report.Items {
		if item.ID == i.ID+":config-test" || item.Status == "repaired" {
			t.Fatalf("unsafe check continued after ownership failure: %+v", item)
		}
	}
}

func TestFailedStartAutomaticallyReportsHealthAndKeepsPortOwner(t *testing.T) {
	a := testApp(t)
	_, i := desktopHealthFixture(t, a, "php")
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	i.Port = listener.Addr().(*net.TCPAddr).Port
	if err := a.saveInstance(i); err != nil {
		t.Fatal(err)
	}
	id, err := a.StartInstance(i.ID)
	if err != nil {
		t.Fatal(err)
	}
	a.wg.Wait()
	if task := a.data.Tasks[0]; task.ID != id || task.State != "failed" || !strings.Contains(task.Error, "端口") {
		t.Fatalf("failed start did not preserve its original error: %+v", task)
	}
	if report := a.data.HealthReport; report == nil || report.Trigger != "operation" || report.Full || report.CompletedAt == "" {
		t.Fatalf("service failure did not publish a lightweight report: %+v", report)
	}
	stored, err := a.instanceByID(i.ID)
	if err != nil || !reflect.DeepEqual(stored, i) {
		t.Fatalf("failed start and check changed the stopped instance: %+v %v", stored, err)
	}
	conn, err := net.Dial("tcp4", listener.Addr().String())
	if err != nil {
		t.Fatalf("port owner was disturbed: %v", err)
	}
	conn.Close()
	if _, err := os.Stat(filepath.Join(filepath.Dir(filepath.Dir(i.ConfigPath)), "process.json")); !os.IsNotExist(err) {
		t.Fatalf("failed start or automatic check created a process record: %v", err)
	}
}
