//go:build windows

package acceptance

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestRootCleanupOnSuccessAndFailure(t *testing.T) {
	for _, fail := range []bool{false, true} {
		t.Run(map[bool]string{false: "success", true: "failure"}[fail], func(t *testing.T) {
			var path string
			run := func() (runErr error) {
				root, err := NewRoot("", "cleanup-test")
				if err != nil {
					return err
				}
				path = root.Path
				defer func() { runErr = errors.Join(runErr, root.Cleanup()) }()
				if filepath.Clean(filepath.Dir(path)) != filepath.Clean(os.TempDir()) {
					t.Fatalf("default runtime is outside system temporary directory: %s", path)
				}
				if err := os.Mkdir(filepath.Join(path, "fixture"), 0700); err != nil {
					return err
				}
				if err := os.WriteFile(filepath.Join(path, "fixture", "test.sql"), []byte("fixture"), 0600); err != nil {
					return err
				}
				if fail {
					return errors.New("expected test failure")
				}
				return nil
			}
			err := run()
			if fail && (err == nil || err.Error() != "expected test failure") || !fail && err != nil {
				t.Fatalf("run error = %v", err)
			}
			if _, err := os.Lstat(path); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("temporary runtime remains: %s: %v", path, err)
			}
		})
	}
}

func TestNewRootRefusesExistingDirectory(t *testing.T) {
	path := t.TempDir()
	marker := filepath.Join(path, "keep.txt")
	if err := os.WriteFile(marker, []byte("existing data"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := NewRoot(path, "existing-test"); !errors.Is(err, os.ErrExist) {
		t.Fatalf("existing directory accepted: %v", err)
	}
	if data, err := os.ReadFile(marker); err != nil || string(data) != "existing data" {
		t.Fatalf("existing data changed: %q, %v", data, err)
	}
}

func TestCleanupRefusesReplacedDirectory(t *testing.T) {
	root, err := NewRoot(filepath.Join(t.TempDir(), "runtime"), "replacement-test")
	if err != nil {
		t.Fatal(err)
	}
	original := root.Path + "-original"
	if err := os.Rename(root.Path, original); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(root.Path, 0700); err != nil {
		t.Fatal(err)
	}
	marker := filepath.Join(root.Path, "keep.txt")
	if err := os.WriteFile(marker, []byte("replacement data"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := root.Cleanup(); err == nil {
		t.Fatal("cleanup accepted a different directory at the same path")
	}
	if _, err := os.Stat(marker); err != nil {
		t.Fatalf("replacement data was removed: %v", err)
	}
}

func TestCleanupRefusesSymbolicLink(t *testing.T) {
	parent := t.TempDir()
	root, err := NewRoot(filepath.Join(parent, "runtime"), "link-test")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(root.Path, root.Path+"-original"); err != nil {
		t.Fatal(err)
	}
	target := t.TempDir()
	marker := filepath.Join(target, "keep.txt")
	if err := os.WriteFile(marker, []byte("unrelated data"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, root.Path); err != nil {
		t.Skipf("cannot create a directory symbolic link: %v", err)
	}
	if err := root.Cleanup(); err == nil {
		t.Fatal("cleanup accepted a symbolic link")
	}
	if _, err := os.Stat(marker); err != nil {
		t.Fatalf("symbolic link target changed: %v", err)
	}
	if _, err := NewRoot(filepath.Join(root.Path, "child"), "link-parent-test"); err == nil {
		t.Fatal("creation accepted a symbolic link ancestor")
	}
}

func TestCleanupRefusesChangedPublicPath(t *testing.T) {
	root, err := NewRoot(filepath.Join(t.TempDir(), "runtime"), "changed-path-test")
	if err != nil {
		t.Fatal(err)
	}
	path := root.Path
	defer func() {
		root.Path = path
		if err := root.Cleanup(); err != nil {
			t.Error(err)
		}
	}()
	root.Path = t.TempDir()
	if err := root.Cleanup(); err == nil {
		t.Fatal("cleanup accepted a modified path")
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("original root was removed: %v", err)
	}
}

func TestNewRootRefusesDriveRoot(t *testing.T) {
	path := filepath.VolumeName(os.TempDir()) + string(filepath.Separator)
	if _, err := NewRoot(path, "volume-test"); err == nil {
		t.Fatal("drive root was accepted")
	}
}
