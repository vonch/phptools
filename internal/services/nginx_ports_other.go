//go:build !windows

package services

import "phptools/internal/model"

func nginxTCPListeners() ([]nginxListener, error) { return nil, errPlatform }

func nginxOwnedListenerProcesses(i model.Instance) (map[int]bool, error) {
	return nil, errPlatform
}
