package desktop

import (
	"os"
	"path/filepath"
	"testing"

	"phptools/internal/model"
	"phptools/internal/store"
)

func TestDatabaseToolsPersistAndRemoveOnlyToolRecords(t *testing.T) {
	a := testApp(t)
	tool, err := a.AddDatabaseTool("phpmyadmin", "http://localhost:8081/phpmyadmin/")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = a.AddDatabaseTool("phpmyadmin", tool.URL); err != nil {
		t.Fatal(err)
	}
	records, err := store.List[model.DatabaseTool](a.db, "database-tool")
	if err != nil || len(records) != 1 || records[0].URL != tool.URL {
		t.Fatalf("tool not saved exactly once: %+v %v", records, err)
	}
	if err = a.db.Put("site", tool.ID, model.Site{ID: tool.ID}); err != nil {
		t.Fatal(err)
	}
	if err = a.RemoveDatabaseTool(tool.ID); err != nil {
		t.Fatal(err)
	}
	records, _ = store.List[model.DatabaseTool](a.db, "database-tool")
	sites, _ := store.List[model.Site](a.db, "site")
	if len(records) != 0 || len(sites) != 1 {
		t.Fatal("removing a tool touched other metadata")
	}
}

func TestDatabaseClientPickerCancelAndValidate(t *testing.T) {
	a := testApp(t)
	a.hooks.PickDatabaseClient = func() (string, error) { return "", nil }
	if path, err := a.SelectDatabaseClient(); err != nil || path != "" {
		t.Fatal("cancel should keep the current selection", path, err)
	}
	file := filepath.Join(t.TempDir(), "fake.exe")
	os.WriteFile(file, []byte("fake"), 0600)
	a.hooks.PickDatabaseClient = func() (string, error) { return file, nil }
	if _, err := a.SelectDatabaseClient(); err == nil {
		t.Fatal("picker accepted a fake executable")
	}
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	a.hooks.PickDatabaseClient = func() (string, error) { return exe, nil }
	if path, err := a.SelectDatabaseClient(); err != nil || path != exe {
		t.Fatal("picker rejected a valid executable", path, err)
	}
}

func TestExternalToolsRequireOwnedRunningMySQL(t *testing.T) {
	a := testApp(t)
	called := false
	a.hooks.OpenDatabaseClient = func(string) error { called = true; return nil }
	a.hooks.OpenURL = func(string) error { called = true; return nil }
	a.data.Instances = append(a.data.Instances,
		model.Instance{ID: "php", Kind: "php"},
		model.Instance{ID: "mysql-stopped", Kind: "mysql", State: "stopped"},
	)
	for _, id := range []string{"foreign", "php", "mysql-stopped"} {
		if err := a.OpenDatabaseTool(id, "demo", "any"); err == nil {
			t.Fatalf("invalid instance %q launched a tool", id)
		}
	}
	if called {
		t.Fatal("invalid operation called external application")
	}
}
