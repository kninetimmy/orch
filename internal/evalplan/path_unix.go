//go:build linux || darwin

package evalplan

import (
	"fmt"
	"os"
	"syscall"
)

func reparse(info os.FileInfo) bool { return info.Mode()&os.ModeSymlink != 0 }

func linkCount(f *os.File) (uint64, error) {
	info, err := f.Stat()
	if err != nil {
		return 0, err
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return 0, fmt.Errorf("file link identity unavailable")
	}
	return uint64(stat.Nlink), nil
}
