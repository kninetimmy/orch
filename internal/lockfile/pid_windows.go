//go:build windows

package lockfile

import (
	"errors"
	"os"
	"syscall"
)

// PIDAlive reports whether a process with the given PID currently
// exists. On Windows os.FindProcess opens a real handle, so it fails
// when no such process exists; access-denied counts as alive (the
// process exists but is not openable). Advisory only — PIDs are
// reused, and the acquiring orch process is expected to exit during a
// healthy Delivery run.
func PIDAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	p, err := os.FindProcess(pid)
	if err != nil {
		return errors.Is(err, syscall.ERROR_ACCESS_DENIED)
	}
	_ = p.Release()
	return true
}
