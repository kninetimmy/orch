package run

import (
	"context"
	"fmt"

	"github.com/kninetimmy/orch/internal/ghops"
	"github.com/kninetimmy/orch/internal/manifest"
	"github.com/kninetimmy/orch/internal/state"
)

// Body-cap policy (task 24), kept engine-side so internal/manifest stays
// policy-free: every Verification.Detail is truncated at ingestion, and
// before any body write the rendered body is kept under GitHub's limit
// by dropping detail text from the oldest verifications first.
const (
	// verificationDetailCap bounds one Verification.Detail (characters).
	verificationDetailCap = 2000
	// verificationTruncationMarker flags a detail cut to the cap.
	verificationTruncationMarker = " … [truncated by orch]"
	// bodyCapHeadroom is the rendered-body ceiling the engine keeps under
	// GitHub's hard limit; over it, oldest verification details are
	// dropped before writing.
	bodyCapHeadroom = 60000
	// githubBodyLimit is GitHub's hard issue/PR body limit, named in the
	// hard-failure error.
	githubBodyLimit = 65536
)

// truncateDetail caps one detail string at verificationDetailCap runes,
// appending the truncation marker when it cuts (PRD §23: the canonical
// Name/Result/At always survive; only free-text detail is bounded).
func truncateDetail(detail string) string {
	r := []rune(detail)
	if len(r) <= verificationDetailCap {
		return detail
	}
	return string(r[:verificationDetailCap]) + verificationTruncationMarker
}

// applyDecision overwrites m's current routing fields (role, executor,
// reviewer, rationale) with d, so the audit record reflects the live
// decision after an escalation reroute. Escalations accumulate
// separately as history.
func applyDecision(m *manifest.Manifest, d state.Decision) {
	m.Role = d.Role
	m.Executor = d.Executor
	m.Reviewer = d.Reviewer
	m.RoutingRationale = d.Rationale
}

// setVerification replaces the verification named v.Name in m or appends
// it when absent. The engine-owned singletons (required-ci, merge,
// abandoned) use this so polling and terminal writes do not grow the
// body; review cycles append under unique per-cycle names.
func setVerification(m *manifest.Manifest, v manifest.Verification) {
	for i := range m.Verifications {
		if m.Verifications[i].Name == v.Name {
			m.Verifications[i] = v
			return
		}
	}
	m.Verifications = append(m.Verifications, v)
}

// prForIssue reads the issue's PR when it has one (PRNumber > 0), or
// returns nil when it does not. Verbs that mirror the manifest onto an
// open PR use it before writeManifest.
func prForIssue(ctx context.Context, gh *ghops.GH, issue *state.Issue) (*ghops.PR, error) {
	if issue.PRNumber == 0 {
		return nil, nil
	}
	pr, err := gh.PR(ctx, issue.PRNumber)
	if err != nil {
		return nil, err
	}
	return &pr, nil
}

// readIssueManifest fetches the GitHub issue and parses its audit
// record. A drifted, malformed, or missing record fails closed (PRD
// §15/§23: resume rebuilds from these bodies, so they must stay
// trustworthy).
func readIssueManifest(ctx context.Context, gh *ghops.GH, number int) (ghops.Issue, manifest.Manifest, error) {
	iss, err := gh.Issue(ctx, number)
	if err != nil {
		return ghops.Issue{}, manifest.Manifest{}, err
	}
	m, err := manifest.Parse(iss.Body)
	if err != nil {
		return ghops.Issue{}, manifest.Manifest{}, fmt.Errorf("read issue #%d audit record: %w", number, err)
	}
	return iss, m, nil
}

// upsertCapped renders m into existing (preserving prose outside the
// managed region) under the body-cap policy: if the rendered body
// exceeds the headroom, it drops Detail text from the oldest
// verifications first; if it still will not fit it fails closed naming
// GitHub's hard limit and the offending size.
func upsertCapped(existing string, m manifest.Manifest) (string, error) {
	body, err := manifest.Upsert(existing, m)
	if err != nil {
		return "", err
	}
	if len(body) <= bodyCapHeadroom {
		return body, nil
	}
	trimmed := m
	trimmed.Verifications = append([]manifest.Verification(nil), m.Verifications...)
	for i := range trimmed.Verifications {
		if trimmed.Verifications[i].Detail == "" {
			continue
		}
		trimmed.Verifications[i].Detail = ""
		body, err = manifest.Upsert(existing, trimmed)
		if err != nil {
			return "", err
		}
		if len(body) <= bodyCapHeadroom {
			return body, nil
		}
	}
	return "", fmt.Errorf("%w: rendered body is %d characters after dropping every verification detail; GitHub rejects bodies over %d", ErrBodyTooLarge, len(body), githubBodyLimit)
}

// writeManifest applies m to the issue body (the durable audit home) and,
// when pr is open, mirrors it onto the PR body (the active surface),
// each under the body cap. A nil pr, or a non-open PR, writes only the
// issue body.
func writeManifest(ctx context.Context, gh *ghops.GH, issue ghops.Issue, pr *ghops.PR, m manifest.Manifest) error {
	issueBody, err := upsertCapped(issue.Body, m)
	if err != nil {
		return err
	}
	if err := gh.SetIssueBody(ctx, issue.Number, issueBody); err != nil {
		return err
	}
	if pr != nil && pr.State == "OPEN" {
		prBody, err := upsertCapped(pr.Body, m)
		if err != nil {
			return err
		}
		if err := gh.SetPRBody(ctx, pr.Number, prBody); err != nil {
			return err
		}
	}
	return nil
}
