//go:build windows

// Command phpstudy-finalize performs the destination side of the approved switch.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/zalando/go-keyring"
	"phptools/internal/desktop"
	"phptools/internal/model"
	"phptools/internal/services"
	"phptools/internal/sites"
	"phptools/internal/store"
)

func main() {
	if handled, err := sites.RunHostsHelper(os.Args[1:]); handled {
		if err != nil {
			os.Exit(1)
		}
		return
	}
	mysqlID := flag.String("mysql-id", "", "verified imported MySQL instance")
	verify := flag.Bool("verify", false, "check switched services without starting or stopping them")
	flag.Parse()
	var err error
	if *verify {
		err = verifySwitch(*mysqlID)
	} else {
		err = run(*mysqlID)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(mysqlID string) error {
	if mysqlID == "" {
		return errors.New("select the imported database explicitly")
	}
	const root = `E:\PHPTools`
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
	if len(allSites) != 55 {
		return fmt.Errorf("expected 53 imported and 2 existing sites, got %d", len(allSites))
	}
	byPackage := map[string]model.Package{}
	for _, pkg := range pkgs {
		byPackage[pkg.ID] = pkg
	}
	m := services.New(root, func(i model.Instance) error { return db.Put("instance", i.ID, i) }, func(id string) (string, error) {
		return keyring.Get(desktop.CredentialService(root), id)
	})
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	var mysql *model.Instance
	for n := range instances {
		if instances[n].ID == mysqlID && instances[n].Kind == "mysql" {
			mysql = &instances[n]
		}
	}
	if mysql == nil || (mysql.Port != 13306 && mysql.Port != 3306) {
		return errors.New("the specified instance is not the staged migration database")
	}
	if mysql.Port == 13306 {
		*mysql, err = m.Stop(ctx, *mysql)
		if err != nil {
			return err
		}
		old, err := os.ReadFile(mysql.ConfigPath)
		if err != nil {
			return err
		}
		if strings.Count(string(old), "port=13306") != 1 {
			return errors.New("staged database configuration has an unexpected port declaration")
		}
		backup := filepath.Join(root, "backups", "mysql-before-switch-"+mysql.ID+".ini")
		if err := writeNew(backup, old); err != nil {
			return err
		}
		updated := strings.Replace(string(old), "port=13306", "port=3306", 1)
		if err := os.WriteFile(mysql.ConfigPath, []byte(updated), 0600); err != nil {
			return err
		}
		mysql.Port = 3306
		if err := db.Put("instance", mysql.ID, *mysql); err != nil {
			_ = os.WriteFile(mysql.ConfigPath, old, 0600)
			return err
		}
	}
	*mysql, err = m.Start(ctx, byPackage[mysql.PackageID], *mysql)
	if err != nil {
		return fmt.Errorf("start imported MySQL: %w", err)
	}
	fmt.Println("Imported MySQL is ready on 3306")
	for n := range instances {
		i := &instances[n]
		if i.Kind != "php" {
			continue
		}
		*i, err = m.Start(ctx, byPackage[i.PackageID], *i)
		if err != nil {
			return fmt.Errorf("start %s: %w", i.Name, err)
		}
		fmt.Printf("Ready %s on %d\n", i.Name, i.Port)
	}
	if err := sites.SyncHosts(root, sites.Domains(allSites)); err != nil {
		return err
	}
	for n := range instances {
		i := &instances[n]
		if i.Kind != "nginx" {
			continue
		}
		*i, err = m.Start(ctx, byPackage[i.PackageID], *i)
		if err != nil {
			return fmt.Errorf("start destination Nginx: %w", err)
		}
		fmt.Printf("Ready Nginx and %d site configurations\n", len(allSites))
	}
	data, err := json.MarshalIndent(instances, "", "  ")
	if err != nil {
		return err
	}
	return writeNew(filepath.Join(root, "backups", "phpstudy-switch-running-instances.json"), data)
}

func writeNew(path string, data []byte) error {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	_, writeErr := f.Write(data)
	return errors.Join(writeErr, f.Close())
}
