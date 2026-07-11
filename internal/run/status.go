package run

import (
	"context"
	"time"

	"github.com/kninetimmy/orch/internal/lockfile"
	"github.com/kninetimmy/orch/internal/state"
)

// StatusSchemaVersion is the status-document schema this build emits.
const StatusSchemaVersion = 1

// StatusDoc is the run-state document `orch run status --json`
// reports. It never loads config, and it never fails on an
// inconsistent state/lock pair — a second host must be able to see
// broken state to report it (PRD §14), so any CheckConsistent problem
// goes into Warning, never the returned error.
type StatusDoc struct {
	SchemaVersion int             `json:"schema_version"`
	Mode          state.Mode      `json:"mode"`
	Consistent    bool            `json:"consistent"`
	Warning       string          `json:"warning,omitempty"`
	Lock          *lockfile.Owner `json:"lock,omitempty"`
	Run           *RunView        `json:"run,omitempty"`
}

// RunView is the active Delivery run's status view. Issues carry the
// full state.Issue shape, so the PR B per-issue lifecycle fields (phase,
// PR number, decision, review cycles, ...) are reported as-is.
type RunView struct {
	ID        string        `json:"id"`
	Host      string        `json:"host"`
	StartedAt time.Time     `json:"started_at"`
	Plan      state.PlanRef `json:"plan"`
	Issues    []state.Issue `json:"issues"`
	// StoppedReason is non-empty when a secret block stopped the run.
	StoppedReason string `json:"stopped_reason,omitempty"`
}

// Status reports the run-state document.
func Status(ctx context.Context, env Env) (*StatusDoc, error) {
	st, err := state.Load(env.RepoRoot)
	if err != nil {
		return nil, err
	}
	owner, err := lockfile.Inspect(env.RepoRoot)
	if err != nil {
		return nil, err
	}

	doc := &StatusDoc{
		SchemaVersion: StatusSchemaVersion,
		Mode:          st.Mode,
		Consistent:    true,
		Lock:          owner,
	}
	if err := state.CheckConsistent(st, owner); err != nil {
		doc.Consistent = false
		doc.Warning = err.Error()
	}
	if st.Run != nil {
		doc.Run = &RunView{
			ID:            st.Run.ID,
			Host:          st.Run.Host,
			StartedAt:     st.Run.StartedAt,
			Plan:          st.Run.Plan,
			Issues:        st.Run.Issues,
			StoppedReason: st.Run.StoppedReason,
		}
	}
	return doc, nil
}
