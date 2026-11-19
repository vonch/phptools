package sites

import (
	"errors"
	"fmt"
	"html"
	"os"
	"path/filepath"
	"strings"
)

func CreateNewProject(envRoot, name, domain string) (string, error) {
	parent := filepath.Join(envRoot, "sites")
	if err := os.MkdirAll(parent, 0700); err != nil {
		return "", errors.New("无法在 sites 目录创建项目文件夹")
	}
	base := projectFolderName(name, domain)
	var dir string
	for n := 1; n < 1000; n++ {
		folder := base
		if n > 1 {
			folder = fmt.Sprintf("%s-%d", base, n)
		}
		candidate := filepath.Join(parent, folder)
		rel, err := filepath.Rel(parent, candidate)
		if err != nil || rel != folder || strings.HasPrefix(rel, "..") {
			return "", errors.New("无法在 sites 目录创建项目文件夹")
		}
		if _, err = os.Lstat(candidate); err == nil {
			continue
		} else if !os.IsNotExist(err) {
			return "", errors.New("无法在 sites 目录创建项目文件夹")
		}
		if err = os.Mkdir(candidate, 0755); err != nil {
			if os.IsExist(err) {
				continue
			}
			return "", errors.New("无法在 sites 目录创建项目文件夹")
		}
		dir = candidate
		break
	}
	if dir == "" {
		return "", errors.New("无法在 sites 目录创建项目文件夹")
	}
	page := projectHomepage(name, domain)
	target := filepath.Join(dir, "index.html")
	file, err := os.OpenFile(target, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0644)
	if err != nil {
		_ = os.RemoveAll(dir)
		return "", errors.New("无法生成项目首页")
	}
	_, err = file.Write(page)
	closeErr := file.Close()
	if err != nil || closeErr != nil {
		_ = os.RemoveAll(dir)
		return "", errors.New("无法生成项目首页")
	}
	return dir, nil
}

func projectFolderName(name, domain string) string {
	cleaned := sanitizeProjectFolder(name)
	if cleaned == "" {
		label, _, _ := strings.Cut(strings.TrimSpace(domain), ".")
		cleaned = sanitizeProjectFolder(label)
	}
	if cleaned == "" {
		cleaned = "site"
	}
	if windowsReservedName(cleaned) {
		cleaned += "-site"
	}
	return cleaned
}

func sanitizeProjectFolder(value string) string {
	var b strings.Builder
	previousDash := false
	for _, r := range strings.TrimSpace(value) {
		if r < 32 || strings.ContainsRune(`<>:"/\|?*`, r) {
			if b.Len() > 0 && !previousDash {
				b.WriteByte('-')
				previousDash = true
			}
			continue
		}
		b.WriteRune(r)
		previousDash = false
	}
	cleaned := strings.Trim(b.String(), " .-")
	runes := []rune(cleaned)
	if len(runes) > 60 {
		cleaned = strings.TrimRight(string(runes[:60]), " .-")
	}
	return cleaned
}

func windowsReservedName(name string) bool {
	stem := name
	if dot := strings.IndexByte(name, '.'); dot >= 0 {
		stem = name[:dot]
	}
	switch strings.ToUpper(stem) {
	case "CON", "PRN", "AUX", "NUL",
		"COM1", "COM2", "COM3", "COM4", "COM5", "COM6", "COM7", "COM8", "COM9",
		"LPT1", "LPT2", "LPT3", "LPT4", "LPT5", "LPT6", "LPT7", "LPT8", "LPT9":
		return true
	default:
		return false
	}
}

func projectHomepage(name, domain string) []byte {
	label := strings.TrimSpace(name)
	if label == "" {
		label = strings.TrimSpace(domain)
	}
	if label == "" {
		label = "新站点"
	}
	body := fmt.Sprintf(`<!DOCTYPE html>
<html lang="zh-CN">
<head>
  <meta charset="utf-8">
  <meta name="viewport" content="width=device-width, initial-scale=1">
  <title>创建成功</title>
  <style>
    body { margin: 0; min-height: 100vh; display: grid; place-items: center; background: #f3f6f8; color: #1d2b36; font: 16px/1.6 "Segoe UI", "Microsoft YaHei", sans-serif; }
    main { box-sizing: border-box; width: min(480px, calc(100%% - 40px)); padding: 36px 32px; background: #fff; border: 1px solid #e3eaee; border-radius: 14px; }
    h1 { margin: 0 0 12px; font-size: 28px; font-weight: 650; }
    p { margin: 0; color: #526674; }
  </style>
</head>
<body>
  <main>
    <h1>创建成功</h1>
    <p>站点「%s」已创建。这是自动生成的静态首页，可以替换成自己的页面。</p>
  </main>
</body>
</html>
`, html.EscapeString(label))
	return []byte(body)
}
