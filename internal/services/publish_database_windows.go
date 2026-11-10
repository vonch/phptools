//go:build windows

package services

import "golang.org/x/sys/windows"

func moveDatabaseExport(temporary, destination string) error {
	from, err := windows.UTF16PtrFromString(temporary)
	if err != nil {
		return err
	}
	to, err := windows.UTF16PtrFromString(destination)
	if err != nil {
		return err
	}
	// Both files are in the same directory. Omitting REPLACE_EXISTING makes
	// publication atomic and refuses a destination created during the export.
	return windows.MoveFileEx(from, to, windows.MOVEFILE_WRITE_THROUGH)
}
