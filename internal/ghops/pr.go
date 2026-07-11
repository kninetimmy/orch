package ghops

import (
	"context"
	"fmt"
	"strconv"
	"strings"
)

// PRSpec describes a PR to open. The body (the PRD §13 audit record)
// is opaque to ghops.
type PRSpec struct {
	// Head is the feature branch, Base the merge target.
	Head, Base string
	Title      string
	Body       string
}

// PR is the read-back view of a pull request.
type PR struct {
	Number int
	// State is "OPEN", "CLOSED", or "MERGED".
	State string
	Title string
	URL   string
	// HeadRefName and BaseRefName are the branch names.
	HeadRefName, BaseRefName string
	// HeadRefOid is the head commit SHA the human's merge approval
	// refers to; MergePR pins it (PRD §8).
	HeadRefOid string
	// MergeStateStatus is GitHub's merge-state summary, for example
	// "CLEAN", "BLOCKED", or "DIRTY".
	MergeStateStatus string
	// MergedAt is the RFC3339 merge time, empty while unmerged.
	MergedAt string
	// Body is the raw PR body (the PRD §13 audit record), which
	// resume/recovery parses back into run state (PRD §23).
	Body string
}

// mergeFlags maps the config-validated merge strategies
// (internal/config: squash|rebase|merge-commit) to gh's merge flags.
// ghops re-validates rather than trusting its caller.
var mergeFlags = map[string]string{
	"squash":       "--squash",
	"rebase":       "--rebase",
	"merge-commit": "--merge",
}

// CreatePR opens one PR for the issue's feature branch (PRD §12
// step 9). The body travels over stdin, never the command line.
func (g *GH) CreatePR(ctx context.Context, spec PRSpec) (number int, url string, err error) {
	if spec.Head == "" || spec.Base == "" {
		return 0, "", fmt.Errorf("pr create requires head and base branches (head %q, base %q)", spec.Head, spec.Base)
	}
	// With a non-TTY stdout gh prints exactly the created PR URL.
	out, err := g.ghStdin(ctx, strings.NewReader(spec.Body),
		"pr", "create", "--head", spec.Head, "--base", spec.Base, "--title", spec.Title, "--body-file", "-")
	if err != nil {
		return 0, "", err
	}
	number, err = parseNumberFromURL(out, "pull")
	if err != nil {
		return 0, "", err
	}
	return number, out, nil
}

// prFields is the JSON field list every PR read pins, shared by PR and
// PRForBranch so both return a fully-populated view.
const prFields = "number,state,title,url,headRefName,baseRefName,headRefOid,mergeStateStatus,mergedAt,body"

// prJSON is the decode target for prFields.
type prJSON struct {
	Number           int    `json:"number"`
	State            string `json:"state"`
	Title            string `json:"title"`
	URL              string `json:"url"`
	HeadRefName      string `json:"headRefName"`
	BaseRefName      string `json:"baseRefName"`
	HeadRefOid       string `json:"headRefOid"`
	MergeStateStatus string `json:"mergeStateStatus"`
	MergedAt         string `json:"mergedAt"`
	Body             string `json:"body"`
}

// toPR converts to the exported view: the field sets are identical, so
// this is a direct struct conversion (tags are ignored in conversions).
func (d prJSON) toPR() PR {
	return PR(d)
}

// PR reads one pull request; the run engine uses it for the
// ready-to-merge report (PRD §12 step 14) and to obtain the
// HeadRefOid a merge approval pins.
func (g *GH) PR(ctx context.Context, number int) (PR, error) {
	var decoded prJSON
	if err := g.ghJSON(ctx, &decoded, "pr", "view", strconv.Itoa(number), "--json", prFields); err != nil {
		return PR{}, err
	}
	if decoded.Number != number {
		return PR{}, fmt.Errorf("gh pr view %d in %s returned PR %d", number, g.root, decoded.Number)
	}
	return decoded.toPR(), nil
}

// PRForBranch returns the single open pull request whose head branch is
// head, or nil when none exists (`gh pr list --head <branch> --state
// open`). pr-open uses it to fail closed on an orphan PR left by a
// crashed run (PRD §23 reconciliation); more than one open PR for a
// branch is impossible on GitHub, so a longer list is an error.
func (g *GH) PRForBranch(ctx context.Context, head string) (*PR, error) {
	var decoded []prJSON
	if err := g.ghJSON(ctx, &decoded, "pr", "list", "--head", head, "--state", "open", "--json", prFields); err != nil {
		return nil, err
	}
	if len(decoded) == 0 {
		return nil, nil
	}
	if len(decoded) > 1 {
		return nil, fmt.Errorf("gh pr list --head %s in %s returned %d open PRs (want at most one)", head, g.root, len(decoded))
	}
	pr := decoded[0].toPR()
	return &pr, nil
}

// SetPRBody replaces the PR body (the PRD §13 audit record is mirrored
// onto the PR while it is the active surface). The body travels over
// stdin, never the command line.
func (g *GH) SetPRBody(ctx context.Context, number int, body string) error {
	_, err := g.ghStdin(ctx, strings.NewReader(body), "pr", "edit", strconv.Itoa(number), "--body-file", "-")
	return err
}

// MergePR merges an approved PR with the configured strategy. It is
// only ever invoked after the human merge gate (PRD §8) and requires
// ExplicitConfirmation. headOID pins the commit the human approved
// (--match-head-commit): if the PR moved after approval, gh refuses
// and the merge fails mechanically instead of merging unreviewed
// commits. Remote branch deletion is deliberately separate
// (gitops.DeleteRemoteBranch, PRD §12 step 18): --delete-branch
// would also delete the local branch and touch the invoking
// checkout, and each destructive act carries its own confirmation.
func (g *GH) MergePR(ctx context.Context, number int, strategy, headOID string, c Confirmation) error {
	if !c.ok {
		return fmt.Errorf("%w: merge PR #%d", ErrNotConfirmed, number)
	}
	flag, ok := mergeFlags[strategy]
	if !ok {
		return fmt.Errorf("unknown merge strategy %q (want squash, rebase, or merge-commit)", strategy)
	}
	if headOID == "" {
		return fmt.Errorf("merge PR #%d requires the approved head commit SHA (PRD §8: approval pins one PR state)", number)
	}
	_, err := g.gh(ctx, "pr", "merge", strconv.Itoa(number), flag, "--match-head-commit", headOID)
	return err
}

// ClosePR closes a PR without merging; abandoning tracked work is
// destructive bookkeeping and requires ExplicitConfirmation.
func (g *GH) ClosePR(ctx context.Context, number int, c Confirmation) error {
	if !c.ok {
		return fmt.Errorf("%w: close PR #%d", ErrNotConfirmed, number)
	}
	_, err := g.gh(ctx, "pr", "close", strconv.Itoa(number))
	return err
}
