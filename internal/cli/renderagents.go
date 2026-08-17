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
// project agent definitions from hosts.<host>.roles. Every rendered
// output path is proved git-ignored before any file is written, so a
// two-host run cannot partially write one host before discovering the
// other is unsafe.
func runRenderAgents(env Env) error {
	cfg, err := config.Load(env.RepoRoot)
	if err != nil {
		return err
	}

	var files []agents.File
	for _, host := range cfg.EnabledHosts() {
		h := cfg.Host(host)
		rendered, err := agents.Render(host, h)
		if err != nil {
			return err
		}
		files = append(files, rendered...)
	}

	ctx := context.Background()
	git, err := gitops.Open(ctx, env.Runner, env.RepoRoot)
	if err != nil {
		return err
	}
	for _, f := range files {
		path := filepath.Join(env.RepoRoot, filepath.FromSlash(f.Path))
		if err := git.RequireIgnoredPath(ctx, path); err != nil {
			return fmt.Errorf("%w; run `orch configure` to add both rendered-agent destinations to .gitignore", err)
		}
	}
	if err := agents.Write(env.RepoRoot, files); err != nil {
		return err
	}
	for _, f := range files {
		fmt.Fprintf(env.Stdout, "wrote %s\n", f.Path)
	}
	return nil
}
