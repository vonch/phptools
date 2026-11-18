//go:build windows

package prerequisites

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"phptools/internal/model"
)

func TestBootstrapInstalledDoesNotPromptOrOpenBrowser(t *testing.T) {
	if !ensureWebView(func() error { return nil }, func(string, bool) bool {
		t.Fatal("installed runtime prompted")
		return false
	}, func(string) error { t.Fatal("unexpected browser"); return nil }) {
		t.Fatal("installed runtime prevented startup")
	}
}

func TestBootstrapMissingCancelDoesNotOpenBrowser(t *testing.T) {
	if ensureWebView(func() error { return errors.New("missing") }, func(message string, retry bool) bool {
		if retry || !strings.Contains(message, "WebView2") {
			t.Fatal(message)
		}
		return false
	}, func(string) error { t.Fatal("cancel opened browser"); return nil }) {
		t.Fatal("missing runtime allowed startup")
	}
}

func TestBootstrapInstallRetryAndFailure(t *testing.T) {
	for _, browserError := range []error{nil, errors.New("no browser")} {
		reads, prompts, opened := 0, 0, 0
		ok := ensureWebView(func() error {
			reads++
			if reads >= 3 {
				return nil
			}
			return errors.New("missing")
		}, func(message string, retry bool) bool {
			prompts++
			if retry != (prompts > 1) {
				t.Fatal("wrong prompt sequence")
			}
			if prompts == 3 && !strings.Contains(message, "仍未检测到") {
				t.Fatal("retry lost failure")
			}
			if browserError != nil && retry && !strings.Contains(message, "no browser") {
				t.Fatal("browser error lost")
			}
			return true
		}, func(url string) error {
			opened++
			expected, _ := InstallURL(WebViewID)
			if url != expected {
				t.Fatal(url)
			}
			return browserError
		})
		if !ok || reads != 3 || opened != 1 || prompts != 3 {
			t.Fatalf("invalid lifecycle: %v %d %d %d", ok, reads, opened, prompts)
		}
	}
}

func TestBootstrapCancelAfterOpeningDoesNotStart(t *testing.T) {
	n := 0
	if ensureWebView(func() error { return errors.New("missing") }, func(string, bool) bool {
		n++
		return n == 1
	}, func(string) error { return nil }) {
		t.Fatal("cancel allowed startup")
	}
}

func TestDependencyRequirementAndProbeErrorsAreVisible(t *testing.T) {
	pkgs := []model.Package{{ID: "php-old", Path: "installed"}, {ID: "mysql-new", Path: "installed"}, {ID: "not-installed"}}
	report := inspect(pkgs, func(d definition) (string, string, string) {
		if d.id == "vc14" && !reflect.DeepEqual(d.dlls, []string{"msvcp140.dll", "vcruntime140.dll", "vcruntime140_1.dll"}) {
			t.Fatal(d.dlls)
		}
		if d.id == "vc12" {
			return "error", "", "access denied"
		}
		return "missing", "", "not installed"
	}, func(p model.Package) (map[string][]string, error) {
		if p.ID == "not-installed" {
			t.Fatal("read uninstalled package")
		}
		if p.ID == "php-old" {
			return map[string][]string{"vc11": {"msvcr110.dll"}}, nil
		}
		return map[string][]string{"vc14": {"vcruntime140_1.dll"}}, errors.New("damaged optional binary")
	})
	if report.CheckedAt == "" || len(report.Items) != 4 || len(report.Warnings) != 1 {
		t.Fatal(report)
	}
	for _, item := range report.Items {
		switch item.ID {
		case WebViewID:
			if len(item.RequiredBy) != 1 {
				t.Fatal(item)
			}
		case "vc11":
			if !reflect.DeepEqual(item.RequiredBy, []string{"php-old"}) {
				t.Fatal(item)
			}
		case "vc14":
			if !reflect.DeepEqual(item.RequiredBy, []string{"mysql-new"}) {
				t.Fatal(item)
			}
		case "vc12":
			if item.State != "error" || item.Message != "access denied" || len(item.RequiredBy) != 0 {
				t.Fatal(item)
			}
		}
	}
}

func TestRuntimeInstallerRejectsUntrustedInput(t *testing.T) {
	for _, id := range []string{"", "https://example.com/installer.exe", "vc14.exe", "../webview2"} {
		if _, err := InstallURL(id); err == nil {
			t.Fatal(id)
		}
	}
	for _, d := range definitions {
		url, err := InstallURL(d.id)
		if err != nil || !strings.HasPrefix(url, "https://") || !strings.Contains(url, "microsoft.com/") {
			t.Fatal(url, err)
		}
	}
	for _, id := range []string{"", "vc11", "vc12", "webview2", "https://aka.ms/vs/17/release/vc_redist.x64.exe"} {
		if _, err := DownloadURL(id); err == nil {
			t.Fatal(id)
		}
	}
	url, err := DownloadURL("vc14")
	if err != nil || url != "https://aka.ms/vs/17/release/vc_redist.x64.exe" {
		t.Fatal(url, err)
	}
}

func TestDLLNamesAndDamagedPackage(t *testing.T) {
	for _, name := range []string{"../vcruntime140.dll", `C:\evil.dll`, `sub\file.dll`, "file.exe", ""} {
		if validDLLName(name) {
			t.Fatal(name)
		}
	}
	if !validDLLName("VCRUNTIME140_1.DLL") || runtimeFamily("VCRUNTIME140_1.DLL") != "vc14" {
		t.Fatal("runtime not recognized")
	}
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "nginx.exe"), []byte("broken PE"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := packageRequirements(model.Package{Kind: "nginx", Path: dir}); err == nil {
		t.Fatal("corrupt executable passed")
	}
	if _, err := inspectDLL(filepath.Join(dir, "missing.dll")); !os.IsNotExist(err) {
		t.Fatal(err)
	}
	if _, err := inspectDLL(filepath.Join(dir, "nginx.exe")); err == nil {
		t.Fatal("invalid runtime passed")
	}
}
