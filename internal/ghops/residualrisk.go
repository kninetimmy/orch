package ghops

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

type GitHubUser struct {
	ID, Login  string
	DatabaseID int64
}

var githubLogin = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9-]{0,38}$`)

func (u GitHubUser) valid() bool {
	return githubNodeID.MatchString(u.ID) && githubLogin.MatchString(u.Login) && u.DatabaseID > 0
}

type RiskSnapshot struct {
	Viewer, Admin GitHubUser
	Repository    struct {
		ID, NameWithOwner, URL, ViewerPermission string
		ViewerCanAdminister                      *bool
		DefaultBranchRef                         struct{ Name string }
		Issue                                    struct {
			ID, URL, State, Body string
			Number               int
		}
		PullRequest struct {
			ID, URL, State, HeadRefName, HeadRefOid, BaseRefName, Body string
			Number                                                     int
			HeadRepository, BaseRepository                             struct{ ID string }
			HeadRef                                                    struct {
				Target struct {
					OID               string
					StatusCheckRollup struct {
						Commit   struct{ OID string }
						Contexts struct {
							Nodes      []riskCheckNode
							TotalCount *int
							PageInfo   struct{ HasNextPage *bool }
						}
					}
				}
			}
		}
	}
}

const riskSnapshotQuery = `query OrchRiskSnapshot($repositoryId: ID!, $issue: Int!, $pr: Int!, $admin: ID!, $ci: Boolean!) {
  viewer { id login databaseId }
  admin: node(id: $admin) { ... on User { id login databaseId } }
  repository: node(id: $repositoryId) {
    ... on Repository {
      id nameWithOwner url viewerCanAdminister viewerPermission defaultBranchRef { name }
      issue(number: $issue) { id number url state body }
      pullRequest(number: $pr) {
        id number url state body headRefName headRefOid baseRefName headRepository { id } baseRepository { id }
        headRef @include(if: $ci) {
          target { ... on Commit {
            oid
            statusCheckRollup {
              commit { oid }
              contexts(first: 100) {
                totalCount pageInfo { hasNextPage }
                nodes {
                  kind: __typename
                  ... on CheckRun { id name status conclusion completedAt checkSuite { app { databaseId } commit { oid } repository { id } } }
                  ... on StatusContext { context }
                }
              }
            }
          } }
        }
      }
    }
  }
}`

// RiskRead reads by repository node ID; numeric issue/PR numbers are never
// resolved against a checkout-selected repository.
func (r *ActivationRepo) RiskRead(ctx context.Context, issue, pr int, admin string, ci bool) (RiskSnapshot, error) {
	var out RiskSnapshot
	err := r.graphql(ctx, "OrchRiskSnapshot", riskSnapshotQuery, map[string]any{
		"repositoryId": r.id, "issue": issue, "pr": pr, "admin": admin, "ci": ci,
	}, &out)
	if err != nil {
		return out, err
	}
	repo := out.Repository
	if repo.ID != r.id || !githubNodeID.MatchString(repo.Issue.ID) || !githubNodeID.MatchString(repo.PullRequest.ID) ||
		repo.Issue.Number != issue || repo.PullRequest.Number != pr ||
		repo.PullRequest.HeadRepository.ID != r.id || repo.PullRequest.BaseRepository.ID != r.id {
		return out, fmt.Errorf("GitHub did not verify the issue and same-repository PR in repository %q", r.id)
	}
	if !out.Viewer.valid() || !out.Admin.valid() || out.Admin.ID != admin ||
		out.Viewer.ID == out.Admin.ID || out.Viewer.DatabaseID == out.Admin.DatabaseID ||
		repo.ViewerCanAdminister == nil || *repo.ViewerCanAdminister || repo.ViewerPermission != "WRITE" {
		return out, fmt.Errorf("residual risk requires distinct GitHub users and a non-admin automation viewer with WRITE permission")
	}
	return out, nil
}

const riskRESTVersion = "X-GitHub-Api-Version: 2026-03-10"

func (r *ActivationRepo) onRiskTarget(target RepoTarget) bool {
	return r.id == target.ID && r.host == "github.com" && r.host == target.Host && target.Valid()
}

func (r *ActivationRepo) RiskRoles(ctx context.Context, target RepoTarget, viewer, admin GitHubUser) error {
	if !r.onRiskTarget(target) || !viewer.valid() || !admin.valid() {
		return fmt.Errorf("cannot verify GitHub repository roles for residual risk")
	}
	for _, entry := range []struct {
		user GitHubUser
		role string
	}{{viewer, "write"}, {admin, "admin"}} {
		var permission struct {
			Permission string
			RoleName   string `json:"role_name"`
			User       struct {
				ID          int64
				Login, Type string
			}
		}
		path := "repos/" + target.Owner + "/" + target.Repository + "/collaborators/" + entry.user.Login + "/permission"
		if err := r.gh.ghJSON(ctx, &permission, "api", "--hostname", r.host, "-H", riskRESTVersion, path); err != nil {
			return fmt.Errorf("verify GitHub repository role: %w", err)
		}
		if permission.User.ID != entry.user.DatabaseID || !strings.EqualFold(permission.User.Login, entry.user.Login) ||
			permission.User.Type != "User" || permission.Permission != entry.role || permission.RoleName != entry.role {
			return fmt.Errorf("GitHub did not confirm the required standard %s role for %s", entry.role, entry.user.Login)
		}
	}
	return nil
}

type RiskPolicy struct {
	RulesetID            int64               `json:"ruleset_id"`
	UpdatedAt            time.Time           `json:"ruleset_updated_at"`
	Strict               bool                `json:"strict"`
	DoNotEnforceOnCreate bool                `json:"do_not_enforce_on_create"`
	Required             []RiskRequiredCheck `json:"required"`
}

type RiskRequiredCheck struct {
	Context       string `json:"context"`
	IntegrationID int64  `json:"integration_id"`
}

// RiskRequiredPolicy supports one repository-local, app-pinned required-check
// ruleset. GitHub's effective branch endpoint includes inherited active rules.
// The separate branch endpoint must explicitly rule out classic protection.
func (r *ActivationRepo) RiskRequiredPolicy(ctx context.Context, target RepoTarget, base string) (RiskPolicy, error) {
	var policy RiskPolicy
	if !r.onRiskTarget(target) || base == "" || base == "." || base == ".." {
		return policy, fmt.Errorf("cannot identify the GitHub base branch for residual risk")
	}
	repo := "repos/" + target.Owner + "/" + target.Repository
	ref := url.PathEscape(base)
	var branch struct {
		Name       string
		Protected  *bool
		Protection struct {
			Enabled  *bool
			Required struct {
				EnforcementLevel string `json:"enforcement_level"`
				Contexts         []string
				Checks           []json.RawMessage
			} `json:"required_status_checks"`
		}
	}
	if err := r.gh.ghJSON(ctx, &branch, "api", "--hostname", r.host, "-H", riskRESTVersion, repo+"/branches/"+ref); err != nil {
		return policy, fmt.Errorf("verify branch protection: %w", err)
	}
	legacy := branch.Protection.Required
	if branch.Name != base || branch.Protected == nil || !*branch.Protected || branch.Protection.Enabled == nil ||
		*branch.Protection.Enabled || legacy.EnforcementLevel != "off" || legacy.Contexts == nil || len(legacy.Contexts) != 0 ||
		legacy.Checks == nil || len(legacy.Checks) != 0 {
		return policy, fmt.Errorf("residual risk requires a protected base with explicitly absent classic branch protection")
	}
	var pages [][]struct {
		Type       string
		SourceType string `json:"ruleset_source_type"`
		Source     string `json:"ruleset_source"`
		ID         int64  `json:"ruleset_id"`
		Parameters json.RawMessage
	}
	if err := r.gh.ghJSON(ctx, &pages, "api", "--hostname", r.host, "-H", riskRESTVersion,
		"--paginate", "--slurp", repo+"/rules/branches/"+ref+"?per_page=100"); err != nil {
		return policy, fmt.Errorf("verify effective GitHub branch rules: %w", err)
	}
	rulesets := make(map[int64]bool)
	for _, page := range pages {
		if page == nil {
			return RiskPolicy{}, fmt.Errorf("incomplete GitHub branch rules")
		}
		for _, rule := range page {
			if rule.SourceType != "Repository" || !strings.EqualFold(rule.Source, target.Owner+"/"+target.Repository) || rule.ID <= 0 {
				return RiskPolicy{}, fmt.Errorf("residual risk does not support inherited or unverified effective GitHub rules")
			}
			rulesets[rule.ID] = true
			switch rule.Type {
			case "deletion", "non_fast_forward", "pull_request", "required_linear_history", "required_signatures":
				// These known non-CI rules are left to GitHub, with no bypass option.
				continue
			case "required_status_checks":
			default:
				return RiskPolicy{}, fmt.Errorf("residual risk does not support effective GitHub rule %q", rule.Type)
			}
			var p struct {
				Strict   *bool               `json:"strict_required_status_checks_policy"`
				OnCreate *bool               `json:"do_not_enforce_on_create"`
				Checks   []RiskRequiredCheck `json:"required_status_checks"`
			}
			dec := json.NewDecoder(strings.NewReader(string(rule.Parameters)))
			dec.DisallowUnknownFields()
			if policy.RulesetID != 0 || dec.Decode(&p) != nil || p.Strict == nil || p.OnCreate == nil || len(p.Checks) == 0 {
				return RiskPolicy{}, fmt.Errorf("residual risk requires one verifiable, nonempty GitHub required-check rule")
			}
			sort.Slice(p.Checks, func(i, j int) bool { return p.Checks[i].Context < p.Checks[j].Context })
			for i, check := range p.Checks {
				if strings.TrimSpace(check.Context) == "" || check.IntegrationID <= 0 || (i > 0 && p.Checks[i-1].Context == check.Context) {
					return RiskPolicy{}, fmt.Errorf("GitHub required checks must have unique contexts and a pinned app")
				}
			}
			policy = RiskPolicy{RulesetID: rule.ID, Strict: *p.Strict, DoNotEnforceOnCreate: *p.OnCreate, Required: p.Checks}
		}
	}
	if policy.RulesetID == 0 {
		return policy, fmt.Errorf("no supported effective GitHub required-check rule")
	}
	ids := make([]int64, 0, len(rulesets))
	for id := range rulesets {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	for _, id := range ids {
		var rule struct {
			ID           int64
			Target       string
			SourceType   string `json:"source_type"`
			Source       string
			Enforcement  string
			ViewerBypass string    `json:"current_user_can_bypass"`
			UpdatedAt    time.Time `json:"updated_at"`
		}
		if err := r.gh.ghJSON(ctx, &rule, "api", "--hostname", r.host, "-H", riskRESTVersion,
			repo+"/rulesets/"+strconv.FormatInt(id, 10)+"?includes_parents=false"); err != nil {
			return RiskPolicy{}, fmt.Errorf("verify effective GitHub ruleset bypass: %w", err)
		}
		if rule.ID != id || rule.Target != "branch" || rule.SourceType != "Repository" || !strings.EqualFold(rule.Source, target.Owner+"/"+target.Repository) ||
			rule.Enforcement != "active" || rule.ViewerBypass != "never" || rule.UpdatedAt.IsZero() {
			return RiskPolicy{}, fmt.Errorf("GitHub did not verify that automation cannot bypass active ruleset %d", id)
		}
		if id == policy.RulesetID {
			policy.UpdatedAt = rule.UpdatedAt
		}
	}
	return policy, nil
}

type riskCheckNode struct {
	Kind, ID, Name, Context, Status, Conclusion string
	CompletedAt                                 time.Time
	CheckSuite                                  struct {
		App        struct{ DatabaseID int64 }
		Commit     struct{ OID string }
		Repository struct{ ID string }
	}
}

type RiskCheck struct {
	RiskRequiredCheck
	CheckID     string    `json:"check_id"`
	CompletedAt time.Time `json:"completed_at"`
	Status      string    `json:"status"`
	Conclusion  string    `json:"conclusion"`
}

// RiskCI verifies every policy entry, not merely the checks that reported.
// Ambiguous same-name runs/statuses and truncated rollups are unsupported.
func (s RiskSnapshot) RiskCI(policy RiskPolicy, now time.Time) ([]RiskCheck, error) {
	pr := s.Repository.PullRequest
	head := pr.HeadRef.Target
	rollup := head.StatusCheckRollup
	page := rollup.Contexts
	if head.OID == "" || head.OID != pr.HeadRefOid || rollup.Commit.OID != head.OID || page.Nodes == nil ||
		page.PageInfo.HasNextPage == nil || *page.PageInfo.HasNextPage || page.TotalCount == nil || *page.TotalCount != len(page.Nodes) {
		return nil, fmt.Errorf("exact-head GitHub check rollup is missing or incomplete")
	}
	byName := make(map[string][]riskCheckNode)
	for _, n := range page.Nodes {
		switch {
		case n.Kind == "CheckRun" && n.Name != "":
			byName[n.Name] = append(byName[n.Name], n)
		case n.Kind == "StatusContext" && n.Context != "":
			byName[n.Context] = append(byName[n.Context], n)
		default:
			return nil, fmt.Errorf("exact-head GitHub check rollup contains an unknown context")
		}
	}
	if len(policy.Required) == 0 {
		return nil, fmt.Errorf("no required GitHub checks")
	}
	out := make([]RiskCheck, 0, len(policy.Required))
	seen := make(map[string]bool)
	for _, required := range policy.Required {
		matches := byName[required.Context]
		if len(matches) != 1 {
			return nil, fmt.Errorf("required GitHub check %q is missing or ambiguous", required.Context)
		}
		n := matches[0]
		if n.Kind != "CheckRun" || !githubNodeID.MatchString(n.ID) || seen[n.ID] || n.Status != "COMPLETED" || n.Conclusion != "SUCCESS" ||
			n.CompletedAt.IsZero() || n.CompletedAt.After(now) || n.CheckSuite.Commit.OID != head.OID ||
			n.CheckSuite.Repository.ID != s.Repository.ID || n.CheckSuite.App.DatabaseID != required.IntegrationID {
			return nil, fmt.Errorf("required GitHub check %q lacks exact-head SUCCESS from app %d", required.Context, required.IntegrationID)
		}
		seen[n.ID] = true
		out = append(out, RiskCheck{required, n.ID, n.CompletedAt, n.Status, n.Conclusion})
	}
	return out, nil
}

type RiskDecision struct {
	ID, URL, Body, State                                                                    string
	Author                                                                                  GitHubUser
	CreatedAt, SubmittedAt                                                                  time.Time
	Editor, LastEditedAt                                                                    json.RawMessage
	IncludesCreatedEdit, IsMinimized, ViewerDidAuthor, CreatedViaEmail, IsCopilotCodeReview *bool
	Commit                                                                                  struct{ OID string }
	PullRequest                                                                             struct{ ID string }
	Repository                                                                              struct{ ID string }
}

const riskDecisionQuery = `query OrchRiskDecision($review: ID!) {
  viewer { id }
  node(id: $review) {
    ... on PullRequestReview {
      id url body state createdAt submittedAt editor { login } lastEditedAt includesCreatedEdit
      isMinimized viewerDidAuthor createdViaEmail isCopilotCodeReview
      author { ... on User { id login databaseId } }
      commit { oid } pullRequest { id } repository { id }
    }
  }
}`

func (r *ActivationRepo) RiskDecision(ctx context.Context, id string, s RiskSnapshot) (RiskDecision, error) {
	var out struct {
		Viewer struct{ ID string }
		Node   RiskDecision
	}
	if len(id) > 130 || !githubNodeID.MatchString(id) {
		return out.Node, fmt.Errorf("provide a GitHub PR review node ID")
	}
	if err := r.graphql(ctx, "OrchRiskDecision", riskDecisionQuery, map[string]any{"review": id}, &out); err != nil {
		return out.Node, err
	}
	d := out.Node
	pr := s.Repository.PullRequest
	if out.Viewer.ID != s.Viewer.ID || d.ID != id || d.Repository.ID != r.id || d.PullRequest.ID != pr.ID ||
		!strings.HasPrefix(d.URL, pr.URL+"#pullrequestreview-") || d.Author != s.Admin || d.Commit.OID != pr.HeadRefOid ||
		d.State != "COMMENTED" || string(d.Editor) != "null" || string(d.LastEditedAt) != "null" {
		return d, fmt.Errorf("GitHub decision must be an unedited COMMENT review by the designated admin on the exact registered PR head")
	}
	if reviewID, err := strconv.ParseUint(strings.TrimPrefix(d.URL, pr.URL+"#pullrequestreview-"), 10, 64); err != nil || reviewID == 0 {
		return d, fmt.Errorf("GitHub did not return a canonical PR review permalink")
	}
	for _, flag := range []*bool{d.IncludesCreatedEdit, d.IsMinimized, d.ViewerDidAuthor, d.CreatedViaEmail, d.IsCopilotCodeReview} {
		if flag == nil || *flag {
			return d, fmt.Errorf("GitHub decision edit or author provenance is unverified")
		}
	}
	return d, nil
}

const riskAuditMutation = `mutation OrchRiskAudit($issue: UpdateIssueInput!, $pr: UpdatePullRequestInput!) {
  issue: updateIssue(input: $issue) { issue { id body state } }
  pr: updatePullRequest(input: $pr) { pullRequest { id body state headRefOid baseRefName } }
}`

func (r *ActivationRepo) WriteRiskAudit(ctx context.Context, s RiskSnapshot, issueBody, prBody string) error {
	var out struct {
		Issue struct {
			Issue struct{ ID, Body, State string }
		}
		PR struct {
			PullRequest struct{ ID, Body, State, HeadRefOid, BaseRefName string }
		}
	}
	issueID, prID := s.Repository.Issue.ID, s.Repository.PullRequest.ID
	if err := r.graphql(ctx, "OrchRiskAudit", riskAuditMutation, map[string]any{
		"issue": map[string]string{"id": issueID, "body": issueBody},
		"pr":    map[string]string{"pullRequestId": prID, "body": prBody},
	}, &out); err != nil {
		return err
	}
	pr := out.PR.PullRequest
	if out.Issue.Issue.ID != issueID || out.Issue.Issue.Body != issueBody || out.Issue.Issue.State != "OPEN" ||
		pr.ID != prID || pr.Body != prBody || pr.State != "OPEN" || pr.HeadRefOid != s.Repository.PullRequest.HeadRefOid ||
		pr.BaseRefName != s.Repository.PullRequest.BaseRefName {
		return fmt.Errorf("GitHub did not confirm both residual-risk audit writes on the unchanged OPEN issue/PR")
	}
	return nil
}

const riskMergeMutation = `mutation OrchRiskMerge($input: MergePullRequestInput!) {
  mergePullRequest(input: $input) { pullRequest { id state headRefOid repository { id } } }
}`

// MergeRiskPR uses the same GitHub expected-head check as --match-head-commit,
// directly on the verified PR node. It cannot enqueue or enable auto-merge.
func (r *ActivationRepo) MergeRiskPR(ctx context.Context, s RiskSnapshot, strategy, head string, c Confirmation) error {
	if !c.ok {
		return ErrNotConfirmed
	}
	pr := s.Repository.PullRequest
	flag, ok := mergeFlags[strategy]
	if !ok || head == "" || pr.HeadRefOid != head || pr.State != "OPEN" {
		return fmt.Errorf("residual-risk merge requires an open PR pinned to the approved head and a known strategy")
	}
	var out struct {
		MergePullRequest struct {
			PullRequest struct {
				ID, State, HeadRefOid string
				Repository            struct{ ID string }
			}
		}
	}
	if err := r.graphql(ctx, "OrchRiskMerge", riskMergeMutation, map[string]any{"input": map[string]string{
		"pullRequestId": pr.ID, "expectedHeadOid": head, "mergeMethod": strings.ToUpper(strings.TrimPrefix(flag, "--")),
	}}, &out); err != nil {
		return err
	}
	merged := out.MergePullRequest.PullRequest
	if merged.ID != pr.ID || merged.Repository.ID != r.id || merged.State != "MERGED" || merged.HeadRefOid != head {
		return fmt.Errorf("GitHub did not confirm the merge of the approved residual-risk PR head")
	}
	return nil
}
