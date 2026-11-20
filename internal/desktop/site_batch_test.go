package desktop

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"phptools/internal/model"
)

func TestCreateSitesRollsBackEveryNewProject(t *testing.T) {
	a := testApp(t)
	if _, err := a.CreateSites(nil); err == nil || !strings.Contains(err.Error(), "请至少填写一个站点") {
		t.Fatal(err)
	}
	tooMany := make([]model.Site, maxBatchSites+1)
	for i := range tooMany {
		tooMany[i] = model.Site{Name: "站点", Domain: "batch.test", Port: 80, Enabled: true}
	}
	if _, err := a.CreateSites(tooMany); err == nil || !strings.Contains(err.Error(), "一次最多创建") {
		t.Fatal(err)
	}
	_, err := a.CreateSites([]model.Site{
		{Name: "商店", Domain: "shop.test", ProjectFolder: "shop", Port: 80, Enabled: true},
		{Name: "博客", Domain: "shop.test", ProjectFolder: "blog", Port: 80, Enabled: true},
	})
	if err == nil || !strings.Contains(err.Error(), "站点「博客」（shop.test）") || !strings.Contains(err.Error(), "已有站点") {
		t.Fatal(err)
	}
	pages, err := filepath.Glob(filepath.Join(a.data.Settings.Root, "sites", "*", "index.html"))
	if err != nil || len(pages) != 0 {
		t.Fatalf("rolled back projects remained: %v %v", pages, err)
	}
	_, err = a.CreateSites([]model.Site{
		{Name: "甲", Domain: "one.test", Port: 80, Enabled: true},
		{Name: "乙", Domain: "two.test", Root: t.TempDir(), Port: 80, Enabled: true},
	})
	if err == nil || !strings.Contains(err.Error(), "只能新建项目") {
		t.Fatal(err)
	}
	pages, err = filepath.Glob(filepath.Join(a.data.Settings.Root, "sites", "*", "index.html"))
	if err != nil || len(pages) != 0 {
		t.Fatalf("partial batch remained: %v %v", pages, err)
	}
	_, err = a.CreateSites([]model.Site{
		{Name: "甲", Domain: "one.test", Port: 80, Enabled: true},
		{Name: "乙", Domain: "two.test", Port: 80, Enabled: true},
	})
	if err == nil || !strings.Contains(err.Error(), "Nginx") {
		t.Fatal(err)
	}
	pages, err = filepath.Glob(filepath.Join(a.data.Settings.Root, "sites", "*", "index.html"))
	if err != nil || len(pages) != 0 {
		t.Fatalf("failed batch remained: %v %v", pages, err)
	}
	if _, err = os.Stat(filepath.Join(a.data.Settings.Root, "sites")); err != nil {
		t.Fatal(err)
	}
}
