//go:build windows

package main

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// Each field is N for SQL NULL, or V followed by hexadecimal bytes. Colons
// separate fields and cannot occur inside either encoding. Sorted per-row
// SHA256 values retain duplicate multiplicity without retaining business data.
func canonicalRows(ctx context.Context, c *mysqlClient, table string, columns [][]string) (string, uint64, error) {
	var parts []string
	for _, col := range columns {
		name := identifier(col[0])
		parts = append(parts, "IF("+name+" IS NULL,'N',CONCAT('V',HEX(CAST("+name+" AS BINARY))))")
	}
	if len(parts) == 0 {
		return "", 0, errors.New("canonical comparison requires column definitions")
	}
	statement := "SELECT SHA2(CONCAT_WS(':'," + strings.Join(parts, ",") + "),256) AS row_hash FROM " + table + " ORDER BY BINARY row_hash;\n"
	cmd := c.command(ctx, c.executable, "--batch", "--raw", "--skip-column-names", "--max-allowed-packet=1G")
	cmd.Stdin = strings.NewReader(statement)
	out, err := cmd.StdoutPipe()
	if err != nil {
		return "", 0, err
	}
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err = cmd.Start(); err != nil {
		return "", 0, err
	}
	hash := sha256.New()
	scanner := bufio.NewScanner(out)
	valid := regexp.MustCompile(`^[0-9a-f]{64}$`)
	var rows uint64
	for scanner.Scan() {
		line := scanner.Text()
		if !valid.MatchString(line) {
			_ = cmd.Process.Kill()
			_ = cmd.Wait()
			return "", 0, errors.New("canonical row digest was NULL or malformed; no truncated comparison accepted")
		}
		hash.Write([]byte(line + "\n"))
		rows++
	}
	if scanErr := scanner.Err(); scanErr != nil {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		return "", 0, scanErr
	}
	if err = cmd.Wait(); err != nil {
		return "", 0, fmt.Errorf("canonical row query failed (%w)", err)
	}
	return hex.EncodeToString(hash.Sum(nil)), rows, nil
}

func verifyProjectLogin(ctx context.Context, executable string) (runErr error) {
	raw, err := os.ReadFile(`D:\phpstudy_pro\WWW\shaungsheng\ycf\application\database.php`)
	if err != nil {
		return err
	}
	user := phpValue(raw, "username")
	password := phpValue(raw, "password")
	database := phpValue(raw, "database")
	if user != "shuangsheng_ycf" || database != "shuangsheng_ycf" || password == "" {
		return errors.New("project account configuration changed")
	}
	client, err := newUserClient(destination, executable, 13306, user, password)
	if err != nil {
		return err
	}
	defer func() { runErr = errors.Join(runErr, client.close()) }()
	rows, err := client.query(ctx, "USE "+identifier(database)+"; SELECT 1,CURRENT_USER();\n")
	if err != nil {
		return err
	}
	if len(rows) != 1 || len(rows[0]) != 2 || rows[0][0] != "1" || !strings.HasPrefix(rows[0][1], user+"@") {
		return errors.New("non-root project login verification failed")
	}
	if err = saveJSON(filepath.Join("build", "migration", "db-project-login.json"), map[string]any{"host": "127.0.0.1", "port": 13306, "configuredUser": user, "currentUser": rows[0][1], "database": database, "select1": true}); err != nil {
		return err
	}
	fmt.Printf("NON-ROOT LOGIN VERIFIED: configured %s authenticated at 127.0.0.1:13306 as %s and opened original database\n", user, rows[0][1])
	return nil
}
