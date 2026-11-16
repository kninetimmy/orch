package gitops

import (
	"os"
	"syscall"
)

func ordinaryWorktreeEntry(info os.FileInfo) bool {
	attrs, ok := info.Sys().(*syscall.Win32FileAttributeData)
	// Lstat's type alone does not expose every reparse tag. No reparse tag is
	// qualified for destructive traversal here, including non-name-surrogates.
	return ok && attrs != nil && attrs.FileAttributes&syscall.FILE_ATTRIBUTE_REPARSE_POINT == 0 &&
		(info.Mode().Type() == 0 || info.Mode().Type() == os.ModeDir)
}
