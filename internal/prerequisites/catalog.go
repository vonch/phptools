package prerequisites

import (
	"errors"
	"strings"
)

const WebViewID = "webview2"

type definition struct {
	id, name, url, download, guide string
	dlls                           []string
}

// Official landing pages, checked on 2026-10-20. The user runs Microsoft's
// installer; PHPTools never downloads DLLs from third parties or elevates itself.
var definitions = []definition{
	{id: WebViewID, name: "Microsoft Edge WebView2", url: "https://developer.microsoft.com/microsoft-edge/webview2/",
		guide: "在微软页面找到 Evergreen Runtime，选择 Bootstrapper（联网安装）或 x64 Standalone Installer（离线安装包）。运行安装程序后回到这里重新检测；如果要求重启 Windows，请先重启。"},
	{id: "vc14", name: "Visual C++ v14 x64（2015 及后续工具集）", url: "https://learn.microsoft.com/cpp/windows/latest-supported-vc-redist",
		download: "https://aka.ms/vs/17/release/vc_redist.x64.exe",
		dlls:     []string{"vcruntime140.dll", "msvcp140.dll"},
		guide:    "在微软页面选择最新 v14 的 X64 安装包 vc_redist.x64.exe，运行后选择安装或修复。按安装程序要求完成重启，再点击重新检测；已经安装旧版也可用此入口更新。"},
	{id: "vc11", name: "Visual C++ 2012 x64（VC11）", url: "https://www.microsoft.com/en-us/download/details.aspx?id=30679",
		dlls:  []string{"msvcr110.dll", "msvcp110.dll"},
		guide: "点击微软页面的 Download，选择 vcredist_x64.exe，运行并按提示安装或修复。此旧版与 v14 并存，不能用 v14 替代；通常供 PHP 5.6 使用。安装后重新检测。"},
	{id: "vc12", name: "Visual C++ 2013 x64", url: "https://learn.microsoft.com/cpp/windows/latest-supported-vc-redist#visual-studio-2013-vc-120-no-longer-supported",
		dlls:  []string{"msvcr120.dll", "msvcp120.dll"},
		guide: "在微软页面的 Visual Studio 2013 区域选择 X64 的 vcredist_x64.exe，运行并按提示安装或修复。它与 2012、v14 并存；仅关联组件需要时安装。安装后重新检测。"},
}

func InstallURL(id string) (string, error) {
	for _, d := range definitions {
		if d.id == id {
			return d.url, nil
		}
	}
	return "", errors.New("未知运行依赖")
}

// DownloadURL is the official installer for a known runtime. The browser
// downloads it; PHPTools does not fetch or launch the file itself.
func DownloadURL(id string) (string, error) {
	for _, d := range definitions {
		if d.id == id && d.download != "" {
			return d.download, nil
		}
	}
	return "", errors.New("未知运行依赖")
}

func runtimeFamily(name string) string {
	name = strings.ToLower(name)
	for _, d := range definitions {
		for _, dll := range d.dlls {
			if name == dll {
				return d.id
			}
		}
	}
	if name == "vcruntime140_1.dll" || name == "concrt140.dll" || strings.HasPrefix(name, "msvcp140_") && strings.HasSuffix(name, ".dll") {
		return "vc14"
	}
	return ""
}
