//go:build windows

package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"syscall"
	"unsafe"

	"github.com/tc-hib/winres"
	"phptools/internal/icon"
)

func main() {
	exe := flag.String("exe", "", "verify that this executable contains the application icon")
	flag.Parse()
	if *exe != "" {
		if err := exeHasIcon(*exe); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		return
	}
	if err := writeSyso(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func writeSyso() error {
	root, err := moduleRoot()
	if err != nil {
		return err
	}
	images, err := winres.NewIconFromImages(icon.Images())
	if err != nil {
		return err
	}
	var resources winres.ResourceSet
	// Resource ID 3 is the icon Wails loads for the window. Explorer uses the
	// same group icon for the file, shortcuts, and the taskbar.
	if err := resources.SetIcon(winres.RT_ICON, images); err != nil {
		return err
	}
	for _, arch := range []struct {
		name string
		arch winres.Arch
	}{
		{"amd64", winres.ArchAMD64},
		{"arm64", winres.ArchARM64},
	} {
		path := filepath.Join(root, "rsrc_windows_"+arch.name+".syso")
		file, err := os.Create(path)
		if err != nil {
			return err
		}
		err = resources.WriteObject(file, arch.arch)
		closeErr := file.Close()
		if err != nil {
			return err
		}
		if closeErr != nil {
			return closeErr
		}
	}
	return nil
}

func exeHasIcon(path string) error {
	absolute, err := filepath.Abs(path)
	if err != nil {
		return err
	}
	wide, err := syscall.UTF16PtrFromString(absolute)
	if err != nil {
		return err
	}
	handle, _, callErr := procLoadLibraryEx.Call(uintptr(unsafe.Pointer(wide)), 0, 2)
	if handle == 0 {
		return fmt.Errorf("cannot read %s: %v", absolute, callErr)
	}
	defer procFreeLibrary.Call(handle)
	// RT_GROUP_ICON is 14. Resource ID 3 is the icon embedded for Explorer and Wails.
	found, _, _ := procFindResource.Call(handle, 3, 14)
	if found == 0 {
		return fmt.Errorf("%s has no embedded application icon", absolute)
	}
	return nil
}

func moduleRoot() (string, error) {
	dir, err := os.Getwd()
	if err != nil {
		return "", err
	}
	for {
		if _, statErr := os.Stat(filepath.Join(dir, "go.mod")); statErr == nil {
			return dir, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", fmt.Errorf("could not find go.mod")
		}
		dir = parent
	}
}

var (
	kernel32          = syscall.NewLazyDLL("kernel32.dll")
	procLoadLibraryEx = kernel32.NewProc("LoadLibraryExW")
	procFreeLibrary   = kernel32.NewProc("FreeLibrary")
	procFindResource  = kernel32.NewProc("FindResourceW")
)
