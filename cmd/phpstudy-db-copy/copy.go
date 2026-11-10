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
	"os"
	"path/filepath"
	"strings"

	"github.com/zalando/go-keyring"
	"phptools/internal/desktop"
	"phptools/internal/model"
	"phptools/internal/services"
	"phptools/internal/store"
)

const businessFilter = " NOT IN ('information_schema','mysql','performance_schema','sys') "

type tableRef struct {
	Schema    string `json:"schema"`
	Name      string `json:"name"`
	Engine    string `json:"engine"`
	Collation string `json:"collation"`
	Type      string `json:"type"`
}

type tableCheck struct {
	Schema   string `json:"schema"`
	Table    string `json:"table"`
	Rows     uint64 `json:"rows"`
	Checksum string `json:"checksum"`
}

type copyReport struct {
	StartedAt                   string         `json:"startedAt"`
	CompletedAt                 string         `json:"completedAt"`
	SourceVersion               string         `json:"sourceVersion"`
	TargetVersion               string         `json:"targetVersion"`
	TargetInstance              string         `json:"targetInstance"`
	Schemas                     []string       `json:"schemas"`
	Tables                      []tableCheck   `json:"tables"`
	Accounts                    int            `json:"accounts"`
	Grants                      int            `json:"grants"`
	Backup                      string         `json:"backup"`
	BackupBytes                 int64          `json:"backupBytes"`
	SHA256                      string         `json:"sha256"`
	LockReleased                bool           `json:"sourceLockReleased"`
	TemporaryCredentialsRemoved bool           `json:"temporaryCredentialsRemoved"`
	AccountsExportRemoved       bool           `json:"accountsExportRemoved"`
	SkippedUnreadableTables     int            `json:"skippedUnreadableTables"`
	PhysicalColdBackup          string         `json:"physicalColdBackup"`
	LogicalFallback             []logicalCheck `json:"logicalFallback,omitempty"`
	NonRootLoginVerified        bool           `json:"nonRootLoginVerified"`
	Error                       string         `json:"error,omitempty"`
}

func copyDatabases(ctx context.Context, statePath, accountsPath string) (runErr error) {
	raw, err := os.ReadFile(statePath)
	if err != nil {
		return err
	}
	var state migrationState
	if err = json.Unmarshal(raw, &state); err != nil {
		return err
	}
	if state.Root != destination || state.Phase != "prepared" || state.Instance.Port != 13306 || state.Instance.ID == "" {
		return errors.New("copy requires the prepared independent instance at port 13306")
	}
	if state.ColdBackup == "" || !state.SourceWriteProtected {
		return errors.New("healthy migration requires complete cold backup and source readonly protection")
	}
	if err = ensureManagerClosed(ctx); err != nil {
		return err
	}
	sourcePwd, err := sourcePassword()
	if err != nil {
		return err
	}
	targetPwd, err := keyring.Get(desktop.CredentialService(destination), state.Instance.ID)
	if err != nil {
		return err
	}
	source, err := newClient(destination, sourceClient, 3306, sourcePwd)
	if err != nil {
		return err
	}
	target, err := newClient(destination, filepath.Join(filepath.Dir(state.Instance.Executable), "mysql.exe"), 13306, targetPwd)
	if err != nil {
		_ = source.close()
		return err
	}
	report := copyReport{StartedAt: model.Now(), TargetInstance: state.Instance.ID}
	report.PhysicalColdBackup = state.ColdBackup
	reportPath := filepath.Join("build", "migration", "db-copy-report.json")
	defer func() {
		var targetCleanup error
		if target != nil {
			targetCleanup = target.close()
		}
		cleanupErr := errors.Join(source.close(), targetCleanup)
		report.TemporaryCredentialsRemoved = cleanupErr == nil
		runErr = errors.Join(runErr, cleanupErr)
		report.CompletedAt = model.Now()
		if runErr != nil {
			report.Error = runErr.Error()
			state.Error = runErr.Error()
			state.Phase = "copy-failed"
		}
		_ = saveJSON(reportPath, report)
		_ = saveJSON(statePath, state)
	}()
	sourceIdentity, err := source.query(ctx, "SELECT @@version,@@port,@@datadir;\n")
	if err != nil {
		return err
	}
	if len(sourceIdentity) != 1 || len(sourceIdentity[0]) != 3 || sourceIdentity[0][0] != "5.7.26" || sourceIdentity[0][1] != "3306" || !strings.EqualFold(filepath.Clean(sourceIdentity[0][2]), `D:\phpstudy_pro\Extensions\MySQL5.7.26\data`) {
		return errors.New("unexpected source server identity")
	}
	report.SourceVersion = sourceIdentity[0][0]
	db, err := store.Open(destination)
	if err != nil {
		return err
	}
	defer db.Close()
	manager := services.New(destination, func(i model.Instance) error { return db.Put("instance", i.ID, i) }, func(id string) (string, error) { return keyring.Get(desktop.CredentialService(destination), id) })
	current := manager.Inspect(state.Instance)
	if current.State != "running" || current.PID == 0 {
		return errors.New("prepared managed instance is not running")
	}
	identity, err := target.query(ctx, "SELECT @@version,@@port,@@datadir;\n")
	if err != nil {
		return err
	}
	if len(identity) != 1 || len(identity[0]) != 3 || identity[0][0] != "5.7.44" || identity[0][1] != "13306" || !strings.EqualFold(filepath.Clean(identity[0][2]), filepath.Clean(state.Instance.DataDir)) {
		return errors.New("unexpected target server identity")
	}
	report.TargetVersion = identity[0][0]
	empty, err := target.query(ctx, "SELECT SCHEMA_NAME FROM information_schema.SCHEMATA WHERE SCHEMA_NAME"+businessFilter+";\n")
	if err != nil {
		return err
	}
	if len(empty) != 0 {
		return errors.New("prepared target contains business databases; refusing overwrite")
	}
	state.Phase = "locking-source"
	if err = saveJSON(statePath, state); err != nil {
		return err
	}
	lock, err := acquireLock(ctx, source)
	if err != nil {
		return err
	}
	defer func() {
		if lock != nil {
			releaseErr := lock.close()
			report.LockReleased = releaseErr == nil
			runErr = errors.Join(runErr, releaseErr)
		}
	}()
	fmt.Println("Source global read lock acquired; capturing one consistent business snapshot")
	rows, err := source.query(ctx, "SELECT SCHEMA_NAME FROM information_schema.SCHEMATA WHERE SCHEMA_NAME"+businessFilter+"ORDER BY SCHEMA_NAME;\n")
	if err != nil {
		return err
	}
	for _, row := range rows {
		report.Schemas = append(report.Schemas, row[0])
	}
	if len(report.Schemas) != 56 {
		return fmt.Errorf("source schema count changed: %d, expected 56; inspect before migration", len(report.Schemas))
	}
	tables, err := tableList(ctx, source)
	if err != nil {
		return err
	}
	tables, ignored, err := healthyTables(ctx, source, tables, sourcePwd)
	if err != nil {
		return err
	}
	report.SkippedUnreadableTables = len(ignored)
	for _, table := range tables {
		if table.Type != "BASE TABLE" {
			return errors.New("source contains a new non-base-table object; inspect object migration first")
		}
	}
	accounts, err := readAccounts(accountsPath)
	if err != nil {
		return err
	}
	if err = verifyAccounts(ctx, source, accounts); err != nil {
		return fmt.Errorf("source account export changed: %w", err)
	}
	state.Phase = "exporting"
	state.SQLBackup = filepath.Join(state.BackupDir, "business-databases.sql")
	report.Backup = state.SQLBackup
	if err = saveJSON(statePath, state); err != nil {
		return err
	}
	if err = exportDatabases(ctx, source, state.SQLBackup, report.Schemas, ignored); err != nil {
		return err
	}
	f, err := os.Open(state.SQLBackup)
	if err != nil {
		return err
	}
	hash := sha256.New()
	n, hashErr := io.Copy(hash, f)
	err = errors.Join(hashErr, f.Close())
	if err != nil {
		return err
	}
	report.BackupBytes = n
	report.SHA256 = hex.EncodeToString(hash.Sum(nil))
	fmt.Printf("Backup complete: %d bytes; checking all %d source tables\n", n, len(tables))
	report.Tables, err = checkTables(ctx, source, tables, "source")
	if err != nil {
		return err
	}
	if err = lock.close(); err != nil {
		lock = nil
		return err
	}
	lock = nil
	report.LockReleased = true
	fmt.Println("Source global read lock released; original source data remains intact")
	state.Phase = "importing"
	if err = saveJSON(statePath, state); err != nil {
		return err
	}
	if err = importDatabases(ctx, target, state.SQLBackup); err != nil {
		return err
	}
	fmt.Println("Business databases imported; checking target table structure and contents")
	state.Phase = "verifying"
	if err = saveJSON(statePath, state); err != nil {
		return err
	}
	targetTables, err := tableList(ctx, target)
	if err != nil {
		return err
	}
	if !equalTableLists(tables, targetTables) {
		return errors.New("source and target table names, engines or collations differ")
	}
	targetChecks, err := checkTables(ctx, target, targetTables, "target")
	if err != nil {
		return err
	}
	if err = compareChecks(report.Tables, targetChecks); err != nil {
		return err
	}
	if err = compareSchemas(ctx, source, target); err != nil {
		return err
	}
	if err = restoreAccounts(ctx, target, accounts); err != nil {
		return err
	}
	if err = keyring.Set(desktop.CredentialService(destination), state.Instance.ID, sourcePwd); err != nil {
		return errors.New("accounts restored but Windows credential update failed")
	}
	if err = target.close(); err != nil {
		return err
	}
	target, err = newClient(destination, filepath.Join(filepath.Dir(state.Instance.Executable), "mysql.exe"), 13306, sourcePwd)
	if err != nil {
		return err
	}
	if err = verifyAccounts(ctx, target, accounts); err != nil {
		return err
	}
	for _, a := range accounts.Accounts {
		report.Accounts++
		report.Grants += len(a.Grants)
	}
	if err = removeAccountsExport(accountsPath); err != nil {
		return err
	}
	report.AccountsExportRemoved = true
	state.Phase = "copied-verified"
	state.CompletedAt = model.Now()
	state.Error = ""
	if err = saveJSON(statePath, state); err != nil {
		return err
	}
	fmt.Printf("VERIFIED %d schemas, %d table counts and checksums, %d source accounts; ready for final port switch\n", len(report.Schemas), len(report.Tables), report.Accounts)
	return nil
}
