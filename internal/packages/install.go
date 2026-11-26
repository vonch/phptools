package packages

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"phptools/internal/model"
)

const areaMarker = ".phptools-packages"
const installMarker = ".phptools-package.json"
const areaSignature = "PHPTools managed packages v1\n"

var packageLocks sync.Map
var areaLocks sync.Map

// Install verifies the entire archive before extraction, then atomically
// publishes a complete package. It never invokes downloaded executables.
func Install(ctx context.Context, root string, pkg model.Package, progress func(done, total int64, message string)) (model.Package, error) {
	if err := validatePackage(pkg); err != nil {
		return pkg, err
	}
	if progress == nil {
		progress = func(int64, int64, string) {}
	}
	if pkg.Kind == "php" {
		pkg = resolveMissingPHP(ctx, pkg, progress)
		if err := validatePackage(pkg); err != nil {
			return pkg, err
		}
	}
	area, err := managedArea(root, true)
	if err != nil {
		return pkg, err
	}
	target := filepath.Join(area, pkg.ID)
	lock, _ := packageLocks.LoadOrStore(target, &sync.Mutex{})
	mu := lock.(*sync.Mutex)
	mu.Lock()
	defer mu.Unlock()
	if err := ctx.Err(); err != nil {
		return pkg, err
	}
	if _, err := os.Lstat(target); err == nil {
		recovered, err := recoverExisting(target, pkg)
		if err == nil && progress != nil {
			progress(1, 1, "发现已完成的受管安装，已恢复登记信息")
		}
		return recovered, err
	} else if !errors.Is(err, os.ErrNotExist) {
		return pkg, err
	}
	staging, err := os.MkdirTemp(area, ".install-"+pkg.ID+"-")
	if err != nil {
		return pkg, err
	}
	defer os.RemoveAll(staging)
	archive := filepath.Join(staging, "download.zip")
	progress(0, 0, "正在下载 "+pkg.Kind+" "+pkg.Version)
	digest, err := downloadZIP(ctx, pkg.URL, archive, progress)
	if connectionInterrupted(err) {
		_ = os.Remove(archive)
		progress(0, 0, "下载中断，正在重试")
		digest, err = downloadZIP(ctx, pkg.URL, archive, progress)
	}
	if archiveURL, ok := phpArchiveURL(pkg.URL); ok && statusCode(err) == http.StatusNotFound {
		_ = os.Remove(archive)
		progress(0, 0, "发行目录没有该版本，改为下载归档")
		digest, err = downloadZIP(ctx, archiveURL, archive, progress)
	}
	if err != nil {
		return pkg, err
	}
	if pkg.SHA256 != "" && !strings.EqualFold(digest, pkg.SHA256) {
		return pkg, errors.New("SHA256 校验失败，下载内容已丢弃")
	}
	if pkg.SignatureURL != "" {
		progress(0, 0, "正在校验发布者 PGP 签名")
		if err := verifyPGP(ctx, archive, pkg); err != nil {
			return pkg, err
		}
	}
	progress(0, 0, "校验通过，正在安全解压")
	payload := filepath.Join(staging, "payload")
	if err := os.Mkdir(payload, 0755); err != nil {
		return pkg, err
	}
	if err := extractZIP(ctx, archive, payload); err != nil {
		return pkg, err
	}
	relative, err := executableRoot(payload, pkg.Kind)
	if err != nil {
		return pkg, err
	}
	actualArch, err := executableArch(filepath.Join(payload, relative), pkg.Kind)
	if err != nil {
		return pkg, err
	}
	pkg.Arch = actualArch
	pkg.Path = filepath.Join(target, relative)
	pkg.InstalledAt = model.Now()
	pkg.Verified = true
	metadata, err := json.MarshalIndent(pkg, "", "  ")
	if err != nil {
		return pkg, err
	}
	if err := os.WriteFile(filepath.Join(payload, installMarker), metadata, 0644); err != nil {
		return pkg, err
	}
	if err := ctx.Err(); err != nil {
		return pkg, err
	}
	if err := noLinks(area); err != nil {
		return pkg, err
	}
	if err := os.Rename(payload, target); err != nil {
		return pkg, fmt.Errorf("发布安装目录：%w", err)
	}
	progress(1, 1, "安装完成")
	return pkg, nil
}

// Uninstall requires a matching ownership marker and exact package path. The
// application layer must first reject packages referenced by any instance.
func Uninstall(root string, pkg model.Package) error {
	if !idPattern.MatchString(pkg.ID) {
		return errors.New("组件 ID 无效")
	}
	area, err := managedArea(root, false)
	if err != nil {
		return err
	}
	target := filepath.Join(area, pkg.ID)
	lock, _ := packageLocks.LoadOrStore(target, &sync.Mutex{})
	mu := lock.(*sync.Mutex)
	mu.Lock()
	defer mu.Unlock()
	if err := noLinks(target); err != nil {
		return err
	}
	data, err := os.ReadFile(filepath.Join(target, installMarker))
	if err != nil {
		return fmt.Errorf("组件缺少 PHPTools 安装归属信息，拒绝删除：%w", err)
	}
	var installed model.Package
	if json.Unmarshal(data, &installed) != nil || installed.ID != pkg.ID || installed.Kind != pkg.Kind || installed.InstalledAt == "" {
		return errors.New("组件归属信息不匹配，拒绝删除")
	}
	expected, err := filepath.Abs(pkg.Path)
	if err != nil {
		return err
	}
	owned, err := filepath.Abs(installed.Path)
	if err != nil {
		return err
	}
	relative, err := filepath.Rel(target, owned)
	if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) || !strings.EqualFold(expected, owned) {
		return errors.New("组件安装路径不属于当前环境，拒绝删除")
	}
	if err := noLinks(owned); err != nil {
		return err
	}
	// os.Root keeps recursive removal inside the verified packages directory even
	// if a descendant symlink is introduced by another process.
	managed, err := os.OpenRoot(area)
	if err != nil {
		return err
	}
	defer managed.Close()
	return managed.RemoveAll(pkg.ID)
}

func managedArea(root string, create bool) (string, error) {
	if strings.TrimSpace(root) == "" {
		return "", errors.New("请先选择独立环境目录")
	}
	absolute, err := filepath.Abs(root)
	if err != nil {
		return "", err
	}
	if filepath.Dir(absolute) == absolute {
		return "", errors.New("环境目录不能是磁盘根目录")
	}
	area := filepath.Join(absolute, "packages")
	lock, _ := areaLocks.LoadOrStore(area, &sync.Mutex{})
	mu := lock.(*sync.Mutex)
	mu.Lock()
	defer mu.Unlock()
	if err := noLinks(area); err != nil {
		return "", err
	}
	if create {
		if err := os.MkdirAll(area, 0755); err != nil {
			return "", err
		}
	}
	marker := filepath.Join(area, areaMarker)
	data, err := os.ReadFile(marker)
	if err == nil {
		if string(data) != areaSignature {
			return "", errors.New("packages 目录归属标记无效")
		}
		return area, nil
	}
	if !create || !errors.Is(err, os.ErrNotExist) {
		return "", fmt.Errorf("环境 packages 目录不属于 PHPTools：%w", err)
	}
	entries, err := os.ReadDir(area)
	if err != nil {
		return "", err
	}
	if len(entries) > 0 {
		return "", errors.New("packages 目录已有非 PHPTools 文件，请选择空的独立环境目录")
	}
	file, err := os.OpenFile(marker, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0644)
	if err != nil {
		if data, readErr := os.ReadFile(marker); readErr == nil && string(data) == areaSignature {
			return area, nil
		}
		return "", err
	}
	_, writeErr := file.WriteString(areaSignature)
	closeErr := file.Close()
	if writeErr != nil {
		return "", writeErr
	}
	if closeErr != nil {
		return "", closeErr
	}
	return area, nil
}

func validatePackage(pkg model.Package) error {
	expectedID := pkg.Kind + "-" + pkg.Version + "-x64"
	validArch := pkg.Arch == "x64"
	if pkg.Kind == "nginx" {
		expectedID = pkg.Kind + "-" + pkg.Version + "-windows"
		validArch = pkg.Arch == "windows" || pkg.Arch == "x86" || pkg.Arch == "x64"
	}
	if !idPattern.MatchString(pkg.ID) || pkg.ID != expectedID || !versionPattern.MatchString(pkg.Version) || !validArch {
		return errors.New("组件标识或架构无效")
	}
	if !pkg.Verified {
		return errors.New("该版本没有可靠的校验基线，禁止安装")
	}
	if pkg.SHA256 != "" && !hashPattern.MatchString(pkg.SHA256) {
		return errors.New("组件 SHA256 格式无效")
	}
	if pkg.SHA256 == "" && pkg.SignatureURL == "" {
		return errors.New("组件缺少 SHA256 或发布者签名")
	}
	u, err := url.Parse(pkg.URL)
	if err != nil || u.Scheme != "https" || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return errors.New("组件 URL 必须是有效的官方 HTTPS 下载地址")
	}
	switch pkg.Kind {
	case "php":
		name := filepath.Base(u.Path)
		if u.Host != "downloads.php.net" || !strings.HasPrefix(u.Path, "/~windows/releases/") || !strings.HasPrefix(name, "php-"+pkg.Version+"-nts-Win32-") || !strings.HasSuffix(name, "-x64.zip") || pkg.SHA256 == "" {
			return errors.New("PHP 必须使用官方 x64 NTS ZIP 和固定 SHA256")
		}
	case "redis":
		if pkg.URL != redisPackage(pkg.Version, pkg.SHA256).URL || pkg.SHA256 == "" {
			return errors.New("Redis 必须使用已校验的 Cygwin 普通 ZIP")
		}
	case "nginx":
		if pkg.URL != nginxPackage(pkg.Version, "").URL || pkg.SignatureURL != pkg.URL+".asc" || pkg.SigningKeyURL != "https://nginx.org/keys/pluknet.key" || pkg.SigningFingerprint != nginxFingerprint {
			return errors.New("Nginx 下载来源或固定签名指纹无效")
		}
	case "mysql":
		p := mysqlPackage(pkg.Version)
		if (p.Channel != "5.7" && p.Channel != "8.0" && p.Channel != "8.4") || pkg.Channel != p.Channel {
			return errors.New("不支持的 MySQL 分支")
		}
		if pkg.URL != p.URL || pkg.SignatureURL != p.SignatureURL || pkg.SigningKeyURL != p.SigningKeyURL || pkg.SigningFingerprint != p.SigningFingerprint {
			return errors.New("MySQL 下载来源或固定签名指纹无效")
		}
	default:
		return errors.New("未知组件类型")
	}
	return nil
}

func downloadZIP(ctx context.Context, raw, dest string, progress func(int64, int64, string)) (string, error) {
	initial, err := url.Parse(raw)
	if err != nil {
		return "", err
	}
	downloadClient := *client
	downloadClient.CheckRedirect = func(req *http.Request, via []*http.Request) error {
		if len(via) >= 8 || req.URL.Scheme != "https" {
			return errors.New("拒绝不安全下载重定向")
		}
		host := req.URL.Hostname()
		allowed := host == initial.Hostname()
		if initial.Hostname() == "downloads.php.net" {
			allowed = host == "downloads.php.net" || host == "windows.php.net"
		}
		if initial.Hostname() == "github.com" {
			allowed = host == "github.com" || host == "release-assets.githubusercontent.com" || host == "objects.githubusercontent.com"
		}
		if initial.Hostname() == "cdn.mysql.com" {
			allowed = host == "cdn.mysql.com" || host == "downloads.mysql.com"
		}
		if !allowed {
			return errors.New("下载重定向离开受信任发布者域名")
		}
		return nil
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, raw, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("User-Agent", "PHPTools/0.1")
	res, err := downloadClient.Do(req)
	if err != nil {
		return "", err
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return "", statusError{res.StatusCode}
	}
	if res.ContentLength > maxArchiveBytes {
		return "", errors.New("组件 ZIP 超出 2 GiB 限制")
	}
	file, err := os.OpenFile(dest, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return "", err
	}
	hash := sha256.New()
	updates := &progressWriter{total: res.ContentLength, callback: progress}
	n, copyErr := io.Copy(io.MultiWriter(file, hash, updates), &contextReader{ctx: ctx, r: io.LimitReader(res.Body, maxArchiveBytes+1)})
	closeErr := file.Close()
	if copyErr != nil {
		return "", fmt.Errorf("下载未完成：%w", copyErr)
	}
	if closeErr != nil {
		return "", closeErr
	}
	if n > maxArchiveBytes {
		return "", errors.New("组件 ZIP 超出大小限制")
	}
	if res.ContentLength >= 0 && n != res.ContentLength {
		return "", errors.New("组件下载长度不完整")
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}
	progress(n, res.ContentLength, "下载完成，正在校验")
	return hex.EncodeToString(hash.Sum(nil)), nil
}

type progressWriter struct {
	done, total int64
	last        time.Time
	callback    func(int64, int64, string)
}

func (p *progressWriter) Write(data []byte) (int, error) {
	p.done += int64(len(data))
	if time.Since(p.last) > 150*time.Millisecond {
		p.last = time.Now()
		p.callback(p.done, p.total, "正在下载组件")
	}
	return len(data), nil
}

type statusError struct{ code int }

func (e statusError) Error() string { return fmt.Sprintf("组件下载失败：HTTP %d", e.code) }

func resolveMissingPHP(ctx context.Context, pkg model.Package, progress func(int64, int64, string)) model.Package {
	if err := probeZIP(ctx, pkg.URL); err == nil || !phpReleaseMissing(err) {
		return pkg
	}
	progress(0, 0, "原下载地址不可用，正在查询官方当前地址")
	fresh, ok := currentPHPRelease(ctx, pkg.Version)
	if !ok || fresh.URL == pkg.URL {
		return pkg
	}
	if fresh.Version != pkg.Version {
		progress(0, 0, "改为下载官方当前版本 "+fresh.Version)
	} else {
		progress(0, 0, "改为使用官方目录里的当前地址")
	}
	return fresh
}

func currentPHPRelease(ctx context.Context, version string) (model.Package, bool) {
	found, err := refreshPHP(ctx)
	if err != nil {
		return model.Package{}, false
	}
	branch := majorMinor(version)
	var exact, newest model.Package
	hasExact, hasNewest := false, false
	for _, item := range found {
		if item.Version == version {
			exact, hasExact = item, true
		}
		if majorMinor(item.Version) == branch && (!hasNewest || compareVersion(item.Version, newest.Version) > 0) {
			newest, hasNewest = item, true
		}
	}
	if hasExact {
		return exact, true
	}
	if hasNewest && newest.Version != version {
		return newest, true
	}
	return model.Package{}, false
}

func majorMinor(version string) string {
	base, _, _ := strings.Cut(version, "-")
	parts := strings.Split(base, ".")
	if len(parts) < 2 {
		return ""
	}
	return parts[0] + "." + parts[1]
}

func phpReleaseMissing(err error) bool {
	code := 0
	if _, scan := fmt.Sscanf(err.Error(), "Windows ZIP HTTP %d", &code); scan == nil && (code == http.StatusNotFound || code == http.StatusGone) {
		return true
	}
	return statusCode(err) == http.StatusNotFound || statusCode(err) == http.StatusGone
}

func connectionInterrupted(err error) bool {
	if err == nil || errors.Is(err, context.Canceled) {
		return false
	}
	if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) || errors.Is(err, context.DeadlineExceeded) {
		return true
	}
	message := err.Error()
	return strings.Contains(message, "EOF") || strings.Contains(message, "connection reset") || strings.Contains(message, "timeout") || strings.Contains(message, "Timeout")
}

func statusCode(err error) int {
	var status statusError
	if errors.As(err, &status) {
		return status.code
	}
	return 0
}

// phpArchiveURL is the same official ZIP after PHP moves it out of the current
// releases directory. The catalog hash still identifies the file.
func phpArchiveURL(raw string) (string, bool) {
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "https" || u.Host != "downloads.php.net" || u.RawQuery != "" || u.Fragment != "" {
		return "", false
	}
	name := path.Base(u.Path)
	if path.Dir(u.Path) != "/~windows/releases" || !strings.HasPrefix(name, "php-") || !strings.HasSuffix(name, "-x64.zip") {
		return "", false
	}
	u.Path = "/~windows/releases/archives/" + name
	return u.String(), true
}

func recoverExisting(target string, pkg model.Package) (model.Package, error) {
	if err := noLinks(target); err != nil {
		return pkg, err
	}
	data, err := os.ReadFile(filepath.Join(target, installMarker))
	if err != nil {
		return pkg, errors.New("组件目录已存在但缺少完整安装归属信息，拒绝覆盖")
	}
	var installed model.Package
	if json.Unmarshal(data, &installed) != nil || installed.ID != pkg.ID || installed.Kind != pkg.Kind || installed.Version != pkg.Version || installed.URL != pkg.URL || !strings.EqualFold(installed.SHA256, pkg.SHA256) || installed.SignatureURL != pkg.SignatureURL || installed.SigningKeyURL != pkg.SigningKeyURL || installed.SigningFingerprint != pkg.SigningFingerprint || installed.InstalledAt == "" {
		return pkg, errors.New("已有安装与当前校验基线不匹配，拒绝覆盖")
	}
	if _, err := time.Parse(time.RFC3339, installed.InstalledAt); err != nil {
		return pkg, errors.New("已有安装的完成时间无效")
	}
	relative, err := executableRoot(target, pkg.Kind)
	if err != nil {
		return pkg, fmt.Errorf("已有安装不完整：%w", err)
	}
	expected := filepath.Join(target, relative)
	actualArch, err := executableArch(expected, pkg.Kind)
	if err != nil || installed.Arch != actualArch {
		return pkg, errors.New("已有安装的程序架构无效")
	}
	if !strings.EqualFold(filepath.Clean(installed.Path), expected) {
		return pkg, errors.New("已有安装的执行目录不属于该组件")
	}
	pkg.Path = expected
	pkg.Arch = actualArch
	pkg.InstalledAt = installed.InstalledAt
	return pkg, nil
}
