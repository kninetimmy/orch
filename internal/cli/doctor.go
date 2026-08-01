package cli

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/kninetimmy/orch/internal/agents"
	"github.com/kninetimmy/orch/internal/config"
	"github.com/kninetimmy/orch/internal/ghops"
	"github.com/kninetimmy/orch/internal/gitops"
	"github.com/kninetimmy/orch/internal/lockfile"
	"github.com/kninetimmy/orch/internal/memhub"
	"github.com/kninetimmy/orch/internal/metrics"
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

			// Best-effort per task 39: a broken network, missing
			// release, or malformed response degrades to a note and
			// never fails doctor. Skipped entirely on unstamped "dev"
			// builds, which have nothing meaningful to compare.
			if Version != "dev" {
				latest, relErr := ghops.LatestRelease(context.Background(), env.Runner, env.RepoRoot)
				switch {
				case relErr != nil:
					fmt.Fprintf(env.Stdout, "note  release check: %v\n", relErr)
				case latest != Version:
					fmt.Fprintf(env.Stdout, "note  newer release available: installed %s, latest %s; re-run the installer to update\n", Version, latest)
				default:
					fmt.Fprintf(env.Stdout, "ok    release: up to date (%s)\n", Version)
				}
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

	// Metrics-ignore trap (only meaningful, and only checked, when the
	// effective configuration has metrics enabled): a repository already
	// in this state gets no chance to self-report it through a failing
	// RequireClean gate until metrics has already written an untracked
	// file, so doctor names it directly.
	if cfgErr == nil && cfg.Metrics.Enabled {
		trapped, trapErr := metrics.TrapActive(cfg.Metrics.Enabled, env.RepoRoot)
		switch {
		case trapErr != nil:
			check("metrics .gitignore", trapErr)
		case trapped:
			check("metrics .gitignore", fmt.Errorf("metrics is enabled but %s is missing from .gitignore; run `orch configure` to add it", metrics.GitignoreLine))
		default:
			check("metrics .gitignore", nil)
		}
	}

	// Rendered codex agent files (only meaningful, and only checked,
	// when hosts.codex is in the effective configuration): the files a
	// Codex session actually loads are machine-local, so a build upgrade
	// or a roles change leaves them silently behind until doctor says so.
	if cfgErr == nil && cfg.Hosts.Codex != nil {
		stale, staleErr := agents.Stale(env.RepoRoot, cfg.Hosts.Codex)
		switch {
		case staleErr != nil:
			check("codex agent files", staleErr)
		case len(stale) > 0:
			check("codex agent files", fmt.Errorf("absent or out of date: %s; run `orch render-agents` to regenerate them", strings.Join(stale, ", ")))
		default:
			check("codex agent files", nil)
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
