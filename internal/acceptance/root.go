//go:build windows

// Package acceptance owns disposable Windows service-test runtimes.
package acceptance

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Root is a newly created runtime. It never adopts an existing directory.
type Root struct {
	Path     string
	path     string
	identity os.FileInfo
	cleaned  bool
}

// NewRoot creates a disposable runtime in the system temporary directory unless
// requested explicitly names a directory that does not exist yet.
func NewRoot(requested, suite string) (*Root, error) {
	if suite == "" || strings.ContainsAny(suite, `/\\:`) || suite == "." || suite == ".." {
		return nil, errors.New("acceptance suite must be a simple directory name")
	}
	var path string
	var err error
	if requested == "" {
		parent, absErr := filepath.Abs(os.TempDir())
		if absErr != nil {
			return nil, absErr
		}
		if err = checkDirectoryChain(parent); err != nil {
			return nil, err
		}
		path, err = os.MkdirTemp(parent, "phptools-"+suite+"-")
	} else {
		path, err = filepath.Abs(requested)
		if err == nil {
			err = checkRootPath(path)
		}
		if err == nil {
			err = checkDirectoryChain(filepath.Dir(path))
		}
		if err == nil {
			err = os.Mkdir(path, 0700)
		}
	}
	if err != nil {
		return nil, fmt.Errorf("create fresh acceptance runtime: %w", err)
	}
	// File.Stat captures the directory's file ID immediately on Windows. Lstat
	// can defer that lookup until SameFile, after a directory has been replaced.
	directory, err := os.Open(path)
	if err != nil {
		return nil, errors.Join(err, os.Remove(path))
	}
	identity, statErr := directory.Stat()
	err = errors.Join(statErr, directory.Close())
	if err != nil {
		return nil, errors.Join(err, os.Remove(path))
	}
	r := &Root{Path: path, path: path, identity: identity}
	if err := r.checkIdentity(); err != nil {
		return nil, err
	}
	return r, nil
}

// Cleanup removes only this newly created directory, after its service and
// database shutdown defers have run. A failed safety check preserves the files.
func (r *Root) Cleanup() error {
	if r == nil || r.cleaned {
		return nil
	}
	if err := r.checkIdentity(); err != nil {
		return err
	}
	if err := checkNoProcesses(r.path); err != nil {
		return err
	}
	// The scan may take time; verify the directory again immediately before use.
	if err := r.checkIdentity(); err != nil {
		return err
	}
	if err := os.RemoveAll(r.path); err != nil {
		return fmt.Errorf("remove acceptance runtime %s: %w", r.path, err)
	}
	r.cleaned = true
	return nil
}

func (r *Root) checkIdentity() error {
	if r.Path != r.path || r.identity == nil {
		return errors.New("acceptance root path or ownership changed; refusing cleanup")
	}
	if err := checkRootPath(r.path); err != nil {
		return err
	}
	if err := checkDirectoryChain(r.path); err != nil {
		return err
	}
	current, err := os.Lstat(r.path)
	if err != nil {
		return err
	}
	if !os.SameFile(r.identity, current) {
		return errors.New("acceptance root was replaced; refusing cleanup")
	}
	return nil
}

func checkRootPath(path string) error {
	if !filepath.IsAbs(path) || filepath.Clean(path) != path || filepath.Dir(path) == path {
		return errors.New("acceptance root must be a non-volume absolute directory")
	}
	return nil
}

func checkDirectoryChain(path string) error {
	for {
		info, err := os.Lstat(path)
		if err != nil {
			return err
		}
		if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("acceptance directory is not a plain directory: %s", path)
		}
		if err := checkReparsePoint(path); err != nil {
			return err
		}
		parent := filepath.Dir(path)
		if parent == path {
			return nil
		}
		path = parent
	}
}
