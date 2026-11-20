package main

import (
	"embed"
	"flag"
	"log"
	"os"
	"path/filepath"
	"sync/atomic"

	"github.com/wailsapp/wails/v3/pkg/application"
	"github.com/wailsapp/wails/v3/pkg/events"
	"github.com/wailsapp/wails/v3/pkg/w32"
	"phptools/internal/dbtools"
	"phptools/internal/desktop"
	"phptools/internal/icon"
	"phptools/internal/prerequisites"
	"phptools/internal/sites"
)

//go:embed all:frontend/dist
var assets embed.FS

// Set only by the UI acceptance build; release builds expose no debugging port.
var uiDebugPort string

// Acceptance builds use a separate instance group so they cannot interrupt the
// user's running manager or connect to its active environment.
var instanceID = "local.phptools.desktop"

func main() {
	if handled, err := sites.RunHostsHelper(os.Args[1:]); handled {
		if err != nil {
			os.Exit(1)
		}
		return
	}
	var native *application.App
	var window *application.WebviewWindow
	var activity *windowActivityController
	var allowed atomic.Bool
	profile := flag.String("config-dir", "", "独立的 PHPTools 配置目录（默认使用当前用户配置目录）")
	flag.Parse()
	if !prerequisites.EnsureWebView() {
		return
	}
	emitStateChanged := func() {
		if native != nil && activity != nil && activity.IsActive() {
			native.Event.Emit("state:changed")
		}
	}
	bridge, err := desktop.New(*profile, desktop.Hooks{
		Notify:    emitStateChanged,
		StateRead: suspendAcceptanceStateRead,
		PickDirectory: func(title, initialDirectory string) (string, error) {
			dialog := native.Dialog.OpenFile().SetTitle(title).CanChooseFiles(false).CanChooseDirectories(true).CanCreateDirectories(true)
			if initialDirectory != "" {
				dialog.SetDirectory(initialDirectory)
			}
			return dialog.PromptForSingleSelection()
		},
		OpenDirectory: func(path string) error {
			return application.InvokeSyncWithError(func() error {
				return w32.ShellExecute(0, "open", path, "", "", w32.SW_SHOWNORMAL)
			})
		},
		PickSQLImport: func() (string, error) {
			return native.Dialog.OpenFile().SetTitle("选择要导入的 SQL 文件").AddFilter("SQL 文件", "*.sql").CanChooseFiles(true).CanChooseDirectories(false).PromptForSingleSelection()
		},
		PickSQLExport: func(filename string) (string, error) {
			dialog := native.Dialog.SaveFile()
			dialog.SetOptions(&application.SaveFileDialogOptions{Title: "导出数据库为 SQL 文件", Filename: filename, CanCreateDirectories: true})
			return dialog.AddFilter("SQL 文件", "*.sql").PromptForSingleSelection()
		},
		PickDatabaseClient: func() (string, error) {
			return native.Dialog.OpenFile().SetTitle("选择数据库管理客户端").AddFilter("Windows 程序", "*.exe").
				CanChooseFiles(true).CanChooseDirectories(false).AttachToWindow(window).PromptForSingleSelection()
		},
		OpenDatabaseClient: dbtools.LaunchClient,
		OpenURL:            func(url string) error { return native.Browser.OpenURL(url) },
		Exit:               func() { allowed.Store(true); native.Quit() },
	})
	if err != nil {
		log.Fatal(err)
	}
	icon := appIcon()
	options := application.Options{
		Name: "PHPTools", Description: "Windows 本地 PHP 环境管理器", Icon: icon,
		Services: []application.Service{application.NewService(bridge)},
		Assets:   application.AssetOptions{Handler: application.BundledAssetFileServer(assets)},
		SingleInstance: &application.SingleInstanceOptions{UniqueID: instanceID, OnSecondInstanceLaunch: func(data application.SecondInstanceData) {
			if activity != nil {
				activity.Show()
			}
		}},
		ShouldQuit: func() bool {
			if allowed.Load() {
				return true
			}
			go func() {
				if err := bridge.Quit(); err != nil {
					activity.Show()
					native.Dialog.Error().SetTitle("仍有服务未停止").SetMessage(err.Error()).Show()
				}
			}()
			return false
		},
	}
	if uiDebugPort != "" {
		if *profile == "" {
			log.Fatal("UI 验收构建必须指定独立的 --config-dir")
		}
		options.Windows.AdditionalBrowserArgs = []string{"--remote-debugging-port=" + uiDebugPort, "--remote-debugging-address=127.0.0.1"}
		options.Windows.WebviewUserDataPath = filepath.Join(*profile, "webview")
	}
	native = application.New(options)
	window = native.Window.NewWithOptions(application.WebviewWindowOptions{Title: "PHPTools", Width: 1280, Height: 850, MinWidth: 1040, MinHeight: 700, URL: "/"})
	activity = newWindowActivityController(windowActivityActions{
		dispatch: application.InvokeAsync,
		hide:     func() { window.Hide() },
		show:     func() { window.Show(); window.Restore(); window.Focus() },
		suspend:  window.SuspendWebView,
		resume:   window.ResumeWebView,
		emit:     func(active bool) { native.Event.Emit("window:activity", map[string]bool{"active": active}) },
		report:   func(err error) { log.Print(err) },
	})
	window.RegisterHook(events.Common.WindowClosing, func(event *application.WindowEvent) { event.Cancel(); activity.Hide() })
	tray := native.SystemTray.New()
	tray.SetIcon(icon)
	tray.SetTooltip("PHPTools · 本地开发环境")
	show := activity.Show
	tray.OnClick(show)
	menu := native.NewMenu()
	menu.Add("打开 PHPTools").OnClick(func(ctx *application.Context) { show() })
	menu.AddSeparator()
	menu.Add("启动全部服务").OnClick(func(ctx *application.Context) { _, _ = bridge.StartAll() })
	menu.Add("停止全部服务").OnClick(func(ctx *application.Context) { _, _ = bridge.StopAll() })
	menu.AddSeparator()
	menu.Add("停止服务并退出").OnClick(func(ctx *application.Context) {
		go func() {
			if err := bridge.Quit(); err != nil {
				show()
				native.Dialog.Error().SetTitle("退出未完成").SetMessage(err.Error()).Show()
			}
		}()
	})
	tray.SetMenu(menu)
	startSuspendAcceptance(native, window, bridge, activity, emitStateChanged)
	go func() {
		_, _ = bridge.StartInstalledPHP()
		_, _ = bridge.ResumeUpdatedInstances()
	}()
	if err = native.Run(); err != nil {
		log.Fatal(err)
	}
}

func appIcon() []byte {
	return icon.PNG()
}
