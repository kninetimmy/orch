package cli

import (
	"fmt"

	"github.com/kninetimmy/orch/internal/config"
	"github.com/kninetimmy/orch/internal/state"
)

// runAbort returns the repository to Assist without deleting any work
// (PRD §15). Releasing a lock held by another owner is deliberate:
// abort is the explicit human takeover, and the released owner is
// printed so the takeover is visible.
func runAbort(env Env) error {
	if _, err := config.Load(env.RepoRoot); err != nil {
		return err
	}
	res, err := state.Abort(env.RepoRoot)
	if err != nil {
		return err
	}

	if res.LockOwner != nil {
		fmt.Fprintf(env.Stdout, "released delivery lock held by %s on %s (pid %d)\n",
			res.LockOwner.Host, res.LockOwner.Hostname, res.LockOwner.PID)
	} else if res.LockCleared {
		fmt.Fprintln(env.Stdout, "cleared unreadable delivery lock")
	}

	switch {
	case res.PriorRun != nil:
		fmt.Fprintf(env.Stdout, "aborted delivery run %s; returned to assist (branches and worktrees preserved)\n", res.PriorRun.ID)
	case res.StateReset:
		fmt.Fprintln(env.Stdout, "reset unreadable state file to assist")
	case !res.LockCleared:
		fmt.Fprintln(env.Stdout, "already in assist; nothing to abort")
	}
	return nil
}
