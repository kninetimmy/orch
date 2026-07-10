//go:build unix

package lockfile

import (
	"errors"
	"os"
	"syscall"
)

// PIDAlive reports whether a process with the given PID currently
// exists. EPERM counts as alive: the process exists but belongs to
// another user. Advisory only — PIDs are reused, and the acquiring
// orch process is expected to exit during a healthy Delivery run.
func PIDAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	p, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	err = p.Signal(syscall.Signal(0))
	return err == nil || errors.Is(err, syscall.EPERM)
}
