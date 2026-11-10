//go:build windows

// Command phpstudy-php-stage prepares independent PHP runtimes for migration.
// It never starts or stops a service or copies binaries from phpStudy.
package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"phptools/internal/model"
	"phptools/internal/packages"
	"phptools/internal/services"
	"phptools/internal/store"
)

type phpMapping struct {
	SourceBranch string `json:"sourceBranch"`
	TargetID     string `json:"targetId"`
	Version      string `json:"version"`
	Port         int    `json:"port"`
	Executable   string `json:"exe"`
	ConfigPath   string `json:"configPath"`
	Reused       bool   `json:"reused"`
}

func main() {
	root := flag.String("root", `E:\PHPTools`, "owned PHPTools destination")
	output := flag.String("output", `build\migration\php-map.json`, "non-sensitive output map")
	extensionsOnly := flag.Bool("extensions-only", false, "match source loaded PHP extensions using existing PHP map")
	inventory := flag.String("source-inventory", `build\migration\inventory\php-source.json`, "read-only PHP source inventory")
	flag.Parse()
	var err error
	if *extensionsOnly {
		err = prepareExtensions(*root, *output, *inventory)
	} else {
		err = run(*root, *output)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func ensureManagerStopped() error {
	command := exec.Command("powershell.exe", "-NoProfile", "-NonInteractive", "-Command", "$ErrorActionPreference='Stop'; $p=Get-Process -Name PHPTools -ErrorAction SilentlyContinue; if ($p) { exit 10 }; exit 0")
	if err := command.Run(); err != nil {
		return fmt.Errorf("cannot confirm PHPTools is closed; no processes were changed: %w", err)
	}
	return nil
}

func backupMetadata(root string) (string, error) {
	path := filepath.Join(root, "metadata.db")
	if _, err := os.Stat(path); err != nil {
		return "", err
	}
	backupDir, err := os.MkdirTemp(filepath.Join(root, "backups"), "phpstudy-migration-php-")
	if err != nil {
		return "", err
	}
	backup := filepath.Join(backupDir, "metadata.db")
	db, err := sql.Open("sqlite", "file:"+filepath.ToSlash(path)+"?mode=ro")
	if err != nil {
		return "", err
	}
	defer db.Close()
	// VACUUM INTO copies a coherent SQLite snapshot, including any WAL content.
	if _, err := db.Exec("VACUUM INTO '" + strings.ReplaceAll(filepath.ToSlash(backup), "'", "''") + "'"); err != nil {
		return "", err
	}
	return backup, nil
}

func branch(version string) string {
	parts := strings.Split(version, ".")
	if len(parts) < 2 {
		return ""
	}
	return strings.Join(parts[:2], ".")
}

func checkPort(port int, instances []model.Instance) error {
	for _, instance := range instances {
		if instance.Port == port {
			return fmt.Errorf("port %d belongs to existing instance %s", port, instance.ID)
		}
	}
	listener, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", port))
	if err != nil {
		return fmt.Errorf("migration port %d unavailable: %w", port, err)
	}
	return listener.Close()
}

func run(root, output string) error {
	if filepath.Clean(root) != `E:\PHPTools` {
		return errors.New("unexpected destination; this migration is limited to E:\\PHPTools")
	}
	if err := ensureManagerStopped(); err != nil {
		return err
	}
	backup, err := backupMetadata(root)
	if err != nil {
		return fmt.Errorf("metadata backup: %w", err)
	}
	fmt.Printf("Metadata backup: %s\n", backup)
	db, err := store.Open(root)
	if err != nil {
		return err
	}
	defer db.Close()
	installed, err := store.List[model.Package](db, "package")
	if err != nil {
		return err
	}
	instances, err := store.List[model.Instance](db, "instance")
	if err != nil {
		return err
	}
	packageByID := make(map[string]model.Package)
	for _, pkg := range installed {
		packageByID[pkg.ID] = pkg
	}
	catalog := make(map[string]model.Package)
	for _, pkg := range packages.SeedCatalog() {
		if pkg.Kind == "php" {
			catalog[pkg.Version] = pkg
		}
	}
	manager := services.New(root, func(i model.Instance) error { return db.Put("instance", i.ID, i) }, nil)
	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Minute)
	defer cancel()
	mappings := make(map[string]phpMapping)
	for _, need := range []struct {
		version string
		port    int
	}{
		{"7.4.33", 9100}, {"8.0.30", 9101}, {"5.6.40", 9102},
		{"7.2.34", 9103}, {"7.3.33", 9104}, {"8.2.34", 9105},
	} {
		phpBranch := branch(need.version)
		var existing []model.Instance
		for _, instance := range instances {
			pkg, ok := packageByID[instance.PackageID]
			if ok && instance.Kind == "php" && branch(pkg.Version) == phpBranch {
				existing = append(existing, instance)
			}
		}
		if len(existing) > 1 {
			return fmt.Errorf("multiple existing PHP %s instances; select explicitly", phpBranch)
		}
		if len(existing) == 1 {
			i := existing[0]
			pkg := packageByID[i.PackageID]
			if i.State != "stopped" || i.PID != 0 {
				return fmt.Errorf("PHP %s is not stopped; no service changed", phpBranch)
			}
			for _, path := range []string{i.Executable, i.ConfigPath} {
				if _, err := os.Stat(path); err != nil {
					return err
				}
			}
			mappings[phpBranch] = phpMapping{phpBranch, i.ID, pkg.Version, i.Port, i.Executable, i.ConfigPath, true}
			fmt.Printf("Reused PHP %s port %d\n", pkg.Version, i.Port)
			continue
		}
		if err := checkPort(need.port, instances); err != nil {
			return err
		}
		pkg, ok := catalog[need.version]
		if !ok {
			return fmt.Errorf("PHP %s unavailable in verified catalog", need.version)
		}
		lastMessage := ""
		pkg, err = packages.Install(ctx, root, pkg, func(done, total int64, message string) {
			if message != lastMessage {
				fmt.Printf("PHP %s: %s\n", need.version, message)
				lastMessage = message
			}
		})
		if err != nil {
			return fmt.Errorf("install PHP %s: %w", need.version, err)
		}
		if err := ensureManagerStopped(); err != nil {
			return err
		}
		if err := db.Put("package", pkg.ID, pkg); err != nil {
			return err
		}
		packageByID[pkg.ID] = pkg
		if err := checkPort(need.port, instances); err != nil {
			return err
		}
		i, err := manager.Create(ctx, pkg, model.InstanceInput{Name: "PHP " + need.version + " (phpStudy migration)", PackageID: pkg.ID, Port: need.port})
		if err != nil {
			return fmt.Errorf("create PHP %s: %w", need.version, err)
		}
		if err := db.Put("instance", i.ID, i); err != nil {
			return err
		}
		instances = append(instances, i)
		mappings[phpBranch] = phpMapping{phpBranch, i.ID, pkg.Version, i.Port, i.Executable, i.ConfigPath, false}
		fmt.Printf("Prepared stopped PHP %s port %d\n", need.version, i.Port)
	}
	if err := os.MkdirAll(filepath.Dir(output), 0700); err != nil {
		return err
	}
	raw, err := json.MarshalIndent(mappings, "", "  ")
	if err != nil {
		return err
	}
	if err := os.WriteFile(output, append(raw, '\n'), 0600); err != nil {
		return err
	}
	fmt.Printf("Prepared %d PHP branches; map=%s\n", len(mappings), output)
	return nil
}
