//go:build windows

package main

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"phptools/internal/model"
)

func TestCanceledRunRemovesOwnedRuntime(t *testing.T) {
	for _, automatic := range []bool{false, true} {
		name := "explicit root"
		if automatic {
			name = "system temporary root"
		}
		t.Run(name, func(t *testing.T) {
			base := t.TempDir()
			source := fixturePackage(t, base)
			root := filepath.Join(base, "test-runtime")
			if automatic {
				root = ""
			}
			reportPath := filepath.Join(base, "reports", "acceptance.json")
			ctx, cancel := context.WithCancel(context.Background())
			cancel()
			if err := run(ctx, root, source, "5.7.44", reportPath); !errors.Is(err, context.Canceled) {
				t.Fatalf("expected cancellation, got %v", err)
			}
			data, err := os.ReadFile(reportPath)
			if err != nil {
				t.Fatal(err)
			}
			var result report
			if err := json.Unmarshal(data, &result); err != nil {
				t.Fatal(err)
			}
			if result.Error == "" || result.Completed == "" {
				t.Fatalf("missing failure report: %+v", result)
			}
			if _, err := os.Stat(result.Root); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("canceled run left runtime %s: %v", result.Root, err)
			}
			if _, err := os.Stat(filepath.Join(source, ".phptools-package.json")); err != nil {
				t.Fatalf("source package was modified: %v", err)
			}
		})
	}
}

func TestReportInsideRuntimeDoesNotRecreateRoot(t *testing.T) {
	base := t.TempDir()
	source := fixturePackage(t, base)
	root := filepath.Join(base, "test-runtime")
	err := run(context.Background(), root, source, "5.7.44", filepath.Join(root, "report.json"))
	if err == nil || !strings.Contains(err.Error(), "report must be outside") {
		t.Fatalf("expected unsafe report path rejection, got %v", err)
	}
	if _, err := os.Stat(root); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("report validation left or recreated runtime: %v", err)
	}
}

func TestExistingRuntimeIsPreserved(t *testing.T) {
	base := t.TempDir()
	source := fixturePackage(t, base)
	root := filepath.Join(base, "existing")
	if err := os.Mkdir(root, 0700); err != nil {
		t.Fatal(err)
	}
	sentinel := filepath.Join(root, "user-data.txt")
	if err := os.WriteFile(sentinel, []byte("preserve"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := run(context.Background(), root, source, "5.7.44", filepath.Join(base, "report.json")); err == nil {
		t.Fatal("existing runtime was accepted")
	}
	if data, err := os.ReadFile(sentinel); err != nil || string(data) != "preserve" {
		t.Fatalf("existing data changed: %q, %v", data, err)
	}
}

func fixturePackage(t *testing.T, base string) string {
	t.Helper()
	runtime := filepath.Join(base, "source")
	area := filepath.Join(runtime, "packages")
	source := filepath.Join(area, "mysql-5.7.44-x64")
	if err := os.MkdirAll(source, 0700); err != nil {
		t.Fatal(err)
	}
	pkg := model.Package{ID: "mysql-5.7.44-x64", Kind: "mysql", Version: "5.7.44", Path: source, Verified: true, InstalledAt: model.Now()}
	data, err := json.Marshal(pkg)
	if err != nil {
		t.Fatal(err)
	}
	for path, content := range map[string][]byte{
		filepath.Join(runtime, ".phptools-runtime"):     []byte("PHPTools runtime v1\n"),
		filepath.Join(area, ".phptools-packages"):       []byte("PHPTools managed packages v1\n"),
		filepath.Join(source, ".phptools-package.json"): data,
	} {
		if err := os.WriteFile(path, content, 0600); err != nil {
			t.Fatal(err)
		}
	}
	return source
}
