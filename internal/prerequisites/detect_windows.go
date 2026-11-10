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
	"unsafe"

	"github.com/wailsapp/wails/v3/pkg/application"
	"golang.org/x/sys/windows"
	"phptools/internal/model"
)

type probe func(definition) (state, version, message string)

func Inspect(packages []model.Package) model.DependencyReport {
	return inspect(packages, probeSystem, packageRequirements)
}

func inspect(packages []model.Package, check probe, requirements func(model.Package) (map[string][]string, error)) model.DependencyReport {
	report := model.DependencyReport{CheckedAt: model.Now(), Items: []model.RuntimeDependency{}, Warnings: []string{}}
	needed := map[string][]string{WebViewID: {"PHPTools 界面"}}
	dlls := map[string][]string{}
	for _, pkg := range packages {
		if pkg.Path == "" {
			continue
		}
		ids, err := requirements(pkg)
		if err != nil {
			report.Warnings = append(report.Warnings, pkg.ID+"：无法完整读取运行依赖，"+err.Error())
		}
		for id, files := range ids {
			needed[id] = append(needed[id], pkg.ID)
			dlls[id] = append(dlls[id], files...)
		}
	}
	for _, d := range definitions {
		d.dlls = append(append([]string{}, d.dlls...), dlls[d.id]...)
		slices.Sort(d.dlls)
		d.dlls = slices.Compact(d.dlls)
		state, version, message := check(d)
		by := needed[d.id]
		slices.Sort(by)
		if by == nil {
			by = []string{}
		}
		report.Items = append(report.Items, model.RuntimeDependency{ID: d.id, Name: d.name,
			State: state, Version: version, Message: message, RequiredBy: by, Guide: d.guide})
	}
	return report
}

func probeSystem(d definition) (state, version, message string) {
	if d.id == WebViewID {
		v, err := application.WebView2RuntimeVersion()
		if err != nil || v == "" {
			return "missing", "", "未找到可供 PHPTools 使用的 WebView2；请安装或修复运行时。"
		}
		return "ready", v, "已通过桌面框架的运行时检测。"
	}
	dir, err := windows.GetSystemDirectory()
	if err != nil {
		return "error", "", "无法读取系统目录：" + err.Error()
	}
	var missing []string
	for _, dll := range d.dlls {
		path := filepath.Join(dir, dll)
		v, err := inspectDLL(path)
		if os.IsNotExist(err) {
			missing = append(missing, dll)
		} else if err != nil {
			return "error", "", dll + " 检测失败：" + err.Error()
		} else if version == "" {
			version = v
		}
	}
	if len(missing) != 0 {
		return "missing", version, "系统缺少：" + strings.Join(missing, "、")
	}
	return "ready", version, "已检测到 x64 系统运行库文件；组件若提示版本过低，请通过官方入口更新或修复。"
}

// Inspect file metadata only. Never load component code into the manager.
func inspectDLL(path string) (string, error) {
	f, err := pe.Open(path)
	if err != nil {
		return "", err
	}
	machine := f.Machine
	f.Close()
	if machine != pe.IMAGE_FILE_MACHINE_AMD64 {
		return "", errors.New("不是 x64 运行库")
	}
	size, err := windows.GetFileVersionInfoSize(path, nil)
	if err != nil {
		return "", err
	}
	if size == 0 || size > 4<<20 {
		return "", errors.New("版本信息大小异常")
	}
	data := make([]byte, size)
	if err = windows.GetFileVersionInfo(path, 0, size, unsafe.Pointer(&data[0])); err != nil {
		return "", err
	}
	var info *windows.VS_FIXEDFILEINFO
	var length uint32
	if err = windows.VerQueryValue(unsafe.Pointer(&data[0]), `\`, unsafe.Pointer(&info), &length); err != nil {
		return "", err
	}
	if info == nil || uintptr(length) < unsafe.Sizeof(*info) || info.Signature != 0xFEEF04BD {
		return "", errors.New("版本信息无效")
	}
	return fmt.Sprintf("%d.%d.%d.%d", info.FileVersionMS>>16, info.FileVersionMS&0xffff, info.FileVersionLS>>16, info.FileVersionLS&0xffff), nil
}
