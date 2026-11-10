package desktop

import (
	"errors"

	"phptools/internal/model"
	"phptools/internal/prerequisites"
)

// This check works before an environment has been selected and performs no
// service operations, installs or registry writes.
func (a *App) GetRuntimeDependencies() model.DependencyReport {
	a.mu.RLock()
	pkgs := append([]model.Package{}, a.data.Packages...)
	a.mu.RUnlock()
	return prerequisites.Inspect(pkgs)
}

func (a *App) OpenRuntimeInstaller(id string) error {
	url, err := prerequisites.InstallURL(id)
	if err != nil {
		return err
	}
	if a.hooks.OpenURL == nil {
		return errors.New("浏览器打开功能尚未初始化")
	}
	return a.hooks.OpenURL(url)
}
