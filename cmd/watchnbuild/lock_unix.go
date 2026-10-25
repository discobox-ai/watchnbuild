//go:build unix

package main

import (
	"errors"
	"os"
	"syscall"
)

func openForLock(path string) (*os.File, error) { return os.Open(path) }

func lockFile(f *os.File) error {
	err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
	if errors.Is(err, syscall.EWOULDBLOCK) {
		return errLocked
	}
	return err
}
