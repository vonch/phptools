package sites

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"unsafe"

	"golang.org/x/sys/windows"
)

type hostsJob struct {
	Domains []string `json:"domains"`
}

func hostsPath() string {
	return filepath.Join(os.Getenv("SystemRoot"), "System32", "drivers", "etc", "hosts")
}

// CheckHosts is read-only: diagnostics must never prompt for elevation or
// replace another application's mappings as a side effect of a health check.
func CheckHosts(domains []string) error {
	original, err := os.ReadFile(hostsPath())
	if err != nil {
		return fmt.Errorf("无法读取 hosts：%w", err)
	}
	next, err := HostsContent(original, domains)
	if err != nil {
		return err
	}
	if !bytes.Equal(original, next) {
		return errors.New("hosts 的 PHPTools 域名映射需要同步")
	}
	return nil
}

func SyncHosts(root string, domains []string) error {
	original, err := os.ReadFile(hostsPath())
	if err != nil {
		return err
	}
	next, err := HostsContent(original, domains)
	if err != nil {
		return err
	}
	if bytes.Equal(original, next) {
		return nil
	}
	if windows.GetCurrentProcessToken().IsElevated() {
		return writeHosts(root, domains)
	}
	jobPath := filepath.Join(root, "cache", "hosts-"+randomID()+".json")
	data, _ := json.Marshal(hostsJob{Domains: domains})
	if err := os.WriteFile(jobPath, data, 0600); err != nil {
		return err
	}
	defer os.Remove(jobPath)
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	verb, _ := windows.UTF16PtrFromString("runas")
	file, _ := windows.UTF16PtrFromString(exe)
	params, _ := windows.UTF16PtrFromString("--sync-hosts " + windows.EscapeArg(jobPath))
	info := shellExecuteInfo{Mask: 0x40, Verb: verb, File: file, Parameters: params, Show: 0}
	info.Size = uint32(unsafe.Sizeof(info))
	result, _, callErr := windows.NewLazySystemDLL("shell32.dll").NewProc("ShellExecuteExW").Call(uintptr(unsafe.Pointer(&info)))
	if result == 0 {
		return fmt.Errorf("更新 hosts 需要管理员授权：%w", callErr)
	}
	defer windows.CloseHandle(info.Process)
	if _, err := windows.WaitForSingleObject(info.Process, windows.INFINITE); err != nil {
		return err
	}
	var code uint32
	if err := windows.GetExitCodeProcess(info.Process, &code); err != nil {
		return err
	}
	if code != 0 {
		return errors.New("hosts 更新失败或管理员授权已取消；原有其他 hosts 条目不变")
	}
	return nil
}

type shellExecuteInfo struct {
	Size       uint32
	Mask       uint32
	Window     uintptr
	Verb       *uint16
	File       *uint16
	Parameters *uint16
	Directory  *uint16
	Show       int32
	Instance   uintptr
	IDList     uintptr
	Class      *uint16
	ClassKey   uintptr
	HotKey     uint32
	Icon       uintptr
	Process    windows.Handle
}

// RunHostsHelper handles a fixed, domain-only privileged operation, never a generic file write.
func RunHostsHelper(args []string) (bool, error) {
	if len(args) == 0 || args[0] != "--sync-hosts" {
		return false, nil
	}
	if len(args) != 2 {
		return true, errors.New("hosts 参数无效")
	}
	path, err := filepath.Abs(args[1])
	if err != nil {
		return true, err
	}
	root := filepath.Dir(filepath.Dir(path))
	if filepath.Base(filepath.Dir(path)) != "cache" || !strings.HasPrefix(filepath.Base(path), "hosts-") || filepath.Ext(path) != ".json" {
		return true, errors.New("hosts 任务路径无效")
	}
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil || !strings.EqualFold(resolved, path) {
		return true, errors.New("hosts 任务不能使用链接")
	}
	marker, err := os.ReadFile(filepath.Join(root, ".phptools-runtime"))
	if err != nil || string(marker) != "PHPTools runtime v1\n" {
		return true, errors.New("hosts 任务不属于 PHPTools 环境")
	}
	info, err := os.Stat(path)
	if err != nil || info.Size() > 65536 {
		return true, errors.New("hosts 任务无效")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return true, err
	}
	var job hostsJob
	if err := json.Unmarshal(data, &job); err != nil {
		return true, err
	}
	return true, writeHosts(root, job.Domains)
}

func writeHosts(root string, domains []string) error {
	path := hostsPath()
	original, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	next, err := HostsContent(original, domains)
	if err != nil {
		return err
	}
	if bytes.Equal(next, original) {
		return nil
	}
	backup := filepath.Join(root, "backups", "hosts-"+randomID()+".bak")
	if err := os.WriteFile(backup, original, 0600); err != nil {
		return err
	}
	// Windows resolver/security software may keep hosts open without delete
	// sharing. Update its existing handle under a byte-range lock, preserving
	// the file's ACL and identity rather than replacing it.
	f, err := os.OpenFile(path, os.O_RDWR, 0)
	if err != nil {
		return err
	}
	defer f.Close()
	var lock windows.Overlapped
	handle := windows.Handle(f.Fd())
	if err = windows.LockFileEx(handle, windows.LOCKFILE_EXCLUSIVE_LOCK|windows.LOCKFILE_FAIL_IMMEDIATELY, 0, 0xffffffff, 0xffffffff, &lock); err != nil {
		return fmt.Errorf("hosts 正被其他程序使用，请稍后重试：%w", err)
	}
	defer windows.UnlockFileEx(handle, 0, 0xffffffff, 0xffffffff, &lock)
	current, err := io.ReadAll(f)
	if err != nil {
		return err
	}
	if !bytes.Equal(current, original) {
		return errors.New("hosts 已被其他程序修改，请重试")
	}
	write := func(data []byte) error {
		n, err := f.WriteAt(data, 0)
		if err != nil {
			return err
		}
		if n != len(data) {
			return io.ErrShortWrite
		}
		if err = f.Truncate(int64(len(data))); err != nil {
			return err
		}
		return f.Sync()
	}
	if err := write(next); err != nil {
		return errors.Join(fmt.Errorf("写入 hosts 失败：%w", err), write(original))
	}
	return nil
}
