package packages

import (
	"archive/zip"
	"context"
	"debug/pe"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"strings"
)

const maxArchiveBytes int64 = 2 << 30
const maxExtractedBytes uint64 = 8 << 30
const maxArchiveEntries = 100000

// safeZIPName enforces Windows rules even when tests run on another OS. Colon
// rejection also prevents alternate data streams; device names and trailing
// dots/spaces would otherwise alias different paths on Windows.
func safeZIPName(name string) (string, error) {
	for _, r := range name {
		if r < 32 {
			return "", fmt.Errorf("ZIP 包含控制字符路径 %q", name)
		}
	}
	if name == "" || strings.ContainsRune(name, 0) || strings.Contains(name, `\`) || strings.HasPrefix(name, "/") || strings.Contains(name, ":") {
		return "", fmt.Errorf("ZIP 包含不安全路径 %q", name)
	}
	dir := strings.HasSuffix(name, "/")
	name = strings.TrimSuffix(name, "/")
	for _, part := range strings.Split(name, "/") {
		if part == "" || part == "." || part == ".." || strings.TrimRight(part, " .") != part || strings.ContainsAny(part, `<>"|?*`) {
			return "", fmt.Errorf("ZIP 包含不安全路径 %q", name)
		}
		base := strings.ToUpper(strings.SplitN(part, ".", 2)[0])
		if base == "CON" || base == "PRN" || base == "AUX" || base == "NUL" || len(base) == 4 && (strings.HasPrefix(base, "COM") || strings.HasPrefix(base, "LPT")) && base[3] >= '1' && base[3] <= '9' {
			return "", fmt.Errorf("ZIP 包含 Windows 设备路径 %q", name)
		}
	}
	clean := path.Clean(name)
	if clean == "." || strings.HasPrefix(clean, "../") {
		return "", errors.New("ZIP 路径越界")
	}
	if dir {
		clean += "/"
	}
	return clean, nil
}

func extractZIP(ctx context.Context, archive, dest string) error {
	zr, err := zip.OpenReader(archive)
	if err != nil {
		return fmt.Errorf("读取 ZIP：%w", err)
	}
	defer zr.Close()
	if len(zr.File) > maxArchiveEntries {
		return errors.New("ZIP 文件数量超出限制")
	}
	seen := map[string]bool{}
	var total uint64
	// Validate every entry before writing any payload.
	for _, f := range zr.File {
		name, err := safeZIPName(f.Name)
		if err != nil {
			return err
		}
		if f.Mode()&os.ModeSymlink != 0 || f.Mode()&os.ModeType != 0 && !f.FileInfo().IsDir() {
			return fmt.Errorf("ZIP 不允许链接或特殊文件：%s", name)
		}
		key := strings.ToLower(strings.TrimSuffix(name, "/"))
		if seen[key] {
			return fmt.Errorf("ZIP 包含重复路径：%s", name)
		}
		seen[key] = true
		if f.UncompressedSize64 > maxExtractedBytes-total {
			return errors.New("ZIP 解压大小超出限制")
		}
		total += f.UncompressedSize64
	}
	root, err := os.OpenRoot(dest)
	if err != nil {
		return err
	}
	defer root.Close()
	for _, f := range zr.File {
		if err := ctx.Err(); err != nil {
			return err
		}
		name := filepath.FromSlash(strings.TrimSuffix(f.Name, "/"))
		if f.FileInfo().IsDir() {
			if err := root.MkdirAll(name, 0755); err != nil {
				return err
			}
			continue
		}
		if err := root.MkdirAll(filepath.Dir(name), 0755); err != nil {
			return err
		}
		input, err := f.Open()
		if err != nil {
			return err
		}
		out, err := root.OpenFile(name, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0644)
		if err != nil {
			input.Close()
			return err
		}
		n, copyErr := io.Copy(out, &contextReader{ctx: ctx, r: io.LimitReader(input, int64(f.UncompressedSize64)+1)})
		closeErr := out.Close()
		input.Close()
		if copyErr != nil {
			return copyErr
		}
		if closeErr != nil {
			return closeErr
		}
		if uint64(n) != f.UncompressedSize64 {
			return fmt.Errorf("ZIP 文件大小异常：%s", f.Name)
		}
	}
	return nil
}

type contextReader struct {
	ctx context.Context
	r   io.Reader
}

func (r *contextReader) Read(p []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	return r.r.Read(p)
}

func executableRoot(dest, kind string) (string, error) {
	executable := ""
	switch kind {
	case "php":
		executable = "php-cgi.exe"
	case "mysql":
		executable = "mysqld.exe"
	case "nginx":
		executable = "nginx.exe"
	case "redis":
		executable = "redis-server.exe"
	default:
		return "", errors.New("未知组件类型")
	}
	matches := []string{}
	err := filepath.WalkDir(dest, func(name string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if d.Type()&os.ModeSymlink != 0 {
			return errors.New("安装目录包含符号链接")
		}
		if !d.IsDir() && strings.EqualFold(d.Name(), executable) {
			matches = append(matches, name)
		}
		return nil
	})
	if err != nil {
		return "", err
	}
	if len(matches) != 1 {
		return "", fmt.Errorf("ZIP 必须包含唯一 %s，实际找到 %d 个", executable, len(matches))
	}
	result := filepath.Dir(matches[0])
	if kind == "mysql" {
		if !strings.EqualFold(filepath.Base(result), "bin") {
			return "", errors.New("MySQL ZIP 缺少 bin 目录")
		}
		result = filepath.Dir(result)
	}
	if kind == "redis" {
		if _, err := os.Stat(filepath.Join(result, "cygwin1.dll")); err != nil {
			return "", errors.New("Redis Cygwin ZIP 缺少 cygwin1.dll 运行库")
		}
	}
	return filepath.Rel(dest, result)
}

// noLinks checks every existing ancestor, including Windows junctions reported
// by os.Lstat as symlinks, before any recursive deletion or publication.
func noLinks(target string) error {
	target, err := filepath.Abs(target)
	if err != nil {
		return err
	}
	for p := target; ; p = filepath.Dir(p) {
		info, err := os.Lstat(p)
		if err == nil {
			if info.Mode()&os.ModeSymlink != 0 {
				return fmt.Errorf("路径不允许符号链接或目录联接：%s", p)
			}
		} else if !errors.Is(err, os.ErrNotExist) {
			return err
		}
		if filepath.Dir(p) == p {
			break
		}
	}
	return nil
}

func executableArch(programDir, kind string) (string, error) {
	name := ""
	switch kind {
	case "php":
		name = "php-cgi.exe"
	case "mysql":
		name = filepath.Join("bin", "mysqld.exe")
	case "nginx":
		name = "nginx.exe"
	case "redis":
		name = "redis-server.exe"
	default:
		return "", errors.New("未知程序架构")
	}
	binary, err := pe.Open(filepath.Join(programDir, name))
	if err != nil {
		return "", fmt.Errorf("读取 Windows PE 程序架构：%w", err)
	}
	defer binary.Close()
	arch := ""
	switch binary.Machine {
	case pe.IMAGE_FILE_MACHINE_AMD64:
		arch = "x64"
	case pe.IMAGE_FILE_MACHINE_I386:
		arch = "x86"
	default:
		return "", fmt.Errorf("不支持的 Windows PE 架构：0x%04x", binary.Machine)
	}
	if kind != "nginx" && arch != "x64" {
		return "", errors.New("所选组件必须包含 Windows x64 程序")
	}
	return arch, nil
}
