//go:build !windows

package desktop

import (
	"errors"

	"phptools/internal/model"
)

type preparedUpdate struct{}

func prepareUpdate(directory, target, source, configDir string, info model.UpdateInfo) (*preparedUpdate, error) {
	return nil, errors.New("应用升级目前仅支持 Windows")
}

func (p *preparedUpdate) Start() error {
	return errors.New("应用升级目前仅支持 Windows")
}

func (p *preparedUpdate) Cleanup() error { return nil }
