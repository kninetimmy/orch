package cli

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/kninetimmy/orch/internal/claudetranscript"
	"github.com/kninetimmy/orch/internal/execx"
)

// claudeVersionTimeout bounds `claude --version` (about 0.15 s) so a hung
// binary cannot stall a session-start hook.
const claudeVersionTimeout = 5 * time.Second

// installedClaudeVersion returns the Claude Code version `claude --version`
// reports, whose output reads like "2.1.301 (Claude Code)".
func installedClaudeVersion(env Env) (string, error) {
	if _, err := env.LookPath("claude"); err != nil {
		return "", fmt.Errorf("claude not found on PATH: %w", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), claudeVersionTimeout)
	defer cancel()
	res, err := env.Runner.Run(ctx, execx.Cmd{Name: "claude", Args: []string{"--version"}, Dir: env.RepoRoot})
	if err != nil {
		return "", err
	}
	if res.ExitCode != 0 {
		return "", fmt.Errorf("`claude --version` exited %d: %s", res.ExitCode, strings.TrimSpace(res.Stderr))
	}
	fields := strings.Fields(res.Stdout)
	if len(fields) == 0 {
		return "", errors.New("`claude --version` printed nothing")
	}
	return fields[0], nil
}

// claudeVersionProblem checks the installed Claude Code against the versions
// the transcript reader recognizes. problem is "" when it is recognized;
// otherwise it says why the context-relay instruction cannot fire.
func claudeVersionProblem(env Env) (version, problem string) {
	version, err := installedClaudeVersion(env)
	switch {
	case err != nil:
		return "", fmt.Sprintf("the installed Claude Code version could not be determined (%v)", err)
	case !claudetranscript.Recognized(version):
		return version, fmt.Sprintf("the installed Claude Code version %s is not one the transcript reader recognizes", version)
	}
	return version, ""
}
