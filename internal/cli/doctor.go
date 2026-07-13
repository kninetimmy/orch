package cli

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/kninetimmy/orch/internal/config"
	"github.com/kninetimmy/orch/internal/ghops"
	"github.com/kninetimmy/orch/internal/gitops"
	"github.com/kninetimmy/orch/internal/lockfile"
	"github.com/kninetimmy/orch/internal/memhub"
	"github.com/kninetimmy/orch/internal/state"
)

func runDoctor(env Env) error {
	fmt.Fprintf(env.Stdout, "note  orch version: %s\n", Version)

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

	if gitErr == nil {
		// gitops.Open also verifies .orchestrator/ sits at the git
		// top level, which the containment guarantees depend on.
		_, repoErr := gitops.Open(context.Background(), env.Runner, env.RepoRoot)
		check("git repository", repoErr)
	}

	_, ghErr := env.LookPath("gh")
	check("gh on PATH", ghErr)

	if ghErr == nil {
		gh, authErr := ghops.Open(context.Background(), env.Runner, env.RepoRoot)
		check("gh authentication", authErr)
		if authErr == nil {
			repo, repoErr := gh.Repo(context.Background())
			switch {
			case errors.Is(repoErr, ghops.ErrNoGitHubRepo):
				// Assist works without a remote (PRD §5); Delivery
				// preflight fails closed on this same probe.
				fmt.Fprintf(env.Stdout, "note  no GitHub repository resolved; Assist works without one, Delivery will fail closed (%v)\n", repoErr)
			case repoErr != nil:
				check("gh repository", repoErr)
			default:
				fmt.Fprintf(env.Stdout, "ok    gh repository: %s\n", repo.NameWithOwner)
			}
		}
	}

	cfg, cfgErr := config.Load(env.RepoRoot)
	check("configuration", cfgErr)

	if cfgErr == nil && config.HasLocalOverride(env.RepoRoot) {
		if len(cfg.Overrides) > 0 {
			fmt.Fprintf(env.Stdout, "note  %s applied; overrides: %s\n", config.LocalOverridePath, strings.Join(cfg.Overrides, ", "))
		} else {
			fmt.Fprintf(env.Stdout, "note  %s present; no overrides set\n", config.LocalOverridePath)
		}
	}

	if cfgErr == nil {
		switch cfg.Memhub.Mode {
		case "off":
			fmt.Fprintf(env.Stdout, "note  memhub: skipped (mode off)\n")
		default:
			mh := memhub.New(env.Runner, env.RepoRoot)
			mhErr := mh.Probe(context.Background())
			if mhErr == nil {
				// Health succeeded; only now does recall run, mirroring
				// the plan/activate gate's skip-recall-on-health-failure
				// rule (PRD §20): recall against a memhub whose status
				// already failed tells us nothing new.
				mhErr = mh.Recall(context.Background())
			}
			if cfg.Memhub.Mode == "required" {
				check("memhub", mhErr)
			} else if mhErr != nil {
				fmt.Fprintf(env.Stdout, "note  memhub: %v\n", mhErr)
			} else {
				fmt.Fprintf(env.Stdout, "ok    memhub\n")
			}
		}
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
