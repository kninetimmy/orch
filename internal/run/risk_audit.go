package run

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"

	"github.com/kninetimmy/orch/internal/ghops"
	"github.com/kninetimmy/orch/internal/manifest"
	"github.com/kninetimmy/orch/internal/state"
)

var riskAuditName = regexp.MustCompile(`^risk-(hold|acceptance|review)-[0-9]+$`)

type riskAudit struct {
	target    *ghops.ActivationRepo
	snapshot  ghops.RiskSnapshot
	policy    ghops.RiskPolicy
	issue, pr manifest.Manifest
}

// Approved work and effective CI policy are bound separately: changing either
// cannot yield a reusable decision even at the exact same commit.
type riskBinding struct {
	RunID         string `json:"run_id"`
	IssueNumber   int    `json:"issue_number"`
	PRNumber      int    `json:"pr_number"`
	RepositoryID  string `json:"repository_id"`
	IssueID       string `json:"issue_id"`
	PullRequestID string `json:"pull_request_id"`
	BaseRef       string `json:"base_ref"`
	AutomationID  string `json:"automation_id"`
	AdminID       string `json:"admin_id"`
	PlanDigest    string `json:"plan_digest"`
	WorkDigest    string `json:"work_digest"`
	PolicyDigest  string `json:"policy_digest"`
	RepairNumber  int    `json:"repair_number"`
}

func (a riskAudit) binding(c *verbCtx) (riskBinding, error) {
	issue, plan := c.issue(), c.st.Run.Plan
	work, err := contentDigest(struct {
		PlanID           string
		Objective        string
		Criteria, Tests  []string
		Local, DependsOn []string
		Decision         *state.Decision
	}{issue.PlanID, issue.Objective, issue.AcceptanceCriteria, issue.RequiredTests, issue.TestsCIDoesNotRun, issue.DependsOn, issue.Decision})
	if err != nil {
		return riskBinding{}, err
	}
	policy, err := contentDigest(a.policy)
	if err != nil {
		return riskBinding{}, err
	}
	repo := a.snapshot.Repository
	return riskBinding{c.st.Run.ID, issue.Number, issue.PRNumber, repo.ID, repo.Issue.ID, repo.PullRequest.ID,
		repo.PullRequest.BaseRefName, a.snapshot.Viewer.ID, a.snapshot.Admin.ID, plan.Digest, work, policy, len(issue.Repairs)}, nil
}

func openRiskAudit(ctx context.Context, c *verbCtx, gh *ghops.GH, head string, ci bool) (riskAudit, error) {
	var a riskAudit
	is := c.issue()
	if c.cfg.ResidualRisk == nil || c.st.Run.Plan.GitHubRepositoryID == "" || runGrantID(c.st) != "" {
		return a, fmt.Errorf("%w: residual risk requires an opted-in, human-approved, repository-bound run; grants cannot use it", ErrBadApproval)
	}
	if is.Decision == nil || !headOIDPattern.MatchString(head) {
		return a, fmt.Errorf("%w: residual risk requires routed review at a full PR head", ErrReviewStale)
	}
	if err := checkApprovedWork(is); err != nil {
		return a, err
	}
	if err := checkDependencies(c.st.Run, is); err != nil {
		return a, err
	}
	repo, err := gh.RepoWithID(ctx)
	if err != nil {
		return a, err
	}
	target, err := repo.Target()
	if err != nil {
		return a, err
	}
	if target.Host != "github.com" || target.ID != c.st.Run.Plan.GitHubRepositoryID {
		return a, fmt.Errorf("%w: live GitHub repository does not match the approved repository", ErrBadApproval)
	}
	a.target, err = gh.ForTarget(target)
	if err != nil {
		return a, err
	}
	read := func(withCI bool) (ghops.RiskSnapshot, error) {
		s, err := a.target.RiskRead(ctx, is.Number, is.PRNumber, c.cfg.ResidualRisk.AdminUserID, withCI)
		if err != nil {
			return s, err
		}
		r := s.Repository
		p := r.PullRequest
		if r.URL != repo.URL || !strings.EqualFold(r.NameWithOwner, repo.NameWithOwner) ||
			r.Issue.URL != is.URL || r.Issue.URL != fmt.Sprintf("%s/issues/%d", repo.URL, is.Number) || r.Issue.State != "OPEN" ||
			p.URL != is.PRURL || p.URL != fmt.Sprintf("%s/pull/%d", repo.URL, is.PRNumber) || p.State != "OPEN" ||
			p.HeadRefName != is.Branch || p.BaseRefName == "" || p.BaseRefName != repo.DefaultBranch ||
			p.BaseRefName != r.DefaultBranchRef.Name || p.HeadRefOid != head {
			return s, fmt.Errorf("%w: registered GitHub issue/PR, OPEN state, base, branch or exact head do not match", ErrReviewStale)
		}
		return s, nil
	}
	before, err := read(false)
	if err != nil {
		return a, err
	}
	if err := a.target.RiskRoles(ctx, target, before.Viewer, before.Admin); err != nil {
		return a, err
	}
	a.policy, err = a.target.RiskRequiredPolicy(ctx, target, before.Repository.PullRequest.BaseRefName)
	if err != nil {
		return a, err
	}
	a.snapshot = before
	if ci {
		a.snapshot, err = read(true)
		if err != nil {
			return a, err
		}
		br, ar := before.Repository, a.snapshot.Repository
		if before.Viewer != a.snapshot.Viewer || before.Admin != a.snapshot.Admin || br.Issue != ar.Issue ||
			br.PullRequest.ID != ar.PullRequest.ID || br.PullRequest.Body != ar.PullRequest.Body {
			return a, fmt.Errorf("%w: GitHub identities or audit changed during residual-risk verification; inspect again", ErrReviewStale)
		}
	}
	a.issue, err = manifest.Parse(a.snapshot.Repository.Issue.Body)
	if err != nil {
		return a, fmt.Errorf("residual-risk issue audit: %w", err)
	}
	a.pr, err = manifest.Parse(a.snapshot.Repository.PullRequest.Body)
	if err != nil {
		return a, fmt.Errorf("residual-risk PR audit: %w", err)
	}
	for _, m := range []manifest.Manifest{a.issue, a.pr} {
		if err := checkRepairManifest(is, m, c.st.Run.Plan.ConfigRevision); err != nil {
			return a, err
		}
		for _, v := range m.Verifications {
			if manualCIAuditPattern.MatchString(v.Name) || v.Name == mergeApprovalName || v.Name == planApprovalName {
				return a, fmt.Errorf("%w: residual risk cannot stack with manual CI, already-merged acknowledgment, or a grant", ErrBadApproval)
			}
		}
		for _, b := range is.Blocks {
			if h := b.RiskHold; h != nil {
				if _, err := riskReceipt(m, riskHoldName(b.ID), h.AuditDigest); err != nil {
					return a, err
				}
				if h.Acceptance != nil {
					if _, err := riskReceipt(m, riskAcceptanceName(b.ID), h.Acceptance.AuditDigest); err != nil {
						return a, err
					}
				}
			}
		}
	}
	return a, nil
}

func riskHoldName(n int) string       { return fmt.Sprintf("risk-hold-%d", n) }
func riskAcceptanceName(n int) string { return fmt.Sprintf("risk-acceptance-%d", n) }
func riskReviewName(n int) string     { return fmt.Sprintf("risk-review-%d", n) }

func riskReceipt(m manifest.Manifest, name, digest string) (manifest.Verification, error) {
	v, found, err := uniqueRiskEntry(m, name)
	if err != nil {
		return v, err
	}
	actual, err := contentDigest(v)
	if err != nil {
		return v, err
	}
	if !found || actual != digest {
		return v, fmt.Errorf("%w: missing or changed residual-risk audit %s", ErrReviewStale, name)
	}
	return v, nil
}

func uniqueRiskEntry(m manifest.Manifest, name string) (manifest.Verification, bool, error) {
	var v manifest.Verification
	found := false
	for _, cur := range m.Verifications {
		if cur.Name == name {
			if found {
				return v, false, fmt.Errorf("%w: duplicate audit verification %s", ErrReviewStale, name)
			}
			v, found = cur, true
		}
	}
	return v, found, nil
}

func riskVerification(name, result, head, at string, record any) (manifest.Verification, error) {
	detail, err := json.Marshal(record)
	return manifest.Verification{Name: name, Result: result, CommitOID: head, At: at, Detail: string(detail)}, err
}

func addRiskEntry(m *manifest.Manifest, v manifest.Verification) error {
	current, exists, err := uniqueRiskEntry(*m, v.Name)
	if err != nil {
		return err
	}
	if exists && current != v {
		return fmt.Errorf("%w: conflicting residual-risk audit %s; preserve it and investigate", ErrReviewStale, v.Name)
	}
	setVerification(m, v)
	return nil
}

func (a riskAudit) write(ctx context.Context) error {
	if !sameReadinessAudit(a.issue, a.pr) {
		return fmt.Errorf("%w: issue and PR audit disagree beyond the exact residual-risk retry", ErrReviewStale)
	}
	r := a.snapshot.Repository
	i, err := upsertCapped(r.Issue.Body, a.issue)
	if err != nil {
		return err
	}
	p, err := upsertCapped(r.PullRequest.Body, a.pr)
	if err != nil {
		return err
	}
	if i == r.Issue.Body && p == r.PullRequest.Body {
		return nil
	}
	return a.target.WriteRiskAudit(ctx, a.snapshot, i, p)
}
