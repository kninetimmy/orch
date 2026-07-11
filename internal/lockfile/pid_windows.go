//go:build windows

package lockfile

import (
	"errors"
	"syscall"
)

// PIDAlive reports whether a process with the given PID currently
// exists and has not exited. Merely opening the PID is not enough: a
// Windows process object outlives its exit while any handle to it
// remains open (Go itself keeps one after exec.Cmd.Wait), so an
// exited process can still be opened. A zero-timeout wait on the
// handle distinguishes the two: WAIT_TIMEOUT means still running,
// signaled means exited. Access-denied on open counts as alive (the
// process exists but is not openable). Advisory only — PIDs are
// reused, and the acquiring orch process is expected to exit during a
// healthy Delivery run.
func PIDAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	h, err := syscall.OpenProcess(syscall.SYNCHRONIZE, false, uint32(pid))
	if err != nil {
		return errors.Is(err, syscall.ERROR_ACCESS_DENIED)
	}
	defer syscall.CloseHandle(h)
	ev, err := syscall.WaitForSingleObject(h, 0)
	return err == nil && ev == uint32(syscall.WAIT_TIMEOUT)
}
