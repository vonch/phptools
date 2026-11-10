//go:build windows

package edge

import (
	"errors"
	"log"

	"golang.org/x/sys/windows"
)

func (e *Chromium) suspensionInterface() (*ICoreWebView2_3, error) {
	if e.shuttingDown || e.webview == nil || e.controller == nil {
		return nil, errors.New("WebView2 is not initialized or has been closed")
	}
	view := e.GetICoreWebView2_3()
	if view == nil {
		return nil, errors.New("WebView2 runtime does not support ICoreWebView2_3")
	}
	return view, nil
}

// Suspend starts a best-effort WebView2 suspension. The controller must already
// be invisible. All calls, including callback, run on the WebView's UI thread.
// Resume, another Suspend, or Close cancels the previous completion callback.
func (e *Chromium) Suspend(callback func(bool, error)) error {
	view, err := e.suspensionInterface()
	if err != nil {
		return err
	}
	defer view.Release()

	e.suspendGeneration++
	generation := e.suspendGeneration
	e.suspendWanted = true
	e.suspendCallback = callback
	handler := newICoreWebView2TrySuspendCompletedHandler(e, generation)
	// Retain our initial reference across the syscall. WebView2 takes its own
	// reference for the asynchronous completion via the same handler registry.
	suspendHandlerAddRef(handler)
	err = view.TrySuspend(handler)
	suspendHandlerRelease(handler)
	if err != nil && generation == e.suspendGeneration {
		e.cancelSuspend()
	}
	return err
}

func (e *Chromium) cancelSuspend() {
	e.suspendGeneration++
	e.suspendWanted = false
	e.suspendCallback = nil
}

// Resume also invalidates a suspension that has not yet completed.
func (e *Chromium) Resume() error {
	e.cancelSuspend()
	return e.resumeWebView()
}

func (e *Chromium) resumeWebView() error {
	view, err := e.suspensionInterface()
	if err != nil {
		return err
	}
	defer view.Release()
	return view.Resume()
}

func (e *Chromium) IsSuspended() (bool, error) {
	view, err := e.suspensionInterface()
	if err != nil {
		return false, err
	}
	defer view.Release()
	return view.GetIsSuspended()
}

func (e *Chromium) suspendCompleted(generation uint64, result uintptr, successful bool) {
	if e.shuttingDown {
		return
	}
	if generation != e.suspendGeneration {
		// Showing a window while TrySuspend is pending must win, even if the
		// runtime delivers a successful completion after our earlier Resume.
		if !e.suspendWanted && successful && int32(result) >= 0 {
			if err := e.resumeWebView(); err != nil {
				log.Printf("[WebView2] Resume after canceled suspension failed: %v", err)
			}
		}
		return
	}
	callback := e.suspendCallback
	e.suspendCallback = nil
	var err error
	if int32(result) < 0 {
		err = windows.Errno(uint32(result))
	}
	if !successful || err != nil {
		e.suspendWanted = false
	}
	if callback != nil {
		callback(successful && err == nil, err)
	}
}
