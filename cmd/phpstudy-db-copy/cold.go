//go:build windows

package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"golang.org/x/sys/windows"
	"phptools/internal/services"
)

const sourceData = `D:\phpstudy_pro\Extensions\MySQL5.7.26\data`
const sourceServer = `D:\phpstudy_pro\Extensions\MySQL5.7.26\bin\mysqld.exe`
const sourceINI = `D:\phpstudy_pro\Extensions\MySQL5.7.26\my.ini`

type coldFile struct {
	Path   string `json:"path"`
	Bytes  int64  `json:"bytes"`
	SHA256 string `json:"sha256"`
}

func coldBackup(ctx context.Context, statePath string) (runErr error) {
	raw, err := os.ReadFile(statePath)
	if err != nil {
		return err
	}
	var state migrationState
	if err = json.Unmarshal(raw, &state); err != nil {
		return err
	}
	if state.Root != destination || state.Phase != "copy-failed" || state.ColdBackup != "" {
		return errors.New("cold backup requires the failed initial export and original source identity")
	}
	identity, err := inspectSourceProcess(ctx, state.SourcePID)
	if err != nil {
		return err
	}
	if identity.Created != state.SourceCreated {
		return errors.New("source process identity changed before cold backup")
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
	state.Phase = "cold-backup-stopping-source"
	if err = saveJSON(statePath, state); err != nil {
		return err
	}
	if err = normalShutdownSource(ctx, client, state.SourcePID); err != nil {
		return err
	}
	fmt.Println("Source stopped normally; copying complete data directory before any healthy-data migration")
	stopped := true
	defer func() {
		if stopped {
			restartCtx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
			restartErr := restartOriginalSource(restartCtx, client, &state)
			cancel()
			runErr = errors.Join(runErr, restartErr)
		}
		if runErr != nil {
			state.Phase = "cold-backup-failed"
			state.Error = runErr.Error()
		}
		_ = saveJSON(statePath, state)
	}()
	backup := filepath.Join(state.BackupDir, "source-data-cold")
	if err = os.Mkdir(backup, 0700); err != nil {
		return err
	}
	if err = services.ProtectPrivatePath(backup, true); err != nil {
		return err
	}
	manifest, err := copyColdFiles(ctx, sourceData, backup)
	if err != nil {
		return err
	}
	var total int64
	for _, f := range manifest {
		total += f.Bytes
	}
	if err = saveJSON(filepath.Join(state.BackupDir, "source-data-cold-manifest.json"), map[string]any{"source": sourceData, "destination": backup, "files": manifest, "fileCount": len(manifest), "totalBytes": total, "allSourceAndDestinationSHA256Match": true, "method": "normal source shutdown, complete file copy, per-file SHA256 verification"}); err != nil {
		return err
	}
	// Preserve the exact source settings alongside the complete physical backup.
	settings, err := os.ReadFile(sourceINI)
	if err != nil {
		return err
	}
	if err = os.WriteFile(filepath.Join(state.BackupDir, "source-my.ini"), settings, 0600); err != nil {
		return err
	}
	state.ColdBackup = backup
	if err = restartOriginalSource(ctx, client, &state); err != nil {
		return err
	}
	stopped = false
	state.Phase = "prepared"
	state.SQLBackup = ""
	state.Error = ""
	if err = saveJSON(statePath, state); err != nil {
		return err
	}
	fmt.Printf("COLD BACKUP VERIFIED: %d files / %d bytes; source restarted PID=%d, original data retained; backup=%s\n", len(manifest), total, state.SourcePID, backup)
	return nil
}

func normalShutdownSource(ctx context.Context, client *mysqlClient, pid int) error {
	h, err := windows.OpenProcess(windows.SYNCHRONIZE|windows.PROCESS_QUERY_LIMITED_INFORMATION, false, uint32(pid))
	if err != nil {
		return err
	}
	defer windows.CloseHandle(h)
	commandCtx, cancel := context.WithTimeout(ctx, time.Minute)
	defer cancel()
	cmd := client.command(commandCtx, filepath.Join(filepath.Dir(sourceClient), "mysqladmin.exe"), "--connect-timeout=5", "shutdown")
	if err = cmd.Run(); err != nil {
		return errors.New("source normal shutdown failed; no force termination attempted")
	}
	status, err := windows.WaitForSingleObject(h, 45000)
	if err != nil {
		return err
	}
	if status != windows.WAIT_OBJECT_0 {
		return errors.New("source has not exited after normal shutdown")
	}
	return nil
}

func restartOriginalSource(ctx context.Context, client *mysqlClient, state *migrationState) error {
	logPath := filepath.Join(state.BackupDir, "source-restart.log")
	log, err := os.OpenFile(logPath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	cmd := exec.Command(sourceServer, "--defaults-file="+sourceINI, "--console")
	cmd.Dir = filepath.Dir(sourceServer)
	cmd.Stdout = log
	cmd.Stderr = log
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
	if err = cmd.Start(); err != nil {
		log.Close()
		return err
	}
	go func() { _ = cmd.Wait(); _ = log.Close() }()
	deadline := time.Now().Add(90 * time.Second)
	for time.Now().Before(deadline) {
		if verifySourceServer(ctx, client) == nil {
			identity, err := inspectSourceProcess(ctx, cmd.Process.Pid)
			if err != nil {
				return err
			}
			state.SourcePID = identity.PID
			state.SourceCreated = identity.Created
			state.SourceShutdownAt = ""
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(500 * time.Millisecond):
		}
	}
	return fmt.Errorf("original source restart did not become ready; inspect %s", logPath)
}

func copyColdFiles(ctx context.Context, source, destination string) ([]coldFile, error) {
	var result []coldFile
	lastProgress := time.Now()
	var copiedBytes int64
	err := filepath.WalkDir(source, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		rel, err := filepath.Rel(source, path)
		if err != nil {
			return err
		}
		if rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || filepath.IsAbs(rel) {
			return errors.New("cold backup path escaped source")
		}
		if entry.Type()&os.ModeSymlink != 0 {
			return errors.New("cold backup encountered a source link; inspect before copying")
		}
		if rel == "." {
			return nil
		}
		target := filepath.Join(destination, rel)
		if entry.IsDir() {
			return os.Mkdir(target, 0700)
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() {
			return errors.New("cold backup encountered a non-regular source file")
		}
		src, err := os.Open(path)
		if err != nil {
			return err
		}
		dst, err := os.OpenFile(target, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
		if err != nil {
			src.Close()
			return err
		}
		sourceHash := sha256.New()
		size, copyErr := io.Copy(io.MultiWriter(dst, sourceHash), src)
		if err = errors.Join(copyErr, src.Close(), dst.Close()); err != nil {
			return err
		}
		if size != info.Size() {
			return fmt.Errorf("cold backup source length changed: %s", rel)
		}
		verify, err := os.Open(target)
		if err != nil {
			return err
		}
		targetHash := sha256.New()
		_, hashErr := io.Copy(targetHash, verify)
		if err = errors.Join(hashErr, verify.Close()); err != nil {
			return err
		}
		if !strings.EqualFold(hex.EncodeToString(sourceHash.Sum(nil)), hex.EncodeToString(targetHash.Sum(nil))) {
			return fmt.Errorf("cold backup hash mismatch: %s", rel)
		}
		result = append(result, coldFile{filepath.ToSlash(rel), size, hex.EncodeToString(sourceHash.Sum(nil))})
		copiedBytes += size
		if time.Since(lastProgress) > 20*time.Second {
			fmt.Printf("Cold backup progress: %d files / %d bytes verified\n", len(result), copiedBytes)
			lastProgress = time.Now()
		}
		return nil
	})
	return result, err
}
