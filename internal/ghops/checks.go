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
// documented by gh) can carry check data, not just probe errors.
func (g *GH) RequiredCI(ctx context.Context, number int) (CISummary, error) {
	var rollup struct {
		StatusCheckRollup []json.RawMessage `json:"statusCheckRollup"`
	}
	if err := g.ghJSON(ctx, &rollup, "pr", "view", strconv.Itoa(number), "--json", "statusCheckRollup"); err != nil {
		return CISummary{}, err
	}
	if rollup.StatusCheckRollup == nil {
		return CISummary{}, fmt.Errorf("gh pr view in %s returned no valid statusCheckRollup array", g.root)
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
		// Data. gh documents 1 (something failed) and 8 (pending) for
		// the human-output mode; with --json, gh 2.87.3 exits 0 in
		// every state (verified live, fact 17). All three are accepted
		// with valid check data; a bare failure exit is not CI evidence.
		// Before #517 only exit 0's no-required-checks notice was
		// accepted (gh 2.87.3, dogfooded run #1). Exit 1 can report the
		// same notice without check data; neither shape claims passing CI.
	default:
		return CISummary{}, fmt.Errorf("gh pr checks in %s exited %d: %s", g.root, res.ExitCode, strings.TrimSpace(res.Stderr))
	}
	trimmed, notice := strings.TrimSpace(res.Stdout), strings.TrimSpace(res.Stderr)
	if trimmed == "" {
		branch, found := strings.CutPrefix(notice, "no required checks reported on the '")
		branch, ended := strings.CutSuffix(branch, "' branch")
		if (res.ExitCode != 0 && res.ExitCode != 1) || !found || !ended || branch == "" || strings.ContainsAny(branch, " \t\r\n") {
			return CISummary{}, fmt.Errorf("gh pr checks in %s exited %d without required-check data: %s", g.root, res.ExitCode, notice)
		}
		return CISummary{State: CINoChecks, Total: total}, nil
	}
	if notice != "" {
		return CISummary{}, fmt.Errorf("gh pr checks in %s exited %d with unexpected stderr alongside required-check data: %s", g.root, res.ExitCode, notice)
	}
	var decoded []struct {
		Name   string `json:"name"`
		State  string `json:"state"`
		Bucket string `json:"bucket"`
		Link   string `json:"link"`
	}
	if err := json.Unmarshal([]byte(trimmed), &decoded); err != nil {
		return CISummary{}, fmt.Errorf("gh pr checks in %s returned unparsable JSON: %w", g.root, err)
	}
	if decoded == nil || (len(decoded) == 0 && res.ExitCode != 0) {
		return CISummary{}, fmt.Errorf("gh pr checks in %s exited %d without a valid required-check array: %s", g.root, res.ExitCode, notice)
	}
	required := make([]Check, len(decoded))
	for i, c := range decoded {
		switch c.Bucket {
		case "pass", "fail", "pending", "skipping", "cancel":
		default:
			return CISummary{}, fmt.Errorf("gh pr checks in %s returned invalid bucket %q for check %q", g.root, c.Bucket, c.Name)
		}
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
