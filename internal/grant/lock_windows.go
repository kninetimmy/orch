//go:build windows

package grant

import (
	"os"
	"syscall"
	"unsafe"
)

var (
	kernel32         = syscall.NewLazyDLL("kernel32.dll")
	procLockFileEx   = kernel32.NewProc("LockFileEx")
	procUnlockFileEx = kernel32.NewProc("UnlockFileEx")
)

const lockfileExclusiveLock = 0x2

// lockFile blocks until f's handle holds an exclusive lock on its first byte.
// The lock belongs to the handle, so two handles in one process contend like
// two processes, and process exit releases it.
func lockFile(f *os.File) error {
	var ol syscall.Overlapped
	if ok, _, err := procLockFileEx.Call(f.Fd(), lockfileExclusiveLock, 0, 1, 0, uintptr(unsafe.Pointer(&ol))); ok == 0 {
		return err
	}
	return nil
}

func unlockFile(f *os.File) error {
	var ol syscall.Overlapped
	if ok, _, err := procUnlockFileEx.Call(f.Fd(), 0, 1, 0, uintptr(unsafe.Pointer(&ol))); ok == 0 {
		return err
	}
	return nil
}
