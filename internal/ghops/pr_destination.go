package ghops

import (
	"net/url"
	"strconv"
	"strings"
)

// MatchesPullRequestRepository compares a canonical PR web URL recorded by the
// engine with one already-expanded Git destination. It establishes the
// hostname/slug, not a GitHub node ID or a new remote lookup. The caller must
// separately ensure the transport authenticates that destination.
func MatchesPullRequestRepository(prURL string, number int, endpoint string) bool {
	pr := repositoryURL(prURL)
	if pr == nil || pr.Scheme != "https" || pr.User != nil || number <= 0 {
		return false
	}
	slug, ok := strings.CutSuffix(strings.TrimPrefix(pr.Path, "/"), "/pull/"+strconv.Itoa(number))
	if !ok {
		return false
	}
	wantHost, wantOwner, wantRepo := repositorySelector(pr.Host, slug)
	if wantHost == "" {
		return false
	}

	var host, path string
	if scp, ok := strings.CutPrefix(endpoint, "git@"); ok {
		host, path, _ = strings.Cut(scp, ":")
	} else {
		remote := repositoryURL(endpoint)
		if remote == nil {
			return false
		}
		var defaultPort string
		switch remote.Scheme {
		case "https":
			defaultPort = "443"
		case "ssh":
			if remote.User == nil || remote.User.String() != "git" {
				return false
			}
			defaultPort = "22"
		default:
			return false
		}
		host = strings.TrimSuffix(remote.Host, ":"+defaultPort)
		path = strings.TrimPrefix(remote.Path, "/")
	}
	gotHost, gotOwner, gotRepo := repositorySelector(host, strings.TrimSuffix(path, ".git"))
	return gotHost == wantHost && gotOwner == wantOwner && gotRepo == wantRepo
}

func repositoryURL(raw string) *url.URL {
	u, err := url.Parse(raw)
	if err != nil || !strings.HasPrefix(raw, u.Scheme+"://") || u.Host == "" || u.RawPath != "" ||
		u.RawQuery != "" || u.ForceQuery || strings.Contains(raw, "#") {
		return nil
	}
	return u
}

func repositorySelector(host, slug string) (string, string, string) {
	nonASCII := func(r rune) bool { return r > 127 }
	if strings.ContainsFunc(host, nonASCII) || strings.ContainsFunc(slug, nonASCII) {
		return "", "", ""
	}
	host, slug = strings.ToLower(host), strings.ToLower(slug)
	owner, repo, ok := strings.Cut(slug, "/")
	if !ok || !validRepoPart(owner) || !validRepoPart(repo) || !repoHost.MatchString(host) {
		return "", "", ""
	}
	return host, owner, repo
}
