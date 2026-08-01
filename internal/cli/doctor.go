package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/kninetimmy/orch/adapters/claude"
	"github.com/kninetimmy/orch/adapters/codex"
	"github.com/kninetimmy/orch/internal/agents"
	"github.com/kninetimmy/orch/internal/config"
	"github.com/kninetimmy/orch/internal/execx"
	"github.com/kninetimmy/orch/internal/ghops"
	"github.com/kninetimmy/orch/internal/gitops"
	"github.com/kninetimmy/orch/internal/lockfile"
	"github.com/kninetimmy/orch/internal/memhub"
	"github.com/kninetimmy/orch/internal/metrics"
	"github.com/kninetimmy/orch/internal/state"
)

type adapterSpec struct {
	host         string
	executable   string
	pluginID     string
	manifestJSON string
	repair       string
}

var (
	claudeAdapter = adapterSpec{
		host:         "claude",
		executable:   "claude",
		pluginID:     "orch-claude@orch",
		manifestJSON: claude.PluginManifestJSON,
		repair:       "run `claude plugin marketplace update orch`, then `claude plugin update orch-claude@orch`, then restart Claude Code",
	}
	codexAdapter = adapterSpec{
		host:         "codex",
		executable:   "codex",
		pluginID:     "orch@orch",
		manifestJSON: codex.PluginManifestJSON,
		repair:       "run `codex plugin marketplace upgrade orch`, then restart Codex CLI",
	}
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

	if cfgErr == nil {
		if cfg.Hosts.Claude != nil {
			check("claude adapter", checkAdapter(env, claudeAdapter))
		}
		if cfg.Hosts.Codex != nil {
			check("codex adapter", checkAdapter(env, codexAdapter))
		}
	}

	if cfgErr == nil && cfg.Hosts.Codex != nil {
		stale, staleErr := agents.Stale(env.RepoRoot, cfg.Hosts.Codex)
		switch {
		case len(stale) > 0:
			detail := strings.Join(stale, ", ")
			if staleErr != nil {
				detail += fmt.Sprintf(" (%v)", staleErr)
			}
			check("codex agent files", fmt.Errorf("absent, unreadable, or out of date: %s; run `orch render-agents` to regenerate them", detail))
		case staleErr != nil:
			check("codex agent files", staleErr)
		default:
			check("codex agent files", nil)
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

type adapterPlugin struct {
	ID        string `json:"id"`
	PluginID  string `json:"pluginId"`
	Version   string `json:"version"`
	Installed bool   `json:"installed"`
	Enabled   bool   `json:"enabled"`
}

func checkAdapter(env Env, spec adapterSpec) error {
	fail := func(detail string) error {
		return fmt.Errorf("%s; %s", detail, spec.repair)
	}

	expected, err := adapterVersion(spec.manifestJSON)
	if err != nil {
		return fail(fmt.Sprintf("read shipped %s adapter version: %v", spec.host, err))
	}
	if _, err := env.LookPath(spec.executable); err != nil {
		return fail(fmt.Sprintf("%s not found on PATH (expected adapter version %q); install it or adjust PATH: %v", spec.executable, expected, err))
	}

	res, err := env.Runner.Run(context.Background(), execx.Cmd{
		Name: spec.executable,
		Args: []string{"plugin", "list", "--json"},
		Dir:  env.RepoRoot,
	})
	command := spec.executable + " plugin list --json"
	if err != nil {
		return fail(fmt.Sprintf("run `%s`: %v", command, err))
	}
	if res.ExitCode != 0 {
		detail := strings.TrimSpace(res.Stderr)
		if detail == "" {
			detail = "no error output"
		}
		return fail(fmt.Sprintf("`%s` exited %d: %s", command, res.ExitCode, detail))
	}

	plugins, err := decodeAdapterPlugins(spec.host, []byte(res.Stdout))
	if err != nil {
		return fail(fmt.Sprintf("malformed `%s` output: %v", command, err))
	}

	var matches []adapterPlugin
	for _, plugin := range plugins {
		id := plugin.ID
		if spec.host == "codex" {
			id = plugin.PluginID
		}
		if id == spec.pluginID {
			matches = append(matches, plugin)
		}
	}

	switch len(matches) {
	case 0:
		return fail(fmt.Sprintf("%s is absent (expected version %q)", spec.pluginID, expected))
	case 1:
		// Continue below.
	default:
		var versions []string
		for _, plugin := range matches {
			if plugin.Version != "" {
				versions = append(versions, plugin.Version)
			}
		}
		if len(versions) > 0 {
			return fail(fmt.Sprintf("%s appears %d times (installed versions %q, expected %q); exactly one is required", spec.pluginID, len(matches), versions, expected))
		}
		return fail(fmt.Sprintf("%s appears %d times; exactly one at version %q is required", spec.pluginID, len(matches), expected))
	}

	plugin := matches[0]
	versionDetail := fmt.Sprintf("expected %q", expected)
	if plugin.Version != "" {
		versionDetail = fmt.Sprintf("installed %q, expected %q", plugin.Version, expected)
	}
	if spec.host == "codex" && !plugin.Installed {
		return fail(fmt.Sprintf("%s is marked not installed (%s)", spec.pluginID, versionDetail))
	}
	if !plugin.Enabled {
		return fail(fmt.Sprintf("%s is disabled (%s)", spec.pluginID, versionDetail))
	}
	if plugin.Version == "" {
		return fail(fmt.Sprintf("%s has no version (expected %q)", spec.pluginID, expected))
	}
	if plugin.Version != expected {
		return fail(fmt.Sprintf("%s version mismatch: installed %q, expected %q", spec.pluginID, plugin.Version, expected))
	}
	return nil
}

func adapterVersion(manifestJSON string) (string, error) {
	var manifest struct {
		Version string `json:"version"`
	}
	if err := json.Unmarshal([]byte(manifestJSON), &manifest); err != nil {
		return "", err
	}
	if manifest.Version == "" {
		return "", errors.New("manifest has no version")
	}
	return manifest.Version, nil
}

func decodeAdapterPlugins(host string, data []byte) ([]adapterPlugin, error) {
	if host == "claude" {
		var plugins []adapterPlugin
		if err := json.Unmarshal(data, &plugins); err != nil {
			return nil, err
		}
		if plugins == nil {
			return nil, errors.New("expected a top-level array")
		}
		return plugins, nil
	}

	var listing struct {
		Installed []adapterPlugin `json:"installed"`
	}
	if err := json.Unmarshal(data, &listing); err != nil {
		return nil, err
	}
	if listing.Installed == nil {
		return nil, errors.New(`expected an object with an "installed" array`)
	}
	return listing.Installed, nil
}
