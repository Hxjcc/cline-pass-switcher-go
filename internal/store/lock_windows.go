package store

import (
	"fmt"
	"os"
	"path/filepath"
	"syscall"
)

func lockDirectory(dir string) (*os.File, error) {
	path := filepath.Join(dir, ".store.lock")
	name, err := syscall.UTF16PtrFromString(path)
	if err != nil {
		return nil, err
	}
	// No sharing: the kernel releases this lock even after a process crash.
	handle, err := syscall.CreateFile(name, syscall.GENERIC_READ|syscall.GENERIC_WRITE, 0, nil, syscall.OPEN_ALWAYS, syscall.FILE_ATTRIBUTE_NORMAL, 0)
	if err != nil {
		return nil, fmt.Errorf("cannot lock data directory (it may already be in use): %w", err)
	}
	return os.NewFile(uintptr(handle), path), nil
}

func syncDirectory(string) error { return nil }
