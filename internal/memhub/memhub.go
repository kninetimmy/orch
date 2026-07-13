// Package memhub is the mechanical PRD §20 client for the external
// memhub CLI. It observes two disciplines: every command runs with
// the primary checkout as its explicit working directory — never a
// worktree, since worktrees never get a memhub DB copy — and the
// package is policy-free. Mode gating (required/best-effort/off)
// stays with callers (internal/run's memhubGate); memhub only reads
// memhub state here and never writes, renders, reindexes, or syncs.
package memhub

import (
	"context"
	"fmt"
	"strings"

	"github.com/kninetimmy/orch/internal/execx"
)

// Client runs read-only memhub CLI commands against one repository
// through an injected execx.Runner.
type Client struct {
	runner execx.Runner
	// dir is the primary checkout root. It must never be a worktree —
	// worktrees never get a memhub DB copy (PRD §20).
	dir string
}

// New binds a Client to the repository whose primary checkout root is
// dir.
func New(runner execx.Runner, dir string) Client {
	return Client{runner: runner, dir: dir}
}

// Probe runs `memhub status` in the primary checkout and reports
// whether memhub is reachable and healthy. A spawn error is returned
// unwrapped; a non-zero exit becomes an error naming the exit code
// and memhub's trimmed stderr.
func (c Client) Probe(ctx context.Context) error {
	res, err := c.runner.Run(ctx, execx.Cmd{Name: "memhub", Args: []string{"status"}, Dir: c.dir})
	if err != nil {
		return err
	}
	if res.ExitCode != 0 {
		return fmt.Errorf("memhub status exited %d: %s", res.ExitCode, strings.TrimSpace(res.Stderr))
	}
	return nil
}
