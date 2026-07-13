package run

import (
	"context"
	"fmt"

	"github.com/kninetimmy/orch/internal/ghops"
	"github.com/kninetimmy/orch/internal/metrics"
	"github.com/kninetimmy/orch/internal/state"
)

// BlockSchemaVersion is the block request/result schema this build
// accepts and emits.
const BlockSchemaVersion = 1

// blockClasses is the closed failure-class set (PRD §15's classes plus
// secret and a catch-all). A secret block stops the whole run.
var blockClasses = map[string]bool{
	"secret":     true,
	"hook":       true,
	"auth":       true,
	"github":     true,
	"validation": true,
	"other":      true,
}

// BlockRequest blocks an in-flight issue for human attention.
type BlockRequest struct {
	SchemaVersion int    `json:"schema_version"`
	IssueNumber   int    `json:"issue_number"`
	Class         string `json:"class"`
	Detail        string `json:"detail"`
}

// BlockResult reports the block and whether it stopped the run.
type BlockResult struct {
	SchemaVersion int         `json:"schema_version"`
	IssueNumber   int         `json:"issue_number"`
	Phase         state.Phase `json:"phase"`
	RunStopped    bool        `json:"run_stopped"`
}

// Block blocks an issue for human action (PRD §15), preserving its
// branch and worktree. It is the only mutating verb allowed on a stopped
// run, and it accepts any non-terminal phase including blocked (re-block
// updates the reason). A secret block also stops the whole run (PRD §16),
// after which every other mutating verb fails closed until the human
// recovers. Unaffected issues continue unless the run stopped.
func Block(ctx context.Context, env Env, reqJSON []byte) (*BlockResult, error) {
	var req BlockRequest
	if err := decodeRequest(reqJSON, &req); err != nil {
		return nil, err
	}
	if req.SchemaVersion != BlockSchemaVersion {
		return nil, fmt.Errorf("%w: schema_version %d is unsupported (this build supports %d)", ErrBadRequest, req.SchemaVersion, BlockSchemaVersion)
	}
	if !blockClasses[req.Class] {
		return nil, fmt.Errorf("%w: class %q is not one of secret, hook, auth, github, validation, other", ErrBadRequest, req.Class)
	}
	if req.Detail == "" {
		return nil, fmt.Errorf("%w: block detail must not be empty", ErrBadRequest)
	}

	c, err := loadVerb(env, req.IssueNumber, nonTerminalPhases, true)
	if err != nil {
		return nil, err
	}
	issue := c.issue()

	// Open gh before mutating state so an unavailable GitHub leaves the
	// run untouched (fail closed, no side effect).
	gh, err := openGitHub(ctx, env)
	if err != nil {
		return nil, err
	}

	reason := fmt.Sprintf("%s: %s", req.Class, req.Detail)
	issue.Phase = state.PhaseBlocked
	issue.BlockedReason = reason
	runStopped := req.Class == "secret"
	if runStopped {
		c.st.Run.StoppedReason = reason
	}
	if err := c.save(); err != nil {
		return nil, err
	}
	if err := gh.SetStatus(ctx, issue.Number, ghops.StatusBlocked); err != nil {
		return nil, wrapAfterMutation(err)
	}

	if err := c.recordMetric(metrics.Event{
		Verb:        "block",
		IssueNumber: issue.Number,
		BlockClass:  req.Class,
		RunStopped:  runStopped,
	}); err != nil {
		return nil, err
	}

	return &BlockResult{
		SchemaVersion: BlockSchemaVersion,
		IssueNumber:   issue.Number,
		Phase:         state.PhaseBlocked,
		RunStopped:    runStopped,
	}, nil
}
