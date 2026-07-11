package run

import (
	"context"
	"fmt"

	"github.com/kninetimmy/orch/internal/ghops"
	"github.com/kninetimmy/orch/internal/manifest"
	"github.com/kninetimmy/orch/internal/state"
)

// AbandonSchemaVersion is the abandon request/result schema this build
// accepts and emits.
const AbandonSchemaVersion = 1

// AbandonStatement is the exact confirmation abandonment requires:
// closing a PR and issue without merging is destructive bookkeeping and
// carries its own explicit approval (PRD §15).
const AbandonStatement = "abandon-issue"

// AbandonRequest abandons an in-flight issue without merging.
type AbandonRequest struct {
	SchemaVersion int    `json:"schema_version"`
	IssueNumber   int    `json:"issue_number"`
	Reason        string `json:"reason"`
	Statement     string `json:"statement"`
}

// AbandonResult reports the abandonment.
type AbandonResult struct {
	SchemaVersion int         `json:"schema_version"`
	IssueNumber   int         `json:"issue_number"`
	Phase         state.Phase `json:"phase"`
}

// Abandon closes an issue's PR and the issue without merging (PRD §15),
// recording the reason in the durable issue audit record. The branch and
// worktree are preserved until cleanup. Each GitHub mutation is guarded
// by a state read so a re-run after a partial failure converges; the
// only state write is the final Save, so a failure before it leaves the
// run unchanged.
func Abandon(ctx context.Context, env Env, reqJSON []byte) (*AbandonResult, error) {
	var req AbandonRequest
	if err := decodeRequest(reqJSON, &req); err != nil {
		return nil, err
	}
	if req.SchemaVersion != AbandonSchemaVersion {
		return nil, fmt.Errorf("%w: schema_version %d is unsupported (this build supports %d)", ErrBadRequest, req.SchemaVersion, AbandonSchemaVersion)
	}
	if req.Statement != AbandonStatement {
		return nil, fmt.Errorf("%w: abandon statement %q does not equal %q", ErrBadRequest, req.Statement, AbandonStatement)
	}
	if req.Reason == "" {
		return nil, fmt.Errorf("%w: abandon reason must not be empty", ErrBadRequest)
	}

	c, err := loadVerb(env, req.IssueNumber, nonTerminalPhases, false)
	if err != nil {
		return nil, err
	}
	issue := c.issue()

	gh, err := openGitHub(ctx, env)
	if err != nil {
		return nil, err
	}

	// Close an open PR (if any).
	if issue.PRNumber > 0 {
		pr, err := gh.PR(ctx, issue.PRNumber)
		if err != nil {
			return nil, err
		}
		if pr.State == "OPEN" {
			if err := gh.ClosePR(ctx, issue.PRNumber, ghops.ExplicitConfirmation()); err != nil {
				return nil, err
			}
		}
	}

	// Record the abandonment on the durable issue audit record.
	iss, m, err := readIssueManifest(ctx, gh, issue.Number)
	if err != nil {
		return nil, err
	}
	setVerification(&m, manifest.Verification{
		Name:   "abandoned",
		Result: "abandoned",
		Detail: truncateDetail(req.Reason),
		At:     env.nowStamp(),
	})
	issueBody, err := upsertCapped(iss.Body, m)
	if err != nil {
		return nil, err
	}
	if err := gh.SetIssueBody(ctx, issue.Number, issueBody); err != nil {
		return nil, err
	}
	if iss.State == "OPEN" {
		if err := gh.CloseIssue(ctx, issue.Number, ghops.ExplicitConfirmation()); err != nil {
			return nil, err
		}
	}

	issue.Phase = state.PhaseAbandoned
	if err := c.save(); err != nil {
		return nil, wrapAfterMutation(err)
	}

	return &AbandonResult{
		SchemaVersion: AbandonSchemaVersion,
		IssueNumber:   issue.Number,
		Phase:         state.PhaseAbandoned,
	}, nil
}
