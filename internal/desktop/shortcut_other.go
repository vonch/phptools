//go:build !windows

package desktop

import "errors"

func (a *App) CreateSitesRootShortcut() error {
	return errors.New("创建桌面快捷方式目前仅支持 Windows")
}
