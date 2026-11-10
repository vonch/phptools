package acceptance

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"unsafe"

	"golang.org/x/sys/windows"
)

func checkReparsePoint(path string) error {
	name, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return err
	}
	attributes, err := windows.GetFileAttributes(name)
	if err != nil {
		return err
	}
	if attributes&windows.FILE_ATTRIBUTE_REPARSE_POINT != 0 {
		return fmt.Errorf("acceptance directory is a reparse point: %s", path)
	}
	return nil
}

func checkNoProcesses(root string) error {
	// Protected system processes cannot always be queried. If an unqueryable
	// process has a name found in the runtime, preserve it rather than guessing.
	names := map[string]bool{}
	if err := filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if err == nil && !entry.IsDir() {
			names[strings.ToLower(entry.Name())] = true
		}
		return err
	}); err != nil {
		return err
	}
	snapshot, err := windows.CreateToolhelp32Snapshot(windows.TH32CS_SNAPPROCESS, 0)
	if err != nil {
		return fmt.Errorf("scan acceptance processes: %w", err)
	}
	defer windows.CloseHandle(snapshot)
	var entry windows.ProcessEntry32
	entry.Size = uint32(unsafe.Sizeof(entry))
	for err = windows.Process32First(snapshot, &entry); err == nil; err = windows.Process32Next(snapshot, &entry) {
		if entry.ProcessID == 0 {
			continue
		}
		handle, queryErr := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, entry.ProcessID)
		var image string
		if queryErr == nil {
			buffer := make([]uint16, 32768)
			size := uint32(len(buffer))
			queryErr = windows.QueryFullProcessImageName(handle, 0, &buffer[0], &size)
			windows.CloseHandle(handle)
			if queryErr == nil {
				image = windows.UTF16ToString(buffer[:size])
			}
		}
		if queryErr != nil {
			if errors.Is(queryErr, windows.ERROR_INVALID_PARAMETER) || errors.Is(queryErr, windows.ERROR_NOT_FOUND) {
				continue // The process exited during the snapshot.
			}
			if names[strings.ToLower(windows.UTF16ToString(entry.ExeFile[:]))] {
				return fmt.Errorf("cannot verify possible acceptance process PID %d; refusing cleanup: %w", entry.ProcessID, queryErr)
			}
			continue
		}
		relative, pathErr := filepath.Rel(root, image)
		if pathErr == nil && relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator)) && !filepath.IsAbs(relative) {
			return fmt.Errorf("acceptance process PID %d is still running from %s; stop it before cleanup", entry.ProcessID, image)
		}
	}
	if !errors.Is(err, windows.ERROR_NO_MORE_FILES) {
		return fmt.Errorf("scan acceptance processes: %w", err)
	}
	return nil
}
