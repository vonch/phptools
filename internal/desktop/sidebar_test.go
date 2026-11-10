package desktop

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"phptools/internal/model"
)

func TestSidebarPersistsWithoutEnvironment(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "中文 settings")
	notified := 0
	a, err := New(dir, Hooks{Notify: func() { notified++ }})
	if err != nil {
		t.Fatal(err)
	}
	want := defaultSidebar()
	want[0], want[4] = want[4], want[0]
	want[1].Hidden = true
	want[1].Label = "我的组件"
	want[7].Label = "偏好设置"
	if err = a.SaveSidebar(want); err != nil {
		t.Fatal(err)
	}
	if notified != 1 || a.db != nil {
		t.Fatal("UI preferences must notify without initializing an environment")
	}
	want[0].Hidden = true
	if a.data.Sidebar[0].Hidden {
		t.Fatal("saved state aliases the caller's slice")
	}
	want[0].Hidden = false
	b, err := New(dir, Hooks{})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(b.data.Sidebar, want) {
		t.Fatalf("restart lost preferences: %+v", b.data.Sidebar)
	}
	if err = b.SaveSidebar(defaultSidebar()); err != nil {
		t.Fatal(err)
	}
	c, err := New(dir, Hooks{})
	if err != nil || !reflect.DeepEqual(c.data.Sidebar, defaultSidebar()) {
		t.Fatalf("restored defaults did not persist: %v", err)
	}
}

func TestSidebarRejectsInvalidMenus(t *testing.T) {
	a, err := New(t.TempDir(), Hooks{})
	if err != nil {
		t.Fatal(err)
	}
	cases := map[string]func([]model.SidebarItem) []model.SidebarItem{
		"missing":         func(v []model.SidebarItem) []model.SidebarItem { return v[1:] },
		"duplicate":       func(v []model.SidebarItem) []model.SidebarItem { v[0] = v[1]; return v },
		"unknown":         func(v []model.SidebarItem) []model.SidebarItem { v[0].Key = "unknown"; return v },
		"settings hidden": func(v []model.SidebarItem) []model.SidebarItem { v[7].Hidden = true; return v },
		"label too long": func(v []model.SidebarItem) []model.SidebarItem {
			v[0].Label = strings.Repeat("界", maxSidebarLabelRunes+1)
			return v
		},
		"label control character": func(v []model.SidebarItem) []model.SidebarItem {
			v[0].Label = "自定义\n名称"
			return v
		},
	}
	for name, change := range cases {
		t.Run(name, func(t *testing.T) {
			if err := a.SaveSidebar(change(defaultSidebar())); err == nil {
				t.Fatal("invalid menu accepted")
			}
			if !reflect.DeepEqual(a.data.Sidebar, defaultSidebar()) {
				t.Fatal("invalid save changed current preferences")
			}
			if _, err := os.Stat(filepath.Join(a.configDir, "sidebar.json")); !os.IsNotExist(err) {
				t.Fatal("invalid save created a preferences file")
			}
		})
	}
}

func TestSidebarLoadsLegacyConfigurationWithoutLabels(t *testing.T) {
	dir := t.TempDir()
	legacy := []struct {
		Key    string `json:"key"`
		Hidden bool   `json:"hidden"`
	}{}
	for _, item := range defaultSidebar() {
		legacy = append(legacy, struct {
			Key    string `json:"key"`
			Hidden bool   `json:"hidden"`
		}{Key: item.Key, Hidden: item.Hidden})
	}
	raw, err := json.Marshal(legacy)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(dir, "sidebar.json"), raw, 0600); err != nil {
		t.Fatal(err)
	}
	a, err := New(dir, Hooks{})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(a.data.Sidebar, defaultSidebar()) || len(a.data.Diagnostics) != 0 {
		t.Fatalf("legacy menu settings should load with default labels: %+v, diagnostics: %+v", a.data.Sidebar, a.data.Diagnostics)
	}
}

func TestSidebarTrimsCustomLabelsBeforePersisting(t *testing.T) {
	a, err := New(t.TempDir(), Hooks{})
	if err != nil {
		t.Fatal(err)
	}
	want := defaultSidebar()
	want[0].Label = "  首页  "
	if err = a.SaveSidebar(want); err != nil {
		t.Fatal(err)
	}
	if a.data.Sidebar[0].Label != "首页" {
		t.Fatalf("custom menu label was not normalized: %q", a.data.Sidebar[0].Label)
	}
}

func TestSidebarFailedWriteKeepsSavedState(t *testing.T) {
	a, err := New(t.TempDir(), Hooks{})
	if err != nil {
		t.Fatal(err)
	}
	if err = a.SaveSidebar(defaultSidebar()); err != nil {
		t.Fatal(err)
	}
	// Force the atomic rename to fail without relying on administrator ACLs.
	path := filepath.Join(a.configDir, "sidebar.json")
	if err = os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err = os.Mkdir(path, 0700); err != nil {
		t.Fatal(err)
	}
	want := defaultSidebar()
	want[0].Hidden = true
	if err = a.SaveSidebar(want); err == nil {
		t.Fatal("write unexpectedly succeeded")
	}
	if !reflect.DeepEqual(a.data.Sidebar, defaultSidebar()) {
		t.Fatal("failed write changed applied preferences")
	}
	if _, err = os.Stat(path + ".tmp"); !os.IsNotExist(err) {
		t.Fatal("failed write left temporary preferences")
	}
}

func TestSidebarInvalidFileUsesDefaultsWithDiagnostic(t *testing.T) {
	invalid := defaultSidebar()
	invalid[7].Hidden = true
	raw, _ := json.Marshal(invalid)
	for _, content := range [][]byte{[]byte("broken JSON"), raw} {
		dir := t.TempDir()
		if err := os.WriteFile(filepath.Join(dir, "sidebar.json"), content, 0600); err != nil {
			t.Fatal(err)
		}
		a, err := New(dir, Hooks{})
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(a.data.Sidebar, defaultSidebar()) || len(a.data.Diagnostics) != 1 {
			t.Fatal("unusable preferences must show defaults and explain the problem")
		}
	}
}

func TestSidebarIsIndependentOfEnvironmentSettings(t *testing.T) {
	a, err := New(t.TempDir(), Hooks{})
	if err != nil {
		t.Fatal(err)
	}
	want := defaultSidebar()
	for i := range want {
		want[i].Hidden = want[i].Key != "settings"
	}
	if err = a.SaveSidebar(want); err != nil {
		t.Fatal(err)
	}
	root := filepath.Join(t.TempDir(), "runtime")
	if err = a.SetRoot(root); err != nil {
		t.Fatal(err)
	}
	defer a.db.Close()
	b, err := New(a.configDir, Hooks{})
	if err != nil {
		t.Fatal(err)
	}
	defer b.db.Close()
	if !reflect.DeepEqual(b.data.Sidebar, want) || b.data.Settings.Root != root {
		t.Fatal("environment initialization overwrote menu preferences")
	}
	if err = b.SaveSidebar(defaultSidebar()); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(filepath.Join(a.configDir, "settings.json"))
	if err != nil {
		t.Fatal(err)
	}
	var config struct {
		Root string `json:"root"`
	}
	if err = json.Unmarshal(raw, &config); err != nil || config.Root != root {
		t.Fatal("menu preferences overwrote environment settings")
	}
}
