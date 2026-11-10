//go:build windows

// Read-only dependency audit. It neither opens a browser nor installs runtimes.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"

	"phptools/internal/model"
	"phptools/internal/prerequisites"
)

func main() {
	path := flag.String("package", "", "可选：只读检查一个已解压的组件程序根目录（MySQL 根目录包含 bin）")
	kind := flag.String("kind", "", "组件类型：php / mysql / nginx / redis")
	flag.Parse()
	var pkgs []model.Package
	if *path != "" {
		pkgs = append(pkgs, model.Package{ID: filepath.Base(*path), Kind: *kind, Path: *path})
	}
	encoder := json.NewEncoder(os.Stdout)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(prerequisites.Inspect(pkgs)); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
