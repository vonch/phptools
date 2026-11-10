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
)

func TestCanceledRunCleansRuntimeAndKeepsFailureReport(t *testing.T) {
	workspace := t.TempDir()
	t.Chdir(workspace)
	source := filepath.Join(workspace, "source")
	if err := os.Mkdir(source, 0700); err != nil {
		t.Fatal(err)
	}
	root := filepath.Join(workspace, "runtime")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := run(ctx, root, source); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled run returned %v", err)
	}
	if _, err := os.Lstat(root); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("canceled run left runtime %s: %v", root, err)
	}
	data, err := os.ReadFile(filepath.Join(workspace, "build", "qa", "health-acceptance.json"))
	if err != nil {
		t.Fatal(err)
	}
	var saved report
	if err := json.Unmarshal(data, &saved); err != nil {
		t.Fatal(err)
	}
	if saved.Root != root || !strings.Contains(saved.Error, context.Canceled.Error()) {
		t.Fatalf("failure report omitted cancellation: %+v", saved)
	}
}

func TestRunRejectsInvalidSourceBeforeCreatingRuntime(t *testing.T) {
	for _, source := range []string{"", "relative-package", filepath.Join(t.TempDir(), "missing-package")} {
		root := filepath.Join(t.TempDir(), "runtime")
		if err := run(context.Background(), root, source); err == nil {
			t.Fatalf("invalid source %q was accepted", source)
		}
		if _, err := os.Lstat(root); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("invalid source created runtime %s: %v", root, err)
		}
	}
}

func TestRunRejectsSourceRuntimeOverlap(t *testing.T) {
	workspace := t.TempDir()
	t.Chdir(workspace)
	source := filepath.Join(workspace, "source")
	if err := os.Mkdir(source, 0700); err != nil {
		t.Fatal(err)
	}
	marker := filepath.Join(source, "keep.txt")
	if err := os.WriteFile(marker, []byte("read-only source"), 0600); err != nil {
		t.Fatal(err)
	}
	root := filepath.Join(source, "runtime")
	if err := run(context.Background(), root, source); err == nil || !strings.Contains(err.Error(), "overlap") {
		t.Fatalf("nested runtime was not rejected: %v", err)
	}
	if _, err := os.Lstat(root); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("rejected nested runtime remains: %v", err)
	}
	if data, err := os.ReadFile(marker); err != nil || string(data) != "read-only source" {
		t.Fatalf("source changed: %q, %v", data, err)
	}
	if err := run(context.Background(), workspace, source); err == nil {
		t.Fatal("existing runtime containing the source was accepted")
	}
}
