//go:build windows

package desktop

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCreateSitesRootShortcutPointsAtTheSitesDirectory(t *testing.T) {
	a := testApp(t)
	desktop := t.TempDir()
	if err := a.createSitesRootShortcut(func() (string, error) { return desktop, nil }); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(desktop, "网站根目录.lnk")
	target, err := shortcutTarget(link)
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(a.data.Settings.Root, "sites")
	gotInfo, err := os.Stat(target)
	if err != nil {
		t.Fatal(err)
	}
	wantInfo, err := os.Stat(want)
	if err != nil {
		t.Fatal(err)
	}
	if !os.SameFile(gotInfo, wantInfo) {
		t.Fatalf("shortcut target %s, want %s", target, want)
	}
	kept := []byte("keep")
	if err = os.WriteFile(link, kept, 0600); err != nil {
		t.Fatal(err)
	}
	err = a.createSitesRootShortcut(func() (string, error) { return desktop, nil })
	if err == nil || !strings.Contains(err.Error(), "没有覆盖") {
		t.Fatalf("existing shortcut was replaced: %v", err)
	}
	data, err := os.ReadFile(link)
	if err != nil || string(data) != string(kept) {
		t.Fatalf("existing shortcut changed: %q %v", data, err)
	}
}

func TestCreateSitesRootShortcutRequiresAnEnvironment(t *testing.T) {
	a, err := New(filepath.Join(t.TempDir(), "settings"), Hooks{})
	if err != nil {
		t.Fatal(err)
	}
	err = a.CreateSitesRootShortcut()
	if err == nil || !strings.Contains(err.Error(), "请先选择独立环境目录") {
		t.Fatalf("missing environment was accepted: %v", err)
	}
}
