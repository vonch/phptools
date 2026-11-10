package store

import (
	"os"
	"path/filepath"
	"testing"
)

func TestOwnershipAndPersistence(t *testing.T) {
	root := filepath.Join(t.TempDir(), "环境 data")
	s, err := Open(root)
	if err != nil {
		t.Fatal(err)
	}
	if err = s.Put("test", "id", map[string]string{"value": "中文"}); err != nil {
		t.Fatal(err)
	}
	s.Close()
	s, err = Open(root)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	values, err := List[map[string]string](s, "test")
	if err != nil || len(values) != 1 || values[0]["value"] != "中文" {
		t.Fatalf("persistence: %v %v", values, err)
	}
}

func TestRejectUnownedDirectory(t *testing.T) {
	root := t.TempDir()
	os.WriteFile(filepath.Join(root, "existing.txt"), []byte("keep"), 0600)
	if s, err := Open(root); err == nil {
		s.Close()
		t.Fatal("adopted unowned directory")
	}
}
