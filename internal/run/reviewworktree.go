package run

import (
	"context"
	"errors"
	"fmt"
	"regexp"

	"github.com/kninetimmy/orch/internal/gitops"
	"github.com/kninetimmy/orch/internal/state"
)

// ReviewWorktreeSchemaVersion is the review-worktree request/result
// schema this build accepts and emits.
const ReviewWorktreeSchemaVersion = 1

// headOIDPattern is the shape head_oid must have: hexadecimal only,
// between an abbreviation git can resolve and a full SHA-256 hash. The
// field is an object id and nothing else, so a symbolic name — a
// branch, HEAD, FETCH_HEAD, a revision expression — is a bad request
// rather than something to resolve. That is not pedantry: a name is
// resolved when git reads it, and a concurrently running verb can move
// what it points at between the moment a reviewer chose it and that
// read, which is exactly how a review ends up reporting findings
// against a commit it never meant to read. An OID cannot move.
var headOIDPattern = regexp.MustCompile(`^[0-9a-fA-F]{7,64}$`)

// ReviewWorktreeRequest asks for a disposable checkout of one reviewed
// commit.
type ReviewWorktreeRequest struct {
	SchemaVersion int    `json:"schema_version"`
	IssueNumber   int    `json:"issue_number"`
	HeadOID       string `json:"head_oid"`
}

// ReviewWorktreeResult reports the provisioned checkout: where it is,
// repo-relative and slash-separated like every other worktree path
// this engine emits, and the commit its HEAD actually resolved to.
type ReviewWorktreeResult struct {
	SchemaVersion int    `json:"schema_version"`
	IssueNumber   int    `json:"issue_number"`
	Worktree      string `json:"worktree"`
	HeadOID       string `json:"head_oid"`
}

// ReviewWorktree checks the reviewed commit out into a disposable
// worktree with a detached HEAD, under the same git-ignored container
// the executor worktrees live in, and reports its repo-relative path.
// A reviewer reads the code there instead of in the primary checkout.
//
// Nothing about that worktree is persisted, and that absence is the
// feature rather than an omission. internal/guard resolves a Delivery
// write against the worktrees run state registers and denies any
// in-repository target that lies inside none of them, so a worktree
// inside the repository that state does not register is read-only to
// the guarded tools with no guard code of its own. Registering it here
// would make it writable and invert the point. Putting it outside the
// repository would be worse still: the guard allows any path with no
// .orchestrator ancestor, so a temp-directory checkout would carry no
// enforcement at all.
//
// What that buys is exactly as wide as the PreToolUse hooks the guard
// sits behind — Claude's Write, Edit, MultiEdit and NotebookEdit, and
// Codex's apply_patch. A write a reviewer performs through a shell
// command is not mediated by those hooks and is not denied by any of
// this. The verb makes a reviewer's tool-mediated writes fail closed;
// it does not make a reviewer read-only.
//
// Two hazards of reviewing in the primary checkout motivate it, both
// observed live: a shell command whose directory change failed ran a
// state-changing git command in the primary checkout instead, and
// concurrent reviewers raced on a shared fetched-ref name there. The
// checkout here is detached at one requested OID, so it holds no
// branch and no ref another reviewer or verb can move under it.
//
// Re-running for the same issue replaces the previous cycle's checkout
// instead of accumulating one per cycle, so a request-changes cycle
// re-provisions at the new head; cleanup removes whatever is left,
// finding it by the same derivation, since state never held it. The
// verb writes no state at all: there is no phase transition, and no
// metrics event, because nothing in the issue's lifecycle advanced.
func ReviewWorktree(ctx context.Context, env Env, reqJSON []byte) (*ReviewWorktreeResult, error) {
	var req ReviewWorktreeRequest
	if err := decodeRequest(reqJSON, &req); err != nil {
		return nil, err
	}
	if req.SchemaVersion != ReviewWorktreeSchemaVersion {
		return nil, fmt.Errorf("%w: schema_version %d is unsupported (this build supports %d)", ErrBadRequest, req.SchemaVersion, ReviewWorktreeSchemaVersion)
	}
	if !headOIDPattern.MatchString(req.HeadOID) {
		return nil, fmt.Errorf("%w: head_oid %q is not an object id; pass the commit's hash, never a ref name a concurrent verb could move", ErrBadRequest, req.HeadOID)
	}

	// The phase gate is loadVerb's, not this verb's: a review checkout
	// only makes sense for an issue whose PR is open and under review,
	// the same pair the review verb accepts.
	c, err := loadVerb(env, req.IssueNumber, []state.Phase{state.PhasePROpen, state.PhaseInReview}, false)
	if err != nil {
		return nil, err
	}
	issue := c.issue()

	git, err := gitops.Open(ctx, env.Runner, env.RepoRoot)
	if err != nil {
		return nil, err
	}

	// Replace an earlier cycle's checkout (idempotent the way cleanup's
	// deletions are: nothing left behind is not registered, so
	// ErrUnknownWorktree is the expected answer, not a failure). The
	// removal is forced because a reviewer legitimately leaves untracked
	// files — test output, build artifacts — in a checkout that exists to
	// be thrown away; gitops refuses to force-remove anything holding a
	// branch, so no work can be reached this way.
	abs := reviewWorktreeAbs(env.RepoRoot, issue.Number)
	if err := git.RemoveDetachedWorktree(ctx, abs); err != nil && !errors.Is(err, gitops.ErrUnknownWorktree) {
		return nil, err
	}
	wt, err := git.AddDetachedWorktree(ctx, abs, req.HeadOID)
	if err != nil {
		return nil, err
	}

	return &ReviewWorktreeResult{
		SchemaVersion: ReviewWorktreeSchemaVersion,
		IssueNumber:   issue.Number,
		Worktree:      reviewWorktreeRel(issue.Number),
		HeadOID:       wt.Head,
	}, nil
}
