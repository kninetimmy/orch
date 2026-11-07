package paths

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"syscall"
)

// EvalSymlinks can leave Windows mount points unresolved. Never trust such an
// ancestor lexically; nonredirecting reparse tags do not change path identity.
func verifyCanonicalPath(path string) (resultErr error) {
	for current := path; ; current = filepath.Dir(current) {
		info, err := os.Lstat(current)
		if err == nil {
			attrs, ok := info.Sys().(*syscall.Win32FileAttributeData)
			if !ok {
				return fmt.Errorf("Windows path attributes unavailable: %s", current)
			}
			if attrs.FileAttributes&syscall.FILE_ATTRIBUTE_REPARSE_POINT != 0 {
				name, err := syscall.UTF16PtrFromString(current)
				if err != nil {
					return err
				}
				var data syscall.Win32finddata
				handle, err := syscall.FindFirstFile(name, &data)
				if err != nil {
					return fmt.Errorf("verify Windows reparse point %s: %w", current, err)
				}
				defer func(handle syscall.Handle) {
					resultErr = errors.Join(resultErr, syscall.FindClose(handle))
				}(handle)
				if redirectingReparse(data.FileAttributes, data.Reserved0) {
					return fmt.Errorf("unresolved Windows redirecting reparse point: %s", current)
				}
			}
		} else if !errors.Is(err, fs.ErrNotExist) {
			return err
		}
		if filepath.Dir(current) == current {
			return nil
		}
	}
}

func redirectingReparse(attributes, tag uint32) bool {
	// Windows marks redirecting (name-surrogate) tags with this bit.
	return attributes&syscall.FILE_ATTRIBUTE_REPARSE_POINT != 0 && tag&0x20000000 != 0
}
