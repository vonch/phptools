package desktop

import (
	"os"
	"path/filepath"
	"testing"
)

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
