package cli

import (
	"fmt"
	"strings"

	"github.com/kninetimmy/orch/internal/config"
)

func runStatus(env Env) error {
	cfg, err := config.Load(env.RepoRoot)
	if err != nil {
		return err
	}
	fmt.Fprintln(env.Stdout, "mode:   assist (state machine not yet implemented)")
	fmt.Fprintf(env.Stdout, "config: %s (schema %d, revision %q)\n", config.Path, cfg.SchemaVersion, cfg.ConfigRevision)
	fmt.Fprintf(env.Stdout, "hosts:  %s\n", strings.Join(cfg.EnabledHosts(), ", "))
	return nil
}
