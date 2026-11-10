//go:build windows

package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

func healthyTables(ctx context.Context, client *mysqlClient, listed []tableRef, password string) ([]tableRef, []tableFault, error) {
	raw, err := os.ReadFile(filepath.Join("build", "migration", "db-source-unreadable-tables.json"))
	if err != nil {
		return nil, nil, err
	}
	var saved struct {
		Unreadable []tableFault `json:"unreadable"`
	}
	if err = json.Unmarshal(raw, &saved); err != nil {
		return nil, nil, err
	}
	if len(saved.Unreadable) != 153 {
		return nil, nil, errors.New("expected explicit 153-table fault list")
	}
	bad := map[string]bool{}
	var check []tableRef
	for _, f := range saved.Unreadable {
		if f.Code != "1146" {
			return nil, nil, errors.New("only confirmed error1146 may be excluded")
		}
		key := f.Schema + "\x00" + f.Table
		if bad[key] {
			return nil, nil, errors.New("duplicate fault entry")
		}
		bad[key] = true
		check = append(check, tableRef{Schema: f.Schema, Name: f.Table})
	}
	failed, err := tableFailures(ctx, client, check, password)
	if err != nil {
		return nil, nil, err
	}
	if len(failed) != 153 {
		return nil, nil, errors.New("fault readability changed after cold backup; inspect before excluding")
	}
	for _, f := range failed {
		if f.Code != "1146" || !bad[f.Schema+"\x00"+f.Table] {
			return nil, nil, errors.New("fault diagnosis changed")
		}
	}
	var healthy []tableRef
	for _, t := range listed {
		if !bad[t.Schema+"\x00"+t.Name] {
			healthy = append(healthy, t)
		}
	}
	if len(healthy) != 3438 {
		return nil, nil, fmt.Errorf("expected 3438 readable tables, found %d", len(healthy))
	}
	return healthy, saved.Unreadable, nil
}
