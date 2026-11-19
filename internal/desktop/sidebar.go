package desktop

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"unicode"
	"unicode/utf8"

	"phptools/internal/model"
)

func defaultSidebar() []model.SidebarItem {
	return []model.SidebarItem{
		{Key: "overview", Label: ""}, {Key: "sites", Label: ""},
		{Key: "certificates", Label: ""},
		{Key: "databases", Label: ""}, {Key: "packages", Label: ""},
		{Key: "instances", Label: ""}, {Key: "health", Label: ""},
		{Key: "logs", Label: ""}, {Key: "settings", Label: ""},
	}
}

const maxSidebarLabelRunes = 32

func normalizeSidebar(items []model.SidebarItem) []model.SidebarItem {
	result := append([]model.SidebarItem{}, items...)
	for i := range result {
		result[i].Label = strings.TrimSpace(result[i].Label)
	}
	return result
}

func validateSidebar(items []model.SidebarItem) error {
	known := make(map[string]bool)
	for _, item := range defaultSidebar() {
		known[item.Key] = true
	}
	if len(items) != len(known) {
		return errors.New("菜单列表必须包含全部菜单")
	}
	seen := make(map[string]bool)
	for _, item := range items {
		if !known[item.Key] || seen[item.Key] {
			return errors.New("菜单列表包含未知或重复的菜单")
		}
		if item.Key == "settings" && item.Hidden {
			return errors.New("设置菜单必须保持显示，以便恢复其他菜单")
		}
		if !utf8.ValidString(item.Label) {
			return errors.New("菜单名称不是有效的 UTF-8 文本")
		}
		label := strings.TrimSpace(item.Label)
		if utf8.RuneCountInString(label) > maxSidebarLabelRunes {
			return fmt.Errorf("菜单名称最多 %d 个字符", maxSidebarLabelRunes)
		}
		for _, r := range label {
			if unicode.IsControl(r) {
				return errors.New("菜单名称不能包含控制字符")
			}
		}
		seen[item.Key] = true
	}
	return nil
}

func withDefaultMenus(items []model.SidebarItem) []model.SidebarItem {
	defaults := defaultSidebar()
	known := make(map[string]bool, len(defaults))
	for _, item := range defaults {
		known[item.Key] = true
	}
	present := make(map[string]bool, len(items))
	result := make([]model.SidebarItem, 0, len(defaults))
	for _, item := range items {
		if !known[item.Key] || present[item.Key] {
			continue
		}
		present[item.Key] = true
		result = append(result, item)
	}
	for index, item := range defaults {
		if present[item.Key] {
			continue
		}
		insertAt := len(result)
		for _, later := range defaults[index+1:] {
			for at, existing := range result {
				if existing.Key == later.Key {
					insertAt = at
					break
				}
			}
			if insertAt != len(result) {
				break
			}
		}
		result = append(result, model.SidebarItem{})
		copy(result[insertAt+1:], result[insertAt:])
		result[insertAt] = item
		present[item.Key] = true
	}
	return result
}

func (a *App) loadSidebar() {
	a.data.Sidebar = defaultSidebar()
	raw, err := os.ReadFile(filepath.Join(a.configDir, "sidebar.json"))
	if errors.Is(err, os.ErrNotExist) {
		return
	}
	var items []model.SidebarItem
	if err == nil {
		err = json.Unmarshal(raw, &items)
	}
	if err == nil {
		items = withDefaultMenus(normalizeSidebar(items))
		err = validateSidebar(items)
	}
	if err != nil {
		a.data.Diagnostics = append(a.data.Diagnostics, model.Diagnostic{
			Level: "warning", Message: "侧栏菜单配置无法读取，已使用默认菜单：" + err.Error(),
		})
		return
	}
	a.data.Sidebar = items
}

func (a *App) SaveSidebar(items []model.SidebarItem) error {
	items = normalizeSidebar(items)
	if err := validateSidebar(items); err != nil {
		return err
	}
	if err := a.saveSidebar(items); err != nil {
		return fmt.Errorf("保存侧栏菜单失败：%w", err)
	}
	a.notify()
	return nil
}

func (a *App) saveSidebar(items []model.SidebarItem) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.closing {
		return errors.New("软件正在停止服务")
	}
	if err := os.MkdirAll(a.configDir, 0700); err != nil {
		return err
	}
	raw, err := json.MarshalIndent(items, "", "  ")
	if err != nil {
		return err
	}
	tmp := filepath.Join(a.configDir, "sidebar.json.tmp")
	defer os.Remove(tmp)
	if err = os.WriteFile(tmp, raw, 0600); err != nil {
		return err
	}
	if err = os.Rename(tmp, filepath.Join(a.configDir, "sidebar.json")); err != nil {
		return err
	}
	a.data.Sidebar = append([]model.SidebarItem{}, items...)
	return nil
}
