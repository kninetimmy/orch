package gitops

import (
	"path/filepath"

	"golang.org/x/sys/windows"
)

func systemOpenSSH() (executable, nullConfig string, err error) {
	dir, err := windows.GetSystemDirectory()
	return filepath.Join(dir, "OpenSSH", "ssh.exe"), "NUL", err
}
