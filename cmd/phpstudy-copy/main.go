//go:build windows

// Command phpstudy-copy stages a local, auditable copy of an existing runtime.
// Source processes and files are never adopted by the PHPTools service manager.
package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"phptools/internal/desktop"
	"phptools/internal/model"
)

func main() {
	mode := flag.String("mode", "target-inventory", "migration phase")
	output := flag.String("output", "", "new JSON report path")
	flag.Parse()
	if err := run(*mode, *output); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(mode, output string) (runErr error) {
	if mode != "target-inventory" {
		return errors.New("unsupported migration phase")
	}
	a, err := desktop.New("", desktop.Hooks{})
	if err != nil {
		return err
	}
	snapshot, err := a.GetState()
	if err != nil {
		return err
	}
	if snapshot.Settings.Root != `E:\PHPTools` {
		return errors.New("unexpected destination root; refusing to modify it")
	}
	for _, instance := range snapshot.Instances {
		if instance.PID != 0 || instance.State != "stopped" {
			return errors.New("destination has active or unknown processes; close it before migration")
		}
	}
	defer func() { runErr = errors.Join(runErr, a.Quit()) }()
	var mysql model.Instance
	for _, instance := range snapshot.Instances {
		if instance.Kind == "mysql" {
			if mysql.ID != "" {
				return errors.New("multiple destination MySQL instances; select one explicitly")
			}
			mysql = instance
		}
	}
	if mysql.ID == "" {
		return errors.New("destination MySQL instance is missing")
	}
	id, err := a.StartInstance(mysql.ID)
	if err != nil {
		return err
	}
	if err := waitTask(a, id, time.Minute); err != nil {
		return err
	}
	databases, err := a.ListDatabases(mysql.ID)
	if err != nil {
		return err
	}
	accounts, err := a.ListDatabaseAccounts(mysql.ID)
	if err != nil {
		return err
	}
	report := struct {
		Root      string                  `json:"root"`
		Instance  model.Instance          `json:"instance"`
		Databases []model.DatabaseInfo    `json:"databases"`
		Accounts  []model.DatabaseAccount `json:"accounts"`
	}{snapshot.Settings.Root, mysql, databases, accounts}
	raw, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		return err
	}
	if output != "" {
		if err := os.MkdirAll(filepath.Dir(output), 0700); err != nil {
			return err
		}
		f, err := os.OpenFile(output, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
		if err != nil {
			return err
		}
		_, writeErr := f.Write(raw)
		if err := errors.Join(writeErr, f.Close()); err != nil {
			return err
		}
	}
	fmt.Printf("Destination inventory: %d databases, %d accounts; report=%s\n", len(databases), len(accounts), output)
	return nil
}

func waitTask(a *desktop.App, id string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		snapshot, err := a.GetState()
		if err != nil {
			return err
		}
		for _, task := range snapshot.Tasks {
			if task.ID != id {
				continue
			}
			switch task.State {
			case "completed":
				return nil
			case "failed", "cancelled":
				return errors.New(task.Error)
			}
		}
		time.Sleep(100 * time.Millisecond)
	}
	return errors.New("migration task timed out")
}
