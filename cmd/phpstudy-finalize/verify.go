//go:build windows

package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"time"

	"github.com/zalando/go-keyring"
	"phptools/internal/desktop"
	"phptools/internal/model"
	"phptools/internal/services"
	"phptools/internal/sites"
	"phptools/internal/store"
)

// Verification deliberately owns no service lifetime: closing the store leaves
// the user's switched services running.
func verifySwitch(mysqlID string) error {
	const root = `E:\PHPTools`
	if mysqlID != "H7OOZJ3MSEHF6PVY4XQ2MHJZIW" {
		return fmt.Errorf("select the approved migration database explicitly")
	}
	db, err := store.Open(root)
	if err != nil {
		return err
	}
	defer db.Close()
	pkgs, err := store.List[model.Package](db, "package")
	if err != nil {
		return err
	}
	instances, err := store.List[model.Instance](db, "instance")
	if err != nil {
		return err
	}
	allSites, err := store.List[model.Site](db, "site")
	if err != nil {
		return err
	}
	byPackage := map[string]model.Package{}
	for _, p := range pkgs {
		byPackage[p.ID] = p
	}
	m := services.New(root, func(i model.Instance) error { return db.Put("instance", i.ID, i) }, func(id string) (string, error) {
		return keyring.Get(desktop.CredentialService(root), id)
	})
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	report := struct {
		CompletedAt string               `json:"completedAt"`
		Sites       int                  `json:"sites"`
		Instances   []model.Instance     `json:"instances"`
		Checks      []model.HealthItem   `json:"checks"`
		Databases   []model.DatabaseInfo `json:"databases"`
		Errors      []string             `json:"errors"`
	}{Sites: len(allSites), Errors: []string{}}
	foundMigration := false
	foundExisting := false
	for _, i := range instances {
		next, checks := m.CheckHealth(ctx, byPackage[i.PackageID], i, true)
		report.Instances = append(report.Instances, next)
		report.Checks = append(report.Checks, checks...)
		for _, check := range checks {
			if check.Status == "error" {
				report.Errors = append(report.Errors, check.TargetName+": "+check.Message)
			}
		}
		if i.Kind == "php" || i.Kind == "nginx" || i.ID == mysqlID {
			ready := false
			for _, check := range checks {
				if check.ID == i.ID+":ready" && check.Status == "ok" {
					ready = true
				}
			}
			if !ready {
				report.Errors = append(report.Errors, i.Name+" did not pass the service response check")
			}
			if next.State != "running" {
				report.Errors = append(report.Errors, i.Name+" is not running")
			}
		}
		if i.ID == "W7RB3QKM7V3M4C7ZQDBKHMLOME" {
			foundExisting = next.Port == 3307 && next.State == "stopped"
		}
		if i.ID == mysqlID {
			foundMigration = next.Port == 3306 && next.State == "running"
			var databases []model.DatabaseInfo
			databases, err = m.ListDatabases(ctx, next)
			if err != nil {
				report.Errors = append(report.Errors, "list imported databases: "+err.Error())
			}
			for _, database := range databases {
				if !database.System {
					report.Databases = append(report.Databases, database)
				}
			}
		}
	}
	if report.Sites != 55 || !foundMigration || !foundExisting || len(report.Databases) != 56 {
		report.Errors = append(report.Errors, fmt.Sprintf("unexpected migration state: sites=%d migration3306=%t existing3307=%t businessDatabases=%d", report.Sites, foundMigration, foundExisting, len(report.Databases)))
	}
	if err := sites.CheckHosts(sites.Domains(allSites)); err != nil {
		report.Errors = append(report.Errors, "hosts: "+err.Error())
	}
	if err := verifyManifests(mysqlID, allSites, report.Databases); err != nil {
		report.Errors = append(report.Errors, err.Error())
	}
	report.CompletedAt = model.Now()
	raw, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		return err
	}
	const output = `build\qa\phpstudy-switch-verification.json`
	if err := os.WriteFile(output, raw, 0600); err != nil {
		return err
	}
	fmt.Printf("Verified %d sites, %d instances, %d business databases; %d errors; report: %s\n", report.Sites, len(report.Instances), len(report.Databases), len(report.Errors), output)
	if len(report.Errors) > 0 {
		return fmt.Errorf("switch verification failed: %v", report.Errors)
	}
	return nil
}
