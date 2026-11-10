package desktop

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"phptools/internal/model"
)

func TestSiteViewPersistsWithoutEnvironment(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "中文 preferences")
	notified := 0
	a, err := New(dir, Hooks{Notify: func() { notified++ }})
	if err != nil {
		t.Fatal(err)
	}
	want := model.SiteViewPreferences{
		HiddenColumns: []string{"root", "enabled"},
		SiteOrder:     []string{"site-b", "site-a"}, SortBy: "domain", SortDirection: "desc",
	}
	input := model.SiteViewPreferences{
		HiddenColumns: []string{"root", "enabled", "root"},
		SiteOrder:     []string{"site-b", "site-a", "site-b"}, SortBy: "domain", SortDirection: "desc",
	}
	if err = a.SaveSiteView(input); err != nil {
		t.Fatal(err)
	}
	if notified != 1 || a.db != nil || a.manager != nil || !reflect.DeepEqual(a.data.SiteView, want) {
		t.Fatal("UI preferences must normalize and notify without initializing services")
	}
	input.HiddenColumns[0] = "name"
	input.SiteOrder[0] = "changed"
	if !reflect.DeepEqual(a.data.SiteView, want) {
		t.Fatal("saved preferences alias caller slices")
	}
	b, err := New(dir, Hooks{})
	if err != nil || !reflect.DeepEqual(b.data.SiteView, want) {
		t.Fatalf("restart lost site preferences: %+v, %v", b, err)
	}
	if err = b.SaveSiteView(defaultSiteView()); err != nil {
		t.Fatal(err)
	}
	c, err := New(dir, Hooks{})
	if err != nil || !reflect.DeepEqual(c.data.SiteView, defaultSiteView()) {
		t.Fatalf("restored defaults did not persist: %v", err)
	}
}

func TestSiteViewRejectsInvalidPreferences(t *testing.T) {
	changes := map[string]func(*model.SiteViewPreferences){
		"unknown column": func(v *model.SiteViewPreferences) { v.HiddenColumns = []string{"unknown"} },
		"all hidden": func(v *model.SiteViewPreferences) {
			v.HiddenColumns = []string{"name", "root", "phpInstanceId", "enabled", "createdAt", "lastActiveAt", "actions"}
		},
		"empty order":       func(v *model.SiteViewPreferences) { v.SiteOrder = []string{" \t"} },
		"unknown sort":      func(v *model.SiteViewPreferences) { v.SortBy = "unknown" },
		"empty sort":        func(v *model.SiteViewPreferences) { v.SortBy = "" },
		"unknown direction": func(v *model.SiteViewPreferences) { v.SortDirection = "descending" },
	}
	for name, change := range changes {
		t.Run(name, func(t *testing.T) {
			notified := 0
			a, err := New(t.TempDir(), Hooks{Notify: func() { notified++ }})
			if err != nil {
				t.Fatal(err)
			}
			preferences := defaultSiteView()
			change(&preferences)
			if err = a.SaveSiteView(preferences); err == nil {
				t.Fatal("invalid preferences accepted")
			}
			if notified != 0 || !reflect.DeepEqual(a.data.SiteView, defaultSiteView()) {
				t.Fatal("invalid save changed preferences or notified")
			}
			if _, err = os.Stat(filepath.Join(a.configDir, "site-view.json")); !os.IsNotExist(err) {
				t.Fatal("invalid save created a preferences file")
			}
		})
	}
}

func TestSiteViewFailedWriteKeepsStateAndRemovesTemporaryFile(t *testing.T) {
	notified := 0
	a, err := New(t.TempDir(), Hooks{Notify: func() { notified++ }})
	if err != nil {
		t.Fatal(err)
	}
	if err = a.SaveSiteView(defaultSiteView()); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(a.configDir, "site-view.json")
	if err = os.Remove(path); err != nil {
		t.Fatal(err)
	}
	// A directory at the destination makes the atomic replacement fail on Windows.
	if err = os.Mkdir(path, 0700); err != nil {
		t.Fatal(err)
	}
	preferences := defaultSiteView()
	preferences.HiddenColumns = []string{"root"}
	if err = a.SaveSiteView(preferences); err == nil {
		t.Fatal("write unexpectedly succeeded")
	}
	if notified != 1 || !reflect.DeepEqual(a.data.SiteView, defaultSiteView()) {
		t.Fatal("failed save applied preferences or notified")
	}
	if _, err = os.Stat(path + ".tmp"); !os.IsNotExist(err) {
		t.Fatal("failed save left a temporary file")
	}
}

func TestSiteViewInvalidFileUsesDefaultsWithDiagnostic(t *testing.T) {
	for _, content := range []string{"broken JSON", `{"hiddenColumns":["unknown"]}`, `{"sortBy":"invalid"}`} {
		dir := t.TempDir()
		if err := os.WriteFile(filepath.Join(dir, "site-view.json"), []byte(content), 0600); err != nil {
			t.Fatal(err)
		}
		a, err := New(dir, Hooks{})
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(a.data.SiteView, defaultSiteView()) || len(a.data.Diagnostics) != 1 {
			t.Fatal("invalid file must use defaults and explain the problem")
		}
	}
}

func TestSiteViewSaveDoesNotChangeSiteConfiguration(t *testing.T) {
	dir := t.TempDir()
	a, err := New(filepath.Join(dir, "profile"), Hooks{})
	if err != nil {
		t.Fatal(err)
	}
	// No database or service manager is set up: changing actual sites would fail.
	runtime := filepath.Join(dir, "runtime")
	if err = os.Mkdir(runtime, 0700); err != nil {
		t.Fatal(err)
	}
	configPath := filepath.Join(runtime, "nginx.conf")
	config := []byte("site configuration must stay unchanged\n")
	if err = os.WriteFile(configPath, config, 0600); err != nil {
		t.Fatal(err)
	}
	a.data.Settings.Root = runtime
	a.data.Sites = []model.Site{{ID: "site-a", Name: "Site A", Domain: "site-a.test", Enabled: true}}
	a.data.Instances = []model.Instance{{ID: "nginx-a", Kind: "nginx", ConfigPath: configPath}}
	sitesBefore := append([]model.Site{}, a.data.Sites...)
	instancesBefore := append([]model.Instance{}, a.data.Instances...)
	preferences := defaultSiteView()
	preferences.HiddenColumns = []string{"name", "root", "enabled", "actions", "name"}
	preferences.SiteOrder = []string{"site-a"}
	if err = a.SaveSiteView(preferences); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(a.data.Sites, sitesBefore) || !reflect.DeepEqual(a.data.Instances, instancesBefore) {
		t.Fatal("view preferences changed sites or service instances")
	}
	current, err := os.ReadFile(configPath)
	if err != nil || !reflect.DeepEqual(current, config) {
		t.Fatal("view preferences changed Nginx configuration")
	}
	entries, err := os.ReadDir(runtime)
	if err != nil || len(entries) != 1 {
		t.Fatal("view preferences generated runtime configuration")
	}
	profileEntries, err := os.ReadDir(a.configDir)
	if err != nil || len(profileEntries) != 1 || profileEntries[0].Name() != "site-view.json" {
		t.Fatal("view preferences changed unrelated profile settings")
	}
}
