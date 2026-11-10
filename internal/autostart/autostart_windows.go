// Package autostart manages this PHPTools profile's Windows login registration.
// Wails' Windows manager identifies entries by executable alone and may delete
// another value name or profile's registration. This package limits access to
// the PHPTools value and verifies both executable and configuration directory.
package autostart

import (
	"encoding/binary"
	"errors"
	"fmt"
	"path/filepath"
	"strings"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"
)

const runKeyPath = `Software\Microsoft\Windows\CurrentVersion\Run`
const approvedKeyPath = `Software\Microsoft\Windows\CurrentVersion\Explorer\StartupApproved\Run`

// State is read from Windows, so the switch also reflects external changes.
// A different program or profile occupying the PHPTools value is a conflict,
// not an enabled switch for the current profile.
type State struct {
	Enabled  bool
	Conflict string
}

type registration struct {
	keyPath         string
	approvedKeyPath string
}

// Status reports whether the given executable and profile launch at login.
func Status(executable, configDir string) (State, error) {
	return (registration{keyPath: runKeyPath, approvedKeyPath: approvedKeyPath}).status(executable, configDir)
}

// Set only changes the PHPTools value owned by this executable and profile.
// It never removes another value or replaces a conflicting registration.
func Set(executable, configDir string, enabled bool) error {
	return (registration{keyPath: runKeyPath, approvedKeyPath: approvedKeyPath}).set(executable, configDir, enabled)
}

func command(executable, configDir string) (string, error) {
	for label, path := range map[string]string{"程序": executable, "配置目录": configDir} {
		if !filepath.IsAbs(path) || strings.ContainsAny(path, "\x00\r\n\"") {
			return "", fmt.Errorf("开机启动的%s必须是有效的绝对路径", label)
		}
	}
	return windows.ComposeCommandLine([]string{filepath.Clean(executable), "--config-dir", filepath.Clean(configDir)}), nil
}

func (r registration) status(executable, configDir string) (State, error) {
	want, err := command(executable, configDir)
	if err != nil {
		return State{}, err
	}
	key, err := registry.OpenKey(registry.CURRENT_USER, r.keyPath, registry.QUERY_VALUE)
	if errors.Is(err, registry.ErrNotExist) {
		return State{}, nil
	}
	if err != nil {
		return State{}, fmt.Errorf("无法读取开机启动项：%w", err)
	}
	defer key.Close()
	state, err := readState(key, want)
	if err != nil || !state.Enabled {
		return state, err
	}
	if conflict := r.approvalConflict(); conflict != "" {
		return State{Conflict: conflict}, nil
	}
	return state, nil
}

// Windows can retain a Run value after the user disables it in Startup apps.
// StartupApproved is only inspected; this application never changes Windows'
// approval decisions. Missing records are normal for newly registered entries.
func (r registration) approvalConflict() string {
	key, err := registry.OpenKey(registry.CURRENT_USER, r.approvedKeyPath, registry.QUERY_VALUE)
	if errors.Is(err, registry.ErrNotExist) {
		return ""
	}
	if err != nil {
		return fmt.Sprintf("无法读取 Windows 的 PHPTools 启动审批状态，请在 Windows“启动应用”中检查后重新检查：%v", err)
	}
	defer key.Close()
	value, _, err := key.GetBinaryValue("PHPTools")
	if errors.Is(err, registry.ErrNotExist) {
		return ""
	}
	if err != nil {
		return fmt.Sprintf("无法读取 Windows 的 PHPTools 启动审批状态，请在 Windows“启动应用”中检查后重新检查：%v", err)
	}
	// The record contains a DWORD state followed by an eight-byte FILETIME.
	// Unknown formats and states must not be presented as successfully enabled.
	if len(value) == 12 {
		switch binary.LittleEndian.Uint32(value[:4]) {
		case 2, 6:
			return ""
		case 3, 7:
			return "PHPTools 已被 Windows 禁用开机启动，请在 Windows“启动应用”中允许 PHPTools 后重新检查。"
		}
	}
	return "无法识别 Windows 的 PHPTools 启动审批状态，请在 Windows“启动应用”中检查后重新检查。"
}

func readState(key registry.Key, want string) (State, error) {
	value, valueType, err := key.GetStringValue("PHPTools")
	if errors.Is(err, registry.ErrNotExist) {
		return State{}, nil
	}
	if errors.Is(err, registry.ErrUnexpectedType) {
		return conflictState(), nil
	}
	if err != nil {
		return State{}, fmt.Errorf("无法读取开机启动项 PHPTools：%w", err)
	}
	if valueType != registry.SZ || !sameCommand(value, want) {
		return conflictState(), nil
	}
	return State{Enabled: true}, nil
}

func conflictState() State {
	return State{Conflict: "开机启动项 PHPTools 已被其他程序或配置目录占用，请先在原程序中关闭。"}
}

func sameCommand(value, want string) bool {
	args, err := windows.DecomposeCommandLine(value)
	if err != nil || len(args) != 3 || args[1] != "--config-dir" {
		return false
	}
	expected, err := windows.DecomposeCommandLine(want)
	if err != nil {
		return false
	}
	return filepath.IsAbs(args[0]) && filepath.IsAbs(args[2]) &&
		strings.EqualFold(filepath.Clean(args[0]), expected[0]) &&
		strings.EqualFold(filepath.Clean(args[2]), expected[2])
}

func (r registration) set(executable, configDir string, enabled bool) error {
	want, err := command(executable, configDir)
	if err != nil {
		return err
	}
	var key registry.Key
	if enabled {
		key, _, err = registry.CreateKey(registry.CURRENT_USER, r.keyPath, registry.QUERY_VALUE|registry.SET_VALUE)
	} else {
		key, err = registry.OpenKey(registry.CURRENT_USER, r.keyPath, registry.QUERY_VALUE|registry.SET_VALUE)
	}
	if !enabled && errors.Is(err, registry.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("无法修改开机启动项：%w", err)
	}
	defer key.Close()
	state, err := readState(key, want)
	if err != nil {
		return err
	}
	if state.Conflict != "" {
		return errors.New(state.Conflict)
	}
	if enabled {
		if err := key.SetStringValue("PHPTools", want); err != nil {
			return fmt.Errorf("无法启用开机启动：%w", err)
		}
	} else if state.Enabled {
		if err := key.DeleteValue("PHPTools"); err != nil && !errors.Is(err, registry.ErrNotExist) {
			return fmt.Errorf("无法关闭开机启动：%w", err)
		}
	}
	return nil
}
