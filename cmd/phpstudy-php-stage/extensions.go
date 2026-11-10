//go:build windows

package main

import (
	"crypto/sha256"
	"debug/pe"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

type sourcePHP struct {
	Version       string `json:"version"`
	INIPath       string `json:"iniPath"`
	ModulesOutput string `json:"modulesOutput"`
}

type extensionCheck struct {
	Branch        string   `json:"branch"`
	TargetVersion string   `json:"targetVersion"`
	SourceVersion string   `json:"sourceVersion"`
	Required      []string `json:"required"`
	Loaded        []string `json:"loaded"`
	Added         []string `json:"added"`
	INIBackup     string   `json:"iniBackup"`
}

func loadJSON(path string, result any) error {
	raw, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	return json.Unmarshal(raw, result)
}

func loadedModules(cli, ini string, extra ...string) ([]string, error) {
	args := []string{"-c", ini}
	if ini == "" {
		args = []string{"-n"}
	}
	args = append(args, extra...)
	args = append(args, "-r", "echo json_encode(get_loaded_extensions());")
	cmd := exec.Command(cli, args...)
	cmd.Dir = filepath.Dir(cli)
	raw, err := cmd.CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("PHP module probe: %w (%s)", err, strings.TrimSpace(string(raw)))
	}
	var result []string
	if err := json.Unmarshal(raw, &result); err != nil {
		return nil, fmt.Errorf("PHP module probe has unexpected warnings: %s", strings.TrimSpace(string(raw)))
	}
	sort.Strings(result)
	return result, nil
}

func containsModule(modules []string, name string) bool {
	for _, module := range modules {
		if strings.EqualFold(module, name) {
			return true
		}
	}
	return false
}

func requiredModules(output string) []string {
	var result []string
	inside := false
	for _, line := range strings.Split(output, "\n") {
		line = strings.TrimSpace(line)
		if line == "[PHP Modules]" {
			inside = true
			continue
		}
		if line == "[Zend Modules]" {
			break
		}
		if inside && line != "" {
			result = append(result, line)
		}
	}
	sort.Strings(result)
	return result
}

func phpBuild(cli string) (string, error) {
	cmd := exec.Command(cli, "-n", "-i")
	cmd.Dir = filepath.Dir(cli)
	raw, err := cmd.Output()
	if err != nil {
		return "", err
	}
	for _, line := range strings.Split(string(raw), "\n") {
		if strings.HasPrefix(line, "PHP Extension Build => ") {
			return strings.TrimSpace(strings.TrimPrefix(line, "PHP Extension Build => ")), nil
		}
	}
	return "", errors.New("PHP extension build was not reported")
}

func requireX64(path string) error {
	exe, err := pe.Open(path)
	if err != nil {
		return err
	}
	defer exe.Close()
	if exe.Machine != pe.IMAGE_FILE_MACHINE_AMD64 {
		return fmt.Errorf("expected x64 PE file: %s", path)
	}
	return nil
}

func prepareRedis(root string, source sourcePHP, mapping phpMapping, created *[]string) (string, error) {
	if mapping.SourceBranch != "7.2" || source.Version != "php7.2.9nts" {
		return "", errors.New("only the audited phpStudy PHP 7.2 Redis extension can be copied")
	}
	sourceCLI := filepath.Join(filepath.Dir(source.INIPath), "php.exe")
	sourceDLL := filepath.Join(filepath.Dir(source.INIPath), "ext", "php_redis.dll")
	targetCLI := filepath.Join(filepath.Dir(mapping.Executable), "php.exe")
	for _, path := range []string{sourceCLI, sourceDLL, targetCLI} {
		if err := requireX64(path); err != nil {
			return "", err
		}
	}
	sourceBuild, err := phpBuild(sourceCLI)
	if err != nil {
		return "", err
	}
	targetBuild, err := phpBuild(targetCLI)
	if err != nil {
		return "", err
	}
	if sourceBuild != targetBuild || sourceBuild != "API20170718,NTS,VC15" {
		return "", fmt.Errorf("Redis PHP ABI mismatch: source %s, target %s", sourceBuild, targetBuild)
	}
	modules, err := loadedModules(targetCLI, "", "-d", "extension="+sourceDLL)
	if err != nil {
		return "", fmt.Errorf("source Redis extension failed actual target PHP load: %w", err)
	}
	if !containsModule(modules, "redis") {
		return "", errors.New("source Redis extension did not register in target PHP")
	}
	dir := filepath.Join(root, "instances", mapping.TargetID, "extensions")
	if err := os.Mkdir(dir, 0700); err == nil {
		*created = append(*created, dir)
	} else if !os.IsExist(err) {
		return "", err
	}
	targetDLL := filepath.Join(dir, "php_redis.dll")
	raw, err := os.ReadFile(sourceDLL)
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256(raw)
	if existing, err := os.ReadFile(targetDLL); err == nil {
		if sha256.Sum256(existing) != digest {
			return "", errors.New("existing instance Redis extension differs; refusing overwrite")
		}
	} else if os.IsNotExist(err) {
		if err := os.WriteFile(targetDLL, raw, 0600); err != nil {
			return "", err
		}
		*created = append(*created, targetDLL)
	} else {
		return "", err
	}
	provenance := struct {
		Source      string `json:"source"`
		Destination string `json:"destination"`
		SHA256      string `json:"sha256"`
		PHPBuild    string `json:"phpExtensionBuild"`
		PEMachine   string `json:"peMachine"`
		VerifiedAt  string `json:"verifiedAt"`
	}{sourceDLL, targetDLL, hex.EncodeToString(digest[:]), targetBuild, "AMD64", time.Now().Format(time.RFC3339)}
	provenancePath := filepath.Join(dir, "php_redis.provenance.json")
	if _, err := os.Stat(provenancePath); os.IsNotExist(err) {
		metadata, err := json.MarshalIndent(provenance, "", "  ")
		if err != nil {
			return "", err
		}
		if err := os.WriteFile(provenancePath, metadata, 0600); err != nil {
			return "", err
		}
		*created = append(*created, provenancePath)
	} else if err != nil {
		return "", err
	}
	return targetDLL, nil
}

func prepareExtensions(root, mapPath, inventoryPath string) (runErr error) {
	if filepath.Clean(root) != `E:\PHPTools` {
		return errors.New("unexpected destination root")
	}
	if err := ensureManagerStopped(); err != nil {
		return err
	}
	mappings := make(map[string]phpMapping)
	if err := loadJSON(mapPath, &mappings); err != nil {
		return err
	}
	var sources []sourcePHP
	if err := loadJSON(inventoryPath, &sources); err != nil {
		return err
	}
	backupDir, err := os.MkdirTemp(filepath.Join(root, "backups"), "phpstudy-migration-ini-")
	if err != nil {
		return err
	}
	originals := make(map[string][]byte)
	var created []string
	defer func() {
		if runErr == nil {
			return
		}
		for path, original := range originals {
			runErr = errors.Join(runErr, os.WriteFile(path, original, 0600))
		}
		for index := len(created) - 1; index >= 0; index-- {
			runErr = errors.Join(runErr, os.Remove(created[index]))
		}
	}()
	var checks []extensionCheck
	for _, source := range sources {
		sourceBranch := branch(strings.TrimPrefix(strings.TrimSuffix(source.Version, "nts"), "php"))
		mapping, ok := mappings[sourceBranch]
		if !ok {
			return fmt.Errorf("no prepared target PHP %s", sourceBranch)
		}
		expectedINI := filepath.Join(root, "instances", mapping.TargetID, "conf", "php.ini")
		if !strings.EqualFold(mapping.ConfigPath, expectedINI) {
			return errors.New("PHP INI is not owned by the mapped destination instance")
		}
		cli := filepath.Join(filepath.Dir(mapping.Executable), "php.exe")
		loaded, err := loadedModules(cli, mapping.ConfigPath)
		if err != nil {
			return fmt.Errorf("PHP %s initial probe: %w", sourceBranch, err)
		}
		required := requiredModules(source.ModulesOutput)
		if len(required) == 0 {
			return fmt.Errorf("source PHP %s module inventory is empty", sourceBranch)
		}
		check := extensionCheck{Branch: sourceBranch, TargetVersion: mapping.Version, SourceVersion: source.Version, Required: required, Added: []string{}}
		var additions []string
		for _, module := range required {
			if containsModule(loaded, module) {
				continue
			}
			name := strings.ToLower(module)
			if name == "redis" {
				dll, err := prepareRedis(root, source, mapping, &created)
				if err != nil {
					return err
				}
				additions = append(additions, "extension=\""+filepath.ToSlash(dll)+"\"")
			} else {
				if name == "gd" && (strings.HasPrefix(mapping.Version, "5.") || strings.HasPrefix(mapping.Version, "7.")) {
					name = "gd2"
				}
				dll := "php_" + name + ".dll"
				if _, err := os.Stat(filepath.Join(filepath.Dir(cli), "ext", dll)); err != nil {
					return fmt.Errorf("source module %s has no bundled target DLL: %w", module, err)
				}
				additions = append(additions, "extension="+dll)
			}
			check.Added = append(check.Added, module)
		}
		if len(additions) > 0 {
			original, err := os.ReadFile(mapping.ConfigPath)
			if err != nil {
				return err
			}
			check.INIBackup = filepath.Join(backupDir, sourceBranch+"-php.ini")
			if err := os.WriteFile(check.INIBackup, original, 0600); err != nil {
				return err
			}
			originals[mapping.ConfigPath] = original
			contents := string(original) + "\n; PHPTools phpStudy migration: source loaded extensions\n" + strings.Join(additions, "\n") + "\n"
			if err := os.WriteFile(mapping.ConfigPath, []byte(contents), 0600); err != nil {
				return err
			}
		}
		check.Loaded, err = loadedModules(cli, mapping.ConfigPath)
		if err != nil {
			return fmt.Errorf("PHP %s final probe: %w", sourceBranch, err)
		}
		for _, required := range check.Required {
			if !containsModule(check.Loaded, required) {
				return fmt.Errorf("PHP %s target is missing required module %s", sourceBranch, required)
			}
		}
		checks = append(checks, check)
		fmt.Printf("PHP %s: %d source modules verified, %d added\n", mapping.Version, len(check.Required), len(check.Added))
	}
	raw, err := json.MarshalIndent(checks, "", "  ")
	if err != nil {
		return err
	}
	report := filepath.Join(filepath.Dir(mapPath), "php-extension-checks.json")
	if err := os.WriteFile(report, append(raw, '\n'), 0600); err != nil {
		return err
	}
	fmt.Printf("All source PHP extensions verified; report=%s; INI backups=%s\n", report, backupDir)
	return nil
}
