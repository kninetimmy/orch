//go:build windows

package lockfile

import (
	"crypto/sha256"
	"fmt"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"unsafe"
)

var (
	kernel32     = syscall.NewLazyDLL("kernel32.dll")
	createMutexW = kernel32.NewProc("CreateMutexW")
	releaseMutex = kernel32.NewProc("ReleaseMutex")
)

// acquireMutationOS uses a named Windows mutex because mutex ownership is
// automatically abandoned when a process exits. Mutex ownership is tied to an
// OS thread, so that thread stays pinned until Release.
func acquireMutationOS(repoRoot string) (func() error, error) {
	abs, err := filepath.Abs(repoRoot)
	if err != nil {
		return nil, fmt.Errorf("resolve repository for Delivery mutation serialization: %w", err)
	}
	resolved, err := filepath.EvalSymlinks(abs)
	if err != nil {
		return nil, fmt.Errorf("resolve repository for Delivery mutation serialization: %w", err)
	}
	sum := sha256.Sum256([]byte(strings.ToLower(filepath.Clean(resolved))))
	name, err := syscall.UTF16PtrFromString(fmt.Sprintf("Global\\OrchDeliveryMutation-%x", sum))
	if err != nil {
		return nil, fmt.Errorf("name Delivery mutation serialization: %w", err)
	}

	runtime.LockOSThread()
	h, _, callErr := createMutexW.Call(0, 0, uintptr(unsafe.Pointer(name)))
	if h == 0 {
		runtime.UnlockOSThread()
		return nil, fmt.Errorf("create Delivery mutation serialization: %w", callErr)
	}
	event, waitErr := syscall.WaitForSingleObject(syscall.Handle(h), syscall.INFINITE)
	if waitErr != nil || (event != syscall.WAIT_OBJECT_0 && event != syscall.WAIT_ABANDONED) {
		_ = syscall.CloseHandle(syscall.Handle(h))
		runtime.UnlockOSThread()
		if waitErr != nil {
			return nil, fmt.Errorf("wait for Delivery mutation serialization: %w", waitErr)
		}
		return nil, fmt.Errorf("wait for Delivery mutation serialization: unexpected result %d", event)
	}

	return func() error {
		ok, _, releaseErr := releaseMutex.Call(h)
		closeErr := syscall.CloseHandle(syscall.Handle(h))
		runtime.UnlockOSThread()
		if ok == 0 {
			return fmt.Errorf("release Delivery mutation serialization: %w", releaseErr)
		}
		if closeErr != nil {
			return fmt.Errorf("close Delivery mutation serialization: %w", closeErr)
		}
		return nil
	}, nil
}
