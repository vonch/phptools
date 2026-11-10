package desktop

import (
	"context"
	"os"
	"path/filepath"
	"testing"

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
