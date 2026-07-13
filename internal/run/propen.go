package run

import (
	"context"
	"fmt"
	"strings"

	"github.com/kninetimmy/orch/internal/ghops"
	"github.com/kninetimmy/orch/internal/gitops"
	"github.com/kninetimmy/orch/internal/manifest"
	"github.com/kninetimmy/orch/internal/metrics"
	"github.com/kninetimmy/orch/internal/state"
)

// PROpenSchemaVersion is the pr-open request/result schema this build
// accepts and emits.
const PROpenSchemaVersion = 1

// VerificationInput is one required-test evidence entry the executor
// supplies (PRD §15: completion requires targeted-test evidence). A
// missing At is stamped by the engine.
type VerificationInput struct {
	Name    string `json:"name"`
	Command string `json:"command,omitempty"`
	Result  string `json:"result"`
	Detail  string `json:"detail,omitempty"`
	At      string `json:"at,omitempty"`
}

// PROpenRequest opens the PR for a dispatched issue, carrying the
// executor's verification evidence.
type PROpenRequest struct {
	SchemaVersion int                 `json:"schema_version"`
	IssueNumber   int                 `json:"issue_number"`
	Verifications []VerificationInput `json:"verifications"`
	// Usage is adapter-reported model usage for this PR-open call
	// (PRD §21 "where available"); optional and best-effort.
	Usage *metrics.Usage `json:"usage,omitempty"`
}

// PROpenResult reports the opened PR.
type PROpenResult struct {
	SchemaVersion int    `json:"schema_version"`
	IssueNumber   int    `json:"issue_number"`
	PRNumber      int    `json:"pr_number"`
	PRURL         string `json:"pr_url"`
}

// PROpen opens a pull request for a dispatched issue (PRD §12 step 9).
// Preconditions: the worktree is clean, the branch is strictly ahead of
// origin/<default> (no empty PRs), and no open PR already exists for the
// branch (an orphan from a crashed run fails closed naming resume/abort).
// It pushes the branch, records the verification evidence in the issue
// and PR audit records, creates the PR, and moves the issue to pr-open /
// awaiting-review. The verification set is written (not appended) so a
// pre-CreatePR crash re-runs cleanly.
func PROpen(ctx context.Context, env Env, reqJSON []byte) (*PROpenResult, error) {
	var req PROpenRequest
	if err := decodeRequest(reqJSON, &req); err != nil {
		return nil, err
	}
	if req.SchemaVersion != PROpenSchemaVersion {
		return nil, fmt.Errorf("%w: schema_version %d is unsupported (this build supports %d)", ErrBadRequest, req.SchemaVersion, PROpenSchemaVersion)
	}
	if err := req.Usage.Validate(); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrBadRequest, err)
	}
	verifications, err := buildVerifications(req.Verifications, env.nowStamp())
	if err != nil {
		return nil, err
	}

	c, err := loadVerb(env, req.IssueNumber, []state.Phase{state.PhaseDispatched}, false)
	if err != nil {
		return nil, err
	}
	issue := c.issue()

	gh, err := openGitHub(ctx, env)
	if err != nil {
		return nil, err
	}
	repo, err := gh.Repo(ctx)
	if err != nil {
		return nil, err
	}
	git, err := gitops.Open(ctx, env.Runner, env.RepoRoot)
	if err != nil {
		return nil, err
	}
	worktree := c.worktreeAbs()
	if err := git.RequireClean(ctx, worktree); err != nil {
		return nil, err
	}
	ahead, err := git.CommitsAhead(ctx, worktree, "origin/"+repo.DefaultBranch, issue.Branch)
	if err != nil {
		return nil, err
	}
	if ahead == 0 {
		return nil, fmt.Errorf("branch %s has no commits beyond origin/%s; refusing to open an empty PR (PRD §16)", issue.Branch, repo.DefaultBranch)
	}
	existing, err := gh.PRForBranch(ctx, issue.Branch)
	if err != nil {
		return nil, err
	}
	if existing != nil {
		return nil, fmt.Errorf("%w: PR #%d for branch %s; run `orch abort` or `orch resume` to adopt it", ErrPRExists, existing.Number, issue.Branch)
	}

	// Push the branch, then record the verifications on the issue body
	// (its manifest, seeded at activation, carries no verifications yet,
	// so a write is idempotent on retry) before creating the PR.
	if err := git.Push(ctx, worktree, "origin", issue.Branch); err != nil {
		return nil, err
	}
	iss, m, err := readIssueManifest(ctx, gh, issue.Number)
	if err != nil {
		return nil, err
	}
	m.Verifications = verifications
	issueBody, err := upsertCapped(iss.Body, m)
	if err != nil {
		return nil, err
	}
	if err := gh.SetIssueBody(ctx, issue.Number, issueBody); err != nil {
		return nil, err
	}
	prBody, err := upsertCapped(prProse(issue, c.st.Run.Plan.Digest), m)
	if err != nil {
		return nil, err
	}
	prNumber, prURL, err := gh.CreatePR(ctx, ghops.PRSpec{
		Head:  issue.Branch,
		Base:  repo.DefaultBranch,
		Title: issue.Title,
		Body:  prBody,
	})
	if err != nil {
		return nil, err
	}

	issue.PRNumber = prNumber
	issue.PRURL = prURL
	issue.Phase = state.PhasePROpen
	if err := c.save(); err != nil {
		return nil, wrapAfterMutation(err)
	}
	if err := gh.SetStatus(ctx, issue.Number, ghops.StatusAwaitingReview); err != nil {
		return nil, wrapAfterMutation(err)
	}
	if err := c.save(); err != nil {
		return nil, wrapAfterMutation(err)
	}

	if err := c.recordMetric(metrics.Event{
		Verb:        "pr-open",
		IssueNumber: issue.Number,
		Usage:       req.Usage,
	}); err != nil {
		return nil, err
	}

	return &PROpenResult{
		SchemaVersion: PROpenSchemaVersion,
		IssueNumber:   issue.Number,
		PRNumber:      prNumber,
		PRURL:         prURL,
	}, nil
}

// buildVerifications converts the supplied inputs into manifest
// verifications, requiring at least one (PRD §15), stamping missing At
// with stamp, and truncating each Detail to the body-cap ceiling.
func buildVerifications(inputs []VerificationInput, stamp string) ([]manifest.Verification, error) {
	if len(inputs) == 0 {
		return nil, fmt.Errorf("%w: pr-open requires at least one verification (PRD §15: completion needs targeted-test evidence)", ErrBadRequest)
	}
	out := make([]manifest.Verification, len(inputs))
	for i, in := range inputs {
		if in.Name == "" {
			return nil, fmt.Errorf("%w: verifications[%d] name is empty", ErrBadRequest, i)
		}
		if in.Result == "" {
			return nil, fmt.Errorf("%w: verifications[%d] result is empty", ErrBadRequest, i)
		}
		at := in.At
		if at == "" {
			at = stamp
		}
		out[i] = manifest.Verification{
			Name:    in.Name,
			Command: in.Command,
			Result:  in.Result,
			Detail:  truncateDetail(in.Detail),
			At:      at,
		}
	}
	return out, nil
}

// prProse is the human-readable portion of a PR body: a one-line summary,
// the Closes link that closes the issue on merge (PRD §12 step 17), and
// the plan digest. The full objective and acceptance criteria live on
// the linked issue.
func prProse(issue *state.Issue, digest string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Delivers issue #%d: %s\n\n", issue.Number, issue.Title)
	fmt.Fprintf(&b, "Closes #%d\n\n", issue.Number)
	fmt.Fprintf(&b, "**Plan digest:** `%s`\n\n", digest)
	b.WriteString("The full objective and acceptance criteria are on the linked issue.\n")
	return b.String()
}
