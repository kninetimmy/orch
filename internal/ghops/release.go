package ghops

import (
	"context"
	"fmt"
	"strings"

	"github.com/kninetimmy/orch/internal/execx"
)

// UpstreamRepo is the fixed github.com/kninetimmy/orch repository
// whose release cadence orch checks itself against. It is never the
// working repository (which may be an unrelated project, or orch
// itself under active development), so LatestRelease takes no GH
// receiver and does not resolve a repo from the working directory.
const UpstreamRepo = "kninetimmy/orch"

// LatestRelease returns the tag name of UpstreamRepo's latest
// published release via `gh api .../releases/latest`, read through
// --jq so the tag is extracted machine-side rather than parsed from
// human-readable output. It is a standalone function rather than a GH
// method: the query is fixed to UpstreamRepo regardless of which
// repository dir belongs to, so it needs only an injected
// execx.Runner and a working directory for the invocation (gh
// requires one; the query itself does not depend on it). Any error —
// gh missing, unauthenticated, offline, or a malformed response — is
// returned as data for the caller to degrade, never panics or exits.
func LatestRelease(ctx context.Context, r execx.Runner, dir string) (string, error) {
	res, err := r.Run(ctx, execx.Cmd{
		Name: "gh",
		Args: []string{"api", "repos/" + UpstreamRepo + "/releases/latest", "--jq", ".tag_name"},
		Dir:  dir,
		Env:  ghEnv,
	})
	if err != nil {
		return "", err
	}
	if res.ExitCode != 0 {
		return "", fmt.Errorf("gh api repos/%s/releases/latest exited %d: %s", UpstreamRepo, res.ExitCode, strings.TrimSpace(res.Stderr))
	}
	tag := strings.TrimSpace(res.Stdout)
	if tag == "" {
		return "", fmt.Errorf("gh api repos/%s/releases/latest returned no tag_name", UpstreamRepo)
	}
	return tag, nil
}
