//go:build windows

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/zalando/go-keyring"
	"phptools/internal/desktop"
	"phptools/internal/model"
)

type logicalCheck struct {
	Schema         string     `json:"schema"`
	Table          string     `json:"table"`
	Rows           uint64     `json:"rows"`
	SourceChecksum string     `json:"sourceChecksum"`
	TargetChecksum string     `json:"targetChecksum"`
	SHA256         string     `json:"canonicalRowsSHA256"`
	DDLIdentical   bool       `json:"ddlIdentical"`
	Columns        [][]string `json:"columns"`
}

func verifyResume(ctx context.Context, statePath, accountsPath string) (runErr error) {
	raw, err := os.ReadFile(statePath)
	if err != nil {
		return err
	}
	var state migrationState
	if err = json.Unmarshal(raw, &state); err != nil {
		return err
	}
	if state.Phase != "copy-failed" || !strings.HasPrefix(state.Error, "row count or checksum mismatch") || !state.SourceWriteProtected || state.ColdBackup == "" {
		return errors.New("verification resume requires completed import and protected source")
	}
	raw, err = os.ReadFile(filepath.Join("build", "migration", "db-copy-report.json"))
	if err != nil {
		return err
	}
	var report copyReport
	if err = json.Unmarshal(raw, &report); err != nil {
		return err
	}
	if len(report.Tables) != 3438 {
		return errors.New("complete source check report missing")
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
		source.close()
		return err
	}
	defer func() {
		var tErr error
		if target != nil {
			tErr = target.close()
		}
		cleanErr := errors.Join(source.close(), tErr)
		report.TemporaryCredentialsRemoved = cleanErr == nil
		runErr = errors.Join(runErr, cleanErr)
		report.CompletedAt = model.Now()
		if runErr != nil {
			state.Error = runErr.Error()
			report.Error = runErr.Error()
		} else {
			state.Error = ""
			report.Error = ""
		}
		_ = saveJSON(filepath.Join("build", "migration", "db-copy-report.json"), report)
		_ = saveJSON(statePath, state)
	}()
	status, err := source.query(ctx, "SELECT @@GLOBAL.read_only,@@GLOBAL.super_read_only;\n")
	if err != nil {
		return err
	}
	if len(status) != 1 || strings.Join(status[0], ",") != "1,1" {
		return errors.New("source write protection is not active")
	}
	tables, err := tableList(ctx, target)
	if err != nil {
		return err
	}
	if len(tables) != 3438 {
		return errors.New("target table count changed")
	}
	targetChecks, err := checkTables(ctx, target, tables, "target-resume")
	if err != nil {
		return err
	}
	if err = saveJSON(filepath.Join("build", "migration", "db-target-checks.json"), targetChecks); err != nil {
		return err
	}
	var differences []map[string]any
	for k, s := range report.Tables {
		t := targetChecks[k]
		if s.Schema != t.Schema || s.Table != t.Table {
			return errors.New("verification order differs")
		}
		if s != t {
			differences = append(differences, map[string]any{"source": s, "target": t})
			fmt.Printf("Difference %s.%s source rows=%d/checksum=%s target rows=%d/checksum=%s\n", s.Schema, s.Table, s.Rows, s.Checksum, t.Rows, t.Checksum)
		}
	}
	if err = saveJSON(filepath.Join("build", "migration", "db-checksum-differences.json"), differences); err != nil {
		return err
	}
	for k, s := range report.Tables {
		t := targetChecks[k]
		if s == t {
			continue
		}
		if s.Rows != t.Rows {
			return fmt.Errorf("actual row count mismatch for %s.%s", s.Schema, s.Table)
		}
		full := identifier(s.Schema) + "." + identifier(s.Table)
		sourceDDL, err := source.query(ctx, "SHOW CREATE TABLE "+full+";\n")
		if err != nil {
			return err
		}
		targetDDL, err := target.query(ctx, "SHOW CREATE TABLE "+full+";\n")
		if err != nil {
			return err
		}
		a, _ := json.Marshal(sourceDDL)
		b, _ := json.Marshal(targetDDL)
		if !bytes.Equal(a, b) {
			_ = saveJSON(filepath.Join("build", "migration", "db-differing-ddl.json"), map[string]any{"table": full, "source": sourceDDL, "target": targetDDL})
			return fmt.Errorf("full CREATE TABLE differs for %s", full)
		}
		metaSQL := "SELECT COLUMN_NAME,COLUMN_TYPE,IS_NULLABLE,COALESCE(COLUMN_DEFAULT,'<SQLNULL>'),EXTRA,COALESCE(CHARACTER_SET_NAME,''),COALESCE(COLLATION_NAME,'') FROM information_schema.COLUMNS WHERE TABLE_SCHEMA=" + sqlString(s.Schema) + " AND TABLE_NAME=" + sqlString(s.Table) + " ORDER BY ORDINAL_POSITION;\n"
		columns, err := source.query(ctx, metaSQL)
		if err != nil {
			return err
		}
		targetColumns, err := target.query(ctx, metaSQL)
		if err != nil {
			return err
		}
		a, _ = json.Marshal(columns)
		b, _ = json.Marshal(targetColumns)
		if !bytes.Equal(a, b) {
			return fmt.Errorf("column definitions differ for %s", full)
		}
		digest, rows, err := canonicalRows(ctx, source, full, columns)
		if err != nil {
			return err
		}
		targetDigest, targetRows, err := canonicalRows(ctx, target, full, columns)
		if err != nil {
			return err
		}
		if digest != targetDigest || rows != s.Rows || targetRows != s.Rows {
			return fmt.Errorf("canonical logical data differs for %s", full)
		}
		report.LogicalFallback = append(report.LogicalFallback, logicalCheck{s.Schema, s.Table, s.Rows, s.Checksum, t.Checksum, digest, true, columns})
		fmt.Printf("Logical row multiset and full DDL match: %s, %d rows\n", full, rows)
	}
	if err = compareSchemas(ctx, source, target); err != nil {
		return err
	}
	var dotProof []map[string]any
	for _, name := range []string{"hcgl.cc", "pzhscg.cc"} {
		a, err := source.query(ctx, "SHOW CREATE DATABASE "+identifier(name)+";\n")
		if err != nil {
			return err
		}
		b, err := target.query(ctx, "SHOW CREATE DATABASE "+identifier(name)+";\n")
		if err != nil {
			return err
		}
		dotProof = append(dotProof, map[string]any{"database": name, "source": a, "target": b})
	}
	if err = saveJSON(filepath.Join("build", "migration", "db-dot-databases.json"), dotProof); err != nil {
		return err
	}
	accounts, err := readAccounts(accountsPath)
	if err != nil {
		return err
	}
	if err = restoreAccounts(ctx, target, accounts); err != nil {
		return err
	}
	if err = keyring.Set(desktop.CredentialService(destination), state.Instance.ID, sourcePwd); err != nil {
		return errors.New("restored source accounts but Windows credential update failed")
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
	if err = verifyProjectLogin(ctx, target.executable); err != nil {
		return err
	}
	report.NonRootLoginVerified = true
	report.Accounts = len(accounts.Accounts)
	for _, a := range accounts.Accounts {
		report.Grants += len(a.Grants)
	}
	if err = removeAccountsExport(accountsPath); err != nil {
		return err
	}
	report.AccountsExportRemoved = true
	state.Phase = "copied-verified"
	state.CompletedAt = model.Now()
	fmt.Printf("VERIFIED %d schemas / %d tables / %d source accounts; %d checksum differences resolved by exact DDL and canonical rows; non-root project login passed\n", len(report.Schemas), len(report.Tables), report.Accounts, len(report.LogicalFallback))
	return nil
}
