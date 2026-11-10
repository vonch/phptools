package desktop

import (
	"errors"
	"os"
	"path/filepath"

	"phptools/internal/autostart"
	"phptools/internal/model"
)

func (a *App) autoStartTarget() (string, string, error) {
	executable, err := os.Executable()
	if err != nil {
		return "", "", err
	}
	configDir, err := filepath.Abs(a.configDir)
	return executable, configDir, err
}

func (a *App) GetAutoStart() (model.AutoStartStatus, error) {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.readAutoStart()
}

func (a *App) readAutoStart() (model.AutoStartStatus, error) {
	executable, configDir, err := a.autoStartTarget()
	if err != nil {
		return model.AutoStartStatus{}, err
	}
	status, err := autostart.Status(executable, configDir)
	return model.AutoStartStatus{Enabled: status.Enabled, Conflict: status.Conflict}, err
}

func (a *App) SetAutoStart(enabled bool) (model.AutoStartStatus, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.closing {
		return model.AutoStartStatus{}, errors.New("软件正在停止服务")
	}
	executable, configDir, err := a.autoStartTarget()
	if err != nil {
		return model.AutoStartStatus{}, err
	}
	if err = autostart.Set(executable, configDir, enabled); err != nil {
		return model.AutoStartStatus{}, err
	}
	return a.readAutoStart()
}
