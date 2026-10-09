package run

import (
	"context"
	"fmt"
	"os"
	"slices"
	"strings"
	"time"

	"github.com/kninetimmy/orch/internal/ghops"
	"github.com/kninetimmy/orch/internal/grant"
	"github.com/kninetimmy/orch/internal/manifest"
	"github.com/kninetimmy/orch/internal/state"
)

// GrantApprovalStatement is the exact assertion an activation approval
// given under an active autonomy grant carries in place of
// ApprovalStatement. Its approved_by must read grant:<id>.
const GrantApprovalStatement = "grant-approve-and-enter-delivery"

// GrantMergeApprovalStatement is the exact assertion a merge approval given
// under an autonomy grant carries in place of MergeApprovalStatement. Its
// approved_by must read grant:<id>.
const GrantMergeApprovalStatement = "grant-approve-merge"

// grantApproverPrefix marks an approver that is a grant, not a person.
const grantApproverPrefix = "grant:"

// humanApprovalSource is the metrics approval source of a human approval.
const humanApprovalSource = "human"

// The audit-record entries naming a grant approval. They are engine-owned
// (engineVerificationNames), so no caller can write or overwrite one.
const (
	planApprovalName  = "plan-approval"
	mergeApprovalName = "merge-approval"
)

func planApprovedLine(id string) string {
	return fmt.Sprintf("Plan approved under Orch grant %s; no person reviewed or approved this plan.", id)
}

func mergeApprovedLine(id string) string {
	return fmt.Sprintf("Merge approved under Orch grant %s; no person reviewed or approved this merge.", id)
}

// runGrantID is the grant that activated st's run, or "" for a run a human
// activated. Activation refuses a human approval naming a grant approver, so
// the recorded plan approver says which it was.
func runGrantID(st *state.State) string {
	id, ok := strings.CutPrefix(st.Run.Plan.ApprovedBy, grantApproverPrefix)
	if !ok {
		return ""
	}
	return id
}

// approvalSource is the metrics approval source for an approver.
func approvalSource(approvedBy string, byGrant bool) string {
	if byGrant {
		return approvedBy
	}
	return humanApprovalSource
}

// grantStop is a refusal of a grant approval, naming the stop that applied.
func grantStop(format string, args ...any) error {
	return fmt.Errorf("%w: %s", ErrGrantStop, fmt.Sprintf(format, args...))
}

// checkGrantHolder applies the stops every grant approval shares: the
// approval names g, g covers gate, and the calling Claude Code session is
// g's current session holder.
func checkGrantHolder(g *grant.Grant, approvedBy, gate string) error {
	id := g.Terms.ID
	if approvedBy != grantApproverPrefix+id {
		return grantStop("approved_by %q is not %q", approvedBy, grantApproverPrefix+id)
	}
	if !slices.Contains(g.Terms.Gates, gate) {
		return grantStop("grant %s does not cover the %s gate (it covers %s)", id, gate, strings.Join(g.Terms.Gates, ", "))
	}
	if session := os.Getenv(grant.SessionEnv); session == "" || session != g.SessionID {
		return grantStop("%s=%q is not grant %s's session holder", grant.SessionEnv, session, id)
	}
	return nil
}

// planGrant applies every activation stop before activation touches
// anything, returning the store and the active grant the approval is
// recorded against once the run exists.
func planGrant(ctx context.Context, env Env, plan *PlanDoc, approvedBy string) (*grant.Store, *grant.Grant, error) {
	store, err := grant.Open(ctx, env.Runner, env.RepoRoot)
	if err != nil {
		return nil, nil, err
	}
	g, err := store.Active(env.now())
	if err != nil {
		return nil, nil, err
	}
	if g == nil {
		return nil, nil, grantStop("no autonomy grant is active (none was created, or it expired or was revoked)")
	}
	if err := checkGrantHolder(g, approvedBy, "plan"); err != nil {
		return nil, nil, err
	}
	if len(g.Runs) >= g.Terms.RunLimit {
		return nil, nil, grantStop("grant %s has used all %d of its runs", g.Terms.ID, g.Terms.RunLimit)
	}
	if plan.Host != "claude" {
		return nil, nil, grantStop("the plan's host is %s; a grant approves only claude runs", plan.Host)
	}
	var risky []string
	for _, pi := range plan.Issues {
		if len(pi.Facts.RiskDomains) > 0 {
			risky = append(risky, fmt.Sprintf("%s (%s)", pi.ID, strings.Join(pi.Facts.RiskDomains, ", ")))
		}
	}
	if len(risky) > 0 {
		return nil, nil, grantStop("plan issues declare a risk domain: %s", strings.Join(risky, "; "))
	}
	return store, g, nil
}

// mergeGrant applies the merge stops that need no GitHub read, returning
// the store and the grant that activated the run.
func mergeGrant(ctx context.Context, c *verbCtx, approvedBy string) (*grant.Store, *grant.Grant, error) {
	id := runGrantID(c.st)
	if id == "" {
		return nil, nil, grantStop("run %s was activated by a human (%s), not by an autonomy grant", c.st.Run.ID, c.st.Run.Plan.ApprovedBy)
	}
	store, err := grant.Open(ctx, c.env.Runner, c.env.RepoRoot)
	if err != nil {
		return nil, nil, err
	}
	g, err := store.Get(id)
	if err != nil {
		return nil, nil, err
	}
	now := c.env.now()
	switch {
	case g.RevokedAt != nil:
		return nil, nil, grantStop("grant %s was revoked at %s", id, g.RevokedAt.UTC().Format(time.RFC3339))
	case !g.Active(now):
		return nil, nil, grantStop("grant %s expired at %s", id, g.Terms.ExpiresAt.UTC().Format(time.RFC3339))
	}
	if err := checkGrantHolder(g, approvedBy, "merge"); err != nil {
		return nil, nil, err
	}
	if !slices.ContainsFunc(g.Runs, func(u grant.Use) bool { return u.Ref == c.st.Run.ID }) {
		return nil, nil, grantStop("grant %s has no record of activating run %s", id, c.st.Run.ID)
	}
	issue := c.issue()
	if len(issue.Attempts) > 0 || len(issue.Blocks) > 0 {
		return nil, nil, grantStop("issue #%d's history holds an escalation, block or block resolution", issue.Number)
	}
	if issue.Decision == nil || issue.Decision.ReviewerDowngraded {
		return nil, nil, grantStop("issue #%d was reviewed by the downgraded reviewer", issue.Number)
	}
	if !grantMerged(g, c) && len(g.Merges) >= g.Terms.MergeLimit {
		return nil, nil, grantStop("grant %s has used all %d of its merges", id, g.Terms.MergeLimit)
	}
	return store, g, nil
}

// grantMerged reports whether g already spent a merge on c's issue: a
// re-run of a grant merge after a crash.
func grantMerged(g *grant.Grant, c *verbCtx) bool {
	ref := grant.MergeRef(c.st.Run.ID, c.issue().Number)
	return slices.ContainsFunc(g.Merges, func(u grant.Use) bool { return u.Ref == ref })
}

// protectedFiles is the subset of files grant.Protected reports protected.
func protectedFiles(files []string) []string {
	var out []string
	for _, f := range files {
		if grant.Protected(f) {
			out = append(out, f)
		}
	}
	return out
}

// fixCycleBlocked reports whether a non-approving review on a grant run
// takes the issue past the grant's fix-cycle limit: m is the record before
// this cycle, whose review-cycle entries hold every earlier verdict. Every
// non-approving review past the limit blocks, including after a human has
// resolved an earlier such block.
func fixCycleBlocked(m manifest.Manifest, limit int) (int, bool) {
	n := 1 // this review
	for _, v := range m.Verifications {
		if reviewCycleNamePattern.MatchString(v.Name) && v.Result != VerdictApprove {
			n++
		}
	}
	return n, n > limit
}

// fixCycleCheck returns the block reason when a non-approving review of
// issue takes it past grant id's fix-cycle limit, or "" when it does not.
// The grant need not still be active: its limit binds the run it activated.
func fixCycleCheck(ctx context.Context, env Env, gh *ghops.GH, id string, issue int) (string, error) {
	store, err := grant.Open(ctx, env.Runner, env.RepoRoot)
	if err != nil {
		return "", err
	}
	g, err := store.Get(id)
	if err != nil {
		return "", err
	}
	_, m, err := readIssueManifest(ctx, gh, issue)
	if err != nil {
		return "", err
	}
	n, over := fixCycleBlocked(m, g.Terms.FixCycleLimit)
	if !over {
		return "", nil
	}
	return fixCycleReason(id, n, g.Terms.FixCycleLimit), nil
}

func fixCycleReason(id string, n, limit int) string {
	return fmt.Sprintf("Non-approving review %d exceeds autonomy grant %s's fix-cycle limit of %d; returning to the human — only `orch run resolve-block` clears this block", n, id, limit)
}
