package cli

import (
	"context"
	"fmt"
	"path/filepath"

	"github.com/kninetimmy/orch/internal/agents"
	"github.com/kninetimmy/orch/internal/config"
	"github.com/kninetimmy/orch/internal/gitops"
)

// runRenderAgents implements `orch render-agents` (PRD §22): it loads
// the effective configuration and renders every enabled host's five
// project agent definitions from hosts.<host>.roles. Every destination
// is proved git-ignored before any file is written, so a two-host run
// cannot partially write one host before discovering the other is
// unsafe.
func runRenderAgents(env Env) error {
	cfg, err := config.Load(env.RepoRoot)
	if err != nil {
		return err
	}

	ctx := context.Background()
	git, err := gitops.Open(ctx, env.Runner, env.RepoRoot)
	if err != nil {
		return err
	}
	for _, host := range cfg.EnabledHosts() {
		destination, err := agents.Destination(host)
		if err != nil {
			return err
		}
		dir := filepath.Join(env.RepoRoot, filepath.FromSlash(destination))
		if err := git.RequireIgnored(ctx, dir); err != nil {
			return fmt.Errorf("%w; run `orch configure` to add both rendered-agent destinations to .gitignore", err)
		}
	}

	var files []agents.File
	for _, host := range cfg.EnabledHosts() {
		h := cfg.Hosts.Claude
		if host == "codex" {
			h = cfg.Hosts.Codex
		}
		rendered, err := agents.Render(host, h)
		if err != nil {
			return err
		}
		files = append(files, rendered...)
	}
	if err := agents.Write(env.RepoRoot, files); err != nil {
		return err
	}
	for _, f := range files {
		fmt.Fprintf(env.Stdout, "wrote %s\n", f.Path)
	}
	return nil
}
