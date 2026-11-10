package sites

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"phptools/internal/model"
)

func TestHostsPreservesOtherEntries(t *testing.T) {
	input := []byte("127.0.0.1 old.test\r\n# BEGIN PHPTOOLS\r\n127.0.0.1 before.test\r\n# END PHPTOOLS\r\n")
	got, err := HostsContent(input, []string{"new.test"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(got), "old.test") || strings.Contains(string(got), "before.test") || !strings.Contains(string(got), "new.test") {
		t.Fatalf("%s", got)
	}
	if _, err := HostsContent([]byte("# BEGIN PHPTOOLS\nkeep.test"), nil); err == nil {
		t.Fatal("accepted malformed block")
	}
}

func TestValidateTraversalAndRender(t *testing.T) {
	root := t.TempDir()
	site := model.Site{ID: "test1", Domain: "demo.test", Root: root, Port: 8081, Enabled: true}
	if _, err := Validate(site, nil, nil); err != nil {
		t.Fatal(err)
	}
	site.Entry = "../"
	if _, err := Validate(site, nil, nil); err == nil {
		t.Fatal("accepted traversal")
	}
	site.Entry = ""
	site.Domain = "x;return 200"
	if _, err := Validate(site, nil, nil); err == nil {
		t.Fatal("accepted domain injection")
	}
	site.Domain = "demo.test"
	php := model.Instance{Port: 9100}
	render := Render(root, site, &php)
	if !strings.Contains(render, "fastcgi_pass 127.0.0.1:9100") || !strings.Contains(render, "try_files $uri =404") {
		t.Fatal(render)
	}
}

func TestApplyRollback(t *testing.T) {
	root := t.TempDir()
	os.Mkdir(filepath.Join(root, "sites"), 0700)
	s := model.Site{ID: "one", Domain: "one.test", Root: root, Port: 8081, Enabled: true}
	rollback, err := Apply(root, []model.Site{s}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = os.Stat(filepath.Join(root, "sites", "one.conf")); err != nil {
		t.Fatal(err)
	}
	if err = rollback(); err != nil {
		t.Fatal(err)
	}
	if _, err = os.Stat(filepath.Join(root, "sites", "one.conf")); !os.IsNotExist(err) {
		t.Fatal("rollback did not remove new site")
	}
}

func TestBlankRewriteStillUsesDefaultLocation(t *testing.T) {
	site := model.Site{ID: "blank", Domain: "blank.test", Port: 8081, Rewrite: " \r\n\t "}
	for _, php := range []*model.Instance{nil, {Port: 9100}} {
		rendered := Render(t.TempDir(), site, php)
		if !strings.Contains(rendered, "location / {") {
			t.Fatal("blank rules removed the default route")
		}
		if php != nil && !strings.Contains(rendered, "/index.php?$query_string") {
			t.Fatal("blank rules removed PHP fallback")
		}
	}
}

func TestHostsPreservesOriginalBytesAndRejectsConflicts(t *testing.T) {
	for _, raw := range []string{"# Existing\n127.0.0.1 old.test", "\ufeff# Existing\r\n127.0.0.1 old.test\r\n"} {
		with, err := HostsContent([]byte(raw), []string{"new.test"})
		if err != nil {
			t.Fatal(err)
		}
		without, err := HostsContent(with, nil)
		if err != nil || !bytes.Equal(without, []byte(raw)) {
			t.Fatal("outside managed block bytes changed")
		}
	}
	if _, err := HostsContent([]byte("192.168.1.5 occupied.test # old mapping\n"), []string{"occupied.test"}); err == nil {
		t.Fatal("accepted conflicting hosts mapping")
	}
}

func TestLocalhostSiteUsesSystemResolution(t *testing.T) {
	site := model.Site{ID: "local", Domain: " LOCALHOST ", Root: t.TempDir(), Port: 8081, Enabled: true}
	validated, err := Validate(site, nil, nil)
	if err != nil || validated.Domain != "localhost" {
		t.Fatalf("localhost validation: %+v, %v", validated, err)
	}
	all := []model.Site{validated, {Domain: "demo.test", Enabled: true}, {Domain: "disabled.test"}}
	if got := Domains(all); len(got) != 1 || got[0] != "demo.test" {
		t.Fatalf("managed hosts included system localhost: %v", got)
	}
	if ValidDomain("localserver") || ValidDomain("localhost;return 200") {
		t.Fatal("localhost exception accepted another single label or injection")
	}
}

func TestPHPPathInfoIsExplicitAndPreservesCustomLocations(t *testing.T) {
	site := model.Site{ID: "path", Domain: "path.test", Root: t.TempDir(), Port: 8081,
		Rewrite: "location /web/ { try_files $uri $uri/ /web/index.html; }\nlocation / { index index.php index.html; }"}
	php := &model.Instance{Port: 9101}
	ordinary := Render(site.Root, site, php)
	if strings.Contains(ordinary, "PATH_INFO") || !strings.Contains(ordinary, "location ~ \\.php$ {") || !strings.Contains(ordinary, "try_files $uri =404;") {
		t.Fatal("ordinary PHP handler changed:", ordinary)
	}
	site.PHPPathInfo = true
	rendered := Render(site.Root, site, php)
	for _, expected := range []string{site.Rewrite, "location ~ \\.php(.*)$ {", "fastcgi_split_path_info ^((?U).+\\.php)(/?.+)$;", "fastcgi_pass 127.0.0.1:9101;", "fastcgi_param SCRIPT_FILENAME $document_root$fastcgi_script_name;", "fastcgi_param PATH_INFO $fastcgi_path_info;", "fastcgi_param PATH_TRANSLATED $document_root$fastcgi_path_info;"} {
		if !strings.Contains(rendered, expected) {
			t.Fatalf("PATH_INFO handler missing %q:\n%s", expected, rendered)
		}
	}
	if strings.Contains(rendered, "try_files $uri =404;") {
		t.Fatal("PATH_INFO request would be rejected as a literal file path")
	}
	if static := Render(site.Root, site, nil); strings.Contains(static, "fastcgi_pass") {
		t.Fatal("PATH_INFO flag enabled PHP without an instance")
	}
}
