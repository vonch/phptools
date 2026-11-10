//go:build windows

package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"sort"
	"strings"
	"syscall"
	"time"
)

type processInfo struct {
	PID        int    `json:"pid"`
	Created    string `json:"created"`
	Executable string `json:"executable"`
}

func snapshot(ctx context.Context, root string) ([]processInfo, error) {
	// This query is read-only and excludes arguments, environment and credentials.
	script := `$ErrorActionPreference='Stop'; $root=$env:PHPTOOLS_REWRITE_PROCESS_ROOT; $items=@(Get-CimInstance Win32_Process | Where-Object { $_.ExecutablePath -and $_.ExecutablePath.StartsWith($root + '\', [StringComparison]::OrdinalIgnoreCase) } | Select-Object @{n='pid';e={$_.ProcessId}}, @{n='created';e={$_.CreationDate.ToString('o')}}, @{n='executable';e={$_.ExecutablePath}}); ConvertTo-Json -Compress -InputObject $items`
	commandCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	cmd := exec.CommandContext(commandCtx, "powershell.exe", "-NoProfile", "-NonInteractive", "-Command", script)
	cmd.Env = append(os.Environ(), "PHPTOOLS_REWRITE_PROCESS_ROOT="+strings.TrimRight(root, `\`))
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: 0x08000000}
	var stderr strings.Builder
	cmd.Stderr = &stderr
	b, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("read-only process snapshot failed: %w: %s", err, stderr.String())
	}
	results := []processInfo{}
	if err = json.Unmarshal(b, &results); err != nil {
		return nil, err
	}
	sort.Slice(results, func(a, b int) bool { return results[a].PID < results[b].PID })
	return results, nil
}
