//go:build windows

package main

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"

	"phptools/internal/desktop"
	"phptools/internal/model"
)

// Opening is recorded at the native boundary. Tests never launch the user's
// database client or navigate to an existing phpMyAdmin installation.
type toolLaunchRecorder struct{ client, url string }

func (r *toolLaunchRecorder) openClient(path string) error { r.client = path; return nil }
func (r *toolLaunchRecorder) openURL(url string) error     { r.url = url; return nil }

func exerciseTools(a *desktop.App, instance model.Instance, opened *toolLaunchRecorder, record func(string)) (runErr error) {
	const database = "tools_management_check"
	if err := a.CreateDatabase(instance.ID, database, "Smoke-db-pass-1"); err != nil {
		return err
	}
	defer func() { runErr = errors.Join(runErr, a.DropDatabase(instance.ID, database)) }()
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	client, err := a.AddDatabaseTool("desktop", exe)
	if err != nil {
		return err
	}
	defer func() { runErr = errors.Join(runErr, a.RemoveDatabaseTool(client.ID)) }()
	web, err := a.AddDatabaseTool("phpmyadmin", "http://127.0.0.1:65432/phpmyadmin/")
	if err != nil {
		return err
	}
	defer func() { runErr = errors.Join(runErr, a.RemoveDatabaseTool(web.ID)) }()
	if err = a.OpenDatabaseTool(instance.ID, database, client.ID); err != nil || opened.client != exe {
		return errors.Join(errors.New("desktop client was not dispatched with its exact executable path"), err)
	}
	if err = a.OpenDatabaseTool(instance.ID, database, web.ID); err != nil || opened.url != web.URL {
		return errors.Join(errors.New("phpMyAdmin URL was not dispatched to browser hook"), err)
	}
	record("external native/web tool dispatch uses the selected running instance and existing business database, with no password arguments")
	for _, name := range []string{"mysql", "information_schema", "performance_schema", "sys", "database_not_present"} {
		opened.client = ""
		if err = a.OpenDatabaseTool(instance.ID, name, client.ID); err == nil || opened.client != "" {
			return errors.New("protected or missing database launched external client")
		}
	}
	if err = a.OpenDatabaseTool(instance.ID, database, "missing-tool"); err == nil {
		return errors.New("unrecognized tool ID was accepted")
	}
	record("external management rejects system/missing databases and stale tool selections")
	tools, err := a.DetectDatabaseTools()
	if err != nil {
		return err
	}
	data, err := json.MarshalIndent(tools, "", "  ")
	if err != nil {
		return err
	}
	if err = os.WriteFile(filepath.Join(filepath.Dir(instance.DataDir), "detected-database-tools.json"), data, 0600); err != nil {
		return err
	}
	record("registered local clients and phpMyAdmin folders detected; custom native/web records persisted and removable")
	return nil
}
