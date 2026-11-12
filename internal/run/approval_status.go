package run

import (
	"context"

	"github.com/kninetimmy/orch/internal/grant"
)

// ApprovalStatusSchemaVersion versions the approval-evidence snapshot, not the
// nested status or grant records, which retain their existing versions.
const ApprovalStatusSchemaVersion = 1

// ApprovalStatusDoc exposes recorded evidence, never permission for a future
// action. ActiveGrant is explicitly null when no grant is active.
type ApprovalStatusDoc struct {
	SchemaVersion int          `json:"schema_version"`
	Status        *StatusDoc   `json:"status"`
	ActiveGrant   *grant.Grant `json:"active_grant"`
	Limitations   []string     `json:"limitations"`
}

// ApprovalStatus combines the existing status path with unlocked grant
// observation. It never loads a lifecycle context, registers a session, records
// approval or spends a grant. Only the grant store's read-only Git probes run.
func ApprovalStatus(ctx context.Context, env Env) (*ApprovalStatusDoc, error) {
	status, err := Status(ctx, env)
	if err != nil {
		return nil, err
	}
	store, err := grant.Open(ctx, env.Runner, env.RepoRoot)
	if err != nil {
		return nil, err
	}
	g, err := store.ObserveActive(env.now())
	if err != nil {
		return nil, err
	}
	return &ApprovalStatusDoc{
		SchemaVersion: ApprovalStatusSchemaVersion,
		Status:        status,
		ActiveGrant:   g,
		Limitations: []string{
			"Recorded plan approvals, merge-report pins and an active grant are evidence only, not authorization for a future action. Separate human approvals and normal lifecycle checks remain required outside the existing grant contract.",
			"Unlocked, non-atomic observation: run state, Delivery lock and individual grant records may change between reads or immediately afterward. Re-inspect after changes; lifecycle verbs must recheck under their existing locks.",
			"Active means only not revoked and not yet expired at observation time. Scope, session holder, remaining budgets, protected paths, live PR/CI and approval eligibility are not evaluated.",
		},
	}, nil
}
