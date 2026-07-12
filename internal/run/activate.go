package run

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/kninetimmy/orch/internal/config"
	"github.com/kninetimmy/orch/internal/ghops"
	"github.com/kninetimmy/orch/internal/gitops"
	"github.com/kninetimmy/orch/internal/manifest"
	"github.com/kninetimmy/orch/internal/routing"
	"github.com/kninetimmy/orch/internal/state"
)

// ActivationSchemaVersion is the activation-request/result schema this
// build accepts and emits.
const ActivationSchemaVersion = 1

// ApprovalStatement is the exact assertion an adapter's human approval
// must carry (PRD §8): the engine cannot verify a human, so this
// string is the recorded proof that one saw the gate and approved it,
// tied to a specific plan digest.
const ApprovalStatement = "approve-and-enter-delivery"

// ActivationRequest is the input to Activate: the full plan plus the
// adapter's record of the human's approval of it.
type ActivationRequest struct {
	SchemaVersion int      `json:"schema_version"`
	Plan          PlanDoc  `json:"plan"`
	Approval      Approval `json:"approval"`
}

// Approval is the adapter's assertion that a human approved plan
// (identified by digest) for entry into Delivery.
type Approval struct {
	PlanDigest string    `json:"plan_digest"`
	ApprovedBy string    `json:"approved_by"`
	ApprovedAt time.Time `json:"approved_at"`
	Statement  string    `json:"statement"`
}

// validate checks a against the freshly recomputed digest: the
// statement must be the exact ApprovalStatement and the digest must
// match. A mismatch is the "adjust the plan and resubmit" loop, not a
// retryable condition.
func (a Approval) validate(digest string) error {
	if a.Statement != ApprovalStatement {
		return fmt.Errorf("%w: approval statement %q does not equal %q", ErrBadApproval, a.Statement, ApprovalStatement)
	}
	if a.PlanDigest != digest {
		return fmt.Errorf("%w: approval plan_digest %q does not match the recomputed digest %q; adjust the plan and resubmit", ErrBadApproval, a.PlanDigest, digest)
	}
	return nil
}

// ActivationResult is the output of a successful Activate.
type ActivationResult struct {
	SchemaVersion int                     `json:"schema_version"`
	RunID         string                  `json:"run_id"`
	Issues        []ActivationResultIssue `json:"issues"`
}

// ActivationResultIssue is one activated issue's created artifacts.
type ActivationResultIssue struct {
	ID       string `json:"id"`
	Number   int    `json:"number"`
	URL      string `json:"url"`
	Branch   string `json:"branch"`
	Worktree string `json:"worktree"`
}

// DecodeActivation decodes data into an ActivationRequest, rejecting
// any field this build does not recognize at any level (including the
// embedded plan) and any unsupported schema_version.
func DecodeActivation(data []byte) (*ActivationRequest, error) {
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	var req ActivationRequest
	if err := dec.Decode(&req); err != nil {
		return nil, fmt.Errorf("%w: decode activation request: %v", ErrBadApproval, err)
	}
	if dec.More() {
		return nil, fmt.Errorf("%w: trailing data after activation request", ErrBadApproval)
	}
	if req.SchemaVersion != ActivationSchemaVersion {
		return nil, fmt.Errorf("%w: schema_version %d is unsupported (this build supports %d)", ErrBadApproval, req.SchemaVersion, ActivationSchemaVersion)
	}
	return &req, nil
}

// wrapAfterEnter marks an error that occurred after state.EnterDelivery
// succeeded: state and the Delivery lock are held, so the remediation
// is `orch abort` (safe — it never touches branches, issues, or
// worktrees) or `orch resume`, never a bare retry.
func wrapAfterEnter(err error) error {
	if err == nil {
		return nil
	}
	return fmt.Errorf("%w (state and the delivery lock are preserved; run `orch abort` to return to assist, or `orch resume` to continue)", err)
}

// Activate validates a plan and its approval, runs the read-only
// Delivery preflights, enters Delivery, and creates one GitHub issue
// plus one branch/worktree per plan issue in wave order. See the
// package doc and internal/run's design notes for the exact
// phase/step ordering and abort-safety invariant; a summary:
//
//   - Phase 1 (pure validation): decode/validate the plan and its
//     approval, derive routing and labels for every issue.
//   - Phase 2 (read-only preflights): Assist+no-lock, clean primary
//     checkout, authenticated GitHub remote, primary on the default
//     branch (F4), every plan-declared area label existing in the
//     repository (area labels are repository-defined per PRD §13, so
//     activation never creates them), the memhub gate, and the
//     worktree container git-ignored (F1).
//   - Phase 3 (idempotent GitHub prep): EnsureLabelTaxonomy — the only
//     mutation before the lock is held (F6).
//   - Phase 4: state.EnterDelivery acquires the lock and records the
//     run.
//   - Phase 5 (per issue, wave order): create the GitHub issue with the
//     PRD §13 audit record, persist state, add the branch/worktree,
//     persist state again.
//   - Phase 6: a final state.Save, then return the result. The run
//     stays in Delivery — dispatching issues onward is PR B.
func Activate(ctx context.Context, env Env, reqJSON []byte) (*ActivationResult, error) {
	// Phase 1: pure validation.
	cfg, err := config.Load(env.RepoRoot)
	if err != nil {
		return nil, err
	}
	req, err := DecodeActivation(reqJSON)
	if err != nil {
		return nil, err
	}
	plan := &req.Plan
	if err := plan.Validate(cfg); err != nil {
		return nil, err
	}
	digest, err := plan.Digest()
	if err != nil {
		return nil, err
	}
	if err := req.Approval.validate(digest); err != nil {
		return nil, err
	}

	profile, err := hostProfile(cfg, plan.Host)
	if err != nil {
		return nil, err
	}
	denylist := modelDenylist(cfg)
	decisionByID := make(map[string]routing.Decision, len(plan.Issues))
	labelsByID := make(map[string]ghops.Labels, len(plan.Issues))
	waveByID := make(map[string]int, len(plan.Issues))
	for _, pi := range plan.Issues {
		d, err := decideIssue(profile, pi)
		if err != nil {
			return nil, err
		}
		l := issueLabels(pi, d)
		if err := l.Validate(denylist...); err != nil {
			return nil, err
		}
		decisionByID[pi.ID] = d
		labelsByID[pi.ID] = l
		waveByID[pi.ID] = pi.Wave
	}

	// Phase 2: read-only preflights.
	if err := requireAssistNoLock(env.RepoRoot); err != nil {
		return nil, err
	}
	git, err := gitops.Open(ctx, env.Runner, env.RepoRoot)
	if err != nil {
		return nil, err
	}
	if err := git.RequireClean(ctx, ""); err != nil {
		return nil, err
	}
	gh, err := ghops.Open(ctx, env.Runner, env.RepoRoot)
	if err != nil {
		return nil, err
	}
	repo, err := gh.Repo(ctx)
	if err != nil {
		return nil, err
	}
	branch, err := git.CurrentBranch(ctx)
	if err != nil {
		return nil, err
	}
	if branch != repo.DefaultBranch {
		return nil, fmt.Errorf("primary checkout is on %s, not the default branch %s; activation requires the primary checkout on the default branch", branch, repo.DefaultBranch)
	}
	if areas := planAreaLabels(plan); len(areas) > 0 {
		missing, err := gh.MissingLabels(ctx, areas)
		if err != nil {
			return nil, err
		}
		if len(missing) > 0 {
			return nil, fmt.Errorf("%w: %s; create them in the repository (gh label create <name>) or remove them from the plan, then resubmit for approval", ErrAreaLabelMissing, strings.Join(missing, ", "))
		}
	}
	if _, err := memhubGate(ctx, cfg.Memhub.Mode, execProber{runner: env.Runner, dir: env.RepoRoot}); err != nil {
		return nil, err
	}
	containerAbs := filepath.Join(env.RepoRoot, filepath.FromSlash(WorktreeContainer))
	if err := git.RequireIgnored(ctx, containerAbs); err != nil {
		return nil, err
	}

	// Phase 3: idempotent GitHub prep (the only pre-lock mutation, F6).
	if _, err := gh.EnsureLabelTaxonomy(ctx); err != nil {
		return nil, err
	}

	// Phase 4: enter Delivery.
	ordered := plan.issuesInWaveOrder()
	stateIssues := make([]state.Issue, len(ordered))
	for i, pi := range ordered {
		// Persist the plan's dependency edges, wave, and the derived
		// routing decision so the PR B verbs can enforce dependencies and
		// spawn executors without re-taking the plan document.
		stateIssues[i] = state.Issue{
			PlanID:    pi.ID,
			Title:     pi.Title,
			Phase:     state.PhasePlanned,
			DependsOn: pi.DependsOn,
			Wave:      pi.Wave,
			Decision:  fromRoutingDecision(decisionByID[pi.ID]),
		}
	}
	planRef := state.PlanRef{
		Title:          plan.Title,
		Digest:         digest,
		ApprovedBy:     req.Approval.ApprovedBy,
		ApprovedAt:     req.Approval.ApprovedAt,
		ConfigRevision: cfg.ConfigRevision,
	}
	st, err := state.EnterDelivery(env.RepoRoot, plan.Host, planRef, stateIssues)
	if err != nil {
		return nil, err
	}

	// Phase 5: per issue, wave order, state persisted after every
	// sub-step.
	result := &ActivationResult{SchemaVersion: ActivationSchemaVersion, RunID: st.Run.ID}
	for j, pi := range ordered {
		d := decisionByID[pi.ID]
		l := labelsByID[pi.ID]

		body := issueBody(pi, waveByID, digest)
		body, err = manifest.Upsert(body, manifest.Manifest{
			SchemaVersion:    manifest.SchemaVersion,
			Role:             d.Role,
			Executor:         d.Executor,
			RoutingRationale: d.Rationale,
			Reviewer:         d.Reviewer,
			ConfigRevision:   cfg.ConfigRevision,
		})
		if err != nil {
			return nil, wrapAfterEnter(err)
		}

		number, url, err := gh.CreateIssue(ctx, pi.Title, body, l, denylist...)
		if err != nil {
			return nil, wrapAfterEnter(err)
		}
		st.Run.Issues[j].Number = number
		st.Run.Issues[j].URL = url
		st.Run.Issues[j].Phase = state.PhaseIssueCreated
		if err := state.Save(env.RepoRoot, st); err != nil {
			return nil, wrapAfterEnter(err)
		}

		issueBranch := branchName(number, pi.Title)
		wt, err := git.AddWorktree(ctx, worktreeAbs(env.RepoRoot, number), issueBranch, repo.DefaultBranch)
		if err != nil {
			return nil, wrapAfterEnter(err)
		}
		st.Run.Issues[j].Branch = wt.Branch
		st.Run.Issues[j].Worktree = worktreeRel(number)
		st.Run.Issues[j].Phase = state.PhaseWorktreeReady
		if err := state.Save(env.RepoRoot, st); err != nil {
			return nil, wrapAfterEnter(err)
		}

		result.Issues = append(result.Issues, ActivationResultIssue{
			ID: pi.ID, Number: number, URL: url, Branch: wt.Branch, Worktree: worktreeRel(number),
		})
	}

	// Phase 6: final save, then return.
	if err := state.Save(env.RepoRoot, st); err != nil {
		return nil, wrapAfterEnter(err)
	}
	return result, nil
}

// planAreaLabels collects the plan's area labels across issues in
// document order, deduplicated case-insensitively (GitHub label names
// are), for the activation preflight.
func planAreaLabels(p *PlanDoc) []string {
	seen := map[string]bool{}
	var areas []string
	for _, pi := range p.Issues {
		for _, a := range pi.AreaLabels {
			folded := strings.ToLower(a)
			if seen[folded] {
				continue
			}
			seen[folded] = true
			areas = append(areas, a)
		}
	}
	return areas
}

// issueBody renders the human-prose portion of a created issue's body
// (PRD §13): objective, acceptance criteria, required tests,
// dependencies named by plan id and wave, usage class, and the plan
// digest. manifest.Upsert appends the machine-readable audit record
// after it.
func issueBody(pi PlanIssue, waveByID map[string]int, digest string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "**Objective**\n\n%s\n\n", pi.Objective)

	b.WriteString("**Acceptance criteria**\n\n")
	for _, ac := range pi.AcceptanceCriteria {
		fmt.Fprintf(&b, "- %s\n", ac)
	}
	b.WriteString("\n**Required tests**\n\n")
	for _, rt := range pi.RequiredTests {
		fmt.Fprintf(&b, "- `%s`\n", rt)
	}

	b.WriteString("\n**Dependencies**\n\n")
	if len(pi.DependsOn) == 0 {
		b.WriteString("_none_\n")
	} else {
		for _, dep := range pi.DependsOn {
			fmt.Fprintf(&b, "- %s (wave %d)\n", dep, waveByID[dep])
		}
	}

	fmt.Fprintf(&b, "\n**Usage class:** %s\n\n", pi.UsageClass)
	fmt.Fprintf(&b, "**Plan digest:** `%s`\n", digest)
	return b.String()
}
