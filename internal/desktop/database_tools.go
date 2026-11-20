package desktop

import (
	"context"
	"errors"

	"phptools/internal/dbtools"
	"phptools/internal/model"
	"phptools/internal/store"
)

func (a *App) detectDatabaseTools() ([]model.DatabaseTool, error) {
	if err := a.needRoot(); err != nil {
		return nil, err
	}
	saved, err := store.List[model.DatabaseTool](a.db, "database-tool")
	if err != nil {
		return nil, err
	}
	a.mu.RLock()
	sites := append([]model.Site{}, a.data.Sites...)
	a.mu.RUnlock()
	return dbtools.Detect(sites, saved), nil
}

func (a *App) DetectDatabaseTools() ([]model.DatabaseTool, error) {
	a.opMu.Lock()
	defer a.opMu.Unlock()
	return a.detectDatabaseTools()
}

func (a *App) SelectDatabaseClient() (string, error) {
	if a.hooks.PickDatabaseClient == nil {
		return "", errors.New("客户端选择器尚未初始化")
	}
	path, err := withoutDialogCancel(a.hooks.PickDatabaseClient())
	if err != nil || path == "" {
		return path, err
	}
	if err = dbtools.ValidateClient(path); err != nil {
		return "", err
	}
	return path, nil
}

func (a *App) AddDatabaseTool(kind, target string) (model.DatabaseTool, error) {
	a.opMu.Lock()
	defer a.opMu.Unlock()
	t, err := dbtools.NewCustom(kind, target)
	if err != nil {
		return t, err
	}
	if err = a.needRoot(); err != nil {
		return t, err
	}
	return t, a.db.Put("database-tool", t.ID, t)
}

func (a *App) RemoveDatabaseTool(id string) error {
	a.opMu.Lock()
	defer a.opMu.Unlock()
	if err := a.needRoot(); err != nil {
		return err
	}
	return a.db.Delete("database-tool", id)
}

func (a *App) OpenDatabaseTool(instanceID, database, toolID string) error {
	return a.databaseOperation(instanceID, func(ctx context.Context, instance model.Instance) error {
		// Recheck the owned, running server and database after a potentially long
		// tool selection, so a deleted database or stopped instance cannot launch.
		list, err := a.manager.ListDatabases(ctx, instance)
		if err != nil {
			return err
		}
		found := false
		for _, db := range list {
			if db.Name == database && !db.System {
				found = true
				break
			}
		}
		if !found {
			return errors.New("目标数据库已不存在或属于系统数据库，请刷新列表")
		}
		tools, err := a.detectDatabaseTools()
		if err != nil {
			return err
		}
		for _, tool := range tools {
			if tool.ID != toolID {
				continue
			}
			if !tool.CanOpen {
				return errors.New("工具尚不可用，请重新选择客户端或设置 phpMyAdmin 地址")
			}
			if tool.Kind == "phpmyadmin" && a.hooks.OpenURL != nil {
				return a.hooks.OpenURL(tool.URL)
			}
			if tool.Kind == "desktop" && a.hooks.OpenDatabaseClient != nil {
				return a.hooks.OpenDatabaseClient(tool.Path)
			}
			return errors.New("数据库工具启动器尚未初始化")
		}
		return errors.New("工具已不可用，请重新检测")
	})
}
