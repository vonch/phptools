package packages

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"phptools/internal/model"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
func mockHTTP(t *testing.T, f roundTripFunc) {
	t.Helper()
	old := client
	copy := *client
	copy.Transport = f
	client = &copy
	t.Cleanup(func() { client = old })
}
func httpData(r *http.Request, status int, data []byte) *http.Response {
	return &http.Response{StatusCode: status, Body: io.NopCloser(bytes.NewReader(data)), ContentLength: int64(len(data)), Header: make(http.Header), Request: r}
}
func testZIP(t *testing.T, files map[string]string) []byte {
	t.Helper()
	var b bytes.Buffer
	w := zip.NewWriter(&b)
	for name, data := range files {
		if strings.HasSuffix(name, ".exe") {
			data = peFixture(data)
		}
		f, e := w.Create(name)
		if e != nil {
			t.Fatal(e)
		}
		if _, e := io.WriteString(f, data); e != nil {
			t.Fatal(e)
		}
	}
	if e := w.Close(); e != nil {
		t.Fatal(e)
	}
	return b.Bytes()
}

func peFixture(payload string) string {
	data := make([]byte, 152)
	copy(data, "MZ")
	binary.LittleEndian.PutUint32(data[60:], 128)
	copy(data[128:], "PE\x00\x00")
	binary.LittleEndian.PutUint16(data[132:], 0x8664)
	return string(data) + payload
}
func hashedPHP(data []byte) model.Package {
	digest := sha256.Sum256(data)
	return phpPackage("8.0.30", "php-8.0.30-nts-Win32-vs16-x64.zip", hex.EncodeToString(digest[:]), false)
}

func TestSeedCatalogTrustAndCoverage(t *testing.T) {
	kinds := map[string]bool{}
	phpBranches := map[string]bool{}
	for _, p := range SeedCatalog() {
		if err := validatePackage(p); err != nil {
			t.Errorf("%s: %v", p.ID, err)
		}
		kinds[p.Kind] = true
		if p.Kind == "php" {
			phpBranches[strings.Join(strings.Split(p.Version, ".")[:2], ".")] = true
		}
	}
	for _, kind := range []string{"php", "mysql", "nginx", "redis"} {
		if !kinds[kind] {
			t.Errorf("missing %s", kind)
		}
	}
	for _, b := range []string{"5.6", "7.0", "7.1", "7.2", "7.3", "7.4", "8.0", "8.1", "8.2", "8.3", "8.4", "8.5"} {
		if !phpBranches[b] {
			t.Errorf("missing PHP %s", b)
		}
	}
}
func TestZIPRejectsUnsafeWindowsNames(t *testing.T) {
	for _, name := range []string{"../escape", "a/../../escape", "/absolute", "C:/absolute", `a\..\escape`, "file:stream", "CON.txt", "a/NUL", "LPT1.log", "a/./b", "a//b", "trailing. ", "file?", ""} {
		t.Run(fmt.Sprintf("%q", name), func(t *testing.T) {
			if _, err := safeZIPName(name); err == nil {
				t.Errorf("accepted %q", name)
			}
		})
	}
	if _, err := safeZIPName("中文 目录/php-cgi.exe"); err != nil {
		t.Fatal(err)
	}
}
func TestZIPRejectsDuplicateAndSymlink(t *testing.T) {
	for _, symlink := range []bool{false, true} {
		t.Run(fmt.Sprint(symlink), func(t *testing.T) {
			var b bytes.Buffer
			w := zip.NewWriter(&b)
			for i, name := range []string{"a.txt", "A.TXT"} {
				h := &zip.FileHeader{Name: name, Method: zip.Deflate}
				if symlink && i == 1 {
					h.Name = "link"
					h.SetMode(os.ModeSymlink | 0777)
				}
				f, _ := w.CreateHeader(h)
				io.WriteString(f, "data")
			}
			w.Close()
			root := t.TempDir()
			archive := filepath.Join(root, "a.zip")
			os.WriteFile(archive, b.Bytes(), 0644)
			dest := filepath.Join(root, "dest")
			os.Mkdir(dest, 0755)
			if err := extractZIP(context.Background(), archive, dest); err == nil {
				t.Fatal("unsafe archive accepted")
			}
			entries, _ := os.ReadDir(dest)
			if len(entries) != 0 {
				t.Fatal("partial extraction before validation")
			}
		})
	}
}
func TestInstallPublishesAndUninstallsOwnedPackage(t *testing.T) {
	data := testZIP(t, map[string]string{"php-cgi.exe": "test payload", "php.ini-development": ";test"})
	pkg := hashedPHP(data)
	mockHTTP(t, func(r *http.Request) (*http.Response, error) { return httpData(r, 200, data), nil })
	root := filepath.Join(t.TempDir(), "中文 环境")
	var updates int
	installed, err := Install(context.Background(), root, pkg, func(int64, int64, string) { updates++ })
	if err != nil {
		t.Fatal(err)
	}
	if installed.Path != filepath.Join(root, "packages", pkg.ID) || installed.InstalledAt == "" || updates == 0 {
		t.Fatalf("incomplete result: %+v", installed)
	}
	if _, err := os.Stat(filepath.Join(installed.Path, "php-cgi.exe")); err != nil {
		t.Fatal(err)
	}
	recovered, err := Install(context.Background(), root, pkg, nil)
	if err != nil || recovered.Path != installed.Path || recovered.InstalledAt != installed.InstalledAt {
		t.Fatalf("owned installation did not recover: %+v %v", recovered, err)
	}
	forged := installed
	forged.Path = t.TempDir()
	if err := Uninstall(root, forged); err == nil {
		t.Fatal("forged uninstall succeeded")
	}
	if err := Uninstall(root, installed); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(installed.Path); !os.IsNotExist(err) {
		t.Fatal("package remains")
	}
}
func TestInstallUsesArchiveWhenReleaseWasMoved(t *testing.T) {
	data := testZIP(t, map[string]string{"php-cgi.exe": "archived"})
	pkg := hashedPHP(data)
	var seen []string
	mockHTTP(t, func(r *http.Request) (*http.Response, error) {
		seen = append(seen, r.URL.Path)
		if strings.Contains(r.URL.Path, "/archives/") {
			return httpData(r, 200, data), nil
		}
		return httpData(r, 404, []byte("missing")), nil
	})
	root := filepath.Join(t.TempDir(), "root")
	installed, err := Install(context.Background(), root, pkg, func(int64, int64, string) {})
	if err != nil {
		t.Fatal(err)
	}
	if installed.URL != pkg.URL || installed.Version != pkg.Version {
		t.Fatalf("catalog identity changed: %+v", installed)
	}
	if len(seen) == 0 || !strings.Contains(seen[len(seen)-1], "/archives/php-8.0.30-nts-Win32-vs16-x64.zip") {
		t.Fatalf("archive was not requested: %v", seen)
	}
}
func TestInstallReplacesMissingPatchWithCurrentRelease(t *testing.T) {
	data := testZIP(t, map[string]string{"php-cgi.exe": "current"})
	digest := sha256.Sum256(data)
	hash := hex.EncodeToString(digest[:])
	pkg := phpPackage("8.4.26", "php-8.4.26-nts-Win32-vs17-x64.zip", strings.Repeat("ab", 32), false)
	catalog := []byte(`{"8.4":{"version":"8.4.28","nts-vs17-x64":{"zip":{"path":"php-8.4.28-nts-Win32-vs17-x64.zip","sha256":"` + hash + `"}}}}`)
	var messages []string
	mockHTTP(t, func(r *http.Request) (*http.Response, error) {
		if strings.HasSuffix(r.URL.Path, "releases.json") {
			return httpData(r, 200, catalog), nil
		}
		if strings.Contains(r.URL.Path, "php-8.4.28-nts-Win32-vs17-x64.zip") {
			return httpData(r, 200, data), nil
		}
		return httpData(r, 404, []byte("missing")), nil
	})
	root := filepath.Join(t.TempDir(), "root")
	installed, err := Install(context.Background(), root, pkg, func(_, _ int64, message string) { messages = append(messages, message) })
	if err != nil {
		t.Fatal(err)
	}
	if installed.Version != "8.4.28" || installed.ID != "php-8.4.28-x64" || installed.SHA256 != hash {
		t.Fatalf("current release was not selected: %+v", installed)
	}
	if _, err := os.Stat(filepath.Join(installed.Path, "php-cgi.exe")); err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(messages, "\n")
	if !strings.Contains(joined, "改为下载官方当前版本 8.4.28") {
		t.Fatalf("progress did not explain the address change: %v", messages)
	}
}
func TestInstallChecksumFailureCleansStaging(t *testing.T) {
	data := testZIP(t, map[string]string{"php-cgi.exe": "test"})
	pkg := hashedPHP(data)
	pkg.SHA256 = strings.Repeat("0", 64)
	mockHTTP(t, func(r *http.Request) (*http.Response, error) { return httpData(r, 200, data), nil })
	root := filepath.Join(t.TempDir(), "root")
	if _, err := Install(context.Background(), root, pkg, nil); err == nil {
		t.Fatal("bad checksum succeeded")
	}
	entries, err := os.ReadDir(filepath.Join(root, "packages"))
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Name() != areaMarker {
		t.Fatalf("staging files remain: %+v", entries)
	}
}
func TestInstallCancelledAndUnownedArea(t *testing.T) {
	data := testZIP(t, map[string]string{"php-cgi.exe": "test"})
	pkg := hashedPHP(data)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	root := filepath.Join(t.TempDir(), "root")
	if _, err := Install(ctx, root, pkg, nil); err == nil {
		t.Fatal("cancelled install succeeded")
	}
	foreign := t.TempDir()
	os.Mkdir(filepath.Join(foreign, "packages"), 0755)
	keep := filepath.Join(foreign, "packages", "keep.txt")
	os.WriteFile(keep, []byte("user"), 0644)
	if _, err := Install(context.Background(), foreign, pkg, nil); err == nil {
		t.Fatal("accepted foreign area")
	}
	if data, _ := os.ReadFile(keep); string(data) != "user" {
		t.Fatal("foreign file changed")
	}
}
func TestInstallerRefusesMissingTrust(t *testing.T) {
	p := SeedCatalog()[0]
	p.Verified = false
	if err := validatePackage(p); err == nil {
		t.Fatal("unverified metadata accepted")
	}
	p = phpPackage("8.0.30", "php-8.0.30-nts-Win32-vs16-x64.zip", strings.Repeat("a", 64), false)
	p.URL = "https://example.org/php.zip"
	if err := validatePackage(p); err == nil {
		t.Fatal("foreign publisher accepted")
	}
}

func TestInstallCancellationDuringDownloadCleansStaging(t *testing.T) {
	data := testZIP(t, map[string]string{"php-cgi.exe": strings.Repeat("test", 10000)})
	pkg := hashedPHP(data)
	ctx, cancel := context.WithCancel(context.Background())
	mockHTTP(t, func(r *http.Request) (*http.Response, error) {
		res := httpData(r, 200, data)
		res.Body = &cancelReadCloser{reader: bytes.NewReader(data), cancel: cancel}
		return res, nil
	})
	root := filepath.Join(t.TempDir(), "root")
	if _, err := Install(ctx, root, pkg, nil); err == nil {
		t.Fatal("mid-download cancellation succeeded")
	}
	entries, _ := os.ReadDir(filepath.Join(root, "packages"))
	for _, entry := range entries {
		if entry.Name() != areaMarker {
			t.Fatal("partial installation remains")
		}
	}
}

type cancelReadCloser struct {
	reader io.Reader
	cancel context.CancelFunc
}

func (r *cancelReadCloser) Read(b []byte) (int, error) {
	n, e := r.reader.Read(b)
	r.cancel()
	return n, e
}
func (r *cancelReadCloser) Close() error { return nil }

func TestInstallRefusesLinkedEnvironment(t *testing.T) {
	parent := t.TempDir()
	outside := t.TempDir()
	link := filepath.Join(parent, "linked")
	if err := os.Symlink(outside, link); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	data := testZIP(t, map[string]string{"php-cgi.exe": "test"})
	if _, err := Install(context.Background(), link, hashedPHP(data), nil); err == nil {
		t.Fatal("linked environment accepted")
	}
	entries, _ := os.ReadDir(outside)
	if len(entries) != 0 {
		t.Fatal("external directory changed")
	}
}

func TestDownloadRejectsPublisherRedirect(t *testing.T) {
	mockHTTP(t, func(r *http.Request) (*http.Response, error) {
		if r.URL.Host == "downloads.php.net" {
			res := httpData(r, 302, nil)
			res.Header.Set("Location", "https://example.invalid/payload.zip")
			return res, nil
		}
		t.Fatal("followed foreign redirect")
		return nil, nil
	})
	if _, err := downloadZIP(context.Background(), phpBaseURL+"test.zip", filepath.Join(t.TempDir(), "archive.zip"), func(int64, int64, string) {}); err == nil {
		t.Fatal("foreign redirect accepted")
	}
}
