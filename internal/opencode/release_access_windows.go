package opencode

import (
	"fmt"
	"os"
	"syscall"
)

// Observe DELETE access without marking, renaming or deleting the directory.
// A cwd/open handle without FILE_SHARE_DELETE (or denied access) must refuse
// before Git can delete the remote branch or deregister either checkout.
// The handle is closed immediately: this does NOT exclude later external entry.
func releaseDeleteAccess(directory string, expected os.FileInfo) error {
	refuse := func() error {
		return fmt.Errorf("OpenCode checkout release: Windows directory delete access unavailable (open handles or permissions); preserve the checkout and inspect before retrying")
	}
	name, err := syscall.UTF16PtrFromString(directory)
	if err != nil {
		return refuse()
	}
	const deleteAccess = 0x00010000 // Windows DELETE standard access right.
	handle, err := syscall.CreateFile(name, deleteAccess,
		syscall.FILE_SHARE_READ|syscall.FILE_SHARE_WRITE|syscall.FILE_SHARE_DELETE,
		nil, syscall.OPEN_EXISTING, syscall.FILE_FLAG_BACKUP_SEMANTICS|syscall.FILE_FLAG_OPEN_REPARSE_POINT, 0)
	if err != nil {
		return refuse()
	}
	file := os.NewFile(uintptr(handle), directory)
	info, statErr := file.Stat()
	closeErr := file.Close()
	if statErr != nil || closeErr != nil || !info.IsDir() || !os.SameFile(expected, info) {
		return refuse()
	}
	return nil
}
