//go:build windows

package prerequisites

import (
	"errors"

	"github.com/wailsapp/wails/v3/pkg/application"
	"github.com/wailsapp/wails/v3/pkg/w32"
)

// EnsureWebView runs before Wails or the environment store is initialised.
// Native dialogs remain usable when no web renderer is installed.
func EnsureWebView() bool {
	return ensureWebView(func() error {
		version, err := application.WebView2RuntimeVersion()
		if err == nil && version == "" {
			err = errors.New("运行时版本为空")
		}
		return err
	}, func(message string, retry bool) bool {
		flags, accepted := uint(w32.MB_YESNO|w32.MB_ICONWARNING), w32.IDYES
		if retry {
			flags, accepted = uint(w32.MB_RETRYCANCEL|w32.MB_ICONINFORMATION), w32.IDRETRY
		}
		return w32.MessageBox(0, message, "PHPTools · 运行依赖", flags) == accepted
	}, func(url string) error {
		return w32.ShellExecute(0, "open", url, "", "", w32.SW_SHOWNORMAL)
	})
}

func ensureWebView(check func() error, prompt func(string, bool) bool, open func(string) error) bool {
	if check() == nil {
		return true
	}
	url, _ := InstallURL(WebViewID)
	if !prompt("PHPTools 需要 Microsoft Edge WebView2 Runtime 才能显示界面。\n\n未检测到可用运行时。是否打开微软官方下载页面？\n\n选择 Evergreen Bootstrapper，或 x64 Standalone Installer，安装完成后回到此窗口。\n选择“否”将退出 PHPTools。", false) {
		return false
	}
	openError := open(url)
	lastError := ""
	for {
		message := "请完成 WebView2 安装，然后点击“重试”继续启动 PHPTools。\n若安装程序要求重启 Windows，请先取消并重启。\n\n官方下载页面：\n" + url
		if openError != nil {
			message = "无法打开浏览器：" + openError.Error() + "\n请手动访问下面的微软页面。\n\n" + message
		}
		if lastError != "" {
			message = lastError + "\n\n" + message
		}
		if !prompt(message, true) {
			return false
		}
		if check() == nil {
			return true
		}
		lastError = "仍未检测到可用的 WebView2。请确认已完成安装或修复。"
	}
}
