//go:build windows

package codexnative

import (
	"os"
	"syscall"
)

func trialSingleLink(file *os.File) bool {
	var info syscall.ByHandleFileInformation
	return syscall.GetFileInformationByHandle(syscall.Handle(file.Fd()), &info) == nil && info.NumberOfLinks == 1
}
