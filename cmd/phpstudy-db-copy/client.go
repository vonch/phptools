//go:build windows

package main

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"phptools/internal/services"
)

type mysqlClient struct{ executable, directory, optionFile string }

func newClient(root, executable string, port int, password string) (*mysqlClient, error) {
	return newUserClient(root, executable, port, "root", password)
}

func newUserClient(root, executable string, port int, user, password string) (*mysqlClient, error) {
	if user == "" || strings.ContainsAny(user, "\r\n\x00") {
		return nil, errors.New("invalid configured database user")
	}
	dir, err := os.MkdirTemp(filepath.Join(root, "cache"), "migration-credentials-")
	if err != nil {
		return nil, err
	}
	failed := true
	defer func() {
		if failed {
			_ = os.RemoveAll(dir)
		}
	}()
	if err = services.ProtectPrivatePath(dir, true); err != nil {
		return nil, err
	}
	path := filepath.Join(dir, "client.cnf")
	p := strings.NewReplacer(`\`, `\\`, `"`, `\"`).Replace(password)
	u := strings.NewReplacer(`\`, `\\`, `"`, `\"`).Replace(user)
	contents := "[client]\nuser=\"" + u + "\"\npassword=\"" + p + "\"\nprotocol=TCP\nhost=127.0.0.1\nport=" + strconv.Itoa(port) + "\ndefault-character-set=utf8mb4\n"
	if err = os.WriteFile(path, []byte(contents), 0600); err != nil {
		return nil, err
	}
	if err = services.ProtectPrivatePath(path, false); err != nil {
		return nil, err
	}
	failed = false
	return &mysqlClient{executable, dir, path}, nil
}

func (c *mysqlClient) close() error { return os.RemoveAll(c.directory) }

func (c *mysqlClient) command(ctx context.Context, executable string, args ...string) *exec.Cmd {
	args = append([]string{"--defaults-file=" + c.optionFile}, args...)
	cmd := exec.CommandContext(ctx, executable, args...)
	cmd.Dir = filepath.Dir(executable)
	cmd.Env = append(os.Environ(), "MYSQL_TEST_LOGIN_FILE="+filepath.Join(c.directory, "unused.mylogin.cnf"))
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
	return cmd
}

func (c *mysqlClient) query(ctx context.Context, statement string) ([][]string, error) {
	cmd := c.command(ctx, c.executable, "--batch", "--raw", "--skip-column-names", "--max-allowed-packet=1G")
	cmd.Stdin = strings.NewReader(statement)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("MySQL statement failed; output withheld to protect credentials (%w)", err)
	}
	text := strings.TrimSuffix(strings.ReplaceAll(stdout.String(), "\r\n", "\n"), "\n")
	if text == "" {
		return nil, nil
	}
	lines := strings.Split(text, "\n")
	rows := make([][]string, 0, len(lines))
	for _, line := range lines {
		rows = append(rows, strings.Split(line, "\t"))
	}
	return rows, nil
}

func sqlString(value string) string {
	return "'" + strings.NewReplacer(`\`, `\\`, "'", "''").Replace(value) + "'"
}
func identifier(value string) string { return "`" + strings.ReplaceAll(value, "`", "``") + "`" }

type sourceLock struct {
	command  *exec.Cmd
	input    io.WriteCloser
	finished chan error
}

func acquireLock(ctx context.Context, c *mysqlClient) (*sourceLock, error) {
	cmd := c.command(ctx, c.executable, "--batch", "--raw", "--skip-column-names", "--unbuffered")
	in, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	out, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err = cmd.Start(); err != nil {
		return nil, err
	}
	lock := &sourceLock{cmd, in, make(chan error, 1)}
	go func() { lock.finished <- cmd.Wait() }()
	ready := make(chan bool, 1)
	go func() {
		scanner := bufio.NewScanner(out)
		for scanner.Scan() {
			if scanner.Text() == "PHPTOOLS_MIGRATION_LOCKED" {
				ready <- true
				return
			}
		}
		ready <- false
	}()
	if _, err = io.WriteString(in, "SET SESSION wait_timeout=28800; SET SESSION lock_wait_timeout=30; FLUSH TABLES WITH READ LOCK; SELECT 'PHPTOOLS_MIGRATION_LOCKED';\n"); err != nil {
		_ = lock.close()
		return nil, err
	}
	select {
	case ok := <-ready:
		if ok {
			return lock, nil
		}
		_ = lock.close()
		return nil, errors.New("source global read lock was not confirmed")
	case <-time.After(45 * time.Second):
		_ = lock.close()
		return nil, errors.New("source global read lock timed out")
	case <-ctx.Done():
		_ = lock.close()
		return nil, ctx.Err()
	}
}

func (l *sourceLock) close() error {
	_, writeErr := io.WriteString(l.input, "UNLOCK TABLES;\nquit\n")
	closeErr := l.input.Close()
	select {
	case err := <-l.finished:
		return errors.Join(writeErr, closeErr, err)
	case <-time.After(10 * time.Second):
		_ = l.command.Process.Kill()
		<-l.finished
		return errors.New("source lock client required termination; connection closed and lock released")
	}
}
