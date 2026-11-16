//go:build !windows

package gitops

import "os"

func ordinaryWorktreeEntry(info os.FileInfo) bool {
	return info.Mode().Type() == 0 || info.Mode().Type() == os.ModeDir
}
