//go:build !windows && !linux && !darwin

package evalplan

import (
	"fmt"
	"os"
)

func reparse(os.FileInfo) bool { return true }

func linkCount(*os.File) (uint64, error) {
	return 0, fmt.Errorf("file link verification unavailable on this platform")
}
