package run

import (
	"context"
	"fmt"
	"regexp"

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
	// reviewDetailCap bounds a review-cycle Verification.Detail
	// (characters). A consolidated review summary spanning several
	// acceptance criteria routinely exceeds verificationDetailCap (task
	// 46: cycle 1 of issue #57 was cut mid-criterion, permanently
	// dropping the reviewer's remaining findings), so review-cycle
	// entries get their own, larger allowance — but not an unbounded one.
	//
	// Every Detail renders twice — once as a human-readable markdown
	// bullet, once inside the canonical JSON data comment below it — so
	// its cost against the body is roughly double its rune count for
	// plain ASCII text, and more for HTML-escaped ("&", "<", ">") or
	// multi-byte runes, which both renders escape. Measured directly
	// with manifest.Upsert against this issue's own audit record (#59/PR
	// #61) with its one review cycle stripped, at both bases
	// writeManifest maintains: the issue body is the durable audit
	// home, the PR body only a mirror of it while the PR stays open. A
	// ~10,362-byte PR-body base (carrying the approved work, routing
	// table, and five pr-open verifications) fits about 4
	// maximal-length review cycles before bodyCapHeadroom's 60,000-byte
	// ceiling starts rotating detail out; the larger ~12,157-byte
	// issue-body base fits about 3. So this cap holds 3-4
	// maximal-length review cycles in practice, versus roughly 11 at
	// the smaller verificationDetailCap. Rotation drops the OLDEST
	// verification's detail first (upsertCapped), which means
	// pr-open's mandatory PRD §15 test evidence would be discarded
	// before an early review-cycle summary; this cap is kept well
	// short of that trade becoming routine rather than rare.
	reviewDetailCap = 6000
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
// Name/Result/CommitOID/At always survive, here and under upsertCapped's
// rotation; only free-text detail is bounded).
func truncateDetail(detail string) string {
	return truncateAt(detail, verificationDetailCap)
}

// truncateReviewDetail caps a review-cycle detail at reviewDetailCap
// runes (see reviewDetailCap), appending the same truncation marker when
// it cuts.
func truncateReviewDetail(detail string) string {
	return truncateAt(detail, reviewDetailCap)
}

// truncateAt caps detail at limit runes, appending the truncation marker
// when it cuts.
func truncateAt(detail string, limit int) string {
	r := []rune(detail)
	if len(r) <= limit {
		return detail
	}
	return string(r[:limit]) + verificationTruncationMarker
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

// stampCommitOID records oid as the commit every verification in vs was
// gathered at (manifest.Verification.CommitOID).
//
// The engine stamps it and no verb accepts one on the wire. A
// caller-supplied OID would be an unverifiable claim about which commit
// an executor's or reviewer's evidence speaks for — exactly the claim
// the field exists to make checkable — so each verb passes a head it
// read itself: the pushed branch head at pr-open, the PR's live head at
// review and ci, the merged head at merge.
//
// An empty oid is left as-is rather than treated as an error: it is the
// honest reading at the one site with no head to read (abandon.go, on an
// issue abandoned before its PR opened), and manifest.Verification
// documents why inventing one there would be worse.
func stampCommitOID(vs []manifest.Verification, oid string) {
	for i := range vs {
		vs[i].CommitOID = oid
	}
}

// prHeadOID is pr's head commit, or "" when the issue has no PR to read
// one from (prForIssue returns nil below phase pr-open).
func prHeadOID(pr *ghops.PR) string {
	if pr == nil {
		return ""
	}
	return pr.HeadRefOid
}

// engineVerificationNames are the singleton verification names the
// engine's own verbs write through setVerification's replace-by-name
// path: required-ci (civerb.go), merge (merge.go), and abandoned
// (abandon.go).
var engineVerificationNames = map[string]bool{
	"required-ci": true,
	"merge":       true,
	"abandoned":   true,
}

// reviewCycleNamePattern matches the per-cycle names Review appends
// (review-cycle-<n>), which is the same shape a caller-supplied
// verification must also avoid — that write is an append, not a
// replace-by-name, so a caller-supplied entry under an existing cycle's
// name would sit as a permanent, confusing duplicate rather than being
// superseded.
var reviewCycleNamePattern = regexp.MustCompile(`^review-cycle-[0-9]+$`)

// rejectEngineOwnedNames fails closed with ErrBadRequest naming the
// first verification in vs whose name collides with an engine-owned
// singleton or a review-cycle-N entry, so the audit record's
// engine-written entries can never be quietly overwritten — or quietly
// duplicated — by a caller-supplied verification of the same name.
func rejectEngineOwnedNames(vs []manifest.Verification) error {
	for _, v := range vs {
		if engineVerificationNames[v.Name] || reviewCycleNamePattern.MatchString(v.Name) {
			return fmt.Errorf("%w: verification name %q is engine-owned and cannot be supplied by a caller", ErrBadRequest, v.Name)
		}
	}
	return nil
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
