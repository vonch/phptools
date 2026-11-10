# Wails runtime patch

This directory contains the runtime sources (`pkg`, `internal`, `go.mod`,
`go.sum`, `LICENSE`, and upstream `README.md`) from
`github.com/wailsapp/wails/v3 v3.0.0-beta.32`. CLI sources and examples are not
included. The root `go.mod` replaces that exact module with this local copy;
the pinned upstream CLI remains responsible for generating bindings.

PHPTools needs WebView2 suspension while its window is hidden in the tray.
The pinned Wails version includes the native interfaces but does not expose
suspend/resume to applications. These changes use its existing COM bindings
and UI-thread dispatch, without accessing private pointers from PHPTools or
modifying the global Go module cache.

Local changes:

- `pkg/application/webview2_runtime_windows.go`: expose the existing loader's
  runtime discovery before creating a window, so PHPTools can display native
  installation guidance on computers without WebView2.

- `internal/webview2/pkg/edge/ICoreWebView2_3.go`: wrap `TrySuspend`, `Resume`,
  and `get_IsSuspended` using the existing interface and vtable.
- `internal/webview2/pkg/edge/ICoreWebView2TrySuspendCompletedHandler.go`:
  retain asynchronous completion handlers through the existing registry.
- `internal/webview2/pkg/edge/chromium_suspend.go`: track pending requests so
  reopening a window wins over a late suspension completion.
- `internal/webview2/pkg/edge/chromium.go`: cancel host callbacks at shutdown.
- `pkg/application/webview_window_suspend_windows.go`: expose
  `SuspendWebView`, `ResumeWebView`, and `IsWebViewSuspended` on Windows.
- `internal/webview2/pkg/edge/chromium_suspend_test.go`: native binding and
  asynchronous lifecycle regression tests.

When upgrading Wails, review these changes against upstream. Keep the existing
Apache-2.0 license. Run the edge tests and PHPTools' isolated `suspendqa`
acceptance build before releasing an upgraded runtime.
