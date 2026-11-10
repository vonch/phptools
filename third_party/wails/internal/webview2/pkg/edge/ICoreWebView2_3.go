//go:build windows

package edge

import (
	"unsafe"

	"golang.org/x/sys/windows"
)

type iCoreWebView2_3Vtbl struct {
	iCoreWebView2_2Vtbl
	TrySuspend                          ComProc
	Resume                              ComProc
	GetIsSuspended                      ComProc
	SetVirtualHostNameToFolderMapping   ComProc
	ClearVirtualHostNameToFolderMapping ComProc
}

type ICoreWebView2_3 struct {
	vtbl *iCoreWebView2_3Vtbl
}

func (i *ICoreWebView2_3) AddRef() uint32 {
	ret, _, _ := i.vtbl.AddRef.Call(uintptr(unsafe.Pointer(i)))

	return uint32(ret)
}

func (i *ICoreWebView2_3) Release() uint32 {
	ret, _, _ := i.vtbl.Release.Call(uintptr(unsafe.Pointer(i)))

	return uint32(ret)
}

func (i *ICoreWebView2_3) TrySuspend(handler *iCoreWebView2TrySuspendCompletedHandler) error {
	hr, _, _ := i.vtbl.TrySuspend.Call(uintptr(unsafe.Pointer(i)), uintptr(unsafe.Pointer(handler)))
	if int32(hr) < 0 {
		return windows.Errno(uint32(hr))
	}
	return nil
}

func (i *ICoreWebView2_3) Resume() error {
	hr, _, _ := i.vtbl.Resume.Call(uintptr(unsafe.Pointer(i)))
	if int32(hr) < 0 {
		return windows.Errno(uint32(hr))
	}
	return nil
}

func (i *ICoreWebView2_3) GetIsSuspended() (bool, error) {
	var suspended int32
	hr, _, _ := i.vtbl.GetIsSuspended.Call(uintptr(unsafe.Pointer(i)), uintptr(unsafe.Pointer(&suspended)))
	if int32(hr) < 0 {
		return false, windows.Errno(uint32(hr))
	}
	return suspended != 0, nil
}

func (i *ICoreWebView2_3) SetVirtualHostNameToFolderMapping(hostName, folderPath string, accessKind COREWEBVIEW2_HOST_RESOURCE_ACCESS_KIND) error {
	_hostName, err := windows.UTF16PtrFromString(hostName)
	if err != nil {
		return err
	}

	_folderPath, err := windows.UTF16PtrFromString(folderPath)
	if err != nil {
		return err
	}

	hr, _, _ := i.vtbl.SetVirtualHostNameToFolderMapping.Call(
		uintptr(unsafe.Pointer(i)),
		uintptr(unsafe.Pointer(_hostName)),
		uintptr(unsafe.Pointer(_folderPath)),
		uintptr(accessKind),
	)
	if windows.Handle(hr) != windows.S_OK {
		return windows.Errno(hr)
	}

	return nil
}

func (i *ICoreWebView2) GetICoreWebView2_3() *ICoreWebView2_3 {
	var result *ICoreWebView2_3

	iidICoreWebView2_3 := NewGUID("{A0D6DF20-3B92-416D-AA0C-437A9C727857}")
	_, _, _ = i.vtbl.QueryInterface.Call(
		uintptr(unsafe.Pointer(i)),
		uintptr(unsafe.Pointer(iidICoreWebView2_3)),
		uintptr(unsafe.Pointer(&result)))

	return result
}

func (e *Chromium) GetICoreWebView2_3() *ICoreWebView2_3 {
	return e.webview.GetICoreWebView2_3()
}
