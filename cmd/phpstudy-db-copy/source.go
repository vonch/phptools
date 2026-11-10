//go:build windows

package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"phptools/internal/model"
)

type sourceProcessIdentity struct {
	PID        int    `json:"pid"`
	Created    string `json:"created"`
	Executable string `json:"executable"`
	PortPID    int    `json:"portPID"`
}

func inspectSourceProcess(ctx context.Context, pid int) (sourceProcessIdentity, error) {
	var result sourceProcessIdentity
	if pid < 1 {
		return result, errors.New("source PID is not recorded")
	}
	script := `$ErrorActionPreference='Stop'; $p=Get-CimInstance Win32_Process -Filter 'ProcessId=` + strconv.Itoa(pid) + `'; if (!$p) {exit 20}; $tcp=Get-NetTCPConnection -State Listen -LocalPort 3306; [pscustomobject]@{pid=[int]$p.ProcessId; created=$p.CreationDate.ToUniversalTime().ToString('o'); executable=$p.ExecutablePath; portPID=[int]$tcp[0].OwningProcess} | ConvertTo-Json -Compress`
	cmd := exec.CommandContext(ctx, "powershell.exe", "-NoProfile", "-NonInteractive", "-Command", script)
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
	raw, err := cmd.Output()
	if err != nil {
		return result, errors.New("cannot verify original source MySQL process and port owner")
	}
	if err = json.Unmarshal(raw, &result); err != nil {
		return result, err
	}
	if result.PID != pid || result.PortPID != pid || !strings.EqualFold(result.Executable, `D:\phpstudy_pro\Extensions\MySQL5.7.26\bin\mysqld.exe`) {
		return result, errors.New("source process identity changed")
	}
	return result, nil
}

func verifySourceServer(ctx context.Context, c *mysqlClient) error {
	rows, err := c.query(ctx, "SELECT @@version,@@port,@@datadir;\n")
	if err != nil {
		return err
	}
	if len(rows) != 1 || len(rows[0]) != 3 || rows[0][0] != "5.7.26" || rows[0][1] != "3306" || !strings.EqualFold(filepath.Clean(rows[0][2]), `D:\phpstudy_pro\Extensions\MySQL5.7.26\data`) {
		return errors.New("source SQL identity mismatch")
	}
	return nil
}

func sourcePreflight(ctx context.Context, statePath, accountsPath string) (runErr error) {
	raw, err := os.ReadFile(statePath)
	if err != nil {
		return err
	}
	var state migrationState
	if err = json.Unmarshal(raw, &state); err != nil {
		return err
	}
	if state.Phase != "prepared" && !(state.Phase == "copy-failed" && state.SQLBackup == "") {
		return errors.New("preflight requires prepared target")
	}
	process, err := inspectSourceProcess(ctx, state.SourcePID)
	if err != nil {
		return err
	}
	password, err := sourcePassword()
	if err != nil {
		return err
	}
	client, err := newClient(destination, sourceClient, 3306, password)
	if err != nil {
		return err
	}
	defer func() { runErr = errors.Join(runErr, client.close()) }()
	if err = verifySourceServer(ctx, client); err != nil {
		return err
	}
	tables, err := tableList(ctx, client)
	if err != nil {
		return err
	}
	if err = saveJSON(filepath.Join("build", "migration", "db-live-tables.json"), tables); err != nil {
		return err
	}
	accounts, err := readAccounts(accountsPath)
	if err != nil {
		return err
	}
	if err = verifyAccounts(ctx, client, accounts); err != nil {
		return err
	}
	state.SourcePID = process.PID
	state.SourceCreated = process.Created
	state.Phase = "prepared"
	state.Error = ""
	if err = saveJSON(statePath, state); err != nil {
		return err
	}
	fmt.Println("PREFLIGHT source process, SQL identity and all 34 account definitions/grants verified; no source lock or data export performed")
	return nil
}

func shutdownSource(ctx context.Context, statePath string) (runErr error) {
	raw, err := os.ReadFile(statePath)
	if err != nil {
		return err
	}
	var state migrationState
	if err = json.Unmarshal(raw, &state); err != nil {
		return err
	}
	if state.Phase != "copied-verified" || state.SourcePID < 1 || state.SourceCreated == "" {
		return errors.New("normal source shutdown requires completed and verified copy plus original identity record")
	}
	process, err := inspectSourceProcess(ctx, state.SourcePID)
	if err != nil {
		return err
	}
	if process.Created != state.SourceCreated {
		return errors.New("source process was replaced; shutdown refused")
	}
	password, err := sourcePassword()
	if err != nil {
		return err
	}
	client, err := newClient(destination, sourceClient, 3306, password)
	if err != nil {
		return err
	}
	defer func() { runErr = errors.Join(runErr, client.close()) }()
	if err = verifySourceServer(ctx, client); err != nil {
		return err
	}
	commandCtx, cancel := context.WithTimeout(ctx, time.Minute)
	defer cancel()
	cmd := client.command(commandCtx, filepath.Join(filepath.Dir(sourceClient), "mysqladmin.exe"), "--connect-timeout=5", "shutdown")
	if err = cmd.Run(); err != nil {
		return errors.New("source normal shutdown failed; no force termination attempted")
	}
	deadline := time.Now().Add(45 * time.Second)
	for time.Now().Before(deadline) {
		conn, e := net.DialTimeout("tcp4", "127.0.0.1:3306", time.Second)
		if e != nil {
			state.SourceShutdownAt = model.Now()
			if err = saveJSON(statePath, state); err != nil {
				return err
			}
			fmt.Println("SOURCE SHUT DOWN normally; original MySQL data retained, port 3306 released")
			return nil
		}
		conn.Close()
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(250 * time.Millisecond):
		}
	}
	return errors.New("source accepted shutdown but port 3306 remains in use; inspect before switching target")
}

func protectSource(ctx context.Context, statePath string) (runErr error) {
	raw, err := os.ReadFile(statePath)
	if err != nil {
		return err
	}
	var state migrationState
	if err = json.Unmarshal(raw, &state); err != nil {
		return err
	}
	if state.Phase != "prepared" || state.ColdBackup == "" {
		return errors.New("source write protection requires completed cold backup and prepared target")
	}
	identity, err := inspectSourceProcess(ctx, state.SourcePID)
	if err != nil {
		return err
	}
	if identity.Created != state.SourceCreated {
		return errors.New("source process identity changed")
	}
	pwd, err := sourcePassword()
	if err != nil {
		return err
	}
	client, err := newClient(destination, sourceClient, 3306, pwd)
	if err != nil {
		return err
	}
	defer func() { runErr = errors.Join(runErr, client.close()) }()
	if err = verifySourceServer(ctx, client); err != nil {
		return err
	}
	rows, err := client.query(ctx, "SELECT @@GLOBAL.read_only,@@GLOBAL.super_read_only;\n")
	if err != nil {
		return err
	}
	if len(rows) != 1 || len(rows[0]) != 2 {
		return errors.New("unexpected readonly status")
	}
	if state.SourceOriginalReadOnly == "" {
		state.SourceOriginalReadOnly = rows[0][0]
		state.SourceOriginalSuperReadOnly = rows[0][1]
		if err = saveJSON(statePath, state); err != nil {
			return err
		}
	}
	rows, err = client.query(ctx, "SET GLOBAL read_only=ON; SET GLOBAL super_read_only=ON; SELECT @@GLOBAL.read_only,@@GLOBAL.super_read_only;\n")
	if err != nil {
		return err
	}
	if len(rows) != 1 || strings.Join(rows[0], ",") != "1,1" {
		return errors.New("source readonly protection not confirmed")
	}
	state.SourceWriteProtected = true
	if err = saveJSON(statePath, state); err != nil {
		return err
	}
	fmt.Printf("SOURCE WRITE PROTECTED: read_only=1, super_read_only=1; original values=%s/%s saved for rollback; no ini changes\n", state.SourceOriginalReadOnly, state.SourceOriginalSuperReadOnly)
	return nil
}
