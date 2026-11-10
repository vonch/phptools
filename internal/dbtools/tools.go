package dbtools

import (
	"crypto/sha256"
	"debug/pe"
	"encoding/hex"
	"errors"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"phptools/internal/model"
)

var clientNames = map[string]string{
	"navicat.exe": "Navicat", "dbeaver.exe": "DBeaver", "heidisql.exe": "HeidiSQL",
	"mysqlworkbench.exe": "MySQL Workbench", "sqlyog.exe": "SQLyog",
	"sqlyogcommunity.exe": "SQLyog Community", "datagrip64.exe": "DataGrip", "datagrip.exe": "DataGrip",
}

func toolID(kind, target string) string {
	if kind == "desktop" {
		target = strings.ToLower(filepath.Clean(target))
	}
	sum := sha256.Sum256([]byte(kind + "\n" + target))
	return hex.EncodeToString(sum[:12])
}

func ValidateClient(path string) error {
	if !filepath.IsAbs(path) || !strings.EqualFold(filepath.Ext(path), ".exe") {
		return errors.New("请选择数据库客户端的 .exe 绝对路径")
	}
	info, err := os.Stat(path)
	if err != nil || !info.Mode().IsRegular() {
		return errors.New("客户端程序不存在或不是普通文件")
	}
	f, err := pe.Open(path)
	if err != nil {
		return errors.New("所选文件不是有效的 Windows 程序")
	}
	defer f.Close()
	if f.Characteristics&pe.IMAGE_FILE_EXECUTABLE_IMAGE == 0 || f.Characteristics&pe.IMAGE_FILE_DLL != 0 {
		return errors.New("请选择可执行程序，不能选择 DLL")
	}
	return nil
}

func NewCustom(kind, target string) (model.DatabaseTool, error) {
	t := model.DatabaseTool{Kind: kind, Source: "手动添加", Custom: true, CanOpen: true}
	switch kind {
	case "desktop":
		if err := ValidateClient(target); err != nil {
			return t, err
		}
		t.Path = filepath.Clean(target)
		t.Name = clientNames[strings.ToLower(filepath.Base(t.Path))]
		if t.Name == "" {
			t.Name = strings.TrimSuffix(filepath.Base(t.Path), filepath.Ext(t.Path))
		}
		t.ID = toolID(kind, t.Path)
	case "phpmyadmin":
		u, err := url.Parse(strings.TrimSpace(target))
		if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Hostname() == "" || u.User != nil {
			return t, errors.New("请输入不含账号密码的完整 http:// 或 https:// phpMyAdmin 地址")
		}
		t.URL = u.String()
		t.Name = "phpMyAdmin"
		t.Note = "打开后请确认所选 MySQL 服务器及端口与当前实例一致。"
		t.ID = toolID(kind, t.URL)
	default:
		return t, errors.New("不支持的数据库工具类型")
	}
	return t, nil
}

type collector struct {
	tools map[string]model.DatabaseTool
}

func (c *collector) add(t model.DatabaseTool) {
	key := t.Kind + ":" + t.URL
	if t.Kind == "desktop" || t.URL == "" {
		key = t.Kind + ":" + strings.ToLower(filepath.Clean(t.Path))
	}
	if old, ok := c.tools[key]; !ok || t.Custom || !old.CanOpen && t.CanOpen {
		c.tools[key] = t
	}
}

func (c *collector) client(path, name, source string) {
	if ValidateClient(path) != nil {
		return
	}
	path = filepath.Clean(path)
	if name == "" {
		name = clientNames[strings.ToLower(filepath.Base(path))]
	}
	if name != "" {
		c.add(model.DatabaseTool{ID: toolID("desktop", path), Kind: "desktop", Name: name, Path: path, Source: source, CanOpen: true})
	}
}

// IsPHPMyAdmin checks distribution markers; it never executes a PHP file.
func IsPHPMyAdmin(dir string) bool {
	info, err := os.Stat(filepath.Join(dir, "libraries"))
	if err != nil || !info.IsDir() {
		return false
	}
	f, err := os.Open(filepath.Join(dir, "index.php"))
	if err != nil {
		return false
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, 64<<10))
	return err == nil && strings.Contains(strings.ToLower(string(data)), "phpmyadmin")
}

func phpMyAdminDirs(parent string) []string {
	dirs := []string{}
	if IsPHPMyAdmin(parent) {
		dirs = append(dirs, parent)
	}
	entries, _ := os.ReadDir(parent)
	for _, e := range entries {
		if e.IsDir() && strings.HasPrefix(strings.ToLower(e.Name()), "phpmyadmin") {
			candidate := filepath.Join(parent, e.Name())
			if IsPHPMyAdmin(candidate) {
				dirs = append(dirs, candidate)
			}
		}
	}
	return dirs
}

// Detect only reads registered apps, PATH, bounded install locations and the
// configured sites. Portable apps outside those locations can be added by hand.
func (c *collector) sites(sites []model.Site) {
	for _, site := range sites {
		webRoot := filepath.Join(site.Root, site.Entry)
		for _, dir := range phpMyAdminDirs(webRoot) {
			t := model.DatabaseTool{Kind: "phpmyadmin", Name: "phpMyAdmin", Path: dir, Source: "PHPTools 站点"}
			if site.Enabled && site.PHPInstanceID != "" {
				rel, err := filepath.Rel(webRoot, dir)
				if err != nil {
					continue
				}
				u := url.URL{Scheme: "http", Host: site.Domain + ":" + strconv.Itoa(site.Port), Path: "/"}
				if rel != "." {
					u.Path += filepath.ToSlash(rel) + "/"
				}
				t.URL, t.CanOpen = u.String(), true
				t.Note = "请先启动站点服务，并在 phpMyAdmin 中核对 MySQL 服务器及端口。"
			} else {
				t.Note = "找到程序目录；站点尚未启用 PHP，或尚未启用站点。可手动添加可用地址。"
			}
			t.ID = toolID(t.Kind, t.Path+"\n"+t.URL)
			c.add(t)
		}
	}
}

func Detect(sites []model.Site, saved []model.DatabaseTool) []model.DatabaseTool {
	c := &collector{tools: map[string]model.DatabaseTool{}}
	detectWindows(c)
	c.sites(sites)
	for _, stored := range saved {
		var target string
		switch stored.Kind {
		case "desktop":
			target = stored.Path
		case "phpmyadmin":
			target = stored.URL
		default:
			continue
		}
		t, err := NewCustom(stored.Kind, target)
		if err != nil {
			if stored.Kind == "desktop" {
				stored.CanOpen, stored.Note = false, "程序文件已不可用，请移除此记录后重新选择。"
				stored.Custom = true
				c.add(stored)
			}
			continue
		}
		c.add(t)
	}
	result := make([]model.DatabaseTool, 0, len(c.tools))
	for _, t := range c.tools {
		result = append(result, t)
	}
	sort.Slice(result, func(i, j int) bool {
		if result[i].CanOpen != result[j].CanOpen {
			return result[i].CanOpen
		}
		if result[i].Name != result[j].Name {
			return result[i].Name < result[j].Name
		}
		return result[i].ID < result[j].ID
	})
	return result
}
