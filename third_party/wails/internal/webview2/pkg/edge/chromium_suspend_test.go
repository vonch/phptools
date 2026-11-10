//go:build windows

package edge

import (
	"errors"
	"testing"
	"unsafe"
	"weak"

	"golang.org/x/sys/windows"
)

type suspensionProbe struct {
	chromium    *Chromium
	pending     []comAddr
	resumeCalls int
	suspended   bool
	requestErr  uintptr
}

func newSuspensionProbe() *suspensionProbe {
	p := &suspensionProbe{chromium: NewChromium()}
	view := &ICoreWebView2_3{vtbl: &iCoreWebView2_3Vtbl{}}
	view.vtbl.Release = NewComProc(func(uintptr) uintptr { return 0 })
	view.vtbl.TrySuspend = NewComProc(func(_, handler uintptr) uintptr {
		if p.requestErr != 0 {
			return p.requestErr
		}
		address := comAddr(handler)
		address.addRef()
		p.pending = append(p.pending, address)
		return 0
	})
	view.vtbl.Resume = NewComProc(func(uintptr) uintptr {
		p.resumeCalls++
		p.suspended = false
		return 0
	})
	view.vtbl.GetIsSuspended = NewComProc(func(_, result uintptr) uintptr {
		*(*int32)(unsafe.Pointer(result)) = int32(boolToInt(p.suspended))
		return 0
	})
	p.chromium.webview = &ICoreWebView2{vtbl: &iCoreWebView2Vtbl{}}
	p.chromium.webview.vtbl.QueryInterface = NewComProc(func(_, _, result uintptr) uintptr {
		*(**ICoreWebView2_3)(unsafe.Pointer(result)) = view
		return 0
	})
	p.chromium.controller = &ICoreWebView2Controller{}
	return p
}

func (p *suspensionProbe) complete(index int, result uintptr, successful bool) {
	address := p.pending[index]
	p.suspended = successful && int32(result) >= 0
	handler := (*iCoreWebView2TrySuspendCompletedHandler)(unsafe.Pointer(address))
	handler.vtbl.Invoke.Call(uintptr(address), result, uintptr(boolToInt(successful)))
	address.release()
}

func TestSuspendResumeWinsOverLateCompletion(t *testing.T) {
	p := newSuspensionProbe()
	called := false
	if err := p.chromium.Suspend(func(bool, error) { called = true }); err != nil {
		t.Fatal(err)
	}
	if count := handlerRefCount(unsafe.Pointer(uintptr(p.pending[0]))); count != 1 {
		t.Fatalf("pending callback has %d references, want one native reference", count)
	}
	if err := p.chromium.Resume(); err != nil {
		t.Fatal(err)
	}
	p.complete(0, 0, true)
	if called || p.suspended || p.resumeCalls != 2 {
		t.Fatalf("late suspension won: callback=%t, suspended=%t, resumes=%d", called, p.suspended, p.resumeCalls)
	}
	if count := handlerRefCount(unsafe.Pointer(uintptr(p.pending[0]))); count != 0 {
		t.Fatalf("completed callback retained %d references", count)
	}
	if suspended, err := p.chromium.IsSuspended(); err != nil || suspended {
		t.Fatalf("runtime state = %t, %v; want resumed", suspended, err)
	}
}

func TestNewSuspendWinsOverEarlierCompletion(t *testing.T) {
	p := newSuspensionProbe()
	oldCalled, currentCalled := false, false
	if err := p.chromium.Suspend(func(bool, error) { oldCalled = true }); err != nil {
		t.Fatal(err)
	}
	if err := p.chromium.Resume(); err != nil {
		t.Fatal(err)
	}
	if err := p.chromium.Suspend(func(success bool, err error) {
		currentCalled = success && err == nil
	}); err != nil {
		t.Fatal(err)
	}
	p.complete(0, 0, true)
	if oldCalled || p.resumeCalls != 1 {
		t.Fatal("earlier completion interfered with the new suspension")
	}
	p.complete(1, 0, true)
	if !currentCalled || !p.suspended {
		t.Fatal("latest suspension did not complete")
	}
}

func TestSuspendReportsRuntimeRefusalAndFailure(t *testing.T) {
	for _, result := range []uintptr{0, 0x80004004} {
		p := newSuspensionProbe()
		called := false
		if err := p.chromium.Suspend(func(success bool, err error) {
			called = true
			if success || (result == 0 && err != nil) || (result != 0 && !errors.Is(err, windows.Errno(result))) {
				t.Errorf("unexpected result: success=%t, err=%v", success, err)
			}
		}); err != nil {
			t.Fatal(err)
		}
		p.complete(0, result, false)
		if !called || p.chromium.suspendWanted {
			t.Fatal("runtime refusal was not reported and cleared")
		}
	}
}

func TestSuspendSynchronousFailureDropsCallback(t *testing.T) {
	p := newSuspensionProbe()
	p.requestErr = 0x8007139F
	before := len(handlerRefs.counts)
	err := p.chromium.Suspend(func(bool, error) { t.Error("callback called after request failure") })
	if !errors.Is(err, windows.Errno(p.requestErr)) || p.chromium.suspendCallback != nil || p.chromium.suspendWanted {
		t.Fatalf("failed suspension retained state: %v", err)
	}
	if len(handlerRefs.counts) != before {
		t.Fatal("synchronous failure retained its callback handler")
	}
}

//go:noinline
func abandonedSuspendCallback() (comAddr, weak.Pointer[hostWindow]) {
	w := &hostWindow{}
	e := NewChromium()
	e.suspendCallback = func(bool, error) { w.events++ }
	h := newICoreWebView2TrySuspendCompletedHandler(e, 1)
	suspendHandlerAddRef(h)
	e.ShuttingDown()
	return comAddr(uintptr(unsafe.Pointer(h))), weak.Make(w)
}

func TestAbandonedSuspendDoesNotRetainWindow(t *testing.T) {
	handler, window := abandonedSuspendCallback()
	defer handler.release()
	if !waitCollected(window) {
		t.Fatal("abandoned suspend callback retained its window")
	}
	h := (*iCoreWebView2TrySuspendCompletedHandler)(unsafe.Pointer(handler))
	h.vtbl.Invoke.Call(uintptr(handler), 0, 1)
}

func TestSuspendRejectsUninitializedAndClosedWebView(t *testing.T) {
	e := NewChromium()
	for _, closed := range []bool{false, true} {
		if closed {
			e.Close()
		}
		if err := e.Suspend(nil); err == nil {
			t.Fatal("Suspend accepted an unavailable WebView")
		}
		if err := e.Resume(); err == nil {
			t.Fatal("Resume accepted an unavailable WebView")
		}
		if _, err := e.IsSuspended(); err == nil {
			t.Fatal("IsSuspended accepted an unavailable WebView")
		}
	}
}

func TestSuspendHandlerQueryInterfaceRetainsReturnedInterface(t *testing.T) {
	h := newICoreWebView2TrySuspendCompletedHandler(NewChromium(), 1)
	for _, iid := range []*GUID{suspendHandlerIID, suspendHandlerIUnknownIID} {
		var result unsafe.Pointer
		hr, _, _ := h.vtbl.QueryInterface.Call(uintptr(unsafe.Pointer(h)), uintptr(unsafe.Pointer(iid)), uintptr(unsafe.Pointer(&result)))
		if hr != 0 || result != unsafe.Pointer(h) || handlerRefCount(result) != 1 {
			t.Fatalf("QueryInterface did not return and retain its interface: 0x%x, %p", hr, result)
		}
		suspendHandlerRelease(h)
	}
	var result unsafe.Pointer
	unknown := NewGUID("{11111111-1111-1111-1111-111111111111}")
	hr, _, _ := h.vtbl.QueryInterface.Call(uintptr(unsafe.Pointer(h)), uintptr(unsafe.Pointer(unknown)), uintptr(unsafe.Pointer(&result)))
	if hr != 0x80004002 || result != nil || handlerRefCount(unsafe.Pointer(h)) != 0 {
		t.Fatalf("unknown interface was accepted: 0x%x, %p", hr, result)
	}
}
