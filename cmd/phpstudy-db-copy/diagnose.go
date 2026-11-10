//go:build windows

package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
)

type tableFault struct {
	Schema   string `json:"schema"`
	Table    string `json:"table"`
	Code     string `json:"code"`
	SQLState string `json:"sqlState"`
	Message  string `json:"message"`
}

func diagnoseSource(ctx context.Context) (runErr error) {
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
	failed, err := tableFailures(ctx, client, tables, password)
	if err != nil {
		return err
	}
	path := filepath.Join("build", "migration", "db-source-unreadable-tables.json")
	if err = saveJSON(path, map[string]any{"listedTables": len(tables), "readableTables": len(tables) - len(failed), "unreadableTables": len(failed), "unreadable": failed, "method": "SHOW CREATE TABLE (read only; no REPAIR, no data changes)"}); err != nil {
		return err
	}
	fmt.Printf("SOURCE DIAGNOSIS: %d listed, %d readable, %d unreadable; report=%s\n", len(tables), len(tables)-len(failed), len(failed), path)
	return nil
}

func tableFailures(ctx context.Context, client *mysqlClient, tables []tableRef, password string) ([]tableFault, error) {
	var statements strings.Builder
	for _, t := range tables {
		fmt.Fprintf(&statements, "SHOW CREATE TABLE %s.%s;\n", identifier(t.Schema), identifier(t.Name))
	}
	cmd := client.command(ctx, client.executable, "--batch", "--raw", "--skip-column-names", "--force")
	cmd.Stdin = strings.NewReader(statements.String())
	cmd.Stdout = io.Discard
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("source read-only diagnosis client failed (%w)", err)
	}
	var failed []tableFault
	pattern := regexp.MustCompile(`ERROR ([0-9]+) \(([A-Z0-9]+)\) at line ([0-9]+): (.*)`)
	for _, line := range strings.Split(strings.ReplaceAll(stderr.String(), "\r\n", "\n"), "\n") {
		if line == "" {
			continue
		}
		m := pattern.FindStringSubmatch(line)
		if len(m) != 5 {
			return nil, errors.New("unexpected source diagnosis error format; output withheld")
		}
		index, err := strconv.Atoi(m[3])
		if err != nil || index < 1 || index > len(tables) {
			return nil, errors.New("invalid source diagnosis line number")
		}
		t := tables[index-1]
		failed = append(failed, tableFault{t.Schema, t.Name, m[1], m[2], strings.ReplaceAll(m[4], password, "[redacted]")})
	}
	return failed, nil
}
