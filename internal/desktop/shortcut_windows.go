//go:build windows

package desktop

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/go-ole/go-ole"
	"github.com/go-ole/go-ole/oleutil"
	"golang.org/x/sys/windows"
)

func (a *App) CreateSitesRootShortcut() error {
	return a.createSitesRootShortcut(userDesktop)
}

func (a *App) createSitesRootShortcut(desktop func() (string, error)) error {
	dir, err := a.sitesDirectory()
	if err != nil {
		return err
	}
	info, err := os.Stat(dir)
	if err != nil {
		return fmt.Errorf("无法访问网站根目录：%w", err)
	}
	if !info.IsDir() {
		return errors.New("网站根目录不是文件夹")
	}
	desktopDir, err := desktop()
	if err != nil {
		return err
	}
	link := filepath.Join(desktopDir, "网站根目录.lnk")
	if _, err = os.Lstat(link); err == nil {
		return errors.New("桌面已有「网站根目录」快捷方式，没有覆盖")
	} else if !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("无法检查桌面快捷方式：%w", err)
	}
	if err = createDirectoryShortcut(link, dir); err != nil {
		return fmt.Errorf("无法创建桌面快捷方式：%w", err)
	}
	return nil
}

func userDesktop() (string, error) {
	dir, err := windows.KnownFolderPath(windows.FOLDERID_Desktop, windows.KF_FLAG_DEFAULT)
	if err != nil {
		return "", fmt.Errorf("无法找到桌面目录：%w", err)
	}
	return dir, nil
}

func createDirectoryShortcut(linkPath, target string) error {
	return withCOM(func() error {
		unknown, err := oleutil.CreateObject("WScript.Shell")
		if err != nil {
			return err
		}
		defer unknown.Release()
		shell, err := unknown.QueryInterface(ole.IID_IDispatch)
		if err != nil {
			return err
		}
		defer shell.Release()
		result, err := oleutil.CallMethod(shell, "CreateShortcut", linkPath)
		if err != nil {
			return err
		}
		shortcut := result.ToIDispatch()
		defer shortcut.Release()
		if _, err = oleutil.PutProperty(shortcut, "TargetPath", target); err != nil {
			return err
		}
		if _, err = oleutil.PutProperty(shortcut, "WorkingDirectory", target); err != nil {
			return err
		}
		if _, err = oleutil.PutProperty(shortcut, "Description", "PHPTools 网站根目录"); err != nil {
			return err
		}
		_, err = oleutil.CallMethod(shortcut, "Save")
		return err
	})
}

func shortcutTarget(linkPath string) (string, error) {
	var target string
	err := withCOM(func() error {
		unknown, err := oleutil.CreateObject("WScript.Shell")
		if err != nil {
			return err
		}
		defer unknown.Release()
		shell, err := unknown.QueryInterface(ole.IID_IDispatch)
		if err != nil {
			return err
		}
		defer shell.Release()
		result, err := oleutil.CallMethod(shell, "CreateShortcut", linkPath)
		if err != nil {
			return err
		}
		shortcut := result.ToIDispatch()
		defer shortcut.Release()
		value, err := oleutil.GetProperty(shortcut, "TargetPath")
		if err != nil {
			return err
		}
		defer value.Clear()
		target = value.ToString()
		return nil
	})
	return target, err
}

func withCOM(fn func() error) error {
	err := ole.CoInitializeEx(0, ole.COINIT_APARTMENTTHREADED)
	release := true
	if err != nil {
		var oleErr *ole.OleError
		if !errors.As(err, &oleErr) {
			return fmt.Errorf("无法初始化快捷方式组件：%w", err)
		}
		switch oleErr.Code() {
		case 1:
		case 0x80010106:
			release = false
		default:
			return fmt.Errorf("无法初始化快捷方式组件：%w", err)
		}
	}
	if release {
		defer ole.CoUninitialize()
	}
	return fn()
}
