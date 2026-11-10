//go:build windows

// Command phpstudy-php-settings applies only audited PHP core settings.
package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"syscall"
	"time"
	"unicode"

	"phptools/internal/desktop"
	"phptools/internal/model"
)

const runtimeRoot = `E:\PHPTools`

var coreKeys = []string{
	"date.timezone", "max_execution_time", "max_input_time", "max_input_vars", "memory_limit",
	"post_max_size", "upload_max_filesize", "max_file_uploads", "error_reporting", "display_errors",
	"display_startup_errors", "log_errors", "default_charset", "default_socket_timeout", "short_open_tag",
	"request_order", "variables_order", "allow_url_fopen", "allow_url_include", "zend.assertions", "realpath_cache_size",
}

type settingsDiff struct {
	Source *string `json:"source"`
	Target *string `json:"target"`
}

type branchSettings struct {
	Branch      string                  `json:"branch"`
	TargetID    string                  `json:"targetId"`
	Source      map[string]*string      `json:"source"`
	Target      map[string]*string      `json:"target"`
	Differences map[string]settingsDiff `json:"differences"`
}

type settingsInput struct {
	Branches []branchSettings `json:"branches"`
}

type applyPlan struct {
	Branch         string            `json:"branch"`
	TargetID       string            `json:"targetId"`
	ConfigPath     string            `json:"configPath"`
	Changes        map[string]string `json:"changes"`
	OriginalSHA256 string            `json:"originalSHA256"`
	Applied        bool              `json:"applied"`
	RolledBack     bool              `json:"rolledBack"`
	Original       string            `json:"-"`
	Modified       string            `json:"-"`
	Instance       model.Instance    `json:"-"`
}

type settingsReport struct {
	StartedAt                string      `json:"startedAt"`
	CompletedAt              string      `json:"completedAt"`
	Plans                    []applyPlan `json:"plans"`
	NonPHPProcessesUnchanged bool        `json:"nonPHPProcessesUnchanged"`
	Error                    string      `json:"error,omitempty"`
}

func main() {
	input := flag.String("input", `build\migration\php-core-settings-diff.json`, "final CGI effective-value comparison")
	output := flag.String("report", `build\qa\phpstudy-php-core-settings-applied.json`, "permanent result report")
	apply := flag.Bool("apply", false, "apply the validated core-setting differences")
	flag.Parse()
	if err := run(context.Background(), *input, *output, *apply); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(ctx context.Context, inputPath, outputPath string, apply bool) (runErr error) {
	if err := requireClosedGUI(ctx); err != nil {
		return err
	}
	raw, err := os.ReadFile(inputPath)
	if err != nil {
		return err
	}
	var input settingsInput
	if err = json.Unmarshal(raw, &input); err != nil {
		return err
	}
	a, err := desktop.New("", desktop.Hooks{})
	if err != nil {
		return err
	}
	// Deliberately do not call App.Quit: it would stop unrelated running services.
	initial, err := a.GetState()
	if err != nil {
		return err
	}
	if initial.Settings.Root != runtimeRoot {
		return errors.New("unexpected PHPTools environment root")
	}
	plans, err := buildPlans(ctx, a, initial, input)
	if err != nil {
		return err
	}
	report := settingsReport{StartedAt: model.Now(), Plans: plans}
	defer func() {
		report.CompletedAt = model.Now()
		if runErr != nil {
			report.Error = runErr.Error()
		}
		data, jsonErr := json.MarshalIndent(report, "", "  ")
		if jsonErr == nil {
			jsonErr = os.MkdirAll(filepath.Dir(outputPath), 0700)
		}
		if jsonErr == nil {
			jsonErr = os.WriteFile(outputPath, data, 0600)
		}
		runErr = errors.Join(runErr, jsonErr)
	}()
	if !apply {
		fmt.Printf("VALIDATED %d PHP plans; no configuration or service changes requested\n", len(plans))
		return nil
	}
	var touched []int
	defer func() {
		if runErr == nil {
			return
		}
		for k := len(touched) - 1; k >= 0; k-- {
			index := touched[k]
			p := &report.Plans[index]
			restoreErr := a.SaveConfig(p.TargetID, p.Original)
			if restoreErr == nil {
				restoreErr = restartPHP(context.Background(), a, p.TargetID)
			}
			p.RolledBack = restoreErr == nil
			p.Applied = false
			runErr = errors.Join(runErr, restoreErr)
		}
	}()
	for k := range report.Plans {
		p := &report.Plans[k]
		if len(p.Changes) == 0 {
			continue
		}
		current, err := a.ReadConfig(p.TargetID)
		if err != nil {
			return err
		}
		if current != p.Original {
			return fmt.Errorf("PHP %s config changed after preflight", p.Branch)
		}
		live, err := probeCGI(ctx, p.Instance)
		if err != nil {
			return err
		}
		if err = checkTargetValues(live, input.Branches[k].Target); err != nil {
			return err
		}
		touched = append(touched, k)
		if err = a.SaveConfig(p.TargetID, p.Modified); err != nil {
			return fmt.Errorf("save PHP %s: %w", p.Branch, err)
		}
		live, err = probeCGI(ctx, p.Instance)
		if err != nil {
			return err
		}
		for key, wanted := range p.Changes {
			if live[key] != wanted {
				return fmt.Errorf("PHP %s saved value mismatch for %s", p.Branch, key)
			}
		}
		if err = restartPHP(ctx, a, p.TargetID); err != nil {
			return fmt.Errorf("restart PHP %s: %w", p.Branch, err)
		}
		p.Applied = true
		fmt.Printf("APPLIED PHP %s: %d core settings; instance restarted\n", p.Branch, len(p.Changes))
	}
	final, err := a.GetState()
	if err != nil {
		return err
	}
	report.NonPHPProcessesUnchanged = nonPHPUnchanged(initial, final)
	if !report.NonPHPProcessesUnchanged {
		return errors.New("an unrelated service identity changed during PHP configuration sync")
	}
	fmt.Println("PHP core settings verified; MySQL, Nginx and Redis process identities unchanged")
	return nil
}

func requireClosedGUI(ctx context.Context) error {
	c := exec.CommandContext(ctx, "powershell.exe", "-NoProfile", "-NonInteractive", "-Command", "if(Get-Process -Name PHPTools -ErrorAction SilentlyContinue){exit 10};exit 0")
	c.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
	if c.Run() != nil {
		return errors.New("PHPTools GUI must be closed by its owner before this tool runs")
	}
	return nil
}

func safeValue(value string) bool {
	if len(value) > 2048 || strings.ContainsAny(value, "\"\\$;[]") {
		return false
	}
	for _, r := range value {
		if unicode.IsControl(r) {
			return false
		}
	}
	return true
}

func digest(text string) string {
	sum := sha256.Sum256([]byte(text))
	return hex.EncodeToString(sum[:])
}

func nonPHPUnchanged(before, after model.Snapshot) bool {
	byID := map[string]model.Instance{}
	for _, i := range after.Instances {
		byID[i.ID] = i
	}
	for _, i := range before.Instances {
		if i.Kind == "php" {
			continue
		}
		n, ok := byID[i.ID]
		if !ok || i.PID != n.PID || i.ProcessCreated != n.ProcessCreated || i.State != n.State {
			return false
		}
	}
	return true
}

func restartPHP(ctx context.Context, a *desktop.App, id string) error {
	taskID, err := a.RestartInstance(id)
	if err != nil {
		return err
	}
	deadline := time.Now().Add(2 * time.Minute)
	for time.Now().Before(deadline) {
		state, err := a.GetState()
		if err != nil {
			return err
		}
		for _, task := range state.Tasks {
			if task.ID != taskID {
				continue
			}
			switch task.State {
			case "completed":
				return nil
			case "failed", "cancelled":
				return errors.New(task.Error)
			}
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(100 * time.Millisecond):
		}
	}
	return errors.New("PHP restart task timed out")
}

func sortedChangeKeys(changes map[string]string) []string {
	keys := make([]string, 0, len(changes))
	for k := range changes {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
