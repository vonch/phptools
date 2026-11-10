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
	"strconv"
	"strings"

	"phptools/internal/services"
)

func tableList(ctx context.Context, c *mysqlClient) ([]tableRef, error) {
	rows, err := c.query(ctx, "SELECT TABLE_SCHEMA,TABLE_NAME,COALESCE(ENGINE,''),COALESCE(TABLE_COLLATION,''),TABLE_TYPE FROM information_schema.TABLES WHERE TABLE_SCHEMA"+businessFilter+"ORDER BY TABLE_SCHEMA,TABLE_NAME;\n")
	if err != nil {
		return nil, err
	}
	result := make([]tableRef, 0, len(rows))
	for _, r := range rows {
		if len(r) != 5 {
			return nil, errors.New("unexpected table metadata")
		}
		result = append(result, tableRef{r[0], r[1], r[2], r[3], r[4]})
	}
	return result, nil
}

func equalTableLists(a, b []tableRef) bool {
	if len(a) != len(b) {
		return false
	}
	for k := range a {
		if a[k] != b[k] {
			return false
		}
	}
	return true
}

func exportDatabases(ctx context.Context, c *mysqlClient, path string, schemas []string, ignored []tableFault) error {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	if err = services.ProtectPrivatePath(path, false); err != nil {
		f.Close()
		return err
	}
	args := []string{"--skip-lock-tables", "--quick", "--hex-blob", "--routines", "--triggers", "--events", "--set-gtid-purged=OFF", "--default-character-set=utf8mb4", "--max-allowed-packet=1G"}
	for _, f := range ignored {
		args = append(args, "--ignore-table="+f.Schema+"."+f.Table)
	}
	args = append(args, "--databases")
	args = append(args, schemas...)
	cmd := c.command(ctx, filepath.Join(filepath.Dir(c.executable), "mysqldump.exe"), args...)
	cmd.Stdout = f
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	err = cmd.Run()
	closeErr := f.Close()
	if err != nil {
		logPath := path + ".export-error.log"
		_ = os.WriteFile(logPath, stderr.Bytes(), 0600)
		return fmt.Errorf("business database export failed; backup is incomplete, protected diagnostics at %s (%w)", logPath, err)
	}
	return closeErr
}

func importDatabases(ctx context.Context, c *mysqlClient, path string) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	cmd := c.command(ctx, c.executable, "--batch", "--binary-mode", "--max-allowed-packet=1G")
	cmd.Stdin = f
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err = cmd.Run(); err != nil {
		return fmt.Errorf("business database import failed (%w)", err)
	}
	return nil
}

func checkTables(ctx context.Context, c *mysqlClient, tables []tableRef, label string) ([]tableCheck, error) {
	result := make([]tableCheck, 0, len(tables))
	for start := 0; start < len(tables); start += 50 {
		end := min(start+50, len(tables))
		var sql strings.Builder
		for _, t := range tables[start:end] {
			full := identifier(t.Schema) + "." + identifier(t.Name)
			fmt.Fprintf(&sql, "SELECT COUNT(*) FROM %s; CHECKSUM TABLE %s EXTENDED;\n", full, full)
		}
		rows, err := c.query(ctx, sql.String())
		if err != nil {
			return nil, fmt.Errorf("%s verification failed near table %d: %w", label, start, err)
		}
		if len(rows) != (end-start)*2 {
			return nil, errors.New("unexpected table verification response")
		}
		for k, t := range tables[start:end] {
			countRow := rows[k*2]
			sumRow := rows[k*2+1]
			if len(countRow) != 1 || len(sumRow) != 2 {
				return nil, errors.New("unexpected COUNT/CHECKSUM columns")
			}
			count, err := strconv.ParseUint(countRow[0], 10, 64)
			if err != nil {
				return nil, err
			}
			if sumRow[1] == "NULL" && count != 0 {
				return nil, fmt.Errorf("%s.%s returned no checksum for nonempty data", t.Schema, t.Name)
			}
			result = append(result, tableCheck{t.Schema, t.Name, count, sumRow[1]})
		}
		if end%250 == 0 || end == len(tables) {
			fmt.Printf("Checked %s tables: %d/%d\n", label, end, len(tables))
		}
	}
	return result, nil
}

func compareChecks(source, target []tableCheck) error {
	if len(source) != len(target) {
		return errors.New("verification table count differs")
	}
	for k := range source {
		if source[k] != target[k] {
			return fmt.Errorf("row count or checksum mismatch for %s.%s", source[k].Schema, source[k].Table)
		}
	}
	return nil
}

func compareSchemas(ctx context.Context, source, target *mysqlClient) error {
	query := "SELECT SCHEMA_NAME,DEFAULT_CHARACTER_SET_NAME,DEFAULT_COLLATION_NAME FROM information_schema.SCHEMATA WHERE SCHEMA_NAME" + businessFilter + "ORDER BY SCHEMA_NAME;\n"
	a, err := source.query(ctx, query)
	if err != nil {
		return err
	}
	b, err := target.query(ctx, query)
	if err != nil {
		return err
	}
	x, _ := json.Marshal(a)
	y, _ := json.Marshal(b)
	if !bytes.Equal(x, y) {
		return errors.New("schema names or default character sets differ")
	}
	return nil
}
