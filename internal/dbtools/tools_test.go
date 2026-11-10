package dbtools

import (
	"io"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"phptools/internal/model"
)

func TestCustomToolRejectsCommandsAndInvalidPrograms(t *testing.T) {
	for _, path := range []string{"navicat.exe", `cmd.exe /c navicat.exe`, filepath.Join(t.TempDir(), "missing.exe")} {
		if _, err := NewCustom("desktop", path); err == nil {
			t.Fatalf("accepted unavailable or command target %q", path)
		}
	}
	bad := filepath.Join(t.TempDir(), "not-a-program.exe")
	os.WriteFile(bad, []byte("not a PE file"), 0600)
	if _, err := NewCustom("desktop", bad); err == nil {
		t.Fatal("accepted fake executable")
	}
	// Copy this valid test program to a path containing spaces and Chinese.
	exe, _ := os.Executable()
	src, err := os.Open(exe)
	if err != nil {
		t.Fatal(err)
	}
	defer src.Close()
	path := filepath.Join(t.TempDir(), "中文 client.EXE")
	dst, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	_, err = io.Copy(dst, src)
	closeErr := dst.Close()
	if err != nil || closeErr != nil {
		t.Fatal(err, closeErr)
	}
	tool, err := NewCustom("desktop", path)
	if err != nil || !tool.CanOpen || tool.Path != path || !tool.Custom {
		t.Fatalf("valid custom executable: %+v %v", tool, err)
	}
	// Path casing must not create a second record on Windows.
	if tool.ID != toolID("desktop", strings.ToUpper(path)) {
		t.Fatal("case-sensitive desktop ID")
	}
}

func TestPHPMyAdminURLValidation(t *testing.T) {
	for _, target := range []string{"javascript:alert(1)", "file:///C:/index.php", "http://", "http://root:password@localhost/", "localhost/phpmyadmin", "http://localhost:invalid/"} {
		if _, err := NewCustom("phpmyadmin", target); err == nil {
			t.Fatalf("accepted invalid URL %q", target)
		}
	}
	tool, err := NewCustom("phpmyadmin", " http://localhost:8081/phpmyadmin/ ")
	if err != nil || tool.URL != "http://localhost:8081/phpmyadmin/" || !tool.CanOpen {
		t.Fatalf("valid URL rejected: %+v %v", tool, err)
	}
}

func TestSiteDiscoveryRequiresDistributionAndRespectsSiteState(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "phpMyAdmin 中文 space")
	os.MkdirAll(filepath.Join(dir, "libraries"), 0700)
	os.WriteFile(filepath.Join(dir, "index.php"), []byte("<?php /* PhpMyAdmin */"), 0600)
	fake := filepath.Join(root, "phpMyAdmin-fake")
	os.MkdirAll(fake, 0700)
	os.WriteFile(filepath.Join(fake, "index.php"), []byte("<?php echo 'hello';"), 0600)
	site := model.Site{Root: root, Domain: "tools.test", Port: 8081, PHPInstanceID: "php1", Enabled: true}
	c := &collector{tools: map[string]model.DatabaseTool{}}
	c.sites([]model.Site{site, site})
	if len(c.tools) != 1 {
		t.Fatalf("discovery did not deduplicate or accepted fake app: %+v", c.tools)
	}
	for _, tool := range c.tools {
		u, err := url.Parse(tool.URL)
		if err != nil || !tool.CanOpen || u.Host != "tools.test:8081" || u.Path != "/phpMyAdmin 中文 space/" {
			t.Fatalf("incorrect site URL: %+v %v", tool, err)
		}
	}
	site.Enabled = false
	c = &collector{tools: map[string]model.DatabaseTool{}}
	c.sites([]model.Site{site})
	for _, tool := range c.tools {
		if tool.CanOpen || tool.URL != "" {
			t.Fatal("disabled site advertised a usable URL")
		}
	}
}

func TestDisplayIconIsAPathNotCommand(t *testing.T) {
	for _, raw := range []string{`"C:\Program Files\Navicat\navicat.exe",0`, `C:\Program Files\Navicat\navicat.exe, 1`} {
		if got := iconPath(raw); got != `C:\Program Files\Navicat\navicat.exe` {
			t.Fatalf("icon path %q", got)
		}
	}
	if got := iconPath(`"C:\Navicat\navicat.exe" --anything`); strings.EqualFold(got, `C:\Navicat\navicat.exe`) {
		t.Fatal("silently interpreted a registry command as a program path")
	}
}
