//go:build unix

package main

import (
	"errors"
	"os"
	"path/filepath"
	"syscall"
)

// A second process cannot safely share in-memory request leases with this one.
// Keep the lock inode in place: removing it could let another process lock a
// different inode before the current holder releases its advisory lock.
func lockStateDirectory(dir string) (func(), error) {
	if err := os.MkdirAll(dir, 0700); err != nil {
		return nil, errors.New("cannot create bridge state directory")
	}
	if err := os.Chmod(dir, 0700); err != nil {
		return nil, errors.New("cannot protect bridge state directory")
	}
	f, err := os.OpenFile(filepath.Join(dir, ".bridge.lock"), os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return nil, errors.New("cannot open bridge state lock")
	}
	if err = f.Chmod(0600); err != nil {
		_ = f.Close()
		return nil, errors.New("cannot protect bridge state lock")
	}
	if err = syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		_ = f.Close()
		return nil, errors.New("bridge state directory is already in use")
	}
	return func() { _ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN); _ = f.Close() }, nil
}
