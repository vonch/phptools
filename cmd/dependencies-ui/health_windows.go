//go:build windows

package main

import (
	"encoding/json"
	"os"
	"path/filepath"

	"phptools/internal/model"
	"phptools/internal/store"
)

// Seed only the acceptance-owned runtime. Its component executable is absent,
// so full checks cannot execute a service. The profile stays uninitialised until
// the UI selects this root; this also avoids startup repairing the missing dirs.
func prepareHealthFixtures(root string) error {
	runtimeRoot := filepath.Join(root, "独立 runtime")
	db, err := store.Open(runtimeRoot)
	if err != nil {
		return err
	}
	defer db.Close()
	pkg := model.Package{
		ID: "qa-missing-nginx", Kind: "nginx", Version: "1.28.0", Arch: "x64",
		Path: filepath.Join(runtimeRoot, "packages", "qa-missing-nginx"), InstalledAt: model.Now(),
	}
	instanceDir := filepath.Join(runtimeRoot, "instances", "qa-health-fixture")
	instance := model.Instance{
		ID: "qa-health-fixture", Name: "验收 · 缺失组件", Kind: pkg.Kind, PackageID: pkg.ID,
		Port: 19991, State: "stopped", CreatedAt: model.Now(),
		Executable: filepath.Join(pkg.Path, "nginx.exe"),
		ConfigPath: filepath.Join(instanceDir, "conf", "nginx.conf"),
		DataDir:    filepath.Join(instanceDir, "data"), LogPath: filepath.Join(instanceDir, "logs", "error.log"),
	}
	for _, directory := range []string{pkg.Path, filepath.Dir(instance.ConfigPath), instance.DataDir} {
		if err := os.MkdirAll(directory, 0700); err != nil {
			return err
		}
	}
	if err := os.WriteFile(instance.ConfigPath, []byte("# UI acceptance configuration; never execute\n"), 0600); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(instance.DataDir, "preserve.txt"), []byte("UI acceptance data must remain unchanged\n"), 0600); err != nil {
		return err
	}
	if err := db.Put("package", pkg.ID, pkg); err != nil {
		return err
	}
	if err := db.Put("instance", instance.ID, instance); err != nil {
		return err
	}
	data, err := json.Marshal(instance)
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(root, "ui-health-fixture.json"), data, 0600)
}
