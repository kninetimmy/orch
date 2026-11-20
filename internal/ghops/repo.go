package ghops

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"regexp"
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

// RepoTarget is the canonical, case-insensitive GitHub repository selector.
type RepoTarget struct {
	Host       string `json:"host"`
	Owner      string `json:"owner"`
	Repository string `json:"repository"`
}

func (t RepoTarget) String() string { return t.Host + "/" + t.Owner + "/" + t.Repository }

var (
	repoHost = regexp.MustCompile(`^[a-z0-9](?:[a-z0-9.-]*[a-z0-9])?$`)
	repoPart = regexp.MustCompile(`^[a-z0-9_.-]+$`)
)

func (t RepoTarget) Valid() bool {
	validPart := func(s string) bool { return s != "." && s != ".." && repoPart.MatchString(s) }
	return repoHost.MatchString(t.Host) && validPart(t.Owner) && validPart(t.Repository)
}

// Target derives an explicit gh selector from the observed repository, not a
// configured remote URL or request parameter. The URL and slug must agree.
func (r Repo) Target() (RepoTarget, error) {
	u, err := url.Parse(r.URL)
	owner, name, ok := strings.Cut(strings.ToLower(r.NameWithOwner), "/")
	if err == nil && ok && u.Scheme == "https" && u.User == nil && u.RawQuery == "" &&
		!u.ForceQuery && u.Fragment == "" && u.RawPath == "" && strings.EqualFold(u.Path, "/"+r.NameWithOwner) {
		target := RepoTarget{Host: strings.ToLower(u.Host), Owner: owner, Repository: name}
		if target.Valid() {
			return target, nil
		}
	}
	return RepoTarget{}, fmt.Errorf("%w: gh repo view did not return a verifiable GitHub host/owner/repository", ErrNoGitHubRepo)
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
