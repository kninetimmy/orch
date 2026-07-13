package cli

import (
	"fmt"

	"github.com/kninetimmy/orch/internal/agents"
	"github.com/kninetimmy/orch/internal/config"
)

// runRenderAgents implements `orch render-agents` (PRD §22): it loads
// the effective configuration (config.Load: committed config.toml plus
// any config.local.toml overlay) and, when hosts.codex is enabled,
// renders the five Codex agent TOMLs into <repo>/.codex/agents/ from
// hosts.codex.roles — the mechanical alternative to hand-editing the
// installed TOMLs adapters/codex/README.md's install step 4 and known
// limitations both point to. It fails closed (config.Load already
// fails closed on a missing or invalid configuration) when the repo is
// not orch-initialized, the configuration is invalid, or hosts.codex
// is not enabled.
func runRenderAgents(env Env) error {
	cfg, err := config.Load(env.RepoRoot)
	if err != nil {
		return err
	}
	if cfg.Hosts.Codex == nil {
		return fmt.Errorf("hosts.codex is not enabled in configuration; enable it with `orch configure` before running `orch render-agents`")
	}

	files, err := agents.Render(cfg.Hosts.Codex)
	if err != nil {
		return err
	}
	if err := agents.Write(env.RepoRoot, files); err != nil {
		return err
	}
	for _, f := range files {
		fmt.Fprintf(env.Stdout, "wrote %s/%s.toml\n", agents.Dir, f.Name)
	}
	return nil
}
