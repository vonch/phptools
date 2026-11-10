package packages

import (
	"context"
	"net/http"
	"phptools/internal/model"
	"strings"
	"testing"
)

func TestRefreshRetainsCacheAndReportsFailures(t *testing.T) {
	mockHTTP(t, func(r *http.Request) (*http.Response, error) { return httpData(r, 503, []byte("unavailable")), nil })
	previous := SeedCatalog()
	previous[0].Path = `D:\PHPTools\packages\owned`
	previous[0].InstalledAt = "2026-10-19T00:00:00Z"
	result, diagnostics, err := Refresh(context.Background(), previous)
	if err == nil || len(diagnostics) != 4 {
		t.Fatalf("expected visible source failures: %v %+v", err, diagnostics)
	}
	found := false
	for _, p := range result {
		if p.ID == previous[0].ID {
			found = p.Path == previous[0].Path && p.InstalledAt == previous[0].InstalledAt
		}
	}
	if !found {
		t.Fatal("installed metadata lost on refresh failure")
	}
}
func TestPHPDiscoveryUsesNTSx64AndPublishedHash(t *testing.T) {
	hash := strings.Repeat("a", 64)
	data := `{"8.0":{"version":"8.0.30","nts-vs16-x64":{"zip":{"path":"php-8.0.30-nts-Win32-vs16-x64.zip","sha256":"` + hash + `"}},"ts-vs16-x64":{"zip":{"path":"php-8.0.30-Win32-vs16-x64.zip","sha256":"` + hash + `"}},"nts-vs16-x86":{"zip":{"path":"php-8.0.30-nts-Win32-vs16-x86.zip","sha256":"` + hash + `"}}}}`
	mockHTTP(t, func(r *http.Request) (*http.Response, error) { return httpData(r, 200, []byte(data)), nil })
	pkgs, err := refreshPHP(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(pkgs) != 1 || pkgs[0].SHA256 != hash || !strings.Contains(pkgs[0].URL, "-nts-") {
		t.Fatalf("wrong packages: %+v", pkgs)
	}
}
func TestRedisHTMLFallbackUsesPlainCygwinDigest(t *testing.T) {
	hash := strings.Repeat("c", 64)
	mockHTTP(t, func(r *http.Request) (*http.Response, error) {
		if r.URL.Host == "api.github.com" {
			return httpData(r, 403, []byte("rate limited")), nil
		}
		if strings.Contains(r.URL.Path, "expanded_assets") {
			data := `<li><a href="/redis-windows/redis-windows/releases/download/8.10.2/Redis-8.10.2-Windows-x64-cygwin-with-Service.zip">service</a><clipboard-copy value="sha256:` + strings.Repeat("f", 64) + `"></clipboard-copy></li><li><a href="/redis-windows/redis-windows/releases/download/8.10.2/Redis-8.10.2-Windows-x64-cygwin.zip">plain</a><clipboard-copy value="sha256:` + hash + `"></clipboard-copy></li>`
			return httpData(r, 200, []byte(data)), nil
		}
		return httpData(r, 200, []byte(`<meta content="/redis-windows/redis-windows/releases/tag/8.10.2">`)), nil
	})
	pkgs, err := refreshRedis(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(pkgs) != 1 || pkgs[0].SHA256 != hash || strings.Contains(pkgs[0].URL, "Service") {
		t.Fatalf("wrong Redis asset: %+v", pkgs)
	}
}

func TestRefreshPreservesInstalledNginxArchitecture(t *testing.T) {
	mockHTTP(t, func(r *http.Request) (*http.Response, error) { return httpData(r, 503, []byte("offline")), nil })
	previous := []model.Package{nginxPackage("1.30.6", "stable")}
	previous[0].Path = `D:\PHPTools\packages\nginx-1.30.6-windows\nginx-1.30.6`
	previous[0].Arch = "x86"
	previous[0].InstalledAt = "2026-10-19T00:00:00Z"
	pkgs, _, _ := Refresh(context.Background(), previous)
	for _, p := range pkgs {
		if p.ID == previous[0].ID {
			if p.Arch != "x86" || p.Path != previous[0].Path {
				t.Fatalf("lost installed metadata: %+v", p)
			}
			return
		}
	}
	t.Fatal("installed nginx missing")
}
