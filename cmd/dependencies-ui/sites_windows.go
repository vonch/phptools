//go:build windows

package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"phptools/internal/model"
	"phptools/internal/store"
)

// Only seeds metadata in the root owned by the acceptance runner.
// No services, passwords, project files or hosts entries are created.
func prepareSiteFixtures(profile, root string) error {
	runtimeRoot := filepath.Join(root, "独立 runtime")
	db, err := store.Open(runtimeRoot)
	if err != nil {
		return err
	}
	defer db.Close()
	project := filepath.Join(root, "目录验收 中文, project")
	if err := os.Mkdir(project, 0700); err != nil {
		return err
	}
	sites := []model.Site{
		{ID: "a-old", Name: "Alpha 项目", Domain: "alpha.test", UpdatedAt: "2026-10-20T09:00:00Z"},
		{ID: "b-legacy", Name: "旧版站点", Domain: "legacy.test"},
		{ID: "c-latest", Name: "喜慧通", Domain: "xihuitong.test", UpdatedAt: "2026-10-20T10:30:00Z"},
		{ID: "d-created", Name: "Demo 10", Domain: "demo10.test", CreatedAt: "2026-10-20T10:00:00Z"},
		{ID: "e-edited", Name: "Demo 2", Domain: "demo2.test", UpdatedAt: "2026-10-20T09:45:00Z"},
	}
	for index := 1; index <= 50; index++ {
		sites = append(sites, model.Site{
			ID: fmt.Sprintf("fixture-%02d", index), Name: fmt.Sprintf("ZZ Test %02d", index),
			Domain: fmt.Sprintf("fixture-%02d.test", index), UpdatedAt: "2026-10-19T09:00:00Z",
		})
	}
	for _, site := range sites {
		site.Root, site.Port = filepath.ToSlash(project), 8081
		if site.ID == "b-legacy" {
			site.Root = filepath.Join(root, "missing-project")
		}
		if err := db.Put("site", site.ID, site); err != nil {
			return err
		}
	}
	if err := os.MkdirAll(profile, 0700); err != nil {
		return err
	}
	// Saved preferences must not resurrect removed actions or change sort order.
	if err := os.WriteFile(filepath.Join(profile, "site-view.json"), []byte(`{"showLogs":true,"pinnedSiteIds":["b-legacy"]}`), 0600); err != nil {
		return err
	}
	config, err := json.Marshal(map[string]string{"root": runtimeRoot})
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(profile, "settings.json"), config, 0600)
}
