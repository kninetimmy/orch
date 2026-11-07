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
	fmt.Fprintf(env.Stdout, "orch:   %s\n", Version)

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
	if len(cfg.Overrides) > 0 {
		fmt.Fprintf(env.Stdout, "local:  %d override(s) from %s\n", len(cfg.Overrides), config.LocalOverridePath)
	}
	fmt.Fprintf(env.Stdout, "hosts:  %s\n", strings.Join(cfg.EnabledHosts(), ", "))
	fmt.Fprintf(env.Stdout, "protected survey: %s\n", protectedListStatus(cfg))

	// Status inspects and reports; doctor is the check that fails.
	if err := state.CheckConsistent(st, owner); err != nil {
		fmt.Fprintf(env.Stdout, "warning: %v\n", err)
	}
	return nil
}

func protectedListStatus(cfg *config.Config) string {
	status := "absent; approved methodology: none"
	if p := cfg.Protected; p != nil {
		status = fmt.Sprintf("present; repository-specific entries: %d; approved methodology: %d", len(p.Paths), p.MethodologyVersion)
	}
	status += fmt.Sprintf("; required methodology: %d", config.SurveyMethodologyVersion)
	if cfg.CheckGrantProtected() != nil {
		return status + "; fresh survey required; ask your agent to configure Orch using its setup workflow"
	}
	return status + "; survey current"
}
