package desktop

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/zalando/go-keyring"
	"phptools/internal/model"
)

func TestInstalledPHPGetsAnInstanceWithoutAManualCreate(t *testing.T) {
	a := testApp(t)
	pkg := model.Package{ID: "php-8.3.1-x64", Kind: "php", Version: "8.3.1", Path: filepath.Join(a.data.Settings.Root, "packages", "php-8.3.1-x64")}
	if err := os.MkdirAll(pkg.Path, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(pkg.Path, "php-cgi.exe"), []byte("not executed"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := a.db.Put("package", pkg.ID, pkg); err != nil {
		t.Fatal(err)
	}
	a.data.Packages = append(a.data.Packages, pkg)
	if err := a.ensureInstalledPHPInstances(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := a.ensureInstalledPHPInstances(context.Background()); err != nil {
		t.Fatal(err)
	}
	var found []model.Instance
	for _, inst := range a.data.Instances {
		if inst.PackageID == pkg.ID {
			found = append(found, inst)
		}
	}
	if len(found) != 1 || found[0].Name != "PHP 8.3.1" || found[0].Port != 9100 || found[0].Kind != "php" {
		t.Fatalf("installed PHP instance = %+v", found)
	}
}

func TestInstalledNginxGetsAnInstance(t *testing.T) {
	a := testApp(t)
	pkg := model.Package{ID: "nginx-1.30.6-windows", Kind: "nginx", Version: "1.30.6", Path: filepath.Join(a.data.Settings.Root, "packages", "nginx-1.30.6-windows")}
	conf := filepath.Join(pkg.Path, "conf")
	if err := os.MkdirAll(conf, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(pkg.Path, "nginx.exe"), []byte("not executed"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(conf, "fastcgi_params"), []byte("fastcgi_param QUERY_STRING $query_string;\n"), 0600); err != nil {
		t.Fatal(err)
	}
	a.data.Packages = append(a.data.Packages, pkg)
	if err := a.ensureServiceInstances(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := a.ensureServiceInstances(context.Background()); err != nil {
		t.Fatal(err)
	}
	var found []model.Instance
	for _, inst := range a.data.Instances {
		if inst.PackageID == pkg.ID {
			found = append(found, inst)
		}
	}
	if len(found) != 1 || found[0].Name != "Nginx 1.30.6" || found[0].Port != 8081 {
		t.Fatalf("installed Nginx instance = %+v", found)
	}
}

func TestInstalledMySQLStoresAPassword(t *testing.T) {
	a := testApp(t)
	pkg := model.Package{ID: "mysql-5.7.44-x64", Kind: "mysql", Version: "5.7.44", Path: filepath.Join(a.data.Settings.Root, "packages", "mysql-5.7.44-x64")}
	if err := os.MkdirAll(filepath.Join(pkg.Path, "bin"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(pkg.Path, "bin", "mysqld.exe"), []byte("not executed"), 0600); err != nil {
		t.Fatal(err)
	}
	a.data.Packages = append(a.data.Packages, pkg)
	if err := a.ensureServiceInstances(context.Background()); err != nil {
		t.Fatal(err)
	}
	var found model.Instance
	for _, inst := range a.data.Instances {
		if inst.PackageID == pkg.ID {
			found = inst
		}
	}
	if found.ID == "" || found.Name != "MySQL 5.7.44" || found.Port != 3307 {
		t.Fatalf("installed MySQL instance = %+v", found)
	}
	t.Cleanup(func() { _ = keyring.Delete(a.credentialService(), found.ID) })
	password, err := a.getSecret(found.ID)
	if err != nil || len(password) < 8 {
		t.Fatalf("stored password: %q %v", password, err)
	}
	raw, err := os.ReadFile(filepath.Join(a.data.Settings.Root, "metadata.db"))
	if err == nil && strings.Contains(string(raw), password) {
		t.Fatal("plaintext password in metadata")
	}
}
