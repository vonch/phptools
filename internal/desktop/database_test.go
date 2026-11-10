package desktop

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"phptools/internal/model"
)

func TestDatabaseOperationsRejectOtherInstanceKinds(t *testing.T) {
	a := testApp(t)
	a.data.Instances = append(a.data.Instances, model.Instance{ID: "php-test", Kind: "php"})
	if _, err := a.ListDatabases("php-test"); err == nil || !strings.Contains(err.Error(), "MySQL") {
		t.Fatalf("non-MySQL instance accepted: %v", err)
	}
	if err := a.CreateDatabase("missing", "example", "password123"); err == nil {
		t.Fatal("unknown instance accepted")
	}
	if _, err := a.ImportDatabase("php-test", "example", filepath.Join(t.TempDir(), "input.sql")); err == nil {
		t.Fatal("non-MySQL import accepted")
	}
	if len(a.data.Tasks) != 0 {
		t.Fatal("invalid operation created a background task")
	}
}

func TestSQLDialogsCancellationValidationAndNoOverwrite(t *testing.T) {
	a := testApp(t)
	if _, err := a.SelectSQLImport(); err == nil {
		t.Fatal("missing import dialog hook accepted")
	}
	a.hooks.PickSQLImport = func() (string, error) { return "", nil }
	if path, err := a.SelectSQLImport(); path != "" || err != nil {
		t.Fatalf("dialog cancellation is not an error: %q %v", path, err)
	}
	a.hooks.PickSQLImport = func() (string, error) { return "relative.sql", nil }
	if _, err := a.SelectSQLImport(); err == nil {
		t.Fatal("relative SQL path accepted")
	}
	dir := t.TempDir()
	source := filepath.Join(dir, "中文 input.SQL")
	a.hooks.PickSQLImport = func() (string, error) { return source, nil }
	if path, err := a.SelectSQLImport(); err != nil || path != source {
		t.Fatalf("absolute uppercase SQL extension rejected: %q %v", path, err)
	}
	target := filepath.Join(dir, "中文 output")
	a.hooks.PickSQLExport = func(suggested string) (string, error) {
		if strings.ContainsAny(suggested, `/\\:`) || !strings.HasSuffix(suggested, ".sql") {
			t.Fatalf("unsafe suggested filename: %q", suggested)
		}
		return target, nil
	}
	if path, err := a.SelectSQLExport(`../../database`); err != nil || path != target+".sql" {
		t.Fatalf("export extension not added: %q %v", path, err)
	}
	if err := os.WriteFile(target+".sql", []byte("keep"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := a.SelectSQLExport("example"); err == nil {
		t.Fatal("existing export accepted")
	}
	data, _ := os.ReadFile(target + ".sql")
	if string(data) != "keep" {
		t.Fatal("existing export modified")
	}
	a.hooks.PickSQLExport = func(string) (string, error) { return "", errors.New("dialog failed") }
	if _, err := a.SelectSQLExport("example"); err == nil {
		t.Fatal("dialog error hidden")
	}
}

func TestQueuedDatabaseImportCanCancelBeforeLifecycleLock(t *testing.T) {
	a := testApp(t)
	a.data.Instances = append(a.data.Instances, model.Instance{ID: "mysql-test", Kind: "mysql"})
	a.opMu.Lock()
	id, err := a.ImportDatabase("mysql-test", "example", filepath.Join(t.TempDir(), "input.sql"))
	if err != nil {
		a.opMu.Unlock()
		t.Fatal(err)
	}
	if err = a.CancelTask(id); err != nil {
		a.opMu.Unlock()
		t.Fatal(err)
	}
	a.opMu.Unlock()
	a.wg.Wait()
	if a.data.Tasks[0].State != "cancelled" {
		t.Fatalf("queued import not cancelled: %+v", a.data.Tasks[0])
	}
	if !strings.Contains(a.data.Tasks[0].Message, "部分 SQL") {
		t.Fatal("cancelled import lost the partial execution warning")
	}
}
