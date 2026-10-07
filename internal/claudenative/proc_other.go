//go:build !unix && !windows

package claudenative

import (
	"fmt"
	"os"
	"os/exec"
)

type processTree struct{}

func startTree(*exec.Cmd) (*processTree, error) {
	return nil, fmt.Errorf("%w: no process tree containment on this platform", ErrUnavailable)
}

func (*processTree) kill() error { return nil }
func (*processTree) close()      {}

// reparsePoint treats every entry as unverifiable on other platforms.
func reparsePoint(os.FileInfo) bool { return true }
