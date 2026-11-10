//go:build windows

package main

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"

	"phptools/internal/desktop"
	"phptools/internal/model"
)

func buildPlans(ctx context.Context, a *desktop.App, state model.Snapshot, input settingsInput) ([]applyPlan, error) {
	if len(input.Branches) != 6 {
		return nil, errors.New("expected exactly six audited PHP branches")
	}
	allowed := map[string]bool{}
	for _, key := range coreKeys {
		allowed[key] = true
	}
	instances := map[string]model.Instance{}
	for _, i := range state.Instances {
		instances[i.ID] = i
	}
	packages := map[string]model.Package{}
	for _, p := range state.Packages {
		packages[p.ID] = p
	}
	seen := map[string]bool{}
	var plans []applyPlan
	for _, b := range input.Branches {
		if seen[b.TargetID] || b.TargetID == "" {
			return nil, errors.New("duplicate or missing PHP target ID")
		}
		seen[b.TargetID] = true
		i, ok := instances[b.TargetID]
		if !ok || i.Kind != "php" || i.State != "running" || i.PID == 0 {
			return nil, fmt.Errorf("PHP %s is not a running managed target", b.Branch)
		}
		p, ok := packages[i.PackageID]
		if !ok || p.Kind != "php" || !strings.HasPrefix(p.Version, b.Branch+".") {
			return nil, errors.New("target package does not match audited PHP branch")
		}
		expected := filepath.Join(runtimeRoot, "instances", i.ID, "conf", "php.ini")
		if !strings.EqualFold(filepath.Clean(i.ConfigPath), expected) {
			return nil, errors.New("target config path is not the expected owned PHP ini")
		}
		for key := range b.Source {
			if !allowed[key] {
				return nil, fmt.Errorf("source key %s is outside the core whitelist", key)
			}
		}
		for key := range b.Target {
			if !allowed[key] {
				return nil, fmt.Errorf("target key %s is outside the core whitelist", key)
			}
		}
		changes := map[string]string{}
		for _, key := range coreKeys {
			src, srcOK := b.Source[key]
			dst, dstOK := b.Target[key]
			if (!srcOK && !dstOK) || (src == nil && dst == nil) {
				if _, diff := b.Differences[key]; diff {
					return nil, errors.New("unsupported directive has an unexpected difference")
				}
				continue
			}
			if !srcOK || !dstOK || src == nil || dst == nil {
				return nil, fmt.Errorf("PHP %s %s lacks an explicit final CGI value", b.Branch, key)
			}
			if !safeValue(*src) || !safeValue(*dst) {
				return nil, fmt.Errorf("PHP %s %s has an unsupported control or ini delimiter", b.Branch, key)
			}
			if *src != *dst {
				d, ok := b.Differences[key]
				if !ok || d.Source == nil || d.Target == nil || *d.Source != *src || *d.Target != *dst {
					return nil, fmt.Errorf("PHP %s %s difference does not match CGI maps", b.Branch, key)
				}
				changes[key] = *src
			}
		}
		for key, d := range b.Differences {
			if !allowed[key] || d.Source == nil || d.Target == nil {
				return nil, errors.New("invalid core difference entry")
			}
			if *d.Source != *b.Source[key] || *d.Target != *b.Target[key] {
				return nil, errors.New("difference entry disagrees with final CGI maps")
			}
		}
		old, err := a.ReadConfig(i.ID)
		if err != nil {
			return nil, err
		}
		live, err := probeCGI(ctx, i)
		if err != nil {
			return nil, err
		}
		if err = checkTargetValues(live, b.Target); err != nil {
			return nil, fmt.Errorf("PHP %s preflight: %w", b.Branch, err)
		}
		modified := old
		if len(changes) > 0 {
			var addition strings.Builder
			addition.WriteString("\n\n; Core settings from the audited phpStudy CGI configuration\n[PHP]\n")
			for _, key := range sortedChangeKeys(changes) {
				fmt.Fprintf(&addition, "%s=\"%s\"\n", key, changes[key])
			}
			modified += addition.String()
		}
		plans = append(plans, applyPlan{Branch: b.Branch, TargetID: i.ID, ConfigPath: i.ConfigPath, Changes: changes, OriginalSHA256: digest(old), Original: old, Modified: modified, Instance: i})
	}
	return plans, nil
}

func checkTargetValues(actual map[string]string, expected map[string]*string) error {
	for key, wanted := range expected {
		value, ok := actual[key]
		if wanted == nil || !ok || value != *wanted {
			return fmt.Errorf("current target CGI value for %s changed from the audited report", key)
		}
	}
	return nil
}
