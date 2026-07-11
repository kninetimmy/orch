package ghops

import (
	"context"
	"fmt"
	"strconv"
	"strings"
)

// Issue is the read-back view of a GitHub issue.
type Issue struct {
	Number int
	// State is GitHub's issue state, "OPEN" or "CLOSED".
	State  string
	Title  string
	URL    string
	Labels []string
	// Body is the raw issue body (the PRD §13 audit record), which
	// resume/recovery parses back into run state (PRD §23).
	Body string
}

// CreateIssue creates an issue carrying the full PRD §13 taxonomy.
// The body (the audit record) is opaque to ghops and travels over
// stdin, never the command line. Labels are validated before any gh
// call; forbiddenAreas is the caller's model-name denylist (PRD §13:
// models do not become GitHub labels).
func (g *GH) CreateIssue(ctx context.Context, title, body string, labels Labels, forbiddenAreas ...string) (number int, url string, err error) {
	flat, err := labels.flatten(forbiddenAreas...)
	if err != nil {
		return 0, "", err
	}
	args := []string{"issue", "create", "--title", title, "--body-file", "-"}
	for _, l := range flat {
		// One --label flag per value: gh splits the comma form on
		// commas, which would corrupt area labels containing one.
		args = append(args, "--label", l)
	}
	// With a non-TTY stdout gh prints exactly the created issue URL.
	out, err := g.ghStdin(ctx, strings.NewReader(body), args...)
	if err != nil {
		return 0, "", err
	}
	number, err = parseNumberFromURL(out, "issues")
	if err != nil {
		return 0, "", err
	}
	return number, out, nil
}

// Issue reads one issue; the run engine uses it to confirm closure
// (PRD §12 step 17).
func (g *GH) Issue(ctx context.Context, number int) (Issue, error) {
	var decoded struct {
		Number int    `json:"number"`
		State  string `json:"state"`
		Title  string `json:"title"`
		URL    string `json:"url"`
		Labels []struct {
			Name string `json:"name"`
		} `json:"labels"`
		Body string `json:"body"`
	}
	if err := g.ghJSON(ctx, &decoded, "issue", "view", strconv.Itoa(number), "--json", "number,state,title,url,labels,body"); err != nil {
		return Issue{}, err
	}
	if decoded.Number != number {
		return Issue{}, fmt.Errorf("gh issue view %d in %s returned issue %d", number, g.root, decoded.Number)
	}
	names := make([]string, len(decoded.Labels))
	for i, l := range decoded.Labels {
		names[i] = l.Name
	}
	return Issue{Number: decoded.Number, State: decoded.State, Title: decoded.Title, URL: decoded.URL, Labels: names, Body: decoded.Body}, nil
}

// SetIssueBody replaces the issue body (the PRD §13 audit record is
// updated as routing decisions and verification results accumulate).
func (g *GH) SetIssueBody(ctx context.Context, number int, body string) error {
	_, err := g.ghStdin(ctx, strings.NewReader(body), "issue", "edit", strconv.Itoa(number), "--body-file", "-")
	return err
}

// SetStatus moves the issue to status to, removing every other
// status label in one edit so the exactly-one-status invariant of
// PRD §13 is restored even if it was violated out-of-band. Removing
// an absent label is a no-op for gh.
func (g *GH) SetStatus(ctx context.Context, number int, to Status) error {
	if !memberOf(string(to), statusNames()) {
		return fmt.Errorf("%w: status %q is not one of %s", ErrBadLabels, to, strings.Join(statusNames(), ", "))
	}
	args := []string{"issue", "edit", strconv.Itoa(number)}
	for _, s := range statuses {
		if s != to {
			args = append(args, "--remove-label", string(s))
		}
	}
	args = append(args, "--add-label", string(to))
	_, err := g.gh(ctx, args...)
	return err
}

// CloseIssue closes the issue. Closing is destructive bookkeeping
// (PRD §12 step 17 confirms closure only after the merge result is
// recorded), so it requires ExplicitConfirmation.
func (g *GH) CloseIssue(ctx context.Context, number int, c Confirmation) error {
	if !c.ok {
		return fmt.Errorf("%w: close issue #%d", ErrNotConfirmed, number)
	}
	_, err := g.gh(ctx, "issue", "close", strconv.Itoa(number))
	return err
}

// parseNumberFromURL extracts the trailing number from a GitHub
// issue or PR URL, requiring the penultimate path segment to be kind
// ("issues" or "pull"); anything unexpected fails closed rather than
// guessing.
func parseNumberFromURL(url, kind string) (int, error) {
	trimmed := strings.TrimRight(strings.TrimSpace(url), "/")
	segments := strings.Split(trimmed, "/")
	if len(segments) < 2 || segments[len(segments)-2] != kind {
		return 0, fmt.Errorf("cannot parse %s number from gh output %q", kind, url)
	}
	n, err := strconv.Atoi(segments[len(segments)-1])
	if err != nil || n <= 0 {
		return 0, fmt.Errorf("cannot parse %s number from gh output %q", kind, url)
	}
	return n, nil
}
