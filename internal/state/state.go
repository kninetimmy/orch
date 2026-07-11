// Package state persists the Assist/Delivery operating mode (PRD §7)
// in .orchestrator/state.json, machine-local and gitignored. A missing
// file means Assist; a file that cannot be read, parsed, or recognized
// is an error so callers fail closed (PRD §15).
package state

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"time"

	"github.com/kninetimmy/orch/internal/manifest"
)

// Path is the repo-relative location of the state file.
const Path = ".orchestrator/state.json"

// SchemaVersion is the state-file schema this build reads and writes.
// v1 never ran a real Delivery (Task 11 is the first slice that
// activates one), so a v1 file on disk can only be a test artifact.
const SchemaVersion = 2

// Mode is the operating mode (PRD §7).
type Mode string

const (
	ModeAssist   Mode = "assist"
	ModeDelivery Mode = "delivery"
)

// PlanRef records the approved plan an active Delivery run was entered
// from (PRD §8). The engine cannot verify a human; this is the
// adapter's assertion, kept for the audit trail.
type PlanRef struct {
	Title          string    `json:"title"`
	Digest         string    `json:"digest"`
	ApprovedBy     string    `json:"approved_by"`
	ApprovedAt     time.Time `json:"approved_at"`
	ConfigRevision string    `json:"config_revision"`
}

// Phase is one issue's position in the Delivery lifecycle (PRD §12).
// The set is closed. PR A (this slice) only ever writes Planned,
// IssueCreated, and WorktreeReady; the remaining values belong to PR B
// and are declared now so the schema needs no v3 migration when PR B
// lands.
type Phase string

const (
	// PhasePlanned is an issue's state immediately after EnterDelivery,
	// before its GitHub issue exists.
	PhasePlanned Phase = "planned"
	// PhaseIssueCreated marks a created GitHub issue, before its
	// branch/worktree exist.
	PhaseIssueCreated Phase = "issue-created"
	// PhaseWorktreeReady marks a created branch and worktree, ready for
	// dispatch.
	PhaseWorktreeReady Phase = "worktree-ready"

	// PR B values, declared now:
	PhaseDispatched    Phase = "dispatched"
	PhasePROpen        Phase = "pr-open"
	PhaseInReview      Phase = "in-review"
	PhaseAwaitingMerge Phase = "awaiting-merge"
	PhaseMerged        Phase = "merged"
	PhaseCleaned       Phase = "cleaned"
	PhaseAbandoned     Phase = "abandoned"
	PhaseBlocked       Phase = "blocked"
)

// Valid reports whether p is a member of the closed Phase set.
func (p Phase) Valid() bool {
	switch p {
	case PhasePlanned, PhaseIssueCreated, PhaseWorktreeReady,
		PhaseDispatched, PhasePROpen, PhaseInReview, PhaseAwaitingMerge,
		PhaseMerged, PhaseCleaned, PhaseAbandoned, PhaseBlocked:
		return true
	default:
		return false
	}
}

// Attempt mirrors routing.Attempt in persistable form, so an issue's
// escalation history (routing.History) survives an orch restart (PR B;
// this package imports manifest, not routing, to stay off the routing
// policy dependency edge).
type Attempt struct {
	Role      manifest.Role      `json:"role"`
	Selection manifest.Selection `json:"selection"`
	Failed    bool               `json:"failed"`
	Reason    string             `json:"reason,omitempty"`
}

// Issue is one plan issue's persisted Delivery state.
type Issue struct {
	PlanID string `json:"plan_id"`
	Title  string `json:"title"`
	Phase  Phase  `json:"phase"`
	Number int    `json:"number,omitempty"`
	URL    string `json:"url,omitempty"`
	// Branch is the feature branch name. Worktree is the worktree
	// directory as a repo-relative slash path.
	Branch   string `json:"branch,omitempty"`
	Worktree string `json:"worktree,omitempty"`

	// PR B fields, declared now to avoid a v3 (see lifecycle.go in
	// internal/run for the verb sketch that will populate them).
	PRNumber        int       `json:"pr_number,omitempty"`
	PRURL           string    `json:"pr_url,omitempty"`
	ApprovedHeadOID string    `json:"approved_head_oid,omitempty"`
	ReviewCycles    int       `json:"review_cycles,omitempty"`
	BlockedReason   string    `json:"blocked_reason,omitempty"`
	Attempts        []Attempt `json:"attempts,omitempty"`
}

// Run describes the active Delivery run.
type Run struct {
	ID        string    `json:"id"`
	Host      string    `json:"host"` // "claude" or "codex"
	StartedAt time.Time `json:"started_at"`
	Plan      PlanRef   `json:"plan"`
	Issues    []Issue   `json:"issues"`
}

// State is the persisted operating state.
type State struct {
	SchemaVersion int       `json:"schema_version"`
	Mode          Mode      `json:"mode"`
	Run           *Run      `json:"run,omitempty"` // non-nil iff Mode is delivery
	UpdatedAt     time.Time `json:"updated_at"`
}

func statePath(repoRoot string) string {
	return filepath.Join(repoRoot, filepath.FromSlash(Path))
}

// Load reads the state under repoRoot. A missing file is Assist, not an
// error. Anything unreadable or inconsistent is an error naming the
// file so the caller can deny and the human can act.
func Load(repoRoot string) (*State, error) {
	data, err := os.ReadFile(statePath(repoRoot))
	if errors.Is(err, fs.ErrNotExist) {
		return &State{SchemaVersion: SchemaVersion, Mode: ModeAssist}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", Path, err)
	}

	var st State
	if err := json.Unmarshal(data, &st); err != nil {
		return nil, fmt.Errorf("parse %s: %v (run `orch abort` to reset to assist)", Path, err)
	}
	if err := st.validate(); err != nil {
		return nil, err
	}
	return &st, nil
}

// validate reports the first schema/consistency violation in st. Load
// applies it to a freshly-decoded file; Save applies it to a value the
// run engine is about to persist, so a bug there fails closed instead
// of writing state that Load would later refuse to read back.
func (st *State) validate() error {
	if st.SchemaVersion != SchemaVersion {
		return fmt.Errorf("%s: unsupported schema_version %d (this build understands %d; run `orch abort` to reset to assist)", Path, st.SchemaVersion, SchemaVersion)
	}
	switch st.Mode {
	case ModeAssist:
		if st.Run != nil {
			return fmt.Errorf("%s: assist mode with a recorded run (run `orch abort` to reset)", Path)
		}
	case ModeDelivery:
		if st.Run == nil {
			return fmt.Errorf("%s: delivery mode without a recorded run (run `orch abort` to reset)", Path)
		}
		if err := st.Run.validateIssues(); err != nil {
			return fmt.Errorf("%s: %v (run `orch abort` to reset)", Path, err)
		}
	default:
		return fmt.Errorf("%s: unknown mode %q (run `orch abort` to reset)", Path, st.Mode)
	}
	return nil
}

// validateIssues checks every issue's phase is a member of the closed
// set and that Number/Branch/Worktree were populated once the
// lifecycle reached the phase that requires them: Number from
// issue-created onward, Branch/Worktree from worktree-ready onward.
func (r *Run) validateIssues() error {
	for i, iss := range r.Issues {
		if !iss.Phase.Valid() {
			return fmt.Errorf("issue %d (%s): invalid phase %q", i, iss.PlanID, iss.Phase)
		}
		if iss.Phase != PhasePlanned && iss.Number <= 0 {
			return fmt.Errorf("issue %d (%s): phase %s requires a positive issue number", i, iss.PlanID, iss.Phase)
		}
		if iss.Phase != PhasePlanned && iss.Phase != PhaseIssueCreated {
			if iss.Branch == "" || iss.Worktree == "" {
				return fmt.Errorf("issue %d (%s): phase %s requires branch and worktree", i, iss.PlanID, iss.Phase)
			}
		}
	}
	return nil
}

// Save persists st under repoRoot: it stamps UpdatedAt, validates (the
// same check Load applies on read), then writes atomically. It is the
// incremental-persistence primitive the run engine calls after every
// Delivery sub-step (PRD §23), so a crash leaves the state file
// matching the last completed step.
func Save(repoRoot string, st *State) error {
	st.UpdatedAt = time.Now().UTC()
	if err := st.validate(); err != nil {
		return err
	}
	return write(repoRoot, st)
}

// write atomically replaces the state file: temp file in the same
// directory, sync, then rename (which replaces on Windows too).
func write(repoRoot string, st *State) error {
	data, err := json.MarshalIndent(st, "", "  ")
	if err != nil {
		return fmt.Errorf("encode %s: %w", Path, err)
	}
	dir := filepath.Dir(statePath(repoRoot))
	f, err := os.CreateTemp(dir, "state-*.tmp")
	if err != nil {
		return fmt.Errorf("write %s: %w", Path, err)
	}
	tmp := f.Name()
	_, err = f.Write(append(data, '\n'))
	if err == nil {
		err = f.Sync()
	}
	if closeErr := f.Close(); err == nil {
		err = closeErr
	}
	if err == nil {
		err = os.Rename(tmp, statePath(repoRoot))
	}
	if err != nil {
		_ = os.Remove(tmp) // best effort; the real state file is untouched
		return fmt.Errorf("write %s: %w", Path, err)
	}
	return nil
}
