package cli

import (
	"errors"
	"fmt"
	"os"

	"github.com/kninetimmy/orch/internal/config"
	"github.com/kninetimmy/orch/internal/lockfile"
	"github.com/kninetimmy/orch/internal/state"
)

func runDoctor(env Env) error {
	failed := false
	check := func(name string, err error) {
		if err != nil {
			failed = true
			fmt.Fprintf(env.Stdout, "FAIL  %s: %v\n", name, err)
			return
		}
		fmt.Fprintf(env.Stdout, "ok    %s\n", name)
	}

	_, gitErr := env.LookPath("git")
	check("git on PATH", gitErr)

	_, ghErr := env.LookPath("gh")
	check("gh on PATH", ghErr)

	_, cfgErr := config.Load(env.RepoRoot)
	check("configuration", cfgErr)

	if config.HasLocalOverride(env.RepoRoot) {
		fmt.Fprintf(env.Stdout, "note  %s present; overrides are not yet applied\n", config.LocalOverridePath)
	}

	st, stErr := state.Load(env.RepoRoot)
	check("state file", stErr)

	owner, lockErr := lockfile.Inspect(env.RepoRoot)
	check("delivery lock", lockErr)

	if stErr == nil && lockErr == nil {
		check("state/lock consistency", state.CheckConsistent(st, owner))
	}

	if owner != nil {
		if hostname, err := os.Hostname(); err == nil && owner.Hostname == hostname && !lockfile.PIDAlive(owner.PID) {
			fmt.Fprintf(env.Stdout, "note  delivery lock: acquiring process (pid %d) is no longer running — normal between commands; if no Delivery run is active, run `orch abort`\n", owner.PID)
		}
	}

	if failed {
		return errors.New("one or more checks failed")
	}
	return nil
}
