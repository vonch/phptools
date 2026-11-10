//go:build windows

package prerequisites

import (
	"debug/pe"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"phptools/internal/model"
)

// Read the package's actual imports, including adjacent DLLs, instead of
// guessing its compiler from its PHP/MySQL version. os.Root confines all reads.
func packageRequirements(pkg model.Package) (map[string][]string, error) {
	needed := map[string][]string{}
	root, err := os.OpenRoot(pkg.Path)
	if err != nil {
		return needed, err
	}
	defer root.Close()
	entries := map[string][]string{
		"php":   {"php.exe", "php-cgi.exe"},
		"mysql": {"bin/mysqld.exe", "bin/mysql.exe", "bin/mysqladmin.exe", "bin/mysqldump.exe"},
		"nginx": {"nginx.exe"}, "redis": {"redis-server.exe"},
	}[pkg.Kind]
	if len(entries) == 0 {
		return needed, errors.New("未知组件类型")
	}
	if pkg.Kind == "php" {
		for _, name := range []string{"curl", "fileinfo", "mbstring", "mysqli", "openssl", "pdo_mysql", "zip"} {
			name = "ext/php_" + name + ".dll"
			if _, err := root.Stat(name); err == nil {
				entries = append(entries, name)
			}
		}
	}
	seen := map[string]bool{}
	var read func(string, string) error
	read = func(name, bin string) error {
		key := strings.ToLower(name)
		if seen[key] {
			return nil
		}
		if len(seen) >= 128 {
			return errors.New("组件依赖层级过多")
		}
		seen[key] = true
		f, err := root.Open(name)
		if err != nil {
			return fmt.Errorf("读取 %s：%w", name, err)
		}
		defer f.Close()
		imports, err := binaryImports(f)
		if err != nil {
			return fmt.Errorf("分析 %s：%w", name, err)
		}
		for _, dll := range imports {
			if !validDLLName(dll) {
				return fmt.Errorf("无效依赖名称：%q", dll)
			}
			local := filepath.Join(bin, dll)
			if _, err := root.Stat(local); err == nil {
				if err := read(local, bin); err != nil {
					return err
				}
			} else if !os.IsNotExist(err) {
				return err
			} else if id := runtimeFamily(dll); id != "" {
				needed[id] = append(needed[id], strings.ToLower(dll))
			}
		}
		return nil
	}
	var problems []error
	for _, name := range entries {
		bin := filepath.Dir(name)
		if pkg.Kind == "php" {
			bin = "."
		}
		if err := read(name, bin); err != nil {
			problems = append(problems, err)
		}
	}
	for id, dlls := range needed {
		slices.Sort(dlls)
		needed[id] = slices.Compact(dlls)
	}
	return needed, errors.Join(problems...)
}

func validDLLName(name string) bool {
	return name != "" && !strings.ContainsAny(name, `/\:`) && strings.EqualFold(filepath.Ext(name), ".dll")
}

func binaryImports(file *os.File) (imports []string, err error) {
	// debug/pe is a metadata parser, not a validator for hostile binaries.
	// Corrupt downloaded/user-edited files must become a diagnostic, not a crash.
	defer func() {
		if recover() != nil {
			imports = nil
			err = errors.New("PE 导入表损坏")
		}
	}()
	f, err := pe.NewFile(file)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	if f.Machine != pe.IMAGE_FILE_MACHINE_AMD64 {
		return nil, errors.New("组件不是 x64 程序")
	}
	symbols, err := f.ImportedSymbols()
	if err != nil {
		return nil, err
	}
	for _, symbol := range symbols {
		_, dll, found := strings.Cut(symbol, ":")
		if found {
			imports = append(imports, strings.ToLower(dll))
		}
	}
	slices.Sort(imports)
	return slices.Compact(imports), nil
}
