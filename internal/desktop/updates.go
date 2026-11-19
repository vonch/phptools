package desktop

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"phptools/internal/model"
)

// GetUpdateInfo reads the last check without a network request or background
// timer. The foreground UI decides when a new check is due.
func (a *App) GetUpdateInfo() model.UpdateInfo {
	a.mu.RLock()
	defer a.mu.RUnlock()
	if a.data.UpdateInfo != nil {
		return *a.data.UpdateInfo
	}
	return model.UpdateInfo{CurrentVersion: a.data.Settings.Version, Configured: true}
}

func (a *App) updateTransport(timeout time.Duration) *http.Client {
	client := http.Client{}
	if a.updateClient != nil {
		client = *a.updateClient
	}
	client.Timeout = timeout
	client.CheckRedirect = updateRedirect
	return &client
}

func (a *App) CheckForUpdate() (model.UpdateInfo, error) {
	a.updateMu.Lock()
	defer a.updateMu.Unlock()
	a.mu.RLock()
	manifestURL, current := a.data.Settings.UpdateManifestURL, a.data.Settings.Version
	if strings.TrimSpace(manifestURL) == "" {
		manifestURL = defaultUpdateManifestURL
	}
	blocked := a.closing || a.updating
	a.mu.RUnlock()
	if blocked {
		return a.GetUpdateInfo(), errors.New("软件正在退出或升级，请稍后检查更新")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	info, err := fetchUpdate(ctx, a.updateTransport(20*time.Second), manifestURL, current)
	if err != nil {
		err = redactUpdateAddress(err)
		info.Error = err.Error()
	}
	a.mu.Lock()
	a.data.UpdateInfo = &info
	a.mu.Unlock()
	a.notify()
	return info, err
}

// InstallUpdate installs the version currently published at the update
// manifest. A previously checked package is not downloaded after the official
// file has been replaced, because that file's size and hash would no longer
// match.
func (a *App) InstallUpdate() (string, error) {
	if runtime.GOOS != "windows" {
		return "", errors.New("应用升级目前仅支持 Windows")
	}
	if a.hooks.Exit == nil {
		return "", errors.New("桌面退出功能尚未初始化")
	}
	a.updateMu.Lock()
	defer a.updateMu.Unlock()
	info := a.GetUpdateInfo()
	if !info.Configured || !info.Available || info.Error != "" {
		return "", errors.New("请先成功检查到可用的新版本，再点击升级")
	}
	fresh, err := a.confirmPublishedUpdate(info)
	if err != nil {
		return "", err
	}
	executable, err := os.Executable()
	if err != nil {
		return "", err
	}
	return a.installUpdateAt(fresh, executable)
}

func (a *App) confirmPublishedUpdate(info model.UpdateInfo) (model.UpdateInfo, error) {
	a.mu.RLock()
	manifestURL, current := a.data.Settings.UpdateManifestURL, a.data.Settings.Version
	a.mu.RUnlock()
	if strings.TrimSpace(manifestURL) == "" {
		manifestURL = defaultUpdateManifestURL
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	fresh, err := fetchUpdate(ctx, a.updateTransport(20*time.Second), manifestURL, current)
	if err != nil {
		return info, redactUpdateAddress(err)
	}
	if !fresh.Available {
		a.rememberUpdate(fresh)
		return fresh, errors.New("当前版本已是最新，无需升级")
	}
	if fresh.LatestVersion != info.LatestVersion || fresh.SHA256 != info.SHA256 || fresh.Size != info.Size || fresh.DownloadURL != info.DownloadURL {
		a.rememberUpdate(fresh)
		return fresh, errors.New("官网安装包已更新，请确认新版本后再升级")
	}
	return fresh, nil
}

func (a *App) rememberUpdate(info model.UpdateInfo) {
	a.mu.Lock()
	a.data.UpdateInfo = &info
	a.mu.Unlock()
	a.notify()
}

func (a *App) installUpdateAt(info model.UpdateInfo, executable string) (string, error) {
	executable, err := filepath.Abs(executable)
	if err != nil {
		return "", err
	}
	resolved, err := filepath.EvalSymlinks(executable)
	if err != nil || !strings.EqualFold(resolved, executable) {
		return "", errors.New("当前程序路径不可用或包含目录链接，请移到普通目录后升级")
	}
	var prepared *preparedUpdate
	var directory string
	var identity os.FileInfo
	cleanup := func() error {
		if prepared != nil {
			return prepared.Cleanup()
		}
		if directory == "" {
			return nil
		}
		current, err := os.Lstat(directory)
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		if err != nil {
			return err
		}
		if identity == nil || !os.SameFile(identity, current) || current.Mode()&(os.ModeSymlink|os.ModeIrregular) != 0 {
			return fmt.Errorf("升级临时目录归属已改变，保留目录：%s", directory)
		}
		return os.RemoveAll(directory)
	}
	finishFailure := func(cause error) error {
		if cleanupErr := cleanup(); cleanupErr != nil {
			// A cleanup failure needs a visible failed task, including the
			// retained path, even when the download was cancelled.
			return fmt.Errorf("升级已停止（%v），临时文件清理失败：%w", cause, cleanupErr)
		}
		return cause
	}
	return a.queueTask("app-update", info.LatestVersion, func(ctx context.Context, id string) error {
		a.progress(id, 0, 0, "正在准备升级到 "+info.LatestVersion)
		directory, err = os.MkdirTemp(filepath.Dir(executable), ".phptools-update-")
		if err != nil {
			return fmt.Errorf("无法写入程序所在目录：%w", err)
		}
		directoryHandle, err := os.Open(directory)
		if err != nil {
			return err
		}
		var statErr error
		identity, statErr = directoryHandle.Stat()
		if err := errors.Join(statErr, directoryHandle.Close()); err != nil {
			return err
		}
		source := filepath.Join(directory, "PHPTools-new.exe")
		err = downloadUpdate(ctx, a.updateTransport(15*time.Minute), info, source, func(done, total int64) {
			a.progress(id, done, total, "正在下载 PHPTools "+info.LatestVersion)
		})
		if err != nil {
			return err
		}
		if err = ctx.Err(); err != nil {
			return err
		}
		a.progress(id, info.Size, info.Size, "校验通过，正在准备重启")
		prepared, err = prepareUpdate(directory, executable, source, a.configDir, info)
		if err == nil {
			a.progress(id, info.Size, info.Size, "准备完成，正在停止服务并升级")
		}
		return err
	}, func(workErr error) error {
		if workErr != nil {
			return finishFailure(workErr)
		}
		if err := a.quit(prepared.Start); err != nil {
			return finishFailure(err)
		}
		// The committed helper now owns these files and removes them after
		// replacement or rollback. Deleting them here would race the helper.
		return nil
	})
}

func (a *App) loadUpdateResult() {
	path := filepath.Join(a.configDir, "update-result.json")
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return
	}
	var result struct {
		Version     string `json:"version"`
		Success     bool   `json:"success"`
		Message     string `json:"message"`
		BackupPath  string `json:"backupPath"`
		Warning     string `json:"warning"`
		StagingPath string `json:"stagingPath"`
	}
	if err == nil {
		// Windows PowerShell may emit a UTF-8 BOM when saving JSON.
		data = []byte(strings.TrimPrefix(string(data), "\ufeff"))
		err = json.Unmarshal(data, &result)
	}
	if err != nil {
		a.data.Diagnostics = append(a.data.Diagnostics, model.Diagnostic{Level: "warning", Message: "无法读取上次升级结果：" + err.Error()})
		return
	}
	if !result.Success {
		message := "上次应用升级未完成：" + result.Message
		if result.BackupPath != "" {
			message += "；旧版本备份：" + result.BackupPath
		}
		a.data.Diagnostics = append(a.data.Diagnostics, model.Diagnostic{Level: "error", Message: message})
	}
	if result.Warning != "" {
		message := "上次应用升级清理未完成：" + result.Warning
		if result.StagingPath != "" {
			message += "；保留目录：" + result.StagingPath
		}
		if result.BackupPath != "" {
			message += "；旧版本备份：" + result.BackupPath
		}
		a.data.Diagnostics = append(a.data.Diagnostics, model.Diagnostic{Level: "warning", Message: message})
	}
	if err := os.Remove(path); err != nil {
		a.data.Diagnostics = append(a.data.Diagnostics, model.Diagnostic{Level: "warning", Message: "升级结果文件清理失败：" + err.Error()})
	}
}
