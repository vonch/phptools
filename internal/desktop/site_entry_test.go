package desktop

import (
	"os"
	"path/filepath"
	"testing"
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
