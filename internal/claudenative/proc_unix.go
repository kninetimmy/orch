//go:build unix

package claudenative

import (
	"errors"
	"os/exec"
	"syscall"
)

// processTree is the child's own process group. Claude Code and the tool
// processes it starts share it unless one deliberately starts a new session.
type processTree struct{ pgid int }

func startTree(cmd *exec.Cmd) (*processTree, error) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	return &processTree{pgid: cmd.Process.Pid}, nil
}

func (t *processTree) kill() error {
	if err := syscall.Kill(-t.pgid, syscall.SIGKILL); err != nil && !errors.Is(err, syscall.ESRCH) {
		return err
	}
	return nil
}

func (t *processTree) close() { _ = t.kill() }
