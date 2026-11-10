package packages

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"sort"
	"strings"
	"time"

	"phptools/internal/model"
)

const phpCatalogURL = "https://downloads.php.net/~windows/releases/releases.json"
const phpBaseURL = "https://downloads.php.net/~windows/releases/"
const nginxFingerprint = "D6786CE303D9A9022998DC6CC8464D549AF75C0A"
const mysqlFingerprint = "BCA43417C3B485DD128EC6D4B7B3B788A8D3785C"
const mysqlOldFingerprint = "859BE8D7C586F538430B19C2467B942D3A79BD29"

var client = &http.Client{Timeout: 20 * time.Minute, CheckRedirect: func(req *http.Request, via []*http.Request) error {
	if len(via) >= 8 {
		return errors.New("下载重定向过多")
	}
	if req.URL.Scheme != "https" {
		return errors.New("拒绝非 HTTPS 重定向")
	}
	return nil
}}
var versionPattern = regexp.MustCompile(`^\d+\.\d+\.\d+$`)
var hashPattern = regexp.MustCompile(`^[a-fA-F0-9]{64}$`)
var idPattern = regexp.MustCompile(`^(?:(?:php|mysql|redis)-[0-9]+\.[0-9]+\.[0-9]+-x64|nginx-[0-9]+\.[0-9]+\.[0-9]+-windows)$`)

// SeedCatalog contains publisher metadata captured on 2026-10-19. The five
// historical PHP NTS hashes are explicitly identified as PHPTools audit hashes;
// their official HTTPS ZIPs and x64 PE versions were inspected independently.
func SeedCatalog() []model.Package {
	php := []struct{ v, build, hash string }{
		{"7.4.33", "vc15", "14ae3250d4447c8ccfc4c45a70d90adfbcd61e728d85f0be56a7ddf8f9c8aace"},
		{"8.0.30", "vs16", "dfb70498ffa2c617f2f655a155564697e3c9cca41709938fd1a5997d1d5b0785"},
		{"8.1.34", "vs16", "9cfe246cb144076c16f5913a3ef88a474c3dd7e60f0f0c8bb95faf68674016cc"},
		{"8.2.34", "vs16", "03249b5c9414c6dbe30276f4a7598bd9d2a7417ee81f709b06b99e9c4a2aff4f"},
		{"8.3.35", "vs16", "25a8e2ac9ff30f1d768d1447c09a600617fa6e6082729f6e95f008b59c91fe45"},
		{"8.4.26", "vs17", "da68394f9193b7f6b89d0c76861a4034ae10efee7fd55a7255d8118c2acf70d7"},
		{"8.5.11", "vs17", "0ea96e0d2b9b737a6036f05cf4e95c49313faa6d0f27bd97edb2742503f0c043"},
	}
	out := make([]model.Package, 0, 18)
	for _, p := range php {
		out = append(out, phpPackage(p.v, "php-"+p.v+"-nts-Win32-"+p.build+"-x64.zip", p.hash, false))
	}
	for _, p := range []struct{ v, build, hash string }{
		{"5.6.40", "VC11", "3d7668280fa4b16f70705539ba1e4ea17eef344c81e82881cbeca26fb7f181f1"},
		{"7.0.33", "VC14", "bba39bde5b0bd50efadc11e2716c7528669945f9d1397d707486e401e67e89fb"},
		{"7.1.33", "VC14", "071438be5bbaee8b34894ddba7852b6040991a3c853ab8141efcb1b6655bbbef"},
		{"7.2.34", "VC15", "3c673eab656e26fd6bc3ad27fe71169ad888b04e21d63d3c6b3151d5ed216563"},
		{"7.3.33", "VC15", "5eaf3cad80e678623f222a42c99bcefcc60eea359d407fb51e805afdb3b13e5e"},
	} {
		p := phpPackage(p.v, "php-"+p.v+"-nts-Win32-"+p.build+"-x64.zip", p.hash, true)
		p.Source = "PHP 官方归档；PHPTools 首版核验摘要（不是 PHP 官方发布摘要）"
		p.SourceURL = phpBaseURL + "archives/"
		out = append(out, p)
	}
	out = append(out, nginxPackage("1.30.6", "stable"), nginxPackage("1.31.7", "mainline"))
	out = append(out, mysqlPackage("5.7.44"), mysqlPackage("8.0.44"), mysqlPackage("8.4.9"))
	out = append(out, redisPackage("8.10.2", "6de5cc7f5adbf97b5928b13766383d4ad424626ef3d8b313ffff12d820ec6fc1"))
	sortPackages(out)
	return out
}
func basePackage(kind, version string) model.Package {
	if kind == "nginx" {
		return model.Package{ID: kind + "-" + version + "-windows", Kind: kind, Version: version, Arch: "windows"}
	}
	return model.Package{ID: kind + "-" + version + "-x64", Kind: kind, Version: version, Arch: "x64"}
}
func phpPackage(v, name, hash string, archive bool) model.Package {
	p := basePackage("php", v)
	p.URL = phpBaseURL + name
	if archive {
		p.URL = phpBaseURL + "archives/" + name
	}
	p.SHA256 = strings.ToLower(hash)
	p.Verified = hashPattern.MatchString(hash)
	p.Source = "PHP 官方 Windows NTS"
	p.SourceURL = phpCatalogURL
	p.Channel = "NTS"
	p.EOL = strings.HasPrefix(v, "5.") || strings.HasPrefix(v, "7.") || strings.HasPrefix(v, "8.0.") || strings.HasPrefix(v, "8.1.")
	return p
}
func nginxPackage(v, channel string) model.Package {
	p := basePackage("nginx", v)
	p.URL = "https://nginx.org/download/nginx-" + v + ".zip"
	p.SignatureURL = p.URL + ".asc"
	p.SigningKeyURL = "https://nginx.org/keys/pluknet.key"
	p.SigningFingerprint = nginxFingerprint
	p.Source = "Nginx 官方 Windows"
	p.SourceURL = "https://nginx.org/en/download.html"
	p.Channel = channel
	p.Verified = true
	return p
}
func mysqlPackage(v string) model.Package {
	p := basePackage("mysql", v)
	parts := strings.Split(v, ".")
	branch := strings.Join(parts[:2], ".")
	p.URL = "https://cdn.mysql.com/Downloads/MySQL-" + branch + "/mysql-" + v + "-winx64.zip"
	p.SignatureURL = p.URL + ".asc"
	p.SigningKeyURL = "https://repo.mysql.com/RPM-GPG-KEY-mysql-2025"
	p.SigningFingerprint = mysqlFingerprint
	if branch == "5.7" {
		p.SigningKeyURL = "https://repo.mysql.com/RPM-GPG-KEY-mysql-2022"
		p.SigningFingerprint = mysqlOldFingerprint
	}
	p.Source = "MySQL 官方 Windows ZIP"
	p.SourceURL = "https://github.com/mysql/mysql-server/tags"
	p.Channel = branch
	p.EOL = branch == "5.7"
	p.Verified = true
	return p
}
func redisPackage(v, hash string) model.Package {
	p := basePackage("redis", v)
	p.URL = "https://github.com/redis-windows/redis-windows/releases/download/" + v + "/Redis-" + v + "-Windows-x64-cygwin.zip"
	p.SHA256 = strings.ToLower(hash)
	p.Source = "redis-windows · 第三方 Cygwin 发行"
	p.SourceURL = "https://github.com/redis-windows/redis-windows/releases/tag/" + v
	p.Channel = "Cygwin"
	p.Verified = hashPattern.MatchString(hash)
	return p
}

// Refresh preserves installed versions and usable cached metadata if one source
// fails; diagnostics make each failed source visible to the caller.
func Refresh(ctx context.Context, previous []model.Package) ([]model.Package, []model.Diagnostic, error) {
	type result struct {
		name string
		pkgs []model.Package
		err  error
	}
	sources := []struct {
		name  string
		fetch func(context.Context) ([]model.Package, error)
	}{{"PHP", refreshPHP}, {"Nginx", refreshNginx}, {"MySQL", refreshMySQL}, {"Redis", refreshRedis}}
	ch := make(chan result, len(sources))
	for _, s := range sources {
		go func() {
			c, cancel := context.WithTimeout(ctx, 50*time.Second)
			defer cancel()
			p, e := s.fetch(c)
			ch <- result{s.name, p, e}
		}()
	}
	merged := map[string]model.Package{}
	for _, p := range previous {
		merged[p.ID] = p
	}
	for _, p := range SeedCatalog() {
		if old, ok := merged[p.ID]; ok {
			p.Path = old.Path
			p.InstalledAt = old.InstalledAt
			if old.Path != "" {
				p.Arch = old.Arch
			}
		}
		merged[p.ID] = p
	}
	diagnostics := []model.Diagnostic{}
	succeeded := 0
	for range sources {
		r := <-ch
		if r.err != nil {
			diagnostics = append(diagnostics, model.Diagnostic{Level: "warning", Message: r.name + " 目录刷新失败，保留缓存：" + r.err.Error()})
		}
		if r.err == nil || len(r.pkgs) > 0 {
			succeeded++
		}
		for _, p := range r.pkgs {
			if old, ok := merged[p.ID]; ok {
				p.Path = old.Path
				p.InstalledAt = old.InstalledAt
				if old.Path != "" {
					p.Arch = old.Arch
				}
			}
			merged[p.ID] = p
		}
	}
	out := make([]model.Package, 0, len(merged))
	for _, p := range merged {
		out = append(out, p)
	}
	sortPackages(out)
	if ctx.Err() != nil {
		return out, diagnostics, ctx.Err()
	}
	if succeeded == 0 {
		return out, diagnostics, errors.New("所有在线组件来源均刷新失败")
	}
	return out, diagnostics, nil
}
func sortPackages(p []model.Package) {
	sort.Slice(p, func(i, j int) bool {
		if p[i].Kind != p[j].Kind {
			return p[i].Kind < p[j].Kind
		}
		return compareVersion(p[i].Version, p[j].Version) > 0
	})
}
func compareVersion(a, b string) int {
	var x, y [3]int
	fmt.Sscanf(a, "%d.%d.%d", &x[0], &x[1], &x[2])
	fmt.Sscanf(b, "%d.%d.%d", &y[0], &y[1], &y[2])
	for i := range x {
		if x[i] > y[i] {
			return 1
		}
		if x[i] < y[i] {
			return -1
		}
	}
	return 0
}
func fetchBytes(ctx context.Context, raw string, limit int64) ([]byte, error) {
	u, e := url.Parse(raw)
	if e != nil || u.Scheme != "https" || u.Host == "" {
		return nil, errors.New("组件来源必须是 HTTPS URL")
	}
	req, e := http.NewRequestWithContext(ctx, http.MethodGet, raw, nil)
	if e != nil {
		return nil, e
	}
	req.Header.Set("User-Agent", "PHPTools/0.1")
	res, e := client.Do(req)
	if e != nil {
		return nil, e
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("%s：HTTP %d", u.Host, res.StatusCode)
	}
	data, e := io.ReadAll(io.LimitReader(res.Body, limit+1))
	if e != nil {
		return nil, e
	}
	if int64(len(data)) > limit {
		return nil, errors.New("来源响应超出大小限制")
	}
	return data, nil
}
func decodeJSON(ctx context.Context, raw string, target any) error {
	data, e := fetchBytes(ctx, raw, 4<<20)
	if e != nil {
		return e
	}
	return json.Unmarshal(data, target)
}
