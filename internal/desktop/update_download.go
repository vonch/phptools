package desktop

import (
	"context"
	"crypto/sha256"
	"debug/pe"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"strconv"
	"strings"
	"time"

	"phptools/internal/model"
)

const maxUpdateSize int64 = 512 * 1024 * 1024
const maxUpdateManifestSize int64 = 256 * 1024

// defaultUpdateManifestURL is the only update source. It is not stored in
// settings and is not sent to the interface.
const defaultUpdateManifestURL = "https://phptools.wuyouzhilian.cn/update.json"

var updateManifestHost = func() string {
	parsed, err := url.Parse(defaultUpdateManifestURL)
	if err != nil {
		panic(err)
	}
	return parsed.Hostname()
}()

var updateURLInError = regexp.MustCompile(`https?://[^\s"]+`)

// Releases use numeric stable versions. Reject unsupported prerelease forms
// instead of comparing them as if they were stable releases.
var updateVersionPattern = regexp.MustCompile(`^(0|[1-9][0-9]{0,8})\.(0|[1-9][0-9]{0,8})\.(0|[1-9][0-9]{0,8})$`)

type updateManifest struct {
	Version     string `json:"version"`
	DownloadURL string `json:"downloadUrl"`
	SHA256      string `json:"sha256"`
	Size        int64  `json:"size"`
	ReleaseDate string `json:"releaseDate"`
	Notes       string `json:"notes"`
	Mandatory   bool   `json:"mandatory"`
}

func validateUpdateURL(value string) error {
	u, err := url.Parse(value)
	if err != nil || u.Scheme != "https" || u.Hostname() == "" || u.User != nil || u.Fragment != "" || len(value) > 8192 {
		return errors.New("更新地址必须为不含账号信息和片段的 HTTPS 地址")
	}
	return nil
}

func redactUpdateText(text string) string {
	if updateManifestHost != "" {
		text = strings.ReplaceAll(text, updateManifestHost, "更新服务器")
	}
	return updateURLInError.ReplaceAllString(text, "更新服务器")
}

// redactUpdateAddress removes request URLs and hostnames from errors shown in
// the interface. An unchanged error is returned as-is so cancellation still
// unwraps.
func redactUpdateAddress(err error) error {
	if err == nil {
		return nil
	}
	text := err.Error()
	redacted := text
	var requestErr *url.Error
	if errors.As(err, &requestErr) && requestErr.URL != "" {
		redacted = strings.ReplaceAll(redacted, requestErr.URL, "更新服务器")
		if parsed, parseErr := url.Parse(requestErr.URL); parseErr == nil && parsed.Hostname() != "" {
			redacted = strings.ReplaceAll(redacted, parsed.Hostname(), "更新服务器")
		}
	}
	redacted = redactUpdateText(redacted)
	if redacted == text {
		return err
	}
	return errors.New(redacted)
}

func updateRedirect(request *http.Request, previous []*http.Request) error {
	if len(previous) >= 5 {
		return errors.New("更新请求重定向次数过多")
	}
	return validateUpdateURL(request.URL.String())
}

func compareUpdateVersion(left, right string) (int, error) {
	if !updateVersionPattern.MatchString(left) || !updateVersionPattern.MatchString(right) {
		return 0, errors.New("版本号必须使用三段数字，例如 0.1.18")
	}
	a, b := strings.Split(left, "."), strings.Split(right, ".")
	for i := range a {
		av, _ := strconv.ParseUint(a[i], 10, 32)
		bv, _ := strconv.ParseUint(b[i], 10, 32)
		if av > bv {
			return 1, nil
		}
		if av < bv {
			return -1, nil
		}
	}
	return 0, nil
}

func fetchUpdate(ctx context.Context, client *http.Client, manifestURL, current string) (model.UpdateInfo, error) {
	info := model.UpdateInfo{CurrentVersion: current, Configured: manifestURL != ""}
	if manifestURL == "" {
		return info, nil
	}
	info.CheckedAt = model.Now()
	if err := validateUpdateURL(manifestURL); err != nil {
		return info, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, manifestURL, nil)
	if err != nil {
		return info, err
	}
	response, err := client.Do(req)
	if err != nil {
		return info, fmt.Errorf("读取更新清单失败：%w", redactUpdateAddress(err))
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return info, fmt.Errorf("读取更新清单失败：HTTP %s", response.Status)
	}
	data, err := io.ReadAll(io.LimitReader(response.Body, maxUpdateManifestSize+1))
	if err != nil {
		return info, err
	}
	if int64(len(data)) > maxUpdateManifestSize {
		return info, errors.New("更新清单超过大小限制")
	}
	var manifest updateManifest
	if err = json.Unmarshal(data, &manifest); err != nil {
		return info, fmt.Errorf("更新清单格式无效：%w", err)
	}
	comparison, err := compareUpdateVersion(manifest.Version, current)
	if err != nil {
		return info, err
	}
	if err = validateUpdateURL(manifest.DownloadURL); err != nil {
		return info, err
	}
	digest, err := hex.DecodeString(manifest.SHA256)
	if err != nil || len(digest) != sha256.Size {
		return info, errors.New("更新清单缺少有效的 SHA-256")
	}
	if manifest.Size < 0 || manifest.Size > maxUpdateSize {
		return info, errors.New("更新包大小超出限制")
	}
	info.LatestVersion, info.Available = manifest.Version, comparison > 0
	info.DownloadURL, info.SHA256, info.Size = manifest.DownloadURL, strings.ToLower(manifest.SHA256), manifest.Size
	info.ReleaseDate, info.Notes, info.Mandatory = manifest.ReleaseDate, redactUpdateText(manifest.Notes), manifest.Mandatory
	return info, nil
}

func downloadUpdate(ctx context.Context, client *http.Client, info model.UpdateInfo, target string, progress func(int64, int64)) (resultErr error) {
	if err := validateUpdateURL(info.DownloadURL); err != nil {
		return err
	}
	expected, err := hex.DecodeString(info.SHA256)
	if err != nil || len(expected) != sha256.Size {
		return errors.New("更新包缺少有效的 SHA-256")
	}
	if info.Size < 0 || info.Size > maxUpdateSize {
		return errors.New("更新包大小超出限制")
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, info.DownloadURL, nil)
	if err != nil {
		return err
	}
	response, err := client.Do(request)
	if err != nil {
		return fmt.Errorf("下载更新失败：%w", redactUpdateAddress(err))
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return fmt.Errorf("下载更新失败：HTTP %s", response.Status)
	}
	if response.ContentLength > maxUpdateSize {
		return errors.New("更新文件超过 512 MB 限制")
	}
	if info.Size > 0 && response.ContentLength >= 0 && response.ContentLength != info.Size {
		return errors.New("更新文件大小与清单不一致")
	}
	file, err := os.OpenFile(target, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	keep := false
	defer func() {
		_ = file.Close()
		if !keep {
			if err := os.Remove(target); err != nil && !errors.Is(err, os.ErrNotExist) {
				resultErr = errors.Join(resultErr, fmt.Errorf("清理更新文件 %s 失败：%w", target, err))
			}
		}
	}()
	total := info.Size
	if total == 0 && response.ContentLength > 0 {
		total = response.ContentLength
	}
	counter := &updateProgress{total: total, notify: progress}
	hash := sha256.New()
	_, err = io.Copy(io.MultiWriter(file, hash, counter), io.LimitReader(response.Body, maxUpdateSize+1))
	if err != nil {
		return fmt.Errorf("下载更新失败：%w", redactUpdateAddress(err))
	}
	if err = ctx.Err(); err != nil {
		return err
	}
	if counter.done == 0 || counter.done > maxUpdateSize || (info.Size > 0 && counter.done != info.Size) {
		return errors.New("更新文件大小校验失败")
	}
	if !strings.EqualFold(hex.EncodeToString(hash.Sum(nil)), info.SHA256) {
		return errors.New("更新文件 SHA-256 校验失败，已保留当前版本")
	}
	if err = file.Sync(); err != nil {
		return err
	}
	if err = file.Close(); err != nil {
		return err
	}
	if err = validateUpdateExecutable(target); err != nil {
		return err
	}
	if progress != nil {
		progress(counter.done, total)
	}
	keep = true
	return nil
}

type updateProgress struct {
	done, total int64
	last        time.Time
	notify      func(int64, int64)
}

func (p *updateProgress) Write(data []byte) (int, error) {
	p.done += int64(len(data))
	if p.notify != nil && time.Since(p.last) >= 200*time.Millisecond {
		p.last = time.Now()
		p.notify(p.done, p.total)
	}
	return len(data), nil
}

func validateUpdateExecutable(path string) error {
	file, err := pe.Open(path)
	if err != nil {
		return fmt.Errorf("更新文件不是 Windows 程序，请使用 PHPTools.exe 下载地址：%w", err)
	}
	defer file.Close()
	_, x64 := file.OptionalHeader.(*pe.OptionalHeader64)
	if file.Machine != pe.IMAGE_FILE_MACHINE_AMD64 || !x64 || file.Characteristics&pe.IMAGE_FILE_EXECUTABLE_IMAGE == 0 || file.Characteristics&pe.IMAGE_FILE_DLL != 0 {
		return errors.New("更新文件必须是 Windows x64 EXE 程序")
	}
	return nil
}
