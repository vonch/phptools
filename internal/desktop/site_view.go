package desktop

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"phptools/internal/model"
)

func defaultSiteView() model.SiteViewPreferences {
	return model.SiteViewPreferences{
		HiddenColumns: []string{}, SiteOrder: []string{},
		SortBy: "manual", SortDirection: "asc",
	}
}

func normalizeSiteView(preferences model.SiteViewPreferences) (model.SiteViewPreferences, error) {
	unique := func(values []string) []string {
		result := make([]string, 0, len(values))
		seen := make(map[string]bool, len(values))
		for _, value := range values {
			if !seen[value] {
				result = append(result, value)
				seen[value] = true
			}
		}
		return result
	}
	preferences.HiddenColumns = unique(preferences.HiddenColumns)
	for _, column := range preferences.HiddenColumns {
		switch column {
		case "name", "root", "phpInstanceId", "enabled", "createdAt", "lastActiveAt", "actions":
		default:
			return model.SiteViewPreferences{}, errors.New("站点列表包含未知的列")
		}
	}
	if len(preferences.HiddenColumns) > 6 {
		return model.SiteViewPreferences{}, errors.New("站点列表至少保留一列显示")
	}
	preferences.SiteOrder = unique(preferences.SiteOrder)
	for _, id := range preferences.SiteOrder {
		if strings.TrimSpace(id) == "" {
			return model.SiteViewPreferences{}, errors.New("站点列表不能包含空站点标识")
		}
	}
	switch preferences.SortBy {
	case "manual", "name", "domain", "root", "environment", "enabled", "createdAt", "lastActiveAt":
	default:
		return model.SiteViewPreferences{}, errors.New("站点排序字段无效")
	}
	if preferences.SortDirection != "asc" && preferences.SortDirection != "desc" {
		return model.SiteViewPreferences{}, errors.New("站点排序方向无效")
	}
	return preferences, nil
}

func (a *App) loadSiteView() {
	a.data.SiteView = defaultSiteView()
	raw, err := os.ReadFile(filepath.Join(a.configDir, "site-view.json"))
	if errors.Is(err, os.ErrNotExist) {
		return
	}
	preferences := defaultSiteView()
	if err == nil {
		err = json.Unmarshal(raw, &preferences)
	}
	if err == nil {
		preferences, err = normalizeSiteView(preferences)
	}
	if err != nil {
		a.data.Diagnostics = append(a.data.Diagnostics, model.Diagnostic{
			Level: "warning", Message: "站点列表配置无法读取，已使用默认显示和排序：" + err.Error(),
		})
		return
	}
	a.data.SiteView = preferences
}

func (a *App) SaveSiteView(preferences model.SiteViewPreferences) error {
	preferences, err := normalizeSiteView(preferences)
	if err != nil {
		return err
	}
	if err = a.saveSiteView(preferences); err != nil {
		return fmt.Errorf("保存站点列表配置失败：%w", err)
	}
	a.notify()
	return nil
}

func (a *App) saveSiteView(preferences model.SiteViewPreferences) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.closing {
		return errors.New("软件正在停止服务")
	}
	if err := os.MkdirAll(a.configDir, 0700); err != nil {
		return err
	}
	raw, err := json.MarshalIndent(preferences, "", "  ")
	if err != nil {
		return err
	}
	tmp := filepath.Join(a.configDir, "site-view.json.tmp")
	defer os.Remove(tmp)
	if err = os.WriteFile(tmp, raw, 0600); err != nil {
		return err
	}
	if err = os.Rename(tmp, filepath.Join(a.configDir, "site-view.json")); err != nil {
		return err
	}
	a.data.SiteView = preferences
	return nil
}
