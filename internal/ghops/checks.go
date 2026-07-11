package ghops

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
)

// CIState is the PRD §16 merge-gate signal over a PR's required
// checks.
type CIState string

// The four CI states. CINoChecks is distinct from CIPassing because
// "if no CI exists, the plan gate states that explicitly" (PRD §16) —
// green and absent must never be conflated.
const (
	CINoChecks CIState = "no-checks"
	CIPending  CIState = "pending"
	CIFailing  CIState = "failing"
	CIPassing  CIState = "passing"
)

// Check is one required check on a PR.
type Check struct {
	Name string
	// State is the check's own state string as reported by gh.
	State string
	// Bucket is gh's classification: pass, fail, pending, skipping,
	// or cancel.
	Bucket string
	// Link points at the check's detail page, for the ready-to-merge
	// report.
	Link string
}

// CISummary reports the required-CI state of one PR: the derived
// gate signal, the required checks as evidence (PRD §15: CI state is
// reported honestly), and the total check count so the engine can
// phrase "CI exists but none of it is required" explicitly.
type CISummary struct {
	State    CIState
	Required []Check
	Total    int
}

// RequiredCI reports the state of the PR's required checks. Two
// probes: `pr view --json statusCheckRollup` distinguishes
// no-CI-at-all (empty rollup — PRD §16's explicit no-CI statement)
// before `pr checks --required` reads the required subset, whose
// exit codes 0 (passing), 1 (something failed), and 8 (pending,
// documented by gh) are all data, not errors.
func (g *GH) RequiredCI(ctx context.Context, number int) (CISummary, error) {
	var rollup struct {
		StatusCheckRollup []json.RawMessage `json:"statusCheckRollup"`
	}
	if err := g.ghJSON(ctx, &rollup, "pr", "view", strconv.Itoa(number), "--json", "statusCheckRollup"); err != nil {
		return CISummary{}, err
	}
	total := len(rollup.StatusCheckRollup)
	if total == 0 {
		return CISummary{State: CINoChecks}, nil
	}

	res, err := g.run(ctx, nil, "pr", "checks", strconv.Itoa(number), "--required", "--json", "name,state,bucket,link")
	if err != nil {
		return CISummary{}, err
	}
	switch res.ExitCode {
	case 0, 1, 8:
		// Data: 0 all passing, 1 something failed, 8 checks pending.
	default:
		return CISummary{}, fmt.Errorf("gh pr checks in %s exited %d: %s", g.root, res.ExitCode, strings.TrimSpace(res.Stderr))
	}
	var decoded []struct {
		Name   string `json:"name"`
		State  string `json:"state"`
		Bucket string `json:"bucket"`
		Link   string `json:"link"`
	}
	if err := json.Unmarshal([]byte(res.Stdout), &decoded); err != nil {
		return CISummary{}, fmt.Errorf("gh pr checks in %s returned unparsable JSON: %w", g.root, err)
	}
	required := make([]Check, len(decoded))
	for i, c := range decoded {
		required[i] = Check{Name: c.Name, State: c.State, Bucket: c.Bucket, Link: c.Link}
	}
	return CISummary{State: deriveCIState(required), Required: required, Total: total}, nil
}

// deriveCIState folds check buckets into the gate signal: any
// fail/cancel is failing, else any pending is pending, else passing.
// No required checks while other checks exist is no-checks — nothing
// gates the merge, and the engine must say so rather than claim
// green (PRD §16).
func deriveCIState(required []Check) CIState {
	if len(required) == 0 {
		return CINoChecks
	}
	state := CIPassing
	for _, c := range required {
		switch c.Bucket {
		case "fail", "cancel":
			return CIFailing
		case "pending":
			state = CIPending
		}
	}
	return state
}
