//go:build windows

package application

import "github.com/wailsapp/wails/v3/internal/webview2/webviewloader"

// WebView2RuntimeVersion uses the same loader discovery as WebviewWindow,
// without creating a window. Applications can offer native installation help
// before attempting to render their web UI.
func WebView2RuntimeVersion() (string, error) {
	return webviewloader.GetAvailableCoreWebView2BrowserVersionString("")
}
