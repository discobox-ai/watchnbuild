package main

import (
	"errors"
	"fmt"
	"os"
)

// errLocked means another process holds the config lock.
var errLocked = errors.New("locked by another process")

// heldLock keeps the config lock's file open for the life of the process.
// It must stay referenced: a collected *os.File is closed by its finalizer,
// and closing it releases the lock.
var heldLock *os.File

// lockConfig takes an exclusive lock on the config file for as long as wnb
// runs, so two watchnbuilds never drive the same project. That is not just
// tidiness: a run command that is itself watchnbuild (build this repo with
// the defaults and you get exactly that) would otherwise start another one
// on the same config, which starts another, forever. With the lock, the
// second one exits at once, and the first sees an ordinary failed run.
//
// The lock is advisory on Unix (flock) and taken past the end of the file on
// Windows, whose locks are mandatory, so reading and editing the config are
// never blocked; on Windows the file is also opened sharing delete access,
// so it can still be replaced by rename or deleted (git checkout, editors
// that save atomically). It is released when the process exits, however it
// exits, and is not inherited by the commands wnb runs.
//
// The lock belongs to the file, not its name: once the config is replaced
// by rename, a new wnb opens the new file and is not excluded. The
// self-launch case is unaffected, since the nested wnb starts at once.
func lockConfig(path string) error {
	f, err := openForLock(path)
	if err != nil {
		return err
	}
	if err := lockFile(f); err != nil {
		f.Close()
		if errors.Is(err, errLocked) {
			return fmt.Errorf("another watchnbuild is already running with %s (if this one was started by that one's run command, point run.command at something other than watchnbuild)", path)
		}
		return fmt.Errorf("locking %s: %w", path, err)
	}
	heldLock = f
	return nil
}
