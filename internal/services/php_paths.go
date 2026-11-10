package services

import (
	"errors"
	"phptools/internal/model"
	"strings"
)

// PHP 7.1 introduced UTF-8 Windows paths. Earlier supported branches cannot
// reliably consume the UTF-8 SCRIPT_FILENAME sent by Nginx. Do not silently
// create short-path aliases or rename the user's project.
// https://www.php.net/manual/en/migration71.windows-support.php
func ValidatePHPPath(pkg model.Package, path string) error {
	if pkg.Kind != "php" {
		return nil
	}
	legacy := strings.HasPrefix(pkg.Version, "5.6.") || strings.HasPrefix(pkg.Version, "7.0.")
	if legacy && strings.IndexFunc(path, func(r rune) bool { return r > 127 }) >= 0 {
		return errors.New("PHP 5.6/7.0 的 Windows 版本存在 UTF-8 路径限制，请使用英文目录及文件名，或选择 PHP 7.1 及以上版本")
	}
	return nil
}
