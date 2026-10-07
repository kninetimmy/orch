//go:build windows

package claudenative

import "syscall"

func processAlive(pid int) bool {
	h, err := syscall.OpenProcess(0x00100000, false, uint32(pid)) // SYNCHRONIZE
	if err != nil {
		return false
	}
	defer func() { _ = syscall.CloseHandle(h) }()
	event, _ := syscall.WaitForSingleObject(h, 0)
	return event == syscall.WAIT_TIMEOUT
}
