package desktop

import (
	"errors"
	"strings"
	"testing"
)

func TestDependencyCheckBeforeRootAndOfficialInstallerOnly(t *testing.T) {
	a, err := New(t.TempDir(), Hooks{})
	if err != nil {
		t.Fatal(err)
	}
	report := a.GetRuntimeDependencies()
	if report.CheckedAt == "" || len(report.Items) != 4 || a.db != nil {
		t.Fatal("dependency check requires an environment")
	}
	opened := ""
	a.hooks.OpenURL = func(url string) error { opened = url; return nil }
	if err = a.OpenRuntimeInstaller("https://example.com/untrusted.exe"); err == nil || opened != "" {
		t.Fatal("arbitrary URL reached launcher")
	}
	if err = a.OpenRuntimeInstaller("vc11"); err != nil || !strings.HasPrefix(opened, "https://www.microsoft.com/") {
		t.Fatal(opened, err)
	}
	want := errors.New("browser unavailable")
	a.hooks.OpenURL = func(string) error { return want }
	if err = a.OpenRuntimeInstaller("webview2"); !errors.Is(err, want) {
		t.Fatal("browser failure was swallowed", err)
	}
}
