package desktop

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"path/filepath"
	"phptools/internal/model"
	"phptools/internal/services"
	"phptools/internal/sites"
)

func (a *App) SelectSiteProjectDirectory() (string, error) {
	if err := a.needRoot(); err != nil {
		return "", err
	}
	if a.hooks.PickDirectory == nil {
		return "", errors.New("目录选择器尚未初始化")
	}
	a.mu.RLock()
	dir := filepath.Join(a.data.Settings.Root, "sites")
	a.mu.RUnlock()
	if err := os.MkdirAll(dir, 0700); err != nil {
		return "", err
	}
	return a.hooks.PickDirectory("选择站点项目目录", dir)
}

func (a *App) SelectSiteEntry(projectRoot string) (model.SiteEntrySelection, error) {
	result := model.SiteEntrySelection{}
	if _, err := sites.RelativeEntry(projectRoot, projectRoot); err != nil {
		return result, err
	}
	if a.hooks.PickDirectory == nil {
		return result, errors.New("目录选择器尚未初始化")
	}
	path, err := a.hooks.PickDirectory("选择项目内的入口目录", filepath.Clean(projectRoot))
	if err != nil {
		return result, err
	}
	if path == "" {
		return result, nil
	}
	entry, err := sites.RelativeEntry(projectRoot, path)
	if err != nil {
		return result, err
	}
	return model.SiteEntrySelection{Entry: entry, Selected: true}, nil
}

func (a *App) GetRewritePresets() []model.RewritePreset { return sites.RewritePresets() }

func (a *App) SaveSite(input model.Site) (model.Site, error) {
	if err := a.needRoot(); err != nil {
		return input, err
	}
	a.opMu.Lock()
	defer a.opMu.Unlock()
	a.mu.RLock()
	old := append([]model.Site{}, a.data.Sites...)
	instances := append([]model.Instance{}, a.data.Instances...)
	root := a.data.Settings.Root
	a.mu.RUnlock()
	creating := strings.TrimSpace(input.ID) == "" && strings.TrimSpace(input.Root) == ""
	input, err := siteForSave(input, old)
	if err != nil {
		return input, err
	}
	var createdDir string
	discardCreated := func() {
		if createdDir != "" {
			_ = os.RemoveAll(createdDir)
		}
	}
	if creating {
		dir, err := sites.CreateNewProject(root, input.Name, input.Domain)
		if err != nil {
			return input, err
		}
		createdDir = dir
		input.Root = dir
		input.Entry = ""
	}
	input, err = sites.Validate(input, old, instances)
	if err != nil {
		discardCreated()
		return input, err
	}
	if input.PHPInstanceID != "" {
		for _, v := range instances {
			if v.ID == input.PHPInstanceID {
				p, err := a.packageByID(v.PackageID)
				if err != nil {
					discardCreated()
					return input, err
				}
				if err = services.ValidatePHPPath(p, filepath.Join(input.Root, input.Entry)); err != nil {
					discardCreated()
					return input, err
				}
			}
		}
	}
	updated := []model.Site{input}
	for _, v := range old {
		if v.ID != input.ID {
			updated = append(updated, v)
		}
	}
	if input.HTTPS {
		backup, err := sites.BackupCertificate(root, input.ID)
		if err != nil {
			discardCreated()
			return input, err
		}
		restore := func() {
			_ = sites.RestoreCertificate(root, input.ID, backup)
		}
		if err = sites.EnsureCertificate(root, input.ID, input.Domain); err != nil {
			restore()
			discardCreated()
			return input, err
		}
		if err = sites.TrustCA(root); err != nil {
			restore()
			discardCreated()
			return input, err
		}
		if err = a.changeSites(old, updated, instances); err != nil {
			restore()
			discardCreated()
			return input, err
		}
		a.notify()
		return input, nil
	}
	if err := a.changeSites(old, updated, instances); err != nil {
		discardCreated()
		return input, err
	}
	a.notify()
	return input, nil
}

func siteForSave(input model.Site, existing []model.Site) (model.Site, error) {
	// Configuration dates belong to the server. Failed saves never persist them.
	input.UpdatedAt = time.Now().UTC().Format(time.RFC3339Nano)
	// Activity is derived from access logs, independently of configuration edits.
	input.LastActiveAt = ""
	if input.ID == "" {
		input.ID = newID()
		input.CreatedAt = input.UpdatedAt
		return input, nil
	}
	for _, site := range existing {
		if site.ID == input.ID {
			input.CreatedAt = site.CreatedAt
			return input, nil
		}
	}
	return input, errors.New("所编辑站点不存在")
}

func (a *App) DeleteSite(id string) error {
	if err := a.needRoot(); err != nil {
		return err
	}
	a.opMu.Lock()
	defer a.opMu.Unlock()
	a.mu.RLock()
	old := append([]model.Site{}, a.data.Sites...)
	instances := append([]model.Instance{}, a.data.Instances...)
	a.mu.RUnlock()
	updated := []model.Site{}
	found := false
	for _, v := range old {
		if v.ID == id {
			found = true
		} else {
			updated = append(updated, v)
		}
	}
	if !found {
		return errors.New("站点不存在")
	}
	if err := a.changeSites(old, updated, instances); err != nil {
		return err
	}
	_ = sites.RemoveCertificate(a.data.Settings.Root, id)
	return nil
}

func (a *App) changeSites(old, updated []model.Site, instances []model.Instance) error {
	var nginx []model.Instance
	for _, v := range instances {
		if v.Kind == "nginx" {
			nginx = append(nginx, v)
		}
	}
	if len(nginx) == 0 {
		return errors.New("请先安装 Nginx 并创建实例，以便校验站点配置")
	}
	root := a.data.Settings.Root
	rollback, err := sites.Apply(root, updated, instances)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	for _, v := range nginx {
		p, err := a.packageByID(v.PackageID)
		if err != nil {
			return errors.Join(err, rollback())
		}
		if err = a.manager.ValidateConfig(ctx, p, v); err != nil {
			return errors.Join(fmt.Errorf("站点配置未通过 Nginx 校验：%w", err), rollback())
		}
	}
	active := false
	for _, v := range nginx {
		if a.manager.Inspect(v).PID != 0 {
			active = true
			break
		}
	}
	if active {
		for _, s := range updated {
			if s.Enabled && s.PHPInstanceID != "" {
				if err := a.start(ctx, s.PHPInstanceID); err != nil {
					return errors.Join(fmt.Errorf("启动站点 PHP 失败：%w", err), rollback())
				}
			}
		}
	}
	if err := sites.SyncHosts(root, sites.Domains(updated)); err != nil {
		return errors.Join(err, rollback())
	}
	reload := func(reloadCtx context.Context) error {
		for _, v := range nginx {
			if a.manager.Inspect(v).PID != 0 {
				p, err := a.packageByID(v.PackageID)
				if err != nil {
					return err
				}
				if err = a.manager.Reload(reloadCtx, p, v); err != nil {
					return err
				}
			}
		}
		return nil
	}
	restore := func(cause error) error {
		filesErr := rollback()
		hostsErr := sites.SyncHosts(root, sites.Domains(old))
		restoreCtx, restoreCancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer restoreCancel()
		return errors.Join(cause, filesErr, hostsErr, reload(restoreCtx))
	}
	if err := reload(ctx); err != nil {
		return restore(fmt.Errorf("加载站点配置失败：%w", err))
	}
	values := map[string]any{}
	for _, v := range updated {
		values[v.ID] = v
	}
	if err := a.db.Replace("site", values); err != nil {
		return restore(err)
	}
	a.mu.Lock()
	a.data.Sites = updated
	a.mu.Unlock()
	return nil
}
