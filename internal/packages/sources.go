package packages

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"net/http"
	"phptools/internal/model"
	"regexp"
	"strings"
)

func refreshPHP(ctx context.Context) ([]model.Package, error) {
	var branches map[string]map[string]json.RawMessage
	if err := decodeJSON(ctx, phpCatalogURL, &branches); err != nil {
		return nil, err
	}
	out := []model.Package{}
	for _, branch := range branches {
		var version string
		if err := json.Unmarshal(branch["version"], &version); err != nil || !versionPattern.MatchString(version) {
			continue
		}
		for build, raw := range branch {
			if !strings.HasPrefix(build, "nts-") || !strings.HasSuffix(build, "-x64") {
				continue
			}
			var item struct {
				Zip struct {
					Path   string `json:"path"`
					SHA256 string `json:"sha256"`
				} `json:"zip"`
			}
			if json.Unmarshal(raw, &item) != nil || !hashPattern.MatchString(item.Zip.SHA256) {
				continue
			}
			expected := "php-" + version + "-nts-Win32-" + strings.TrimSuffix(strings.TrimPrefix(build, "nts-"), "-x64") + "-x64.zip"
			if !strings.EqualFold(item.Zip.Path, expected) {
				continue
			}
			out = append(out, phpPackage(version, item.Zip.Path, item.Zip.SHA256, false))
		}
	}
	if len(out) == 0 {
		return nil, errors.New("官方 JSON 未包含可校验的 Windows x64 NTS ZIP")
	}
	return out, nil
}

func refreshNginx(ctx context.Context) ([]model.Package, error) {
	body, err := fetchBytes(ctx, "https://nginx.org/en/download.html", 2<<20)
	if err != nil {
		return nil, err
	}
	matches := regexp.MustCompile(`href="/download/nginx-(\d+\.\d+\.\d+)\.zip"`).FindAllStringSubmatch(string(body), -1)
	out := []model.Package{}
	seen := map[string]bool{}
	for _, m := range matches {
		if seen[m[1]] {
			continue
		}
		seen[m[1]] = true
		channel := "mainline"
		if len(out) == 1 {
			channel = "stable"
		}
		p := nginxPackage(m[1], channel)
		if _, err := fetchBytes(ctx, p.SignatureURL, 64<<10); err != nil {
			return nil, fmt.Errorf("%s 签名：%w", p.Version, err)
		}
		out = append(out, p)
		if len(out) == 2 {
			break
		}
	}
	if len(out) != 2 {
		return nil, errors.New("未找到 Nginx 官方稳定版和主线版 Windows ZIP")
	}
	return out, nil
}

func refreshMySQL(ctx context.Context) ([]model.Package, error) {
	var tags []struct {
		Name string `json:"name"`
	}
	apiErr := decodeJSON(ctx, "https://api.github.com/repos/mysql/mysql-server/tags?per_page=100", &tags)
	versions := []string{}
	if apiErr == nil {
		for _, tag := range tags {
			versions = append(versions, strings.TrimPrefix(tag.Name, "mysql-"))
		}
	} else {
		data, err := fetchBytes(ctx, "https://github.com/mysql/mysql-server/tags", 4<<20)
		if err != nil {
			return nil, fmt.Errorf("官方 GitHub 标签 API：%v；网页：%w", apiErr, err)
		}
		for _, m := range regexp.MustCompile(`mysql-((?:5\.7|8\.0|8\.4)\.\d+)`).FindAllStringSubmatch(string(data), -1) {
			versions = append(versions, m[1])
		}
	}
	best := map[string]string{}
	for _, v := range versions {
		if !versionPattern.MatchString(v) {
			continue
		}
		parts := strings.Split(v, ".")
		branch := strings.Join(parts[:2], ".")
		if branch != "5.7" && branch != "8.0" && branch != "8.4" {
			continue
		}
		if best[branch] == "" || compareVersion(v, best[branch]) > 0 {
			best[branch] = v
		}
	}
	if len(best) == 0 {
		return nil, errors.New("官方 GitHub 标签中未发现受支持的 MySQL 分支")
	}
	out := []model.Package{}
	errs := []string{}
	for _, v := range best {
		p := mysqlPackage(v)
		if err := probeZIP(ctx, p.URL); err != nil {
			errs = append(errs, v+"："+err.Error())
			continue
		}
		if _, err := fetchBytes(ctx, p.SignatureURL, 64<<10); err != nil {
			errs = append(errs, v+" 签名："+err.Error())
			continue
		}
		out = append(out, p)
	}
	if len(out) == 0 {
		return nil, errors.New(strings.Join(errs, "；"))
	}
	if len(errs) > 0 {
		return out, errors.New(strings.Join(errs, "；"))
	}
	return out, nil
}
func probeZIP(ctx context.Context, raw string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodHead, raw, nil)
	if err != nil {
		return err
	}
	req.Header.Set("User-Agent", "PHPTools/0.1")
	res, err := client.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return fmt.Errorf("Windows ZIP HTTP %d", res.StatusCode)
	}
	return nil
}

func refreshRedis(ctx context.Context) ([]model.Package, error) {
	var release struct {
		TagName string `json:"tag_name"`
		Assets  []struct {
			Name   string `json:"name"`
			URL    string `json:"browser_download_url"`
			Digest string `json:"digest"`
		} `json:"assets"`
	}
	apiErr := decodeJSON(ctx, "https://api.github.com/repos/redis-windows/redis-windows/releases/latest", &release)
	if apiErr == nil && versionPattern.MatchString(release.TagName) {
		for _, a := range release.Assets {
			if a.Name == "Redis-"+release.TagName+"-Windows-x64-cygwin.zip" && strings.HasPrefix(a.Digest, "sha256:") {
				p := redisPackage(release.TagName, strings.TrimPrefix(a.Digest, "sha256:"))
				if p.Verified && a.URL == p.URL {
					return []model.Package{p}, nil
				}
			}
		}
	}
	// GitHub publishes the same asset digest in its public release HTML. This
	// adapter permits refresh when the unauthenticated REST API is rate limited.
	page, err := fetchBytes(ctx, "https://github.com/redis-windows/redis-windows/releases/latest", 4<<20)
	if err != nil {
		return nil, fmt.Errorf("Redis release API：%v；网页：%w", apiErr, err)
	}
	tag := regexp.MustCompile(`/redis-windows/redis-windows/releases/tag/(\d+\.\d+\.\d+)`).FindSubmatch(page)
	if len(tag) != 2 {
		return nil, errors.New("Redis release 页面没有正式版标签")
	}
	v := string(tag[1])
	assets, err := fetchBytes(ctx, "https://github.com/redis-windows/redis-windows/releases/expanded_assets/"+v, 4<<20)
	if err != nil {
		return nil, err
	}
	name := "Redis-" + v + "-Windows-x64-cygwin.zip"
	for _, row := range regexp.MustCompile(`(?s)<li\b[^>]*>.*?</li>`).FindAllString(string(assets), -1) {
		if !strings.Contains(html.UnescapeString(row), `/releases/download/`+v+`/`+name+`"`) {
			continue
		}
		digest := regexp.MustCompile(`value="sha256:([a-fA-F0-9]{64})"`).FindStringSubmatch(row)
		if len(digest) == 2 {
			return []model.Package{redisPackage(v, digest[1])}, nil
		}
	}
	return nil, errors.New("Redis Cygwin 普通 ZIP 缺少发布的 SHA256，拒绝加入可安装目录")
}
