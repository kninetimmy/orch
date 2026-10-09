//go:build unix

package grant

import (
	"os"
	"syscall"
)

// lockFile blocks until f holds an exclusive flock. flock belongs to the open
// file description, so two opens in one process contend like two processes.
func lockFile(f *os.File) error { return syscall.Flock(int(f.Fd()), syscall.LOCK_EX) }

func unlockFile(f *os.File) error { return syscall.Flock(int(f.Fd()), syscall.LOCK_UN) }
