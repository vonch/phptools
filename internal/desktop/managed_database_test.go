package desktop

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/zalando/go-keyring"
	"phptools/internal/model"
	"phptools/internal/store"
)

func TestManagedDatabaseIsStoredBeforeMySQLIsRunning(t *testing.T) {
	a := testApp(t)
	root := a.data.Settings.Root
	pkg := model.Package{ID: "mysql-managed", Kind: "mysql", Version: "5.7.44", Path: filepath.Join(root, "packages", "mysql-managed")}
	if err := os.MkdirAll(filepath.Join(pkg.Path, "bin"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(pkg.Path, "bin", "mysqld.exe"), nil, 0600); err != nil {
		t.Fatal(err)
	}
	a.data.Packages = append(a.data.Packages, pkg)
	instance, err := a.CreateInstance(model.InstanceInput{Name: "目录 MySQL", PackageID: pkg.ID, Port: 23380, Password: "Instance-pass-1"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = keyring.Delete(a.credentialService(), instance.ID) })
	const password = "Database-pass-1"
	created, err := a.CreateManagedDatabase(instance.ID, "shop_db", password)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = keyring.Delete(a.credentialService(), databaseCredentialID(created.ID)) })
	if created.State != "pending" || created.User != "shop_db" || created.Host != "localhost" || created.Password != password {
		t.Fatalf("record was not saved independently of MySQL: %+v", created)
	}
	for _, name := range []string{"metadata.db", "metadata.db-wal"} {
		raw, err := os.ReadFile(filepath.Join(root, name))
		if err == nil && strings.Contains(string(raw), password) {
			t.Fatal("database password was written into metadata")
		}
	}
	if _, err = a.CreateManagedDatabase(instance.ID, "shop_db", password); err == nil {
		t.Fatal("duplicate software record accepted")
	}
	listed, err := a.ListManagedDatabases(instance.ID)
	if err != nil || len(listed) != 1 || listed[0].Password != password || listed[0].State != "pending" {
		t.Fatalf("list: %+v %v", listed, err)
	}
	again, err := a.SyncManagedDatabase(created.ID)
	if err != nil || again.State != "pending" {
		t.Fatalf("stopped MySQL was treated as a direct database create: %+v %v", again, err)
	}
	if err = a.DeleteManagedDatabase(created.ID); err != nil {
		t.Fatal(err)
	}
	records, err := store.List[model.ManagedDatabase](a.db, managedDatabaseKind)
	if err != nil || len(records) != 0 {
		t.Fatalf("record remained after delete: %+v %v", records, err)
	}
	if _, err = keyring.Get(a.credentialService(), databaseCredentialID(created.ID)); err == nil {
		t.Fatal("deleted database kept its password")
	}
}
