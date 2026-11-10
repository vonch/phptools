//go:build windows

package edge

import "unsafe"

type iCoreWebView2TrySuspendCompletedHandlerVtbl struct {
	_IUnknownVtbl
	Invoke ComProc
}

type iCoreWebView2TrySuspendCompletedHandler struct {
	vtbl       *iCoreWebView2TrySuspendCompletedHandlerVtbl
	owner      *Chromium
	generation uint64
}

var suspendHandlerIID = NewGUID("{00F82EAA-8052-4486-8D16-6EFC5E143F72}")
var suspendHandlerIUnknownIID = NewGUID("{00000000-0000-0000-C000-000000000046}")

func suspendHandlerQueryInterface(this *iCoreWebView2TrySuspendCompletedHandler, refiid, object uintptr) uintptr {
	if object == 0 || refiid == 0 {
		return 0x80004003 // E_POINTER
	}
	result := (*unsafe.Pointer)(unsafe.Pointer(object))
	*result = nil
	iid := *(*GUID)(unsafe.Pointer(refiid))
	if iid != *suspendHandlerIID && iid != *suspendHandlerIUnknownIID {
		return 0x80004002 // E_NOINTERFACE
	}
	*result = unsafe.Pointer(this)
	suspendHandlerAddRef(this)
	return 0
}

func suspendHandlerAddRef(this *iCoreWebView2TrySuspendCompletedHandler) uintptr {
	return handlerAddRef(unsafe.Pointer(this))
}

func suspendHandlerRelease(this *iCoreWebView2TrySuspendCompletedHandler) uintptr {
	return handlerRelease(unsafe.Pointer(this))
}

func suspendHandlerInvoke(this *iCoreWebView2TrySuspendCompletedHandler, errorCode, successful uintptr) uintptr {
	this.owner.suspendCompleted(this.generation, errorCode, successful != 0)
	return 0
}

var suspendHandlerVtbl = iCoreWebView2TrySuspendCompletedHandlerVtbl{
	_IUnknownVtbl{
		NewComProc(suspendHandlerQueryInterface),
		NewComProc(suspendHandlerAddRef),
		NewComProc(suspendHandlerRelease),
	},
	NewComProc(suspendHandlerInvoke),
}

func newICoreWebView2TrySuspendCompletedHandler(owner *Chromium, generation uint64) *iCoreWebView2TrySuspendCompletedHandler {
	return &iCoreWebView2TrySuspendCompletedHandler{
		vtbl: &suspendHandlerVtbl, owner: owner, generation: generation,
	}
}
