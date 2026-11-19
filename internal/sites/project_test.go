package sites

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestNewProjectCreatesHomepageUnderSites(t *testing.T) {
	env := t.TempDir()
	taken := filepath.Join(env, "sites", "yanshi")
	if err := os.MkdirAll(taken, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(taken, "keep.txt"), []byte("keep"), 0600); err != nil {
		t.Fatal(err)
	}
	dir, err := CreateNewProject(env, "演示", "demo.test", "yanshi")
	if err != nil {
		t.Fatal(err)
	}
	if dir != filepath.Join(env, "sites", "yanshi2") {
		t.Fatal(dir)
	}
	if data, err := os.ReadFile(filepath.Join(taken, "keep.txt")); err != nil || string(data) != "keep" {
		t.Fatal("existing project was changed")
	}
	page, err := os.ReadFile(filepath.Join(dir, "index.html"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(page), "<h1>创建成功</h1>") || !strings.Contains(string(page), "站点「演示」已创建") {
		t.Fatal(string(page))
	}
	unsafe, err := CreateNewProject(env, `<script>alert("x")</script>`, "safe.test", "")
	if err != nil {
		t.Fatal(err)
	}
	page, err = os.ReadFile(filepath.Join(unsafe, "index.html"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(page), "<script>alert") || !strings.Contains(string(page), "&lt;script&gt;") {
		t.Fatal(string(page))
	}
	parent, err := os.MkdirTemp(env, "outside")
	if err != nil {
		t.Fatal(err)
	}
	escaped, err := CreateNewProject(env, filepath.Join("..", filepath.Base(parent)), "escape.test", "")
	if err != nil {
		t.Fatal(err)
	}
	rel, err := filepath.Rel(filepath.Join(env, "sites"), escaped)
	if err != nil || strings.HasPrefix(rel, "..") {
		t.Fatal(escaped)
	}
	reserved, err := CreateNewProject(env, "CON", "aux.test", "")
	if err != nil || !strings.HasSuffix(reserved, "consite") {
		t.Fatalf("%s %v", reserved, err)
	}
}

func TestProjectFolderNameUsesLowercaseEnglish(t *testing.T) {
	if got := projectFolderName("演示", "other.test", "  Yan Shi! "); got != "yanshi" {
		t.Fatal(got)
	}
	if got := projectFolderName("My Site", "ignored.test", ""); got != "mysite" {
		t.Fatal(got)
	}
	if got := projectFolderName("a/b:c", "ignored.test", ""); got != "abc" {
		t.Fatal(got)
	}
	if got := projectFolderName(" .. ", "shop.test", ""); got != "shop" {
		t.Fatal(got)
	}
	if got := projectFolderName("", "", ""); got != "site" {
		t.Fatal(got)
	}
	if got := projectFolderName("CON", "aux.test", "CON"); got != "consite" {
		t.Fatal(got)
	}
}
