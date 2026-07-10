package cli

import (
	"fmt"
	"strings"
	"time"

	"github.com/kninetimmy/orch/internal/config"
	"github.com/kninetimmy/orch/internal/lockfile"
	"github.com/kninetimmy/orch/internal/state"
)

func runStatus(env Env) error {
	cfg, err := config.Load(env.RepoRoot)
	if err != nil {
		return err
	}
	st, err := state.Load(env.RepoRoot)
	if err != nil {
		return err
	}
	owner, err := lockfile.Inspect(env.RepoRoot)
	if err != nil {
		return err
	}

	if st.Mode == state.ModeDelivery {
		fmt.Fprintf(env.Stdout, "mode:   delivery (run %s, host %s, started %s)\n",
			st.Run.ID, st.Run.Host, st.Run.StartedAt.Format(time.RFC3339))
	} else {
		fmt.Fprintln(env.Stdout, "mode:   assist")
	}
	if owner != nil {
		fmt.Fprintf(env.Stdout, "lock:   held by %s on %s (pid %d, acquired %s)\n",
			owner.Host, owner.Hostname, owner.PID, owner.AcquiredAt.Format(time.RFC3339))
	}
	fmt.Fprintf(env.Stdout, "config: %s (schema %d, revision %q)\n", config.Path, cfg.SchemaVersion, cfg.ConfigRevision)
	fmt.Fprintf(env.Stdout, "hosts:  %s\n", strings.Join(cfg.EnabledHosts(), ", "))

	// Status inspects and reports; doctor is the check that fails.
	if err := state.CheckConsistent(st, owner); err != nil {
		fmt.Fprintf(env.Stdout, "warning: %v\n", err)
	}
	return nil
}
