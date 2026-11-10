//go:build windows

package main

import (
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	_ "modernc.org/sqlite"

	"phptools/internal/model"
	"phptools/internal/sites"
)

type sourceSite struct {
	SourceConfig       string `json:"sourceConfig"`
	SourceConfigSHA256 string `json:"sourceConfigSHA256"`
	Domain             string `json:"domain"`
	DocumentRoot       string `json:"documentRoot"`
	Port               int    `json:"port"`
	PHPVersion         string `json:"phpVersion"`
	RewriteFiles       []struct {
		Path   string `json:"path"`
		SHA256 string `json:"sha256"`
	} `json:"rewriteFiles"`
}

type phpMapping struct {
	TargetID string `json:"targetId"`
}

func readJSON(path string, output any) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	return json.Unmarshal(data, output)
}

func readRecords[T any](db *sql.DB, kind string) ([]T, error) {
	rows, err := db.Query("SELECT payload FROM records WHERE kind=? ORDER BY id", kind)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []T{}
	for rows.Next() {
		var raw string
		var value T
		if err = rows.Scan(&raw); err != nil {
			return nil, err
		}
		if err = json.Unmarshal([]byte(raw), &value); err != nil {
			return nil, err
		}
		result = append(result, value)
	}
	return result, rows.Err()
}

func preparePlan(root, inventory, phpMap, output string) ([]model.Site, []model.Site, []model.Instance, []model.Package, error) {
	var source struct{ Sites []sourceSite }
	var mapping map[string]phpMapping
	if err := readJSON(inventory, &source); err != nil {
		return nil, nil, nil, nil, err
	}
	if len(source.Sites) != 53 {
		return nil, nil, nil, nil, fmt.Errorf("source count changed: %d; recheck migration scope", len(source.Sites))
	}
	if err := readJSON(phpMap, &mapping); err != nil {
		return nil, nil, nil, nil, err
	}
	db, err := sql.Open("sqlite", "file:"+filepath.ToSlash(filepath.Join(root, "metadata.db"))+"?mode=ro")
	if err != nil {
		return nil, nil, nil, nil, err
	}
	defer db.Close()
	existing, err := readRecords[model.Site](db, "site")
	if err != nil {
		return nil, nil, nil, nil, err
	}
	instances, err := readRecords[model.Instance](db, "instance")
	if err != nil {
		return nil, nil, nil, nil, err
	}
	packages, err := readRecords[model.Package](db, "package")
	if err != nil {
		return nil, nil, nil, nil, err
	}
	previous := []model.Site{}
	if err = readJSON(output, &previous); err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, nil, nil, nil, fmt.Errorf("read stable site IDs: %w", err)
	}
	byAddress := map[string]string{}
	for _, site := range previous {
		key := fmt.Sprintf("%s:%d", site.Domain, site.Port)
		if byAddress[key] != "" {
			return nil, nil, nil, nil, errors.New("previous plan has duplicate addresses")
		}
		byAddress[key] = site.ID
	}
	plan := make([]model.Site, 0, len(source.Sites))
	for _, item := range source.Sites {
		parts := strings.Split(item.PHPVersion, ".")
		if len(parts) != 3 || mapping[strings.Join(parts[:2], ".")].TargetID == "" {
			return nil, nil, nil, nil, fmt.Errorf("PHP mapping missing for %s", item.Domain)
		}
		id := byAddress[fmt.Sprintf("%s:%d", item.Domain, item.Port)]
		if id == "" {
			id = rand.Text()
		}
		rules, err := sourceRules(item)
		if err != nil {
			return nil, nil, nil, nil, fmt.Errorf("%s: %w", item.Domain, err)
		}
		site := model.Site{ID: id, Name: item.Domain, Domain: item.Domain, Root: item.DocumentRoot, Port: item.Port,
			PHPInstanceID: mapping[strings.Join(parts[:2], ".")].TargetID, PHPPathInfo: true, Enabled: true, Rewrite: rules}
		site, err = sites.Validate(site, append(append([]model.Site{}, existing...), plan...), instances)
		if err != nil {
			return nil, nil, nil, nil, fmt.Errorf("%s: %w", item.Domain, err)
		}
		plan = append(plan, site)
	}
	data, err := json.MarshalIndent(plan, "", "  ")
	if err == nil {
		err = os.MkdirAll(filepath.Dir(output), 0700)
	}
	if err == nil {
		err = os.WriteFile(output, data, 0600)
	}
	return plan, existing, instances, packages, err
}

func verifiedSource(path, expected string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256(data)
	if hex.EncodeToString(digest[:]) != expected {
		return "", fmt.Errorf("source changed since inventory: %s", path)
	}
	return strings.TrimPrefix(strings.ReplaceAll(string(data), "\r\n", "\n"), "\ufeff"), nil
}

func siteDigest(plan []model.Site) (string, error) {
	data, err := json.Marshal(plan)
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256(data)
	return hex.EncodeToString(digest[:]), nil
}

var includeLine = regexp.MustCompile(`^include\s+(.+);$`)

// sourceRules deliberately accepts only the inventoried phpStudy server shape.
// Includes are expanded at their original position, retaining nested locations.
func sourceRules(item sourceSite) (string, error) {
	text, err := verifiedSource(item.SourceConfig, item.SourceConfigSHA256)
	if err != nil {
		return "", err
	}
	includes := map[string]string{}
	for _, include := range item.RewriteFiles {
		body, err := verifiedSource(include.Path, include.SHA256)
		if err != nil {
			return "", err
		}
		if regexp.MustCompile(`(?m)^\s*include\s`).MatchString(body) {
			return "", errors.New("nested source include requires explicit review")
		}
		includes[strings.ToLower(filepath.Clean(include.Path))] = body
	}
	depth := 0
	php := false
	phpCount := 0
	var output []string
	for _, line := range strings.Split(text, "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}
		before := depth
		depth += strings.Count(line, "{") - strings.Count(line, "}")
		if before == 0 && trimmed != "server {" {
			return "", fmt.Errorf("unexpected source server header: %q", trimmed)
		}
		if before == 0 || depth == 0 {
			continue
		}
		if before == 1 && trimmed == `location ~ \.php(.*)$ {` {
			php, phpCount = true, phpCount+1
		}
		if php {
			if depth == 1 {
				php = false
			}
			continue
		}
		first := strings.Fields(trimmed)[0]
		if before == 1 && (first == "listen" || first == "server_name" || first == "root" || first == "error_log" || first == "access_log") {
			continue
		}
		if first == "include" {
			match := includeLine.FindStringSubmatch(trimmed)
			if len(match) != 2 {
				return "", errors.New("unexpected include syntax")
			}
			path := strings.Trim(match[1], `"`)
			body, ok := includes[strings.ToLower(filepath.Clean(filepath.FromSlash(path)))]
			if !ok {
				return "", fmt.Errorf("include was not inventoried: %s", path)
			}
			output = append(output, body)
			continue
		}
		output = append(output, line)
	}
	if depth != 0 || php || phpCount != 1 {
		return "", errors.New("unexpected source server/PHP block structure")
	}
	return strings.Join(output, "\n"), nil
}
