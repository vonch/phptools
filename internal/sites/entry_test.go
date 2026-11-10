package sites

import (
	"os"
	"path/filepath"
	"testing"

	"phptools/internal/model"
)

func TestRelativeEntrySelection(t *testing.T) {
	root := filepath.Join(t.TempDir(), "项目 space")
	child := filepath.Join(root, "public", "入口")
	if err := os.MkdirAll(child, 0700); err != nil {
		t.Fatal(err)
	}
	entry, err := RelativeEntry(root, child)
	if err != nil || entry != "public/入口" {
		t.Fatalf("child: %q %v", entry, err)
	}
	entry, err = RelativeEntry(root, root)
	if err != nil || entry != "" {
		t.Fatalf("root: %q %v", entry, err)
	}
	for _, path := range []string{filepath.Dir(root), t.TempDir(), filepath.Join(root, "missing"), "public"} {
		if _, err := RelativeEntry(root, path); err == nil {
			t.Fatalf("accepted invalid entry %q", path)
		}
	}
	file := filepath.Join(root, "index.php")
	os.WriteFile(file, nil, 0600)
	if _, err := RelativeEntry(root, file); err == nil {
		t.Fatal("accepted file as entry directory")
	}
}

func TestEntryCannotEscapeThroughDirectoryLink(t *testing.T) {
	root := t.TempDir()
	external := t.TempDir()
	link := filepath.Join(root, "linked")
	if err := os.Symlink(external, link); err != nil {
		t.Skip("directory link creation unavailable:", err)
	}
	if _, err := RelativeEntry(root, link); err == nil {
		t.Fatal("accepted link to external directory")
	}
	site := model.Site{ID: "entry-link", Domain: "entry.test", Root: root, Entry: "linked", Port: 8081}
	if _, err := Validate(site, nil, nil); err == nil {
		t.Fatal("site validation accepted external link")
	}
}
