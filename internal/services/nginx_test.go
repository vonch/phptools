package services

import (
	"context"
	"errors"
	"net/netip"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"phptools/internal/model"
)

func TestNginxScannerHandlesInlineCommentsAndQuotes(t *testing.T) {
	ds, err := scanNginx("# pid ignored;\npid \"D:/中文 目录/nginx.pid\"; error_log 'D:/logs/#error.log'; events { worker_connections 1024; }\n")
	if err != nil {
		t.Fatal(err)
	}
	if len(ds) != 4 || ds[0].args[1] != "D:/中文 目录/nginx.pid" || ds[1].args[1] != "D:/logs/#error.log" || ds[3].depth != 1 {
		t.Fatalf("%+v", ds)
	}
	for _, bad := range []string{"pid \"open;", "events { worker_connections 1;", "pid path", "pid path; }"} {
		if _, err = scanNginx(bad); err == nil {
			t.Fatalf("accepted %q", bad)
		}
	}
}

func nginxConfigFixture(t *testing.T) (*Manager, model.Instance) {
	t.Helper()
	root := t.TempDir()
	m := New(root, nil, nil)
	pkg := fixturePackage(t, root, "nginx", "1.31.8")
	if err := os.WriteFile(filepath.Join(pkg.Path, "conf", "mime.types"), []byte("types { text/html html; }\n"), 0600); err != nil {
		t.Fatal(err)
	}
	i, err := m.Create(context.Background(), pkg, model.InstanceInput{Name: "Nginx", PackageID: pkg.ID, Port: 19991})
	if err != nil {
		t.Fatal(err)
	}
	return m, i
}

func TestAdoptSiteConfigDirectoryRewritesTheOldInclude(t *testing.T) {
	m, i := nginxConfigFixture(t)
	previous := nginxQuote(filepath.Join(m.root, "sites", "*.conf"))
	current := nginxQuote(m.siteConfigGlob())
	data, err := os.ReadFile(i.ConfigPath)
	if err != nil || !strings.Contains(string(data), current) {
		t.Fatal("new nginx config does not include conf")
	}
	if err = os.WriteFile(i.ConfigPath, []byte(strings.Replace(string(data), current, previous, 1)), 0600); err != nil {
		t.Fatal(err)
	}
	if err = m.AdoptSiteConfigDirectory(); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(i.ConfigPath)
	if err != nil || strings.Contains(string(got), previous) || !strings.Contains(string(got), current) {
		t.Fatalf("%s %v", got, err)
	}
}

func TestNginxListenEndpointsIncludeSitePortsAndImplicitDefaults(t *testing.T) {
	m, i := nginxConfigFixture(t)
	config := `# listen 127.0.0.1:9999;
server { listen 127.0.0.1:80; listen 127.0.0.1:80; include fastcgi_params; }
server { listen [::1]:443 ssl; }
server { server_name implicit.test; }
`
	if err := os.WriteFile(filepath.Join(m.root, "conf", "site.conf"), []byte(config), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(m.root, "conf", "disabled.conf.disabled"), []byte("server { listen 127.0.0.1:9998; }"), 0600); err != nil {
		t.Fatal(err)
	}
	listens, err := m.nginxListenEndpoints(i)
	if err != nil {
		t.Fatal(err)
	}
	expected := []nginxListen{
		{address: netip.MustParseAddr("0.0.0.0"), port: 80},
		{address: netip.MustParseAddr("127.0.0.1"), port: 80},
		{address: netip.MustParseAddr("::1"), port: 443},
		{address: netip.MustParseAddr("127.0.0.1"), port: 19991},
	}
	if !reflect.DeepEqual(listens, expected) {
		t.Fatalf("got %+v, expected %+v", listens, expected)
	}
	// The instance itself uses 19991; a phpStudy wildcard listener on 80 must
	// still prevent startup because an included site also asks for port 80.
	listener := nginxListener{nginxListen: nginxListen{address: netip.IPv4Unspecified(), port: 80}, pid: 100}
	if err := checkNginxListeners(listens, []nginxListener{listener}, func(int) bool { return false }); err == nil || !strings.Contains(err.Error(), "80") {
		t.Fatalf("included port conflict was missed: %v", err)
	}
}

func TestNginxListenEndpointsCheckRepeatedIncludeInEachServer(t *testing.T) {
	m, i := nginxConfigFixture(t)
	if err := os.WriteFile(filepath.Join(filepath.Dir(i.ConfigPath), "fastcgi_params"), []byte("listen 127.0.0.1:10080;\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(m.root, "conf", "site.conf"), []byte("server { include fastcgi_params; }\nserver { include fastcgi_params; }\n"), 0600); err != nil {
		t.Fatal(err)
	}
	listens, err := m.nginxListenEndpoints(i)
	if err != nil {
		t.Fatal(err)
	}
	if len(listens) != 2 || listens[0].port != 10080 || listens[1].port != 19991 {
		t.Fatalf("repeated include was skipped: %+v", listens)
	}
	if err := os.WriteFile(filepath.Join(filepath.Dir(i.ConfigPath), "fastcgi_params"), []byte("include fastcgi_params;\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := m.nginxListenEndpoints(i); err == nil || !strings.Contains(err.Error(), "循环") {
		t.Fatalf("include cycle was accepted: %v", err)
	}
}

func TestNginxListenAddressesAndExternalOwners(t *testing.T) {
	for _, tc := range []struct {
		value, address string
		port           uint16
	}{
		{"80", "0.0.0.0", 80},
		{"*:443", "0.0.0.0", 443},
		{"127.0.0.1:8080", "127.0.0.1", 8080},
		{"127.0.0.1", "127.0.0.1", 80},
		{"[::]:443", "::", 443},
		{"[::1]:8443", "::1", 8443},
		{"[::1]", "::1", 80},
	} {
		t.Run(tc.value, func(t *testing.T) {
			got, err := parseNginxListen(tc.value)
			if err != nil || got.address.String() != tc.address || got.port != tc.port {
				t.Fatalf("%+v %v", got, err)
			}
		})
	}
	for _, value := range []string{"0", "65536", "localhost:80", "unix:nginx.sock", "$PORT", "127.0.0.1:http", "[::1]:"} {
		if _, err := parseNginxListen(value); err == nil {
			t.Fatalf("accepted uncheckable listen %q", value)
		}
	}
	requested := nginxListen{address: netip.MustParseAddr("127.0.0.1"), port: 80}
	for _, tc := range []struct {
		address string
		port    uint16
		owned   bool
		blocked bool
	}{
		{"0.0.0.0", 80, false, true},
		{"127.0.0.1", 80, false, true},
		{"::", 80, false, true},
		{"127.0.0.1", 80, true, false},
		{"0.0.0.0", 81, false, false},
		{"127.0.0.2", 80, false, false},
		{"::1", 80, false, false},
	} {
		row := nginxListener{nginxListen: nginxListen{address: netip.MustParseAddr(tc.address), port: tc.port}, pid: 123}
		err := checkNginxListeners([]nginxListen{requested}, []nginxListener{row}, func(pid int) bool { return pid == 123 && tc.owned })
		if (err != nil) != tc.blocked {
			t.Fatalf("listener %+v owned=%v: %v", row, tc.owned, err)
		}
	}
	// An owned listener does not hide another program sharing the same port.
	rows := []nginxListener{
		{nginxListen: requested, pid: 123},
		{nginxListen: nginxListen{address: netip.IPv4Unspecified(), port: 80}, pid: 456},
	}
	if err := checkNginxListeners([]nginxListen{requested}, rows, func(pid int) bool { return pid == 123 }); err == nil || !strings.Contains(err.Error(), "456") {
		t.Fatalf("external owner hidden by own worker: %v", err)
	}
}

func TestNginxListenerWorkerOwnershipRequiresExactMasterAndConfig(t *testing.T) {
	i := model.Instance{State: "running", PID: 100, ProcessCreated: 500, Executable: filepath.Join(t.TempDir(), "nginx.exe"), ConfigPath: filepath.Join(t.TempDir(), "nginx.conf")}
	parents := map[int]int{200: 100, 300: 100, 400: 100, 500: 100, 600: 200, 700: 999}
	identity := func(pid int) (uint64, string, error) {
		switch pid {
		case 300:
			return 400, i.Executable, nil // predates this master: reused parent PID
		case 400:
			return 600, filepath.Join(t.TempDir(), "foreign.exe"), nil
		}
		return 600, i.Executable, nil
	}
	verify := func(p processRecord) error {
		if p.PID == 500 || p.ConfigArgument != i.ConfigPath {
			return errors.New("wrong configuration command line")
		}
		return nil
	}
	owned, err := nginxOwnedProcessIDs(i, parents, identity, verify)
	if err != nil || !reflect.DeepEqual(owned, map[int]bool{100: true, 200: true}) {
		t.Fatalf("untrusted worker accepted: %+v %v", owned, err)
	}
	if _, err := nginxOwnedProcessIDs(i, parents, identity, func(processRecord) error { return errProcessGone }); !errors.Is(err, errProcessGone) {
		t.Fatalf("unverified master accepted: %v", err)
	}
	i.State = "stopped"
	owned, err = nginxOwnedProcessIDs(i, parents, identity, verify)
	if err != nil || len(owned) != 0 {
		t.Fatalf("stale stopped master accepted: %+v %v", owned, err)
	}
}

func TestNginxManagedPIDCannotBeChangedOrHiddenByInlineDirective(t *testing.T) {
	root := t.TempDir()
	m := New(root, nil, nil)
	pkg := fixturePackage(t, root, "nginx", "1.31.8")
	if err := os.WriteFile(filepath.Join(pkg.Path, "conf", "mime.types"), []byte("types { text/html html; }\n"), 0600); err != nil {
		t.Fatal(err)
	}
	i, err := m.Create(context.Background(), pkg, model.InstanceInput{Name: "Nginx", PackageID: pkg.ID, Port: 19991})
	if err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(i.ConfigPath)
	if err != nil {
		t.Fatal(err)
	}
	if err = m.validateNginxConfig(i); err != nil {
		t.Fatal(err)
	}
	oldPID := "pid " + nginxQuote(filepath.Join(root, "instances", i.ID, "nginx.pid")) + ";"
	for name, bad := range map[string]string{
		"external pid":      strings.Replace(string(b), oldPID, "pid \"D:/phpstudy/nginx.pid\";", 1),
		"second inline pid": string(b) + "pid \"D:/phpstudy/nginx.pid\";\n",
		"missing pid":       strings.Replace(string(b), oldPID, "# removed", 1),
		"external log":      string(b) + "error_log \"D:/phpstudy/error.log\";\n",
		"external include":  string(b) + "include \"D:/phpstudy/nginx.conf\";\n",
		"external temp":     strings.Replace(string(b), "http {", "http { client_body_temp_path \"D:/phpstudy/tmp\";", 1),
	} {
		t.Run(name, func(t *testing.T) {
			if err := os.WriteFile(i.ConfigPath, []byte(bad), 0600); err != nil {
				t.Fatal(err)
			}
			if err := m.validateNginxConfig(i); err == nil {
				t.Fatal("unsafe config accepted")
			}
		})
	}
}
