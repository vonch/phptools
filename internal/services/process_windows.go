//go:build windows

package services

import (
	"encoding/binary"
	"errors"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
	"syscall"
	"unsafe"

	"phptools/internal/model"
)

var errProcessGone = errors.New("进程已退出")

var (
	kernel32                  = syscall.NewLazyDLL("kernel32.dll")
	queryFullProcessImageName = kernel32.NewProc("QueryFullProcessImageNameW")
	getProcessTimes           = kernel32.NewProc("GetProcessTimes")
	getExitCodeProcess        = kernel32.NewProc("GetExitCodeProcess")
	terminateProcess          = kernel32.NewProc("TerminateProcess")
	openEvent                 = kernel32.NewProc("OpenEventW")
	setEvent                  = kernel32.NewProc("SetEvent")
	localFree                 = kernel32.NewProc("LocalFree")
	ntQueryInformationProcess = syscall.NewLazyDLL("ntdll.dll").NewProc("NtQueryInformationProcess")
	advapi32                  = syscall.NewLazyDLL("advapi32.dll")
	convertSecurityDescriptor = advapi32.NewProc("ConvertStringSecurityDescriptorToSecurityDescriptorW")
	setFileSecurity           = advapi32.NewProc("SetFileSecurityW")
)

func hideConsole(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: 0x08000000}
}

func openQuery(pid int) (syscall.Handle, error) {
	if pid <= 0 {
		return 0, errProcessGone
	}
	h, err := syscall.OpenProcess(0x1000, false, uint32(pid))
	if err != nil {
		if errors.Is(err, syscall.Errno(87)) {
			return 0, errProcessGone
		}
		return 0, err
	}
	return h, nil
}

func identityHandle(h syscall.Handle) (uint64, string, error) {
	var exit uint32
	if ok, _, err := getExitCodeProcess.Call(uintptr(h), uintptr(unsafe.Pointer(&exit))); ok == 0 {
		return 0, "", err
	}
	if exit != 259 {
		return 0, "", errProcessGone
	}
	var created, ended, kernel, user syscall.Filetime
	if ok, _, err := getProcessTimes.Call(uintptr(h), uintptr(unsafe.Pointer(&created)), uintptr(unsafe.Pointer(&ended)), uintptr(unsafe.Pointer(&kernel)), uintptr(unsafe.Pointer(&user))); ok == 0 {
		return 0, "", err
	}
	buf := make([]uint16, 32768)
	size := uint32(len(buf))
	if ok, _, err := queryFullProcessImageName.Call(uintptr(h), 0, uintptr(unsafe.Pointer(&buf[0])), uintptr(unsafe.Pointer(&size))); ok == 0 {
		return 0, "", err
	}
	return uint64(created.HighDateTime)<<32 | uint64(created.LowDateTime), syscall.UTF16ToString(buf[:size]), nil
}

func processIdentity(pid int) (uint64, string, error) {
	h, err := openQuery(pid)
	if err != nil {
		return 0, "", err
	}
	defer syscall.CloseHandle(h)
	return identityHandle(h)
}

type unicodeString struct {
	Length        uint16
	MaximumLength uint16
	Buffer        uintptr
}

func processCommandLine(h syscall.Handle) (string, error) {
	// ProcessCommandLineInformation copies both UNICODE_STRING and its string into
	// our buffer. Check the returned address before interpreting it.
	var needed uint32
	_, _, _ = ntQueryInformationProcess.Call(uintptr(h), 60, 0, 0, uintptr(unsafe.Pointer(&needed)))
	if needed < uint32(unsafe.Sizeof(unicodeString{})) || needed > 1024*1024 {
		return "", errors.New("无法查询进程启动配置")
	}
	buf := make([]byte, needed)
	status, _, _ := ntQueryInformationProcess.Call(uintptr(h), 60, uintptr(unsafe.Pointer(&buf[0])), uintptr(len(buf)), uintptr(unsafe.Pointer(&needed)))
	if int32(status) < 0 {
		return "", fmt.Errorf("查询进程命令行失败：NTSTATUS 0x%x", status)
	}
	u := *(*unicodeString)(unsafe.Pointer(&buf[0]))
	base := uintptr(unsafe.Pointer(&buf[0]))
	end := base + uintptr(len(buf))
	if u.Length%2 != 0 || u.Buffer < base || u.Buffer > end || uintptr(u.Length) > end-u.Buffer {
		return "", errors.New("无效进程命令行缓冲区")
	}
	if u.Length == 0 {
		return "", errors.New("进程命令行为空")
	}
	offset := int(u.Buffer - base)
	units := make([]uint16, int(u.Length)/2)
	for n := range units {
		units[n] = binary.LittleEndian.Uint16(buf[offset+n*2 : offset+n*2+2])
	}
	return syscall.UTF16ToString(units), nil
}

func normalizedCommand(value string) string {
	return strings.ToLower(strings.ReplaceAll(value, `\`, `/`))
}

func verifyProcess(p processRecord) error {
	h, err := openQuery(p.PID)
	if err != nil {
		return err
	}
	defer syscall.CloseHandle(h)
	created, exe, err := identityHandle(h)
	if err != nil {
		return err
	}
	if created != p.Created || !samePath(exe, p.Executable) {
		return errProcessGone
	}
	line, err := processCommandLine(h)
	if err != nil {
		return err
	}
	if p.ConfigArgument == "" || !strings.Contains(normalizedCommand(line), normalizedCommand(p.ConfigArgument)) {
		return errors.New("进程命令行不包含此实例配置路径")
	}
	return nil
}

func terminateOwned(i model.Instance) error {
	if i.Kind != "php" {
		return errors.New("只允许对受管 PHP-CGI 使用 Windows TerminateProcess")
	}
	p := processRecord{PID: i.PID, Created: i.ProcessCreated, Executable: i.Executable, Config: i.ConfigPath, ConfigArgument: configArgument(i)}
	if err := verifyProcess(p); err != nil {
		return err
	}
	h, err := syscall.OpenProcess(0x1001, false, uint32(i.PID))
	if err != nil {
		return err
	}
	defer syscall.CloseHandle(h)
	created, exe, err := identityHandle(h)
	if err != nil {
		return err
	}
	if created != i.ProcessCreated || !samePath(exe, i.Executable) {
		return errors.New("进程身份已发生变化，拒绝停止")
	}
	// php-cgi on Windows exposes no graceful-stop command or Unix signal handler.
	// Only its verified dedicated process is terminated; databases never use this.
	if ok, _, err := terminateProcess.Call(uintptr(h), 0); ok == 0 {
		return err
	}
	return nil
}

func mysqlShutdownEvent(i model.Instance) error {
	if i.Kind != "mysql" {
		return errors.New("原生 MySQL 停机事件只适用于受管数据库")
	}
	p := processRecord{PID: i.PID, Created: i.ProcessCreated, Executable: i.Executable, Config: i.ConfigPath, ConfigArgument: configArgument(i)}
	if err := verifyProcess(p); err != nil {
		return err
	}
	h, err := openQuery(i.PID)
	if err != nil {
		return err
	}
	defer syscall.CloseHandle(h)
	created, exe, err := identityHandle(h)
	if err != nil {
		return err
	}
	if created != i.ProcessCreated || !samePath(exe, i.Executable) {
		return errors.New("MySQL 进程身份发生变化")
	}
	var name string
	if strings.HasPrefix(i.PackageID, "mysql-5.7.") {
		name = "MySQLShutdown" + strconv.Itoa(i.PID)
	} else if strings.HasPrefix(i.PackageID, "mysql-8.") {
		line, err := processCommandLine(h)
		if err != nil {
			return err
		}
		if strings.Contains(" "+line+" ", " --no-monitor ") {
			name = "MYSQLShutdown" + strconv.Itoa(i.PID)
		} else {
			name = "mysqld" + strconv.Itoa(i.PID) + "_shutdown"
		}
	} else {
		return errors.New("当前 MySQL 版本没有经过核实的 Windows 停机事件协议")
	}
	// Official sql/mysqld.cc shutdown threads flush storage and close connections:
	// https://github.com/mysql/mysql-server/blob/mysql-5.7.44/sql/mysqld.cc
	// https://github.com/mysql/mysql-server/blob/mysql-8.4.11/sql/mysqld.cc
	utf, err := syscall.UTF16PtrFromString(name)
	if err != nil {
		return err
	}
	event, _, callErr := openEvent.Call(0x0002, 0, uintptr(unsafe.Pointer(utf)))
	if event == 0 {
		return fmt.Errorf("无法打开受管 MySQL 优雅关闭事件：%w", callErr)
	}
	defer syscall.CloseHandle(syscall.Handle(event))
	if ok, _, callErr := setEvent.Call(event); ok == 0 {
		return fmt.Errorf("无法触发 MySQL 优雅关闭事件：%w", callErr)
	}
	return nil
}

func protectPath(path string, directory bool) error {
	sddl := "D:P(A;;FA;;;SY)(A;;FA;;;OW)"
	if directory {
		sddl = "D:P(A;OICI;FA;;;SY)(A;OICI;FA;;;OW)"
	}
	s, err := syscall.UTF16PtrFromString(sddl)
	if err != nil {
		return err
	}
	var descriptor uintptr
	if ok, _, err := convertSecurityDescriptor.Call(uintptr(unsafe.Pointer(s)), 1, uintptr(unsafe.Pointer(&descriptor)), 0); ok == 0 {
		return fmt.Errorf("无法建立凭据文件访问控制：%w", err)
	}
	defer localFree.Call(descriptor)
	name, err := syscall.UTF16PtrFromString(path)
	if err != nil {
		return err
	}
	if ok, _, err := setFileSecurity.Call(uintptr(unsafe.Pointer(name)), 0x80000004, descriptor); ok == 0 {
		return fmt.Errorf("无法限制凭据文件访问权限：%w", err)
	}
	return nil
}
