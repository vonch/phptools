package desktop

import (
	"context"
	"crypto/rand"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/zalando/go-keyring"
	"phptools/internal/model"
)

func testApp(t *testing.T) *App {
	t.Helper()
	a, err := New(filepath.Join(t.TempDir(), "settings"), Hooks{})
	if err != nil {
		t.Fatal(err)
	}
	if err = a.SetRoot(filepath.Join(t.TempDir(), "独立 environment")); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		a.wg.Wait()
		if a.db != nil {
			a.db.Close()
		}
	})
	return a
}

func TestRootPersistsWithoutAdoptingPhpStudy(t *testing.T) {
	a := testApp(t)
	configDir := a.configDir
	root := a.data.Settings.Root
	a.db.Close()
	a.db = nil
	b, err := New(configDir, Hooks{})
	if err != nil {
		t.Fatal(err)
	}
	defer b.db.Close()
	if b.data.Settings.Root != root {
		t.Fatal("root was not persisted")
	}
	old := filepath.Join(t.TempDir(), "phpstudy")
	os.MkdirAll(filepath.Join(old, "COM"), 0700)
	os.WriteFile(filepath.Join(old, "COM", "phpstudy_pro.exe"), nil, 0600)
	c, err := New(t.TempDir(), Hooks{})
	if err != nil {
		t.Fatal(err)
	}
	if err = c.SetRoot(filepath.Join(old, "WWW", "runtime")); err == nil {
		t.Fatal("adopted phpStudy directory")
	}
}

func TestTaskCancelReportsAndRetainsHistory(t *testing.T) {
	a := testApp(t)
	started := make(chan struct{})
	id, err := a.task("test", "target", func(ctx context.Context, id string) error { close(started); <-ctx.Done(); return ctx.Err() })
	if err != nil {
		t.Fatal(err)
	}
	<-started
	if _, err = a.task("test", "target", func(context.Context, string) error { return nil }); err == nil {
		t.Fatal("duplicate task accepted")
	}
	if err = a.CancelTask(id); err != nil {
		t.Fatal(err)
	}
	a.wg.Wait()
	s, err := a.GetState()
	if err != nil {
		t.Fatal(err)
	}
	if len(s.Tasks) != 1 || s.Tasks[0].State != "cancelled" {
		t.Fatalf("%+v", s.Tasks)
	}
}

func TestCredentialsNeverEnterMetadataAndRemovedInstanceRetainsData(t *testing.T) {
	a := testApp(t)
	root := a.data.Settings.Root
	p := model.Package{ID: "mysql-test", Kind: "mysql", Version: "5.7.44", Path: filepath.Join(root, "packages", "mysql-test")}
	os.MkdirAll(filepath.Join(p.Path, "bin"), 0700)
	os.WriteFile(filepath.Join(p.Path, "bin", "mysqld.exe"), nil, 0600)
	a.data.Packages = append(a.data.Packages, p)
	password := "Test-" + rand.Text()
	input := model.InstanceInput{Name: "测试 MySQL", PackageID: p.ID, Port: 23379, Password: password}
	i, err := a.CreateInstance(input)
	if err != nil {
		t.Fatal(err)
	}
	defer keyring.Delete(a.credentialService(), i.ID)
	secret, err := a.getSecret(i.ID)
	if err != nil || secret != password {
		t.Fatalf("credential roundtrip failed: %v", err)
	}
	for _, name := range []string{"metadata.db", "metadata.db-wal"} {
		raw, err := os.ReadFile(filepath.Join(root, name))
		if err == nil && strings.Contains(string(raw), password) {
			t.Fatal("plaintext password in metadata")
		}
	}
	os.WriteFile(filepath.Join(i.DataDir, "retained.txt"), []byte("keep"), 0600)
	if err = a.UninstallPackage(p.ID); err == nil {
		t.Fatal("uninstalled referenced package")
	}
	if err = a.DeleteInstance(i.ID); err != nil {
		t.Fatal(err)
	}
	if _, err = os.Stat(filepath.Join(i.DataDir, "retained.txt")); err != nil {
		t.Fatal("instance data deleted")
	}
	if _, err = a.getSecret(i.ID); err != nil {
		t.Fatal("retained database credential deleted")
	}
}

func TestQuitCancelsJobsBeforeClosingDatabase(t *testing.T) {
	a := testApp(t)
	exited := false
	a.hooks.Exit = func() { exited = true }
	_, err := a.task("test", "quit", func(ctx context.Context, _ string) error {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(time.Second):
			return errors.New("did not cancel")
		}
	})
	if err != nil {
		t.Fatal(err)
	}
	if err = a.Quit(); err != nil {
		t.Fatal(err)
	}
	a.db = nil
	if !exited {
		t.Fatal("exit hook was not called")
	}
	if _, err = a.task("test", "after-quit", func(context.Context, string) error { return nil }); err == nil {
		t.Fatal("accepted task after shutdown")
	}
}

func TestCatalogRefreshDoesNotResurrectConcurrentUninstall(t *testing.T) {
	old := model.Package{ID: "nginx-1.31.7-windows", Arch: "windows", Path: `D:\old`, InstalledAt: "previous"}
	removed := old
	removed.Path = ""
	removed.InstalledAt = ""
	got := mergeInstalledCatalog([]model.Package{old}, []model.Package{removed})
	if len(got) != 1 || got[0].Path != "" || got[0].InstalledAt != "" {
		t.Fatal("refresh resurrected uninstalled package")
	}
	installed := old
	installed.Arch = "x86"
	got = mergeInstalledCatalog([]model.Package{old}, []model.Package{installed})
	if got[0].Arch != "x86" {
		t.Fatal("refresh discarded actual PE architecture")
	}
}

func TestRootSettingsFailureCanRetry(t *testing.T) {
	config := t.TempDir()
	blocker := filepath.Join(config, "settings.json.tmp")
	os.Mkdir(blocker, 0700)
	a, err := New(config, Hooks{})
	if err != nil {
		t.Fatal(err)
	}
	root := filepath.Join(t.TempDir(), "retry")
	if err = a.SetRoot(root); err == nil {
		t.Fatal("expected profile persistence failure")
	}
	if a.db != nil || a.manager != nil || a.data.Settings.Root != "" {
		t.Fatal("partially initialized state survived failed settings write")
	}
	os.Remove(blocker)
	if err = a.SetRoot(root); err != nil {
		t.Fatal(err)
	}
	defer a.db.Close()
}

func TestUninstallCannotDeleteFilesWhenMetadataCommitFails(t *testing.T) {
	a := testApp(t)
	path := filepath.Join(a.data.Settings.Root, "packages", "owned")
	os.MkdirAll(path, 0700)
	exe := filepath.Join(path, "php-cgi.exe")
	os.WriteFile(exe, nil, 0600)
	p := model.Package{ID: "php-failure", Path: path}
	a.data.Packages = append(a.data.Packages, p)
	a.db.Close()
	if err := a.UninstallPackage(p.ID); err == nil {
		t.Fatal("expected closed database error")
	}
	if _, err := os.Stat(exe); err != nil {
		t.Fatal("package deleted before durable metadata update")
	}
}
