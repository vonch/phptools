//go:build !windows

package services

import (
	"errors"
	"os/exec"
	"phptools/internal/model"
)

var errProcessGone = errors.New("进程已退出")
var errPlatform = errors.New("PHPTools 服务管理仅支持 Windows")

func hideConsole(cmd *exec.Cmd)                       {}
func processIdentity(pid int) (uint64, string, error) { return 0, "", errPlatform }
func verifyProcess(p processRecord) error             { return errPlatform }
func terminateOwned(i model.Instance) error           { return errPlatform }
func protectPath(path string, directory bool) error   { return errPlatform }
func mysqlShutdownEvent(i model.Instance) error       { return errPlatform }
