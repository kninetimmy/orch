package run

import (
	"context"
	"fmt"
	"strings"

	"github.com/kninetimmy/orch/internal/ghops"
	"github.com/kninetimmy/orch/internal/manifest"
	"github.com/kninetimmy/orch/internal/metrics"
	"github.com/kninetimmy/orch/internal/state"
)

// CISchemaVersion is the ci request/result schema this build accepts and
// emits.
const CISchemaVersion = 1

// CIRequest asks for the current required-CI state of an issue's PR.
type CIRequest struct {
	SchemaVersion int `json:"schema_version"`
	IssueNumber   int `json:"issue_number"`
}

// CICheck is one required check in the CI result.
type CICheck struct {
	Name   string `json:"name"`
	State  string `json:"state"`
	Bucket string `json:"bucket"`
	Link   string `json:"link,omitempty"`
}

// CIResult is the honest tri-state required-CI report (PRD §16); a
// no-checks state is explicit, never conflated with passing.
type CIResult struct {
	SchemaVersion int       `json:"schema_version"`
	IssueNumber   int       `json:"issue_number"`
	State         string    `json:"state"`
	Required      []CICheck `json:"required"`
	Total         int       `json:"total"`
}

// CI reads the required-CI state of an issue's PR and folds it into the
// audit record under the singleton verification "required-ci", replacing
// the previous entry by name so polling never grows the body. It makes
// no phase change; the only mutations are the two body writes, so a
// failure leaves the run unchanged and a re-run converges.
func CI(ctx context.Context, env Env, reqJSON []byte) (*CIResult, error) {
	var req CIRequest
	if err := decodeRequest(reqJSON, &req); err != nil {
		return nil, err
	}
	if req.SchemaVersion != CISchemaVersion {
		return nil, fmt.Errorf("%w: schema_version %d is unsupported (this build supports %d)", ErrBadRequest, req.SchemaVersion, CISchemaVersion)
	}

	c, err := loadVerb(env, req.IssueNumber, []state.Phase{state.PhasePROpen, state.PhaseInReview, state.PhaseAwaitingMerge}, false)
	if err != nil {
		return nil, err
	}
	issue := c.issue()

	gh, err := openGitHub(ctx, env)
	if err != nil {
		return nil, err
	}
	summary, err := gh.RequiredCI(ctx, issue.PRNumber)
	if err != nil {
		return nil, err
	}

	iss, m, err := readIssueManifest(ctx, gh, issue.Number)
	if err != nil {
		return nil, err
	}
	setVerification(&m, manifest.Verification{
		Name:   "required-ci",
		Result: string(summary.State),
		Detail: truncateDetail(ciDetail(summary)),
		At:     env.nowStamp(),
	})
	pr, err := prForIssue(ctx, gh, issue)
	if err != nil {
		return nil, err
	}
	if err := writeManifest(ctx, gh, iss, pr, m); err != nil {
		return nil, err
	}

	if err := c.recordMetric(metrics.Event{
		Verb:        "ci",
		IssueNumber: issue.Number,
		CIState:     string(summary.State),
	}); err != nil {
		return nil, err
	}

	return &CIResult{
		SchemaVersion: CISchemaVersion,
		IssueNumber:   issue.Number,
		State:         string(summary.State),
		Required:      ciChecks(summary),
		Total:         summary.Total,
	}, nil
}

// ciDetail renders the required checks as "name=bucket" pairs for the
// manifest detail, or a plain statement when none are required.
func ciDetail(s ghops.CISummary) string {
	if len(s.Required) == 0 {
		return "no required checks"
	}
	parts := make([]string, len(s.Required))
	for i, ch := range s.Required {
		parts[i] = ch.Name + "=" + ch.Bucket
	}
	return strings.Join(parts, ", ")
}

// ciChecks converts the ghops checks into the JSON result shape.
func ciChecks(s ghops.CISummary) []CICheck {
	if len(s.Required) == 0 {
		return nil
	}
	out := make([]CICheck, len(s.Required))
	for i, ch := range s.Required {
		out[i] = CICheck{Name: ch.Name, State: ch.State, Bucket: ch.Bucket, Link: ch.Link}
	}
	return out
}
