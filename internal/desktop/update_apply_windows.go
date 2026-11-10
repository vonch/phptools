//go:build windows

package desktop

import (
	"crypto/rand"
	"crypto/sha256"
	_ "embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"golang.org/x/sys/windows"
	"phptools/internal/model"
)

//go:embed update_apply.ps1
var updateApplyScript []byte

type updateApplyPlan struct {
	Directory     string `json:"directory"`
	Target        string `json:"target"`
	Source        string `json:"source"`
	Backup        string `json:"backup"`
	ConfigDir     string `json:"configDir"`
	RestartArgs   string `json:"restartArgs"`
	Version       string `json:"version"`
	SHA256        string `json:"sha256"`
	PreviousHash  string `json:"previousHash"`
	Token         string `json:"token"`
	ParentPID     int    `json:"parentPid"`
	ParentCreated string `json:"parentCreated"`
}

type preparedUpdate struct {
	directory string
	identity  os.FileInfo
	plan      updateApplyPlan
	command   *exec.Cmd
	done      chan error
	started   bool
}

func prepareUpdate(directory, target, source, configDir string, info model.UpdateInfo) (*preparedUpdate, error) {
	for _, path := range []string{directory, target, source, configDir} {
		if !filepath.IsAbs(path) || strings.ContainsAny(path, "\x00\r\n\"") {
			return nil, errors.New("升级路径必须是有效的绝对路径")
		}
		if err := checkUpdatePlainPath(path); err != nil {
			return nil, err
		}
	}
	directory, target, source, configDir = filepath.Clean(directory), filepath.Clean(target), filepath.Clean(source), filepath.Clean(configDir)
	if !strings.EqualFold(filepath.Dir(directory), filepath.Dir(target)) || !strings.EqualFold(filepath.Dir(source), directory) {
		return nil, errors.New("升级文件必须位于当前程序目录下的独立暂存目录")
	}
	directoryHandle, err := os.Open(directory)
	if err != nil {
		return nil, err
	}
	directoryInfo, statErr := directoryHandle.Stat()
	err = errors.Join(statErr, directoryHandle.Close())
	if err != nil || !directoryInfo.IsDir() {
		return nil, errors.New("升级暂存目录不可用")
	}
	if configInfo, err := os.Stat(configDir); err != nil || !configInfo.IsDir() {
		return nil, errors.New("应用配置目录不可用")
	}
	for _, path := range []string{target, source} {
		if file, err := os.Stat(path); err != nil || !file.Mode().IsRegular() {
			return nil, errors.New("升级源和目标必须是普通程序文件")
		}
	}
	sourceHash, err := updateApplyHash(source)
	if err != nil {
		return nil, err
	}
	if len(info.SHA256) != sha256.Size*2 || !strings.EqualFold(sourceHash, info.SHA256) {
		return nil, errors.New("升级文件 SHA-256 校验失败")
	}
	previousHash, err := updateApplyHash(target)
	if err != nil {
		return nil, err
	}
	parentCreated, err := updateParentCreation(os.Getpid())
	if err != nil {
		return nil, err
	}
	p := &preparedUpdate{directory: directory, identity: directoryInfo}
	p.plan = updateApplyPlan{
		Directory: directory, Target: target, Source: source,
		Backup: filepath.Join(directory, "previous.exe"), ConfigDir: configDir,
		RestartArgs: windows.ComposeCommandLine([]string{"--config-dir", configDir}),
		Version:     info.LatestVersion, SHA256: sourceHash, PreviousHash: previousHash,
		Token: rand.Text(), ParentPID: os.Getpid(), ParentCreated: parentCreated,
	}
	for name, data := range map[string][]byte{
		".update-owner": []byte(p.plan.Token), "apply-update.ps1": updateApplyScript,
	} {
		if err := writeNewUpdateFile(filepath.Join(directory, name), data); err != nil {
			return nil, err
		}
	}
	if err := p.writePlan(); err != nil {
		return nil, err
	}
	return p, nil
}

func (p *preparedUpdate) writePlan() error {
	raw, err := json.Marshal(p.plan)
	if err != nil {
		return err
	}
	return writeNewUpdateFile(filepath.Join(p.directory, "plan.json"), raw)
}

// Start is called only after managed services have stopped. It waits until the
// helper holds the original process handle, then commits the handoff. The
// helper still cannot replace anything until this process actually exits.
func (p *preparedUpdate) Start() error {
	if p.started {
		return errors.New("升级辅助程序已经启动")
	}
	if err := p.checkOwnership(); err != nil {
		return err
	}
	systemDir, err := windows.GetSystemDirectory()
	if err != nil {
		return err
	}
	command := exec.Command(filepath.Join(systemDir, "WindowsPowerShell", "v1.0", "powershell.exe"),
		"-NoProfile", "-NonInteractive", "-WindowStyle", "Hidden", "-ExecutionPolicy", "Bypass",
		"-File", filepath.Join(p.directory, "apply-update.ps1"), "-PlanPath", filepath.Join(p.directory, "plan.json"))
	command.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: windows.CREATE_NO_WINDOW}
	if err := command.Start(); err != nil {
		return fmt.Errorf("无法启动升级辅助程序：%w", err)
	}
	p.command, p.done, p.started = command, make(chan error, 1), true
	go func() { p.done <- command.Wait(); close(p.done) }()
	deadline := time.NewTimer(15 * time.Second)
	ticker := time.NewTicker(50 * time.Millisecond)
	defer deadline.Stop()
	defer ticker.Stop()
	for {
		select {
		case err := <-p.done:
			return fmt.Errorf("升级辅助程序未完成准备：%v", err)
		case <-deadline.C:
			_ = p.stopHelper()
			return errors.New("升级辅助程序准备超时，当前版本已保留")
		case <-ticker.C:
			ready, err := os.ReadFile(filepath.Join(p.directory, "ready"))
			if err != nil {
				continue
			}
			if string(ready) != p.plan.Token {
				_ = p.stopHelper()
				return errors.New("升级辅助程序准备凭据不匹配")
			}
			if err := writeUpdateCommit(filepath.Join(p.directory, "commit"), []byte(p.plan.Token)); err != nil {
				_ = p.stopHelper()
				return err
			}
			return nil
		}
	}
}

// Cleanup is for failures before shutdown. A committed helper cleans its own
// staging directory after replacement or rollback. A recovery backup is never
// discarded when the original executable could not be restored.
func (p *preparedUpdate) Cleanup() error {
	if p == nil {
		return nil
	}
	if err := p.stopHelper(); err != nil {
		return err
	}
	if _, err := os.Lstat(p.directory); os.IsNotExist(err) {
		return nil
	}
	if err := p.checkOwnership(); err != nil {
		return err
	}
	if _, err := os.Lstat(p.plan.Backup); err == nil {
		hash, hashErr := updateApplyHash(p.plan.Target)
		if hashErr != nil || !strings.EqualFold(hash, p.plan.PreviousHash) {
			return fmt.Errorf("升级备份已保留，请检查 %s", p.plan.Backup)
		}
	} else if !os.IsNotExist(err) {
		return err
	}
	return os.RemoveAll(p.directory)
}

func (p *preparedUpdate) stopHelper() error {
	if p.command == nil || p.done == nil {
		return nil
	}
	select {
	case <-p.done:
		return nil
	default:
	}
	if p.checkOwnership() == nil {
		_ = writeNewUpdateFile(filepath.Join(p.directory, "abort"), []byte(p.plan.Token))
		select {
		case <-p.done:
			return nil
		case <-time.After(3 * time.Second):
		}
	}
	if err := p.command.Process.Kill(); err != nil && !errors.Is(err, os.ErrProcessDone) {
		return fmt.Errorf("无法结束本次升级辅助程序：%w", err)
	}
	select {
	case <-p.done:
		return nil
	case <-time.After(5 * time.Second):
		return fmt.Errorf("升级辅助程序尚未结束，已保留 %s", p.directory)
	}
}

func (p *preparedUpdate) checkOwnership() error {
	if p.directory != p.plan.Directory || p.identity == nil {
		return errors.New("升级暂存目录归属不匹配")
	}
	if err := checkUpdatePlainPath(p.directory); err != nil {
		return err
	}
	current, err := os.Lstat(p.directory)
	if err != nil || !os.SameFile(p.identity, current) {
		return errors.New("升级暂存目录已被替换")
	}
	marker, err := os.ReadFile(filepath.Join(p.directory, ".update-owner"))
	if err != nil || string(marker) != p.plan.Token {
		return errors.New("升级暂存目录缺少本次归属凭据")
	}
	return nil
}

func checkUpdatePlainPath(path string) error {
	for path = filepath.Clean(path); ; path = filepath.Dir(path) {
		name, err := windows.UTF16PtrFromString(path)
		if err != nil {
			return err
		}
		attributes, err := windows.GetFileAttributes(name)
		if err != nil {
			return fmt.Errorf("升级路径不可用 %s：%w", path, err)
		}
		if attributes&windows.FILE_ATTRIBUTE_REPARSE_POINT != 0 {
			return fmt.Errorf("升级路径包含目录链接或特殊文件：%s", path)
		}
		if filepath.Dir(path) == path {
			return nil
		}
	}
}

func updateApplyHash(path string) (string, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer file.Close()
	hash := sha256.New()
	if _, err := io.Copy(hash, file); err != nil {
		return "", err
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}

func updateParentCreation(pid int) (string, error) {
	handle, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, uint32(pid))
	if err != nil {
		return "", err
	}
	defer windows.CloseHandle(handle)
	var created, ended, kernel, user windows.Filetime
	if err := windows.GetProcessTimes(handle, &created, &ended, &kernel, &user); err != nil {
		return "", err
	}
	return strconv.FormatUint(uint64(created.HighDateTime)<<32|uint64(created.LowDateTime), 10), nil
}

func writeNewUpdateFile(path string, data []byte) error {
	file, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	_, writeErr := file.Write(data)
	err = errors.Join(writeErr, file.Close())
	if err != nil {
		return errors.Join(err, os.Remove(path))
	}
	return nil
}

func writeUpdateCommit(path string, token []byte) error {
	// The helper treats the signal's existence as a committed handoff. Publish
	// it only after the complete token has been written and its handle closed.
	pending := path + ".pending"
	if err := writeNewUpdateFile(pending, token); err != nil {
		return err
	}
	from, err := windows.UTF16PtrFromString(pending)
	if err == nil {
		var to *uint16
		to, err = windows.UTF16PtrFromString(path)
		if err == nil {
			// MoveFile refuses an existing destination, preserving the original
			// create-once ownership requirement of the handoff signal.
			err = windows.MoveFile(from, to)
		}
	}
	if err != nil {
		return errors.Join(err, os.Remove(pending))
	}
	return nil
}
