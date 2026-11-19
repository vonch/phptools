package desktop

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"phptools/internal/model"
)

func TestSiteProjectPickerStartsInSites(t *testing.T) {
	a := testApp(t)
	var title, initial string
	a.hooks.PickDirectory = func(gotTitle, gotInitial string) (string, error) {
		title, initial = gotTitle, gotInitial
		return filepath.Join(gotInitial, "shop"), nil
	}
	got, err := a.SelectSiteProjectDirectory()
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(a.data.Settings.Root, "sites")
	if title != "选择站点项目目录" || initial != want || got != filepath.Join(want, "shop") {
		t.Fatalf("picker title %q initial %q result %q", title, initial, got)
	}
	info, err := os.Stat(want)
	if err != nil || !info.IsDir() {
		t.Fatal(err)
	}
}

func TestFailedNewProjectDoesNotLeaveFolder(t *testing.T) {
	a := testApp(t)
	_, err := a.SaveSite(model.Site{Name: "演示", Domain: "demo.test", Port: 80, Enabled: true})
	if err == nil || !strings.Contains(err.Error(), "Nginx") {
		t.Fatal(err)
	}
	pages, err := filepath.Glob(filepath.Join(a.data.Settings.Root, "sites", "*", "index.html"))
	if err != nil || len(pages) != 0 {
		t.Fatalf("new project folder remained: %v %v", pages, err)
	}
	project := t.TempDir()
	if err = os.WriteFile(filepath.Join(project, "keep.txt"), []byte("keep"), 0600); err != nil {
		t.Fatal(err)
	}
	_, err = a.SaveSite(model.Site{Name: "已有", Domain: "kept.test", Root: project, Port: 80, Enabled: true})
	if err == nil {
		t.Fatal("existing project should still require nginx")
	}
	if _, err = os.Stat(filepath.Join(project, "index.html")); !os.IsNotExist(err) {
		t.Fatal("existing project received a generated homepage")
	}
}

func TestEntryPickerDistinguishesCancelFromProjectRoot(t *testing.T) {
	root := t.TempDir()
	child := filepath.Join(root, "public")
	os.Mkdir(child, 0700)
	selected := ""
	calls := 0
	a, err := New(t.TempDir(), Hooks{PickDirectory: func(title, initial string) (string, error) {
		calls++
		if initial != filepath.Clean(root) {
			t.Errorf("picker initial %q", initial)
		}
		return selected, nil
	}})
	if err != nil {
		t.Fatal(err)
	}
	result, err := a.SelectSiteEntry(root)
	if err != nil || result.Selected {
		t.Fatalf("cancel: %+v %v", result, err)
	}
	selected = root
	result, err = a.SelectSiteEntry(root)
	if err != nil || !result.Selected || result.Entry != "" {
		t.Fatalf("project root: %+v %v", result, err)
	}
	selected = child
	result, err = a.SelectSiteEntry(root)
	if err != nil || !result.Selected || result.Entry != "public" {
		t.Fatalf("child: %+v %v", result, err)
	}
	selected = t.TempDir()
	if _, err = a.SelectSiteEntry(root); err == nil {
		t.Fatal("outside project accepted")
	}
	before := calls
	if _, err = a.SelectSiteEntry("relative"); err == nil || calls != before {
		t.Fatal("picker opened without valid project")
	}
}
