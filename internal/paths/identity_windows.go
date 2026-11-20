package paths

import (
	"fmt"
	"os"
	"syscall"
)

// DirectoryID is a stable filesystem identity while this directory exists.
// It is not a hash of directory contents.
func DirectoryID(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer func() { _ = f.Close() }()
	var info syscall.ByHandleFileInformation
	if err := syscall.GetFileInformationByHandle(syscall.Handle(f.Fd()), &info); err != nil {
		return "", err
	}
	if info.FileAttributes&syscall.FILE_ATTRIBUTE_DIRECTORY == 0 || info.FileIndexHigh == 0 && info.FileIndexLow == 0 {
		return "", fmt.Errorf("directory identity unavailable for %s", path)
	}
	return fmt.Sprintf("%x:%08x%08x:%x", info.VolumeSerialNumber, info.FileIndexHigh, info.FileIndexLow, info.CreationTime.Nanoseconds()), nil
}
