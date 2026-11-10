package dbtools

import (
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"

	"golang.org/x/sys/windows/registry"
)

type installedApp struct {
	name, location, icon string
}

func installedApps() []installedApp {
	apps := []installedApp{}
	for _, root := range []registry.Key{registry.CURRENT_USER, registry.LOCAL_MACHINE} {
		for _, view := range []uint32{registry.WOW64_64KEY, registry.WOW64_32KEY} {
			key, err := registry.OpenKey(root, `Software\Microsoft\Windows\CurrentVersion\Uninstall`, registry.READ|view)
			if err != nil {
				continue
			}
			names, _ := key.ReadSubKeyNames(-1)
			for _, name := range names {
				sub, err := registry.OpenKey(key, name, registry.QUERY_VALUE|view)
				if err != nil {
					continue
				}
				display, _, _ := sub.GetStringValue("DisplayName")
				lower := strings.ToLower(display)
				if knownProduct(lower) {
					location, _, _ := sub.GetStringValue("InstallLocation")
					icon, _, _ := sub.GetStringValue("DisplayIcon")
					apps = append(apps, installedApp{display, location, icon})
				}
				sub.Close()
			}
			key.Close()
		}
	}
	return apps
}

func knownProduct(name string) bool {
	for _, product := range []string{"navicat", "dbeaver", "heidisql", "mysql workbench", "sqlyog", "datagrip"} {
		if strings.Contains(name, product) {
			return true
		}
	}
	return false
}

// DisplayIcon is a path with an optional numeric icon index, never a command.
func iconPath(value string) string {
	value = strings.TrimSpace(value)
	if i := strings.LastIndex(value, ","); i >= 0 {
		if _, err := strconv.Atoi(strings.TrimSpace(value[i+1:])); err == nil {
			value = value[:i]
		}
	}
	value = strings.Trim(strings.TrimSpace(value), `"`)
	if expanded, err := registry.ExpandString(value); err == nil {
		value = expanded
	}
	return value
}

func (c *collector) installDirectory(dir, source string) {
	if dir == "" || !filepath.IsAbs(dir) {
		return
	}
	for exe, name := range clientNames {
		for _, path := range []string{filepath.Join(dir, exe), filepath.Join(dir, "bin", exe)} {
			c.client(path, name, source)
		}
	}
}

func detectWindows(c *collector) {
	for _, app := range installedApps() {
		path := iconPath(app.icon)
		if clientNames[strings.ToLower(filepath.Base(path))] != "" {
			c.client(path, app.name, "已安装程序")
		}
		c.installDirectory(app.location, "已安装程序")
	}
	for exe, name := range clientNames {
		if path, err := exec.LookPath(exe); err == nil && filepath.IsAbs(path) {
			c.client(path, name, "PATH")
		}
		for _, root := range []registry.Key{registry.CURRENT_USER, registry.LOCAL_MACHINE} {
			for _, view := range []uint32{registry.WOW64_64KEY, registry.WOW64_32KEY} {
				key, err := registry.OpenKey(root, `Software\Microsoft\Windows\CurrentVersion\App Paths\`+exe, registry.QUERY_VALUE|view)
				if err != nil {
					continue
				}
				path, _, _ := key.GetStringValue("")
				key.Close()
				c.client(iconPath(path), name, "应用注册路径")
			}
		}
	}
	patterns := []string{
		"PremiumSoft/Navicat*/navicat.exe", "DBeaver*/dbeaver.exe", "HeidiSQL/heidisql.exe",
		"MySQL/MySQL Workbench*/MySQLWorkbench.exe", "SQLyog*/SQLyog*.exe", "JetBrains/DataGrip*/bin/datagrip*.exe",
		"Programs/DBeaver*/dbeaver.exe", "Programs/HeidiSQL/heidisql.exe",
	}
	for _, base := range []string{os.Getenv("ProgramFiles"), os.Getenv("ProgramFiles(x86)"), os.Getenv("LOCALAPPDATA")} {
		if base == "" {
			continue
		}
		for _, pattern := range patterns {
			matches, _ := filepath.Glob(filepath.Join(base, filepath.FromSlash(pattern)))
			for _, path := range matches {
				if name := clientNames[strings.ToLower(filepath.Base(path))]; name != "" {
					c.client(path, name, "常见安装目录")
				}
			}
		}
	}
}

// The selected GUI client runs independently. No credentials or connection
// arguments are passed, and it is never added to the managed service list.
func LaunchClient(path string) error {
	if err := ValidateClient(path); err != nil {
		return err
	}
	cmd := exec.Command(path)
	cmd.Dir = filepath.Dir(path)
	if err := cmd.Start(); err != nil {
		return err
	}
	return cmd.Process.Release()
}
