package evalplan

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"unsafe"
)

var finalDrivePath = syscall.NewLazyDLL("kernel32.dll").NewProc("GetFinalPathNameByHandleW")

// EvalSymlinks does not resolve DOS drive mappings such as SUBST. Verify the
// existing drive root, even for a future directory, using the same native
// handle-path API as codexnative's finalIsolationPath. Aliases are refused.
func verifyDriveRoot(path string) (resultErr error) {
	root := filepath.VolumeName(path) + string(filepath.Separator)
	name, err := syscall.UTF16PtrFromString(root)
	if err != nil {
		return err
	}
	// 0x80 is FILE_READ_ATTRIBUTES; no directory or artifact contents are read.
	handle, err := syscall.CreateFile(name, 0x80, syscall.FILE_SHARE_READ|syscall.FILE_SHARE_WRITE|syscall.FILE_SHARE_DELETE, nil, syscall.OPEN_EXISTING, syscall.FILE_FLAG_BACKUP_SEMANTICS, 0)
	if err != nil {
		return fmt.Errorf("verify Windows drive alias for %s: %w", root, err)
	}
	defer func() { resultErr = errors.Join(resultErr, syscall.CloseHandle(handle)) }()
	if err := finalDrivePath.Find(); err != nil {
		return fmt.Errorf("verify Windows drive alias: %w", err)
	}
	buffer := make([]uint16, 32768)
	n, _, callErr := finalDrivePath.Call(uintptr(handle), uintptr(unsafe.Pointer(&buffer[0])), uintptr(len(buffer)), 0)
	if n == 0 || n >= uintptr(len(buffer)) {
		return fmt.Errorf("verify Windows drive alias: native path unavailable (length %d): %v", n, callErr)
	}
	resolved := syscall.UTF16ToString(buffer[:n])
	if !strings.HasPrefix(resolved, `\\?\`) || !strings.EqualFold(filepath.Clean(strings.TrimPrefix(resolved, `\\?\`)), filepath.Clean(root)) {
		return fmt.Errorf("windows drive alias forbidden or unverifiable: %s", root)
	}
	return nil
}

func reparse(info os.FileInfo) bool {
	attrs, ok := info.Sys().(*syscall.Win32FileAttributeData)
	return !ok || attrs.FileAttributes&syscall.FILE_ATTRIBUTE_REPARSE_POINT != 0
}

func linkCount(f *os.File) (uint64, error) {
	var info syscall.ByHandleFileInformation
	if err := syscall.GetFileInformationByHandle(syscall.Handle(f.Fd()), &info); err != nil {
		return 0, err
	}
	return uint64(info.NumberOfLinks), nil
}
