package services

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"phptools/internal/model"
)

func TestInspectHealthListsRepairsWithoutMutating(t *testing.T) {
	m, pkg, i := healthFixture(t, "nginx")
	i.State, i.Error = "error", "old failure"
	dir, _ := m.instanceDir(i.ID)
	paths := []string{filepath.Dir(i.LogPath), filepath.Join(dir, "temp")}
	for _, path := range paths {
		if err := os.Remove(path); err != nil {
			t.Fatal(err)
		}
	}
	saves := 0
	m.save = func(model.Instance) error { saves++; return nil }
	_, items := m.InspectHealth(context.Background(), pkg, i, true)
	for _, code := range []string{"logs", "temp", "process"} {
		item := healthResult(t, items, code)
		if item.Status != "warning" || !item.Repairable || item.RepairID != item.ID {
			t.Fatalf("missing actionable repair for %s: %+v", code, item)
		}
	}
	if saves != 0 {
		t.Fatal("inspection persisted metadata")
	}
	for _, path := range paths {
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Fatalf("inspection recreated %s: %v", path, err)
		}
	}
}

func TestRepairHealthChangesOnlyTheSelectedItem(t *testing.T) {
	for _, selected := range []string{"logs", "temp", "process"} {
		t.Run(selected, func(t *testing.T) {
			m, pkg, i := healthFixture(t, "nginx")
			i.State, i.Error = "error", "old failure"
			dir, _ := m.instanceDir(i.ID)
			paths := map[string]string{"logs": filepath.Dir(i.LogPath), "temp": filepath.Join(dir, "temp")}
			for _, path := range paths {
				if err := os.Remove(path); err != nil {
					t.Fatal(err)
				}
			}
			config, err := os.ReadFile(i.ConfigPath)
			if err != nil {
				t.Fatal(err)
			}
			saves := 0
			m.save = func(current model.Instance) error {
				saves++
				if current.State != "stopped" || current.Error != "" {
					t.Fatalf("incorrect repaired metadata: %+v", current)
				}
				return nil
			}
			item, err := m.RepairHealth(context.Background(), pkg, i, selected)
			if err != nil || item.Status != "repaired" || item.Repairable {
				t.Fatalf("selected repair failed: %+v %v", item, err)
			}
			for code, path := range paths {
				_, err := os.Stat(path)
				if code == selected && err != nil || code != selected && !os.IsNotExist(err) {
					t.Fatalf("repair %s changed wrong directory %s: %v", selected, code, err)
				}
			}
			if selected == "process" && saves != 1 || selected != "process" && saves != 0 {
				t.Fatalf("repair %s persisted unexpected metadata: %d", selected, saves)
			}
			if after, err := os.ReadFile(i.ConfigPath); err != nil || string(after) != string(config) {
				t.Fatalf("configuration changed: %v", err)
			}
			if _, err := os.Stat(filepath.Join(dir, "process.json")); !os.IsNotExist(err) {
				t.Fatalf("repair created process record: %v", err)
			}
		})
	}
}

func TestHealthDoesNotOfferUnsafeRepairs(t *testing.T) {
	for _, condition := range []string{"log-file", "custom-logs", "invalid-package", "unmarked-root"} {
		t.Run(condition, func(t *testing.T) {
			m, pkg, i := healthFixture(t, "nginx")
			logs := filepath.Dir(i.LogPath)
			if err := os.Remove(logs); err != nil {
				t.Fatal(err)
			}
			switch condition {
			case "log-file":
				if err := os.WriteFile(logs, []byte("keep this file"), 0600); err != nil {
					t.Fatal(err)
				}
			case "custom-logs":
				i.LogPath = filepath.Join(filepath.Dir(logs), "custom", "error.log")
			case "invalid-package":
				pkg.ID = "unknown"
			case "unmarked-root":
				if err := os.Remove(filepath.Join(m.root, ".phptools-runtime")); err != nil {
					t.Fatal(err)
				}
			}
			_, items := m.InspectHealth(context.Background(), pkg, i, false)
			for _, item := range items {
				if item.Repairable || item.RepairID != "" {
					t.Fatalf("unsafe condition offered repair: %+v", item)
				}
			}
			if item, err := m.RepairHealth(context.Background(), pkg, i, "logs"); err == nil {
				t.Fatalf("unsafe repair accepted: %+v", item)
			}
			if condition == "log-file" {
				if content, err := os.ReadFile(logs); err != nil || string(content) != "keep this file" {
					t.Fatalf("blocking file changed: %v", err)
				}
			} else if _, err := os.Stat(logs); !os.IsNotExist(err) {
				t.Fatalf("unsafe repair created logs: %v", err)
			}
		})
	}
}

func TestRepairHealthReportsSaveFailureAndCancellation(t *testing.T) {
	m, pkg, i := healthFixture(t, "php")
	i.State, i.Error = "error", "old failure"
	m.save = func(model.Instance) error { return errors.New("test write denied") }
	item, err := m.RepairHealth(context.Background(), pkg, i, "process")
	if err == nil || item.Status != "error" || !item.Repairable {
		t.Fatalf("save failure was presented as repaired: %+v %v", item, err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := m.RepairHealth(ctx, pkg, i, "process"); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled repair returned %v", err)
	}
}
