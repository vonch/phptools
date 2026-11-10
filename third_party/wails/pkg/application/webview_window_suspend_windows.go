//go:build windows && !server

package application

import "errors"

func (w *WebviewWindow) suspensionWindow() (*windowsWebviewWindow, error) {
	if w == nil || w.isDestroyed() {
		return nil, errors.New("window has been destroyed")
	}
	window, ok := w.impl.(*windowsWebviewWindow)
	if !ok || window.chromium == nil {
		return nil, errors.New("WebView2 window is not initialized")
	}
	return window, nil
}

// SuspendWebView requests best-effort suspension after Hide has completed.
// callback runs on the UI thread; it is canceled by ResumeWebView, another
// suspension request, or destruction of the underlying WebView.
func (w *WebviewWindow) SuspendWebView(callback func(bool, error)) error {
	if globalApplication == nil || globalApplication.impl == nil {
		return errors.New("application is not running")
	}
	return InvokeSyncWithError(func() error {
		window, err := w.suspensionWindow()
		if err != nil {
			return err
		}
		if window.windowShown {
			return errors.New("hide the window before suspending WebView2")
		}
		return window.chromium.Suspend(callback)
	})
}

// ResumeWebView resumes timers and invalidates any pending suspension request.
// Call it before Show when restoring a suspended tray window.
func (w *WebviewWindow) ResumeWebView() error {
	if globalApplication == nil || globalApplication.impl == nil {
		return errors.New("application is not running")
	}
	return InvokeSyncWithError(func() error {
		window, err := w.suspensionWindow()
		if err != nil {
			return err
		}
		return window.chromium.Resume()
	})
}

// IsWebViewSuspended reports the runtime's actual suspension state.
func (w *WebviewWindow) IsWebViewSuspended() (bool, error) {
	if globalApplication == nil || globalApplication.impl == nil {
		return false, errors.New("application is not running")
	}
	return InvokeSyncWithResultAndError(func() (bool, error) {
		window, err := w.suspensionWindow()
		if err != nil {
			return false, err
		}
		return window.chromium.IsSuspended()
	})
}
