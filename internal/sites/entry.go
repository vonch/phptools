package sites

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
)

// RelativeEntry is shared by the native picker and site validation. Both the
// selected spelling and its resolved target must stay inside the project.
func RelativeEntry(projectRoot, selected string) (string, error) {
	if !filepath.IsAbs(projectRoot) || strings.ContainsAny(projectRoot, "\r\n\"\x00") {
		return "", errors.New("请先选择有效的项目绝对目录")
	}
	if !filepath.IsAbs(selected) || strings.ContainsAny(selected, "\r\n\"\x00") {
		return "", errors.New("请选择有效的入口绝对目录")
	}
	root := filepath.Clean(projectRoot)
	target := filepath.Clean(selected)
	for _, path := range []string{root, target} {
		info, err := os.Stat(path)
		if err != nil || !info.IsDir() {
			return "", errors.New("项目目录或入口目录不存在")
		}
	}
	rel, err := filepath.Rel(root, target)
	if err != nil || outsideProject(rel) {
		return "", errors.New("入口目录必须是项目目录本身或其子目录")
	}
	realRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		return "", err
	}
	realTarget, err := filepath.EvalSymlinks(target)
	if err != nil {
		return "", err
	}
	realRel, err := filepath.Rel(realRoot, realTarget)
	if err != nil || outsideProject(realRel) {
		return "", errors.New("入口目录链接到了项目目录之外，请选择项目内的真实目录")
	}
	if rel == "." {
		return "", nil
	}
	return filepath.ToSlash(rel), nil
}

func outsideProject(rel string) bool {
	return rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || filepath.IsAbs(rel)
}
