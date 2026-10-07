//go:build !unix && !windows

package claudenative

import (
	"fmt"
	"os/exec"
)

type processTree struct{}

func startTree(*exec.Cmd) (*processTree, error) {
	return nil, fmt.Errorf("%w: no process tree containment on this platform", ErrUnavailable)
}

func (*processTree) kill() error { return nil }
func (*processTree) close()      {}
