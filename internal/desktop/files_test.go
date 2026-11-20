package desktop

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"phptools/internal/model"
)

func TestOpenSitesRootOpensTheEnvironmentSitesDirectory(t *testing.T) {
	var opened string
	a := testApp(t)
	a.hooks.OpenDirectory = func(path string) error {
		opened = path
		return nil
	}
	if err := a.OpenSitesRoot(); err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(a.data.Settings.Root, "sites")
	if opened != want {
		t.Fatalf("opened %s, want %s", opened, want)
	}
}

func TestOpenDirectoryUsesExactNormalizedDirectoryAndReportsFailure(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "中文 project, with spaces")
	if err := os.Mkdir(dir, 0700); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(dir, "file.txt")
	if err := os.WriteFile(file, nil, 0600); err != nil {
		t.Fatal(err)
	}
	var opened []string
	var launchErr error
	a := &App{hooks: Hooks{OpenDirectory: func(path string) error {
		opened = append(opened, path)
		return launchErr
	}}}
	if err := a.OpenDirectory(filepath.ToSlash(dir) + "/"); err != nil {
		t.Fatal(err)
	}
	if len(opened) != 1 || opened[0] != dir {
		t.Fatalf("unexpected directory: %v", opened)
	}
	for _, invalid := range []string{"relative", file, filepath.Join(dir, "missing"), dir + "\x00"} {
		if err := a.OpenDirectory(invalid); err == nil {
			t.Fatalf("invalid directory accepted: %q", invalid)
		}
	}
	if len(opened) != 1 {
		t.Fatal("invalid input reached the system opener")
	}
	launchErr = errors.New("shell unavailable")
	if err := a.OpenDirectory(dir); !errors.Is(err, launchErr) || !strings.Contains(err.Error(), dir) {
		t.Fatalf("launcher failure was hidden: %v", err)
	}
	a.hooks.OpenDirectory = nil
	if err := a.OpenDirectory(dir); err == nil {
		t.Fatal("missing opener was silently accepted")
	}
}

func TestSiteVisitURLUsesHTTPSWhenEnabled(t *testing.T) {
	if got := siteVisitURL(model.Site{Domain: "shop.test", Port: 80, HTTPS: true}); got != "https://shop.test/" {
		t.Fatal(got)
	}
	if got := siteVisitURL(model.Site{Domain: "shop.test", Port: 443, HTTPS: true}); got != "https://shop.test/" {
		t.Fatal(got)
	}
	if got := siteVisitURL(model.Site{Domain: "shop.test", Port: 8080}); got != "http://shop.test:8080/" {
		t.Fatal(got)
	}
}
