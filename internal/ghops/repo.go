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
	// ID is GitHub's repository node ID, requested only for Delivery target binding.
	ID string
}

// RepoTarget pairs the canonical, case-insensitive selector with the exact
// repository node ID. A different repository can take over the same name.
type RepoTarget struct {
	Host       string `json:"host"`
	Owner      string `json:"owner"`
	Repository string `json:"repository"`
	ID         string `json:"id"`
}

func (t RepoTarget) String() string { return t.Host + "/" + t.Owner + "/" + t.Repository }

var (
	repoHost = regexp.MustCompile(`^[a-z0-9](?:[a-z0-9.-]*[a-z0-9])?$`)
	repoPart = regexp.MustCompile(`^[a-z0-9_.-]+$`)
	// Accept opaque tokens in both GitHub node ID alphabets (legacy and
	// current). Never decode, trim, or case-fold an ID to compare it.
	githubNodeID = regexp.MustCompile(`^[A-Za-z0-9_+/-]+={0,2}$`)
)

func (t RepoTarget) Valid() bool {
	validPart := func(s string) bool { return s != "." && s != ".." && repoPart.MatchString(s) }
	return repoHost.MatchString(t.Host) && validPart(t.Owner) && validPart(t.Repository) && githubNodeID.MatchString(t.ID)
}

// Target derives the approval identity from one observed repository, not a
// configured remote URL or request parameter. The URL and slug must agree.
func (r Repo) Target() (RepoTarget, error) {
	if !githubNodeID.MatchString(r.ID) {
		return RepoTarget{}, fmt.Errorf("%w: gh repo view returned a missing or malformed GitHub repository ID", ErrNoGitHubRepo)
	}
	u, err := url.Parse(r.URL)
	owner, name, ok := strings.Cut(strings.ToLower(r.NameWithOwner), "/")
	if err == nil && ok && u.Scheme == "https" && u.User == nil && u.RawQuery == "" &&
		!u.ForceQuery && u.Fragment == "" && u.RawPath == "" && strings.EqualFold(u.Path, "/"+r.NameWithOwner) {
		target := RepoTarget{Host: strings.ToLower(u.Host), Owner: owner, Repository: name, ID: r.ID}
		if target.Valid() {
			return target, nil
		}
	}
	return RepoTarget{}, fmt.Errorf("%w: gh repo view did not return a verifiable GitHub host/owner/repository", ErrNoGitHubRepo)
}

const repoViewFields = "nameWithOwner,defaultBranchRef,url"

// Repo resolves the GitHub repository from the checkout's remotes.
func (g *GH) Repo(ctx context.Context) (Repo, error) {
	return g.viewRepo(ctx, repoViewFields)
}

// RepoWithID requests the node ID alongside the visible target, so the ID
// and selector come from the same read. Repo.Target validates both.
func (g *GH) RepoWithID(ctx context.Context) (Repo, error) {
	return g.viewRepo(ctx, repoViewFields+",id")
}

// Any non-zero gh exit maps to ErrNoGitHubRepo — gh does not
// distinguish "no remotes" from network failure by exit code, and
// both fail Delivery closed (PRD §5); the wrapped stderr names the
// actual cause for humans.
func (g *GH) viewRepo(ctx context.Context, fields string) (Repo, error) {
	var decoded struct {
		NameWithOwner    string `json:"nameWithOwner"`
		DefaultBranchRef struct {
			Name string `json:"name"`
		} `json:"defaultBranchRef"`
		URL string `json:"url"`
		ID  string `json:"id"`
	}
	res, err := g.run(ctx, nil, "repo", "view", "--json", fields)
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
		ID:            decoded.ID,
	}, nil
}
