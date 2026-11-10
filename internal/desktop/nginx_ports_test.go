package desktop

import (
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"phptools/internal/model"
	"phptools/internal/store"
)

func TestSaveSiteRejectsExternalSitePortAndRollsBackBeforeHosts(t *testing.T) {
	a := testApp(t)
	occupied, err := net.Listen("tcp4", "0.0.0.0:0")
	if err != nil {
		t.Fatal(err)
	}
	defer occupied.Close()
	port := occupied.Addr().(*net.TCPAddr).Port
	spare, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	primaryPort := spare.Addr().(*net.TCPAddr).Port
	spare.Close()
	pkg := model.Package{ID: "nginx-port-test", Kind: "nginx", Version: "1.31.7", Path: filepath.Join(a.data.Settings.Root, "packages", "nginx-port-test")}
	if err = os.MkdirAll(filepath.Join(pkg.Path, "conf"), 0700); err != nil {
		t.Fatal(err)
	}
	for name, contents := range map[string]string{
		"nginx.exe":           "fixture: must never execute",
		"conf/mime.types":     "types { text/html html; }",
		"conf/fastcgi_params": "fastcgi_param QUERY_STRING $query_string;",
	} {
		if err = os.WriteFile(filepath.Join(pkg.Path, name), []byte(contents), 0600); err != nil {
			t.Fatal(err)
		}
	}
	a.data.Packages = append(a.data.Packages, pkg)
	if _, err = a.CreateInstance(model.InstanceInput{Name: "Nginx port test", PackageID: pkg.ID, Port: primaryPort}); err != nil {
		t.Fatal(err)
	}
	project := t.TempDir()
	_, err = a.SaveSite(model.Site{Name: "Port conflict", Domain: "port-conflict.test", Root: project, Port: port, Enabled: true})
	// The fake executable cannot reach hosts even if the port guard regresses.
	// Check the exact failure reason so an executable error cannot pass this test.
	if err == nil || !strings.Contains(err.Error(), strconv.Itoa(port)) || !strings.Contains(err.Error(), "其他进程") {
		t.Fatalf("expected included site port conflict before nginx -t: %v", err)
	}
	if len(a.data.Sites) != 0 {
		t.Fatal("failed site entered the live snapshot")
	}
	stored, err := store.List[model.Site](a.db, "site")
	if err != nil || len(stored) != 0 {
		t.Fatalf("failed site persisted: %+v %v", stored, err)
	}
	files, err := filepath.Glob(filepath.Join(a.data.Settings.Root, "sites", "*.conf"))
	if err != nil || len(files) != 0 {
		t.Fatalf("failed site configuration was not rolled back: %v %v", files, err)
	}
}
