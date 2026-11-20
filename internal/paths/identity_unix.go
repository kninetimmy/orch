//go:build unix

package paths

import (
	"fmt"
	"os"
	"syscall"
)

// DirectoryID is a stable filesystem identity while this directory exists.
// It is not a hash of directory contents.
func DirectoryID(path string) (string, error) {
	info, err := os.Stat(path)
	if err != nil {
		return "", err
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !info.IsDir() || !ok || stat.Ino == 0 {
		return "", fmt.Errorf("directory identity unavailable for %s", path)
	}
	return fmt.Sprintf("%x:%x", stat.Dev, stat.Ino), nil
}
