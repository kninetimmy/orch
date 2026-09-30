//go:build windows

package codexnative

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"unsafe"
)

var finalPathName = syscall.NewLazyDLL("kernel32.dll").NewProc("GetFinalPathNameByHandleW")

// EvalSymlinks alone does not resolve Windows junctions on the supported Go
// runtime. Ask Windows for the final handle path, including long names for 8.3
// aliases. For a missing deny target, resolve its deepest existing ancestor.
func finalIsolationPath(path string) (string, error) {
	dir := path
	var rest []string
	for {
		name, err := syscall.UTF16PtrFromString(dir)
		if err != nil {
			return "", err
		}
		handle, err := syscall.CreateFile(name, 0x80, syscall.FILE_SHARE_READ|syscall.FILE_SHARE_WRITE|syscall.FILE_SHARE_DELETE, nil, syscall.OPEN_EXISTING, syscall.FILE_FLAG_BACKUP_SEMANTICS, 0)
		if err == nil {
			defer syscall.CloseHandle(handle)
			buffer := make([]uint16, 32768)
			n, _, callErr := finalPathName.Call(uintptr(handle), uintptr(unsafe.Pointer(&buffer[0])), uintptr(len(buffer)), 0)
			if n == 0 || n >= uintptr(len(buffer)) {
				return "", callErr
			}
			resolved := syscall.UTF16ToString(buffer[:n])
			if !strings.HasPrefix(resolved, `\\?\`) || strings.HasPrefix(resolved, `\\?\UNC\`) {
				return "", errors.New("unsupported final Windows path namespace")
			}
			resolved = strings.TrimPrefix(resolved, `\\?\`)
			for i := len(rest) - 1; i >= 0; i-- {
				resolved = filepath.Join(resolved, rest[i])
			}
			return filepath.Clean(resolved), nil
		}
		if !errors.Is(err, os.ErrNotExist) {
			return "", err
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", err
		}
		rest = append(rest, filepath.Base(dir))
		dir = parent
	}
}
