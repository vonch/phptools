package services

import (
	"context"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"phptools/internal/model"
)

func healthFixture(t *testing.T, kind string) (*Manager, model.Package, model.Instance) {
	t.Helper()
	root := t.TempDir()
	m := New(root, nil, nil)
	pkg := fixturePackage(t, root, kind, "8.4.8")
	if err := os.WriteFile(filepath.Join(root, ".phptools-runtime"), []byte("PHPTools runtime v1\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if kind == "nginx" {
		if err := os.WriteFile(filepath.Join(pkg.Path, "conf", "mime.types"), []byte("types { text/html html; }\n"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	i, err := m.Create(context.Background(), pkg, model.InstanceInput{Name: kind, PackageID: pkg.ID, Port: 29991})
	if err != nil {
		t.Fatal(err)
	}
	return m, pkg, i
}

func healthResult(t *testing.T, items []model.HealthItem, code string) model.HealthItem {
	t.Helper()
	for _, item := range items {
		if strings.HasSuffix(item.ID, ":"+code) {
			return item
		}
	}
	t.Fatalf("missing %s result: %+v", code, items)
	return model.HealthItem{}
}

func TestHealthRepairsOnlyDisposableDirectoriesAndIsIdempotent(t *testing.T) {
	m, pkg, i := healthFixture(t, "nginx")
	dir, _ := m.instanceDir(i.ID)
	for _, path := range []string{filepath.Dir(i.LogPath), filepath.Join(dir, "temp")} {
		if err := os.Remove(path); err != nil {
			t.Fatal(err)
		}
	}
	config, err := os.ReadFile(i.ConfigPath)
	if err != nil {
		t.Fatal(err)
	}
	got, items := m.CheckHealth(context.Background(), pkg, i, false)
	if got != i {
		t.Fatalf("healthy stopped metadata changed: %+v", got)
	}
	for _, code := range []string{"logs", "temp"} {
		if item := healthResult(t, items, code); item.Status != "repaired" {
			t.Fatalf("missing repair: %+v", item)
		}
	}
	_, items = m.CheckHealth(context.Background(), pkg, got, false)
	for _, item := range items {
		if item.Status != "ok" {
			t.Fatalf("second check was not idempotent: %+v", item)
		}
	}
	current, err := os.ReadFile(i.ConfigPath)
	if err != nil || string(current) != string(config) {
		t.Fatalf("configuration changed: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "process.json")); !os.IsNotExist(err) {
		t.Fatalf("check synthesized a process record: %v", err)
	}
}

func TestHealthNeverRecreatesDataOrConfiguration(t *testing.T) {
	for _, code := range []string{"data", "config", "instance"} {
		t.Run(code, func(t *testing.T) {
			m, pkg, i := healthFixture(t, "redis")
			path := i.DataDir
			if code == "config" {
				path = i.ConfigPath
			} else if code == "instance" {
				path, _ = m.instanceDir(i.ID)
			}
			if err := os.RemoveAll(path); err != nil {
				t.Fatal(err)
			}
			_, items := m.CheckHealth(context.Background(), pkg, i, true)
			if _, err := os.Stat(path); !os.IsNotExist(err) {
				t.Fatalf("required path was recreated: %s (%v)", path, err)
			}
			resultCode := code
			if code == "instance" {
				resultCode = "instance-path"
			}
			if item := healthResult(t, items, resultCode); item.Status != "error" {
				t.Fatalf("required path was not reported: %+v", item)
			}
		})
	}
}

func TestHealthRejectsUnmarkedEnvironmentAndMismatchedPackage(t *testing.T) {
	for _, bad := range []string{"marker", "package", "kind", "executable"} {
		t.Run(bad, func(t *testing.T) {
			m, pkg, i := healthFixture(t, "redis")
			if err := os.Remove(filepath.Dir(i.LogPath)); err != nil {
				t.Fatal(err)
			}
			switch bad {
			case "marker":
				if err := os.WriteFile(filepath.Join(m.root, ".phptools-runtime"), []byte("unknown"), 0600); err != nil {
					t.Fatal(err)
				}
			case "package":
				pkg.ID = "other-package"
			case "kind":
				pkg.Kind = "php"
			case "executable":
				i.Executable = filepath.Join(t.TempDir(), "redis-server.exe")
			}
			got, items := m.CheckHealth(context.Background(), pkg, i, true)
			if got != i {
				t.Fatalf("rejected input was changed: %+v", got)
			}
			if _, err := os.Stat(filepath.Dir(i.LogPath)); !os.IsNotExist(err) {
				t.Fatalf("rejected instance had files repaired: %v", err)
			}
			for _, item := range items {
				if item.Status == "repaired" {
					t.Fatalf("unsafe input reported repair: %+v", item)
				}
			}
		})
	}
}

func TestLightHealthDoesNotExecuteOrScanPackageTree(t *testing.T) {
	m, pkg, i := healthFixture(t, "php")
	// A duplicate executable would make findExecutable fail. Lightweight checks
	// must only stat the recorded component and never execute this invalid file.
	duplicateDir := filepath.Join(pkg.Path, "nested")
	if err := os.Mkdir(duplicateDir, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(duplicateDir, "php-cgi.exe"), []byte("never execute"), 0600); err != nil {
		t.Fatal(err)
	}
	_, items := m.CheckHealth(context.Background(), pkg, i, false)
	for _, item := range items {
		if item.Status != "ok" {
			t.Fatalf("light check unexpectedly scanned or executed package: %+v", item)
		}
	}
	_, items = m.CheckHealth(context.Background(), pkg, i, true)
	if item := healthResult(t, items, "config-test"); item.Status != "error" {
		t.Fatalf("full check missed ambiguous executable: %+v", item)
	}
}

func TestFullHealthReportsStoppedPortConflictWithoutExecuting(t *testing.T) {
	m, pkg, i := healthFixture(t, "php")
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	i.Port = listener.Addr().(*net.TCPAddr).Port
	got, items := m.CheckHealth(context.Background(), pkg, i, true)
	if got != i {
		t.Fatalf("port conflict changed stopped instance: %+v", got)
	}
	if item := healthResult(t, items, "port"); item.Status != "error" {
		t.Fatalf("port conflict was not reported: %+v", item)
	}
	for _, item := range items {
		if strings.HasSuffix(item.ID, ":config-test") {
			t.Fatalf("executed invalid fixture despite port conflict: %+v", item)
		}
	}
}
