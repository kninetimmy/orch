package cli

import (
	"errors"
	"fmt"

	"github.com/kninetimmy/orch/internal/config"
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

	if failed {
		return errors.New("one or more checks failed")
	}
	return nil
}
