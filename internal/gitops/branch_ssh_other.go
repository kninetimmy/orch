//go:build !windows

package gitops

func systemOpenSSH() (executable, nullConfig string, err error) {
	return "/usr/bin/ssh", "/dev/null", nil
}
