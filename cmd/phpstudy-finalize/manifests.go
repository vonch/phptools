//go:build windows

package main

import (
	"encoding/json"
	"fmt"
	"os"

	"phptools/internal/model"
)

func verifyManifests(mysqlID string, current []model.Site, databases []model.DatabaseInfo) error {
	var plan []model.Site
	raw, err := os.ReadFile(`build\migration\sites-import-plan.json`)
	if err != nil {
		return err
	}
	if err := json.Unmarshal(raw, &plan); err != nil {
		return err
	}
	byID := map[string]model.Site{}
	for _, site := range current {
		byID[site.ID] = site
	}
	for _, site := range plan {
		if byID[site.ID] != site {
			return fmt.Errorf("imported site differs from verified plan: %s", site.Domain)
		}
	}
	var copied struct {
		TargetInstance string     `json:"targetInstance"`
		Schemas        []string   `json:"schemas"`
		Tables         []struct{} `json:"tables"`
		Error          string     `json:"error"`
	}
	raw, err = os.ReadFile(`build\migration\db-copy-report.json`)
	if err != nil {
		return err
	}
	if err := json.Unmarshal(raw, &copied); err != nil {
		return err
	}
	if len(plan) != 53 || copied.TargetInstance != mysqlID || copied.Error != "" || len(copied.Schemas) != 56 || len(copied.Tables) != 3438 {
		return fmt.Errorf("migration reports do not confirm the approved site and database copy")
	}
	expected := map[string]bool{}
	for _, name := range copied.Schemas {
		expected[name] = true
	}
	for _, database := range databases {
		if !expected[database.Name] {
			return fmt.Errorf("unexpected migrated database: %s", database.Name)
		}
		delete(expected, database.Name)
	}
	if len(expected) != 0 {
		return fmt.Errorf("%d migrated databases are missing", len(expected))
	}
	return nil
}
