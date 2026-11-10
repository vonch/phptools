//go:build windows

package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"phptools/internal/model"
)

func sourcePackages(root string) ([]model.Package, error) {
	marker, err := os.ReadFile(filepath.Join(root, ".phptools-runtime"))
	if err != nil || string(marker) != "PHPTools runtime v1\n" {
		return nil, errors.New("source root is not a PHPTools managed runtime")
	}
	entries, err := os.ReadDir(filepath.Join(root, "packages"))
	if err != nil {
		return nil, err
	}
	found := map[string][]model.Package{}
	for _, entry := range entries {
		if !entry.IsDir() || entry.Type()&os.ModeSymlink != 0 {
			continue
		}
		b, err := os.ReadFile(filepath.Join(root, "packages", entry.Name(), ".phptools-package.json"))
		if err != nil {
			continue
		}
		var pkg model.Package
		if json.Unmarshal(b, &pkg) != nil {
			continue
		}
		if !pkg.Verified || !within(filepath.Join(root, "packages", entry.Name()), pkg.Path) {
			continue
		}
		if pkg.Kind == "php" && !strings.HasPrefix(pkg.Version, "8.0.") {
			continue
		}
		if pkg.Kind == "nginx" && !strings.HasSuffix(pkg.ID, "-windows") {
			continue
		}
		if pkg.Kind != "php" && pkg.Kind != "nginx" {
			continue
		}
		found[pkg.Kind] = append(found[pkg.Kind], pkg)
	}
	result := []model.Package{}
	for _, kind := range []string{"php", "nginx"} {
		candidates := found[kind]
		if len(candidates) == 0 {
			return nil, fmt.Errorf("no verified installed %s package available in source root", kind)
		}
		sort.Slice(candidates, func(a, b int) bool {
			return versionNumber(candidates[a].Version) > versionNumber(candidates[b].Version)
		})
		result = append(result, candidates[0])
	}
	return result, nil
}

func versionNumber(v string) int {
	var a, b, c int
	_, _ = fmt.Sscanf(v, "%d.%d.%d", &a, &b, &c)
	return a*1000000 + b*1000 + c
}

func copyPackage(root string, pkg model.Package) (model.Package, int, error) {
	if strings.ContainsAny(pkg.ID, `/\:`) || pkg.ID == "" || pkg.ID == "." || pkg.ID == ".." {
		return pkg, 0, errors.New("unsafe source package ID")
	}
	destination := filepath.Join(root, "packages", pkg.ID)
	if !within(filepath.Join(root, "packages"), destination) {
		return pkg, 0, errors.New("package copy target escaped managed test root")
	}
	manifest := map[string]string{}
	count := 0
	err := filepath.WalkDir(pkg.Path, func(source string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.Type()&os.ModeSymlink != 0 {
			return errors.New("source package contains a link")
		}
		relative, err := filepath.Rel(pkg.Path, source)
		if err != nil {
			return err
		}
		target := filepath.Join(destination, relative)
		if !within(destination, target) {
			return errors.New("package copy path escaped destination")
		}
		if entry.Name() == ".phptools-package.json" {
			return nil
		}
		if info, err := os.Lstat(target); err == nil {
			if info.Mode()&os.ModeSymlink != 0 {
				return errors.New("package destination contains a link")
			}
			if info.IsDir() != entry.IsDir() {
				return errors.New("package destination file type mismatch")
			}
		} else if !os.IsNotExist(err) {
			return err
		}
		if entry.IsDir() {
			return os.MkdirAll(target, 0700)
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() {
			return errors.New("non-regular source package file")
		}
		input, err := os.Open(source)
		if err != nil {
			return err
		}
		defer input.Close()
		sourceHash := sha256.New()
		if _, err = io.Copy(sourceHash, input); err != nil {
			return err
		}
		digest := sourceHash.Sum(nil)
		manifest[filepath.ToSlash(relative)] = hex.EncodeToString(digest)
		if output, err := os.Open(target); err == nil {
			targetHash := sha256.New()
			_, hashErr := io.Copy(targetHash, output)
			closeErr := output.Close()
			if hashErr != nil {
				return hashErr
			}
			if closeErr != nil {
				return closeErr
			}
			if !bytes.Equal(digest, targetHash.Sum(nil)) {
				return fmt.Errorf("existing package file differs; refusing overwrite: %s", target)
			}
			count++
			return nil
		} else if !os.IsNotExist(err) {
			return err
		}
		if _, err = input.Seek(0, io.SeekStart); err != nil {
			return err
		}
		output, err := os.OpenFile(target, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
		if err != nil {
			return err
		}
		_, copyErr := io.Copy(output, input)
		closeErr := output.Close()
		if copyErr != nil {
			return copyErr
		}
		if closeErr != nil {
			return closeErr
		}
		count++
		return nil
	})
	if err != nil {
		return pkg, count, err
	}
	pkg.Path = destination
	pkg.InstalledAt = model.Now()
	b, err := json.MarshalIndent(struct {
		Package model.Package     `json:"package"`
		Files   map[string]string `json:"files"`
	}{pkg, manifest}, "", "  ")
	if err != nil {
		return pkg, count, err
	}
	if err = os.WriteFile(filepath.Join(destination, ".rewritecheck-package.json"), b, 0600); err != nil {
		return pkg, count, err
	}
	return pkg, count, nil
}
