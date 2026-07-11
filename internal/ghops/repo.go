package ghops

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
)

// Repo identifies the GitHub repository gh resolves from the primary
// checkout's remotes.
type Repo struct {
	// NameWithOwner is the owner/name slug, for example "octo/orch".
	NameWithOwner string
	// DefaultBranch is the repository's default branch name.
	DefaultBranch string
	// URL is the repository's web URL.
	URL string
}

// Repo resolves the GitHub repository from the checkout's remotes.
// Any non-zero gh exit maps to ErrNoGitHubRepo — gh does not
// distinguish "no remotes" from network failure by exit code, and
// both fail Delivery closed (PRD §5); the wrapped stderr names the
// actual cause for humans.
func (g *GH) Repo(ctx context.Context) (Repo, error) {
	var decoded struct {
		NameWithOwner    string `json:"nameWithOwner"`
		DefaultBranchRef struct {
			Name string `json:"name"`
		} `json:"defaultBranchRef"`
		URL string `json:"url"`
	}
	res, err := g.run(ctx, nil, "repo", "view", "--json", "nameWithOwner,defaultBranchRef,url")
	if err != nil {
		return Repo{}, err
	}
	if res.ExitCode != 0 {
		return Repo{}, fmt.Errorf("%w: %s", ErrNoGitHubRepo, strings.TrimSpace(res.Stderr))
	}
	if err := json.Unmarshal([]byte(res.Stdout), &decoded); err != nil {
		return Repo{}, fmt.Errorf("gh repo view in %s returned unparsable JSON: %w", g.root, err)
	}
	if decoded.NameWithOwner == "" {
		return Repo{}, fmt.Errorf("gh repo view in %s returned no nameWithOwner", g.root)
	}
	return Repo{
		NameWithOwner: decoded.NameWithOwner,
		DefaultBranch: decoded.DefaultBranchRef.Name,
		URL:           decoded.URL,
	}, nil
}
