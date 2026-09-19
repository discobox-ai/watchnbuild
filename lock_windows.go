//go:build windows

package main

import (
	"errors"
	"os"

	"golang.org/x/sys/windows"
)

// openForLock opens path sharing read, write and delete access. os.Open
// omits delete sharing, and holding such a handle for wnb's lifetime would
// stop the config being deleted or renamed over — a git checkout that
// changes it, or an editor's atomic save, would fail.
func openForLock(path string) (*os.File, error) {
	p, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return nil, err
	}
	h, err := windows.CreateFile(p, windows.GENERIC_READ,
		windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE,
		nil, windows.OPEN_EXISTING, windows.FILE_ATTRIBUTE_NORMAL, 0)
	if err != nil {
		return nil, &os.PathError{Op: "open", Path: path, Err: err}
	}
	return os.NewFile(uintptr(h), path), nil
}

// lockFile locks one byte at the highest offset rather than the file's
// contents: Windows locks are mandatory, and locking the contents would make
// the config unreadable to everything else — including wnb's own reads.
func lockFile(f *os.File) error {
	ol := &windows.Overlapped{Offset: 0xFFFFFFFF, OffsetHigh: 0x7FFFFFFF}
	err := windows.LockFileEx(windows.Handle(f.Fd()),
		windows.LOCKFILE_EXCLUSIVE_LOCK|windows.LOCKFILE_FAIL_IMMEDIATELY, 0, 1, 0, ol)
	if errors.Is(err, windows.ERROR_LOCK_VIOLATION) {
		return errLocked
	}
	return err
}
