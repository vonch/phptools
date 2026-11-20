package desktop

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"phptools/internal/model"
	"strings"
	"time"
)

func (a *App) ReadConfig(id string) (string, error) {
	v, err := a.instanceByID(id)
	if err != nil {
		return "", err
	}
	data, err := os.ReadFile(v.ConfigPath)
	return string(data), err
}

func (a *App) SaveConfig(id, content string) error {
	if len(content) > 2<<20 || strings.ContainsRune(content, 0) {
		return errors.New("配置内容无效或超过 2MB")
	}
	if err := a.needRoot(); err != nil {
		return err
	}
	a.opMu.Lock()
	defer a.opMu.Unlock()
	v, err := a.instanceByID(id)
	if err != nil {
		return err
	}
	p, err := a.packageByID(v.PackageID)
	if err != nil {
		return err
	}
	old, err := os.ReadFile(v.ConfigPath)
	if err != nil {
		return err
	}
	backup := filepath.Join(a.data.Settings.Root, "backups", v.ID+"-"+newID()+".conf")
	if err = os.WriteFile(backup, old, 0600); err != nil {
		return err
	}
	if err = os.WriteFile(v.ConfigPath, []byte(content), 0600); err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	if err = a.manager.ValidateConfig(ctx, p, v); err != nil {
		return errors.Join(fmt.Errorf("配置校验失败：%w", err), os.WriteFile(v.ConfigPath, old, 0600))
	}
	if v.Kind == "nginx" && a.manager.Inspect(v).PID != 0 {
		if err = a.manager.Reload(ctx, p, v); err != nil {
			restoreErr := os.WriteFile(v.ConfigPath, old, 0600)
			reloadErr := a.manager.Reload(ctx, p, v)
			return errors.Join(err, restoreErr, reloadErr)
		}
	}
	a.mu.Lock()
	a.data.Diagnostics = append(a.data.Diagnostics, model.Diagnostic{Level: "info", Message: "配置已保存并备份；PHP、MySQL 和 Redis 配置需重启对应实例生效"})
	a.mu.Unlock()
	return nil
}

func (a *App) ReadLog(kind, id string, lines int) (string, error) {
	if lines < 1 {
		lines = 200
	}
	if lines > 2000 {
		lines = 2000
	}
	var path string
	switch kind {
	case "instance":
		v, err := a.instanceByID(id)
		if err != nil {
			return "", err
		}
		path = v.LogPath
	case "site":
		a.mu.RLock()
		for _, s := range a.data.Sites {
			if s.ID == id {
				path = filepath.Join(a.data.Settings.Root, "logs", s.ID+"-error.log")
			}
		}
		a.mu.RUnlock()
		if path == "" {
			return "", errors.New("站点不存在")
		}
	default:
		return "", errors.New("日志类型无效")
	}
	f, err := os.Open(path)
	if errors.Is(err, os.ErrNotExist) {
		return "尚无日志", nil
	}
	if err != nil {
		return "", err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return "", err
	}
	offset := max(int64(0), info.Size()-(1<<20))
	if _, err = f.Seek(offset, io.SeekStart); err != nil {
		return "", err
	}
	raw, err := io.ReadAll(io.LimitReader(f, 1<<20))
	if err != nil {
		return "", err
	}
	text := strings.Split(strings.TrimRight(string(raw), "\r\n"), "\n")
	if len(text) > lines {
		text = text[len(text)-lines:]
	}
	return strings.Join(text, "\n"), nil
}

func (a *App) sitesDirectory() (string, error) {
	if err := a.needRoot(); err != nil {
		return "", err
	}
	a.mu.RLock()
	root := a.data.Settings.Root
	a.mu.RUnlock()
	return filepath.Join(root, "sites"), nil
}

func (a *App) OpenSitesRoot() error {
	dir, err := a.sitesDirectory()
	if err != nil {
		return err
	}
	return a.OpenDirectory(dir)
}

func (a *App) OpenDirectory(path string) error {
	if !filepath.IsAbs(path) {
		return errors.New("目录必须是绝对路径")
	}
	path = filepath.Clean(path)
	info, err := os.Stat(path)
	if err != nil {
		return fmt.Errorf("无法访问目录 %s：%w", path, err)
	}
	if !info.IsDir() {
		return errors.New("请选择文件夹，而不是文件")
	}
	if a.hooks.OpenDirectory == nil {
		return errors.New("目录打开功能尚未初始化")
	}
	if err := a.hooks.OpenDirectory(path); err != nil {
		return fmt.Errorf("打开目录 %s 失败：%w", path, err)
	}
	return nil
}

func siteVisitURL(site model.Site) string {
	if site.HTTPS {
		return fmt.Sprintf("https://%s/", site.Domain)
	}
	return fmt.Sprintf("http://%s:%d/", site.Domain, site.Port)
}

func (a *App) VisitSite(id string) error {
	a.mu.RLock()
	defer a.mu.RUnlock()
	for _, s := range a.data.Sites {
		if s.ID == id {
			if !s.Enabled {
				return errors.New("站点尚未启用")
			}
			if a.hooks.OpenURL == nil {
				return errors.New("浏览器未初始化")
			}
			return a.hooks.OpenURL(siteVisitURL(s))
		}
	}
	return errors.New("站点不存在")
}
