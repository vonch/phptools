//go:build windows

package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

type accountExport struct {
	SourceVersion string          `json:"source_version"`
	Accounts      []sourceAccount `json:"accounts"`
}

type sourceAccount struct {
	User      string   `json:"user"`
	Host      string   `json:"host"`
	CreateSQL string   `json:"create_sql"`
	Grants    []string `json:"grants"`
}

func accountExportPath(path string) (string, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	base, err := filepath.Abs(filepath.Join("build", "migration"))
	if err != nil {
		return "", err
	}
	dir := filepath.Dir(abs)
	if filepath.Base(abs) != "accounts.json" || !strings.HasPrefix(filepath.Base(dir), ".source-accounts-") || !strings.EqualFold(filepath.Dir(dir), base) {
		return "", errors.New("account export must be this migration's protected temporary accounts.json")
	}
	info, err := os.Lstat(dir)
	if err != nil {
		return "", err
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return "", errors.New("invalid account export directory")
	}
	return abs, nil
}

func readAccounts(path string) (accountExport, error) {
	var result accountExport
	abs, err := accountExportPath(path)
	if err != nil {
		return result, err
	}
	raw, err := os.ReadFile(abs)
	if err != nil {
		return result, err
	}
	if err = json.Unmarshal(raw, &result); err != nil {
		return result, err
	}
	if result.SourceVersion != "5.7.26" || len(result.Accounts) != 34 {
		return result, errors.New("unexpected source account export")
	}
	for _, a := range result.Accounts {
		if a.User == "mysql.sys" || a.User == "mysql.session" || !strings.HasPrefix(a.CreateSQL, "CREATE USER ") || len(a.Grants) == 0 {
			return result, errors.New("account export contains unsupported entries")
		}
		if a.User == "root" && a.Host != "localhost" {
			return result, errors.New("source has additional root host accounts; inspect management credential conflict")
		}
	}
	return result, nil
}

func removeAccountsExport(path string) error {
	abs, err := accountExportPath(path)
	if err != nil {
		return err
	}
	if err = os.Remove(abs); err != nil {
		return err
	}
	return os.Remove(filepath.Dir(abs))
}

func restoreAccounts(ctx context.Context, target *mysqlClient, export accountExport) error {
	var sql strings.Builder
	var root *sourceAccount
	for k := range export.Accounts {
		a := &export.Accounts[k]
		if a.User == "root" {
			root = a
			continue
		}
		sql.WriteString(a.CreateSQL)
		sql.WriteString(";\n")
		for _, grant := range a.Grants {
			sql.WriteString(grant)
			sql.WriteString(";\n")
		}
	}
	if root == nil {
		return errors.New("source root account is missing")
	}
	start := strings.Index(root.CreateSQL, " IDENTIFIED WITH ")
	if start < 0 {
		return errors.New("source root authentication clause is missing")
	}
	// Retain both management endpoints with the source password; alter root last.
	sql.WriteString("ALTER USER 'root'@'127.0.0.1'")
	sql.WriteString(root.CreateSQL[start:])
	sql.WriteString(";\n")
	sql.WriteString(strings.Replace(root.CreateSQL, "CREATE USER ", "ALTER USER ", 1))
	sql.WriteString(";\n")
	for _, grant := range root.Grants {
		sql.WriteString(grant)
		sql.WriteString(";\n")
	}
	_, err := target.query(ctx, sql.String())
	return err
}

func verifyAccounts(ctx context.Context, c *mysqlClient, export accountExport) error {
	for _, a := range export.Accounts {
		account := sqlString(a.User) + "@" + sqlString(a.Host)
		created, err := c.query(ctx, "SHOW CREATE USER "+account+";\n")
		if err != nil {
			return err
		}
		if len(created) != 1 || len(created[0]) != 1 || created[0][0] != a.CreateSQL {
			return fmt.Errorf("account authentication attributes differ for %s@%s", a.User, a.Host)
		}
		rows, err := c.query(ctx, "SHOW GRANTS FOR "+account+";\n")
		if err != nil {
			return err
		}
		actual := make([]string, 0, len(rows))
		for _, row := range rows {
			if len(row) != 1 {
				return errors.New("unexpected account grants response")
			}
			actual = append(actual, row[0])
		}
		expected := append([]string{}, a.Grants...)
		sort.Strings(actual)
		sort.Strings(expected)
		if strings.Join(actual, "\n") != strings.Join(expected, "\n") {
			return fmt.Errorf("account grants differ for %s@%s", a.User, a.Host)
		}
	}
	return nil
}
