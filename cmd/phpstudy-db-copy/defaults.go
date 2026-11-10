//go:build windows

package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/zalando/go-keyring"
	"phptools/internal/desktop"
	"phptools/internal/model"
	"phptools/internal/services"
	"phptools/internal/store"
)

const sourceDefaultsQuery = "SELECT @@skip_name_resolve,@@max_allowed_packet,@@default_storage_engine,@@sql_mode,@@character_set_server,@@collation_server;\n"

func syncSourceDefaults(ctx context.Context, statePath string) (runErr error) {
	raw, err := os.ReadFile(statePath)
	if err != nil {
		return err
	}
	var state migrationState
	if err = json.Unmarshal(raw, &state); err != nil {
		return err
	}
	if state.Root != destination || state.Phase != "prepared" || state.Instance.Port != 13306 {
		return errors.New("defaults sync requires prepared port 13306")
	}
	pwd, err := sourcePassword()
	if err != nil {
		return err
	}
	source, err := newClient(destination, sourceClient, 3306, pwd)
	if err != nil {
		return err
	}
	defer func() { runErr = errors.Join(runErr, source.close()) }()
	rows, err := source.query(ctx, sourceDefaultsQuery)
	if err != nil {
		return err
	}
	if len(rows) != 1 || len(rows[0]) != 6 {
		return errors.New("unexpected source defaults response")
	}
	keys := []string{"skip-name-resolve", "max_allowed_packet", "default-storage-engine", "sql_mode", "character-set-server", "collation-server"}
	var appendSettings strings.Builder
	appendSettings.WriteString("\n; Exact source SQL and account-matching defaults\n")
	for k, v := range rows[0] {
		if !regexp.MustCompile(`^[A-Za-z0-9_,]+$`).MatchString(v) {
			return errors.New("unsupported source SQL default value")
		}
		if keys[k] == "skip-name-resolve" && v == "0" {
			continue
		}
		fmt.Fprintf(&appendSettings, "%s=%s\n", keys[k], v)
	}
	config, err := os.ReadFile(state.Instance.ConfigPath)
	if err != nil {
		return err
	}
	var retained []string
	for _, line := range strings.Split(string(config), "\n") {
		key, _, hasValue := strings.Cut(strings.TrimSpace(line), "=")
		if hasValue {
			normalized := strings.ReplaceAll(strings.ToLower(key), "_", "-")
			remove := false
			for _, controlled := range keys {
				if normalized == strings.ReplaceAll(controlled, "_", "-") {
					remove = true
					break
				}
			}
			if remove {
				continue
			}
		}
		retained = append(retained, line)
	}
	cleanedConfig := strings.Join(retained, "\n")
	backup := filepath.Join(state.BackupDir, "my-before-defaults.ini")
	if err = os.WriteFile(backup, config, 0600); err != nil {
		return err
	}
	db, err := store.Open(destination)
	if err != nil {
		return err
	}
	defer db.Close()
	instances, err := store.List[model.Instance](db, "instance")
	if err != nil {
		return err
	}
	for _, saved := range instances {
		if saved.ID == state.Instance.ID {
			if saved.DataDir != state.Instance.DataDir || saved.Port != 13306 || saved.ConfigPath != state.Instance.ConfigPath {
				return errors.New("prepared instance changed")
			}
			state.Instance = saved
		}
	}
	service := desktop.CredentialService(destination)
	manager := services.New(destination, func(i model.Instance) error { return db.Put("instance", i.ID, i) }, func(id string) (string, error) { return keyring.Get(service, id) })
	i := manager.Inspect(state.Instance)
	i, err = manager.Stop(ctx, i)
	state.Instance = i
	if err != nil {
		return err
	}
	if err = os.WriteFile(i.ConfigPath, []byte(cleanedConfig+appendSettings.String()), 0600); err != nil {
		return err
	}
	i, err = manager.Start(ctx, state.Package, i)
	state.Instance = i
	if err != nil {
		return err
	}
	if err = saveJSON(statePath, state); err != nil {
		return err
	}
	targetPwd, err := keyring.Get(service, i.ID)
	if err != nil {
		return err
	}
	target, err := newClient(destination, filepath.Join(filepath.Dir(i.Executable), "mysql.exe"), 13306, targetPwd)
	if err != nil {
		return err
	}
	defer func() { runErr = errors.Join(runErr, target.close()) }()
	targetRows, err := target.query(ctx, sourceDefaultsQuery)
	if err != nil {
		return err
	}
	x, _ := json.Marshal(rows)
	y, _ := json.Marshal(targetRows)
	if err = saveJSON(filepath.Join("build", "migration", "db-source-defaults.json"), map[string]any{"keys": keys, "source": rows[0], "target": targetRows[0]}); err != nil {
		return err
	}
	if string(x) != string(y) {
		return errors.New("target SQL defaults do not match source")
	}
	if err = saveJSON(statePath, state); err != nil {
		return err
	}
	if err = saveJSON(filepath.Join("build", "migration", "db-source-defaults.json"), map[string]any{"keys": keys, "source": rows[0], "target": targetRows[0]}); err != nil {
		return err
	}
	fmt.Println("SQL DEFAULTS VERIFIED: source and target name resolution, packet limit, storage engine, SQL mode, server charset and collation match; only new port 13306 was restarted")
	return nil
}
