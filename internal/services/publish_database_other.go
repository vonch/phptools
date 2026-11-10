//go:build !windows

package services

func moveDatabaseExport(temporary, destination string) error { return errPlatform }
