package run

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kninetimmy/orch/internal/execx/execxtest"
	"github.com/kninetimmy/orch/internal/lockfile"
	"github.com/kninetimmy/orch/internal/manifest"
	"github.com/kninetimmy/orch/internal/paths"
	"github.com/kninetimmy/orch/internal/state"
)

const readinessHead = "0123456789abcdef0123456789abcdef01234567"
const readinessOldHead = "abcdef0123456789abcdef0123456789abcdef0123"

func readinessManifest() manifest.Manifest {
	m := baseManifest()
	m.Verifications = []manifest.Verification{
		{Name: "acceptance-criterion-1", Result: JudgmentSatisfied, CommitOID: readinessHead, Detail: "verified", At: "2026-11-11T12:00:00Z"},
		{Name: "review-cycle-1", Result: VerdictApprove, CommitOID: readinessHead, Detail: "reviewed", At: "2026-11-11T12:00:00Z"},
	}
	return m
}

func readinessBody(t *testing.T, m manifest.Manifest) string {
	t.Helper()
	body, err := manifest.Upsert("issue prose\n", m)
	if err != nil {
		t.Fatal(err)
	}
	return body
}

func readinessPRCall(t *testing.T, issue state.Issue, head, prState, body string) execxtest.Call {
	t.Helper()
	call := ghPRViewCall(issue.PRNumber, prState, head)
	call.Stdout = fmt.Sprintf(`{"number":%d,"state":%q,"url":%q,"headRefName":%q,"headRefOid":%q,"body":%s}`,
		issue.PRNumber, prState, issue.PRURL, issue.Branch, head, jsonQuote(t, body))
	return call
}

// The exact read-only allowlist is enforced by Script. There is no real git,
// GitHub or memhub runner, even with metrics and a Claude session enabled.
func inspectReadiness(t *testing.T, root string, number int, calls ...execxtest.Call) (*ReadinessDoc, error) {
	t.Helper()
	before := readinessTree(t, root)
	canonical, err := paths.Canonical(root)
	if err != nil {
		t.Fatal(err)
	}
	for i := range calls {
		calls[i].Dir = canonical
	}
	script := &execxtest.Script{T: t, Calls: calls}
	doc, err := Readiness(t.Context(), Env{RepoRoot: root, Runner: script, ClaudeSession: architectSession, Now: fixedNow}, number)
	script.AssertExhausted()
	if readinessTree(t, root) != before {
		t.Fatal("readiness changed repository names, kinds, modes, modification times or bytes")
	}
	assertNoMetricsDir(t, root)
	return doc, err
}

func readinessTree(t *testing.T, root string) string {
	t.Helper()
	var tree strings.Builder
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		fmt.Fprintf(&tree, "%s\x00%s\x00", path, info.Mode())
		if info.Mode().IsRegular() {
			data, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			fmt.Fprintf(&tree, "%d\x00%s", info.ModTime().UnixNano(), data)
		}
		tree.WriteByte(0)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return tree.String()
}

func readinessCalls(t *testing.T, issue state.Issue, head, prState, body, mirror, ci string) []execxtest.Call {
	t.Helper()
	pr := readinessPRCall(t, issue, head, prState, mirror)
	calls := []execxtest.Call{ghAuth(), pr, ghIssueViewCall(t, issue.Number, "OPEN", body)}
	if ci == "no-checks" {
		calls = append(calls, ghRollupEmptyCall(issue.PRNumber))
	} else {
		calls = append(calls,
			execxtest.Call{Name: "gh", Args: []string{"pr", "view", fmt.Sprint(issue.PRNumber), "--json", "statusCheckRollup"}, Stdout: `{"statusCheckRollup":[{}]}`},
			execxtest.Call{Name: "gh", Args: []string{"pr", "checks", fmt.Sprint(issue.PRNumber), "--required", "--json", "name,state,bucket,link"}, Stdout: ci})
	}
	return append(calls, pr)
}

func TestReadinessEvidence(t *testing.T) {
	for _, name := range []string{
		"current review", "recorded gate", "stale review", "stale gate", "request changes", "missing cycle", "missing commit", "invalid commit", "duplicate cycle", "future cycle", "missing earlier cycle",
		"contradictory verdict", "missing judgment", "stale judgment", "unsatisfied judgment", "extra judgment", "wrong work", "wrong routing", "wrong revision", "mirror mismatch", "detail rotation", "missing mirror", "missing manifest", "drifted manifest", "wrong phase", "closed PR", "missing PR",
	} {
		t.Run(name, func(t *testing.T) {
			issue := fixtureIssue("a", 1, state.PhaseInReview)
			issue.LastReviewVerdict, issue.ReviewCycles = VerdictApprove, 1
			m := readinessManifest()
			head, prState := readinessHead, "OPEN"
			wantMet, wantCurrent, wantDrift := false, false, false
			switch name {
			case "current review", "detail rotation":
				wantMet, wantCurrent = true, true
			case "recorded gate":
				issue.Phase, issue.ApprovedHeadOID = state.PhaseAwaitingMerge, readinessHead
				wantMet, wantCurrent = true, true
			case "stale review":
				head, wantDrift = readinessOldHead, true
			case "stale gate":
				issue.Phase, issue.ApprovedHeadOID = state.PhaseAwaitingMerge, readinessOldHead
				wantCurrent, wantDrift = true, true
			case "request changes":
				issue.LastReviewVerdict, m.Verifications[1].Result = VerdictRequestChanges, VerdictRequestChanges
				wantCurrent = true
			case "missing cycle":
				m.Verifications = m.Verifications[:1]
			case "missing commit":
				m.Verifications[1].CommitOID = ""
			case "invalid commit":
				m.Verifications[1].CommitOID = "HEAD"
				wantDrift = true
			case "duplicate cycle":
				m.Verifications = append(m.Verifications, m.Verifications[1])
			case "future cycle":
				m.Verifications[1].Name = "review-cycle-2"
			case "missing earlier cycle":
				issue.ReviewCycles, m.Verifications[1].Name = 2, "review-cycle-2"
			case "contradictory verdict":
				m.Verifications[1].Result = VerdictRequestChanges
			case "missing judgment":
				m.Verifications = m.Verifications[1:]
			case "stale judgment":
				m.Verifications[0].CommitOID = readinessOldHead
			case "unsatisfied judgment":
				m.Verifications[0].Result = JudgmentUnsatisfied
			case "extra judgment":
				m.Verifications = append(m.Verifications, manifest.Verification{Name: "acceptance-criterion-2", Result: JudgmentSatisfied, CommitOID: readinessHead})
			case "wrong work":
				m.Objective = "different work"
			case "wrong routing":
				m.Reviewer.Model = "another-reviewer"
			case "wrong revision":
				m.ConfigRevision = "other"
			case "wrong phase":
				issue.Phase, wantCurrent = state.PhasePROpen, true
			case "closed PR":
				prState, wantCurrent = "CLOSED", true
			case "missing PR":
				issue = fixtureIssue("a", 1, state.PhaseDispatched)
			}
			body := readinessBody(t, m)
			mirror := body
			switch name {
			case "mirror mismatch":
				m.Verifications[1].CommitOID = readinessOldHead
				mirror = readinessBody(t, m)
			case "detail rotation":
				m.Verifications[0].Detail, m.Verifications[1].Detail = "", ""
				mirror = readinessBody(t, m)
			case "missing mirror":
				mirror = "no audit record"
			case "missing manifest":
				body = "no audit record"
			case "drifted manifest":
				body = strings.Replace(body, "reviewed", "hand-edited", 1)
			}
			root := setupDeliveryRepoWithConfig(t, metricsEnabledConfigTOML, "r1", []state.Issue{issue})
			var calls []execxtest.Call
			if issue.PRNumber != 0 {
				calls = readinessCalls(t, issue, head, prState, body, mirror, "no-checks")
			}
			doc, err := inspectReadiness(t, root, 1, calls...)
			if err != nil {
				t.Fatal(err)
			}
			if doc.SchemaVersion != 1 || doc.Phase != issue.Phase || doc.PRNumber != issue.PRNumber || doc.MergeReport.PrerequisitesMet != wantMet || doc.Review.Current != wantCurrent || doc.HeadDrift != wantDrift || doc.Merge.Authorized || len(doc.Merge.Requirements) == 0 || !strings.Contains(doc.Merge.ApprovalRequired, MergeApprovalStatement) {
				t.Fatalf("readiness=%+v review=%+v merge-report=%+v merge=%+v", doc, doc.Review, doc.MergeReport, doc.Merge)
			}
			if name == "current review" || name == "recorded gate" {
				if doc.Review.Evidence == nil || doc.Review.Evidence.CommitOID != readinessHead || doc.MergeReport.Preview == nil || doc.MergeReport.Preview.HeadOID != readinessHead || doc.MergeReport.Preview.NoCIStatement == "" {
					t.Fatalf("lost exact evidence: %+v", doc)
				}
				if doc.MergeReport.Recorded != (name == "recorded gate") || doc.MergeReport.HeadCurrent != (name == "recorded gate") {
					t.Fatalf("preview synthesized a recorded gate: %+v", doc.MergeReport)
				}
			}
			if name == "stale review" && (doc.Review.Evidence == nil || doc.Review.Evidence.CommitOID != readinessHead || doc.HeadOID != readinessOldHead) {
				t.Fatal("stale review lost its exact reviewed/live commit identities")
			}
		})
	}
}

func TestReadinessCIAndProbeFailures(t *testing.T) {
	for _, name := range []string{"passing", "pending", "failing", "no-checks", "none required", "auth failure", "PR failure", "issue failure", "rollup failure", "checks failure", "checks empty exit1", "checks empty exit8", "checks null", "checks malformed", "head moved", "final PR failure", "invalid live head", "wrong PR branch"} {
		t.Run(name, func(t *testing.T) {
			issue := fixtureIssue("a", 1, state.PhaseInReview)
			issue.LastReviewVerdict, issue.ReviewCycles = VerdictApprove, 1
			root := setupDeliveryRepoWithConfig(t, metricsEnabledConfigTOML, "r1", []state.Issue{issue})
			body := readinessBody(t, readinessManifest())
			bucket, checkState := "pass", "SUCCESS"
			switch name {
			case "pending":
				bucket, checkState = "pending", "IN_PROGRESS"
			case "failing":
				bucket, checkState = "fail", "FAILURE"
			}
			ci := fmt.Sprintf(`[{"name":"test","state":%q,"bucket":%q,"link":"https://checks/test"}]`, checkState, bucket)
			switch name {
			case "none required":
				ci = ""
			case "no-checks":
				ci = "no-checks"
			}
			calls := readinessCalls(t, issue, readinessHead, "OPEN", body, body, ci)
			if name == "none required" {
				calls[4].Stderr = "no required checks reported on the 'main' branch"
			}
			if name == "pending" {
				calls[4].Exit = 8 // Documented gh pending exit is data, not failure.
			}
			if name == "failing" {
				calls[4].Exit = 1 // Likewise for failing required checks.
			}
			wantErr := ""
			switch name {
			case "auth failure", "PR failure", "issue failure", "rollup failure", "checks failure", "final PR failure":
				index := map[string]int{"auth failure": 0, "PR failure": 1, "issue failure": 2, "rollup failure": 3, "checks failure": 4, "final PR failure": 5}[name]
				calls[index].Exit, calls[index].Stderr = 2, "read probe failed"
				if name != "issue failure" { // An unavailable audit is reported explicitly as unverified.
					calls, wantErr = calls[:index+1], "exited 2"
				}
			case "checks malformed":
				calls[4].Stdout = "not JSON"
				calls, wantErr = calls[:5], "unparsable JSON"
			case "checks empty exit1", "checks empty exit8":
				calls[4].Stdout, calls[4].Exit, calls[4].Stderr = "", 1, "read probe failed"
				if name == "checks empty exit8" {
					calls[4].Exit = 8
				}
				calls, wantErr = calls[:5], "without required-check data: read probe failed"
			case "checks null":
				calls[4].Stdout = "null"
				calls, wantErr = calls[:5], "without a valid required-check array"
			case "head moved":
				calls[5] = readinessPRCall(t, issue, readinessOldHead, "OPEN", body)
				wantErr = "changed during readiness"
			case "invalid live head":
				calls[1] = readinessPRCall(t, issue, "", "OPEN", body)
				calls[5] = calls[1]
			case "wrong PR branch":
				calls[1].Stdout = strings.ReplaceAll(calls[1].Stdout, issue.Branch, "different-branch")
				calls[5] = calls[1]
			}
			doc, err := inspectReadiness(t, root, 1, calls...)
			if wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), wantErr) || doc != nil {
					t.Fatalf("report=%+v err=%v, want %s", doc, err, wantErr)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			wantState := name
			switch name {
			case "none required":
				wantState = "no-checks"
			case "issue failure", "invalid live head", "wrong PR branch":
				wantState = "passing"
			}
			wantMet := name == "passing" || name == "no-checks" || name == "none required"
			if doc.CI == nil || doc.CI.State != wantState || doc.MergeReport.PrerequisitesMet != wantMet || doc.Merge.Authorized {
				t.Fatalf("report=%+v CI=%+v merge-report=%+v", doc, doc.CI, doc.MergeReport)
			}
			if name == "passing" || name == "pending" || name == "failing" {
				if len(doc.CI.Required) != 1 || doc.CI.Required[0].Bucket != bucket || doc.CI.Required[0].Link != "https://checks/test" || doc.CI.Total != 1 {
					t.Fatalf("check evidence lost: %+v", doc.CI)
				}
			}
			if name == "no-checks" || name == "none required" {
				wantTotal := 0
				if name == "none required" {
					wantTotal = 1
				}
				if len(doc.CI.Required) != 0 || doc.CI.Total != wantTotal || doc.MergeReport.Preview.NoCIStatement == "" {
					t.Fatalf("absent versus optional CI lost: %+v", doc.CI)
				}
			}
		})
	}
}

func TestReadinessLocalFailures(t *testing.T) {
	for _, name := range []string{"zero", "negative", "unknown", "duplicate", "Assist", "corrupt state", "corrupt lock", "missing lock"} {
		t.Run(name, func(t *testing.T) {
			issue := fixtureIssue("a", 1, state.PhaseInReview)
			root := setupDeliveryRepoWithConfig(t, metricsEnabledConfigTOML, "r1", []state.Issue{issue})
			number := 1
			switch name {
			case "zero":
				number = 0
			case "negative":
				number = -1
			case "unknown":
				number = 99
			case "duplicate":
				st := loadRun(t, root)
				st.Run.Issues = append(st.Run.Issues, fixtureIssue("b", 1, state.PhaseInReview))
				if err := state.Save(root, st); err != nil {
					t.Fatal(err)
				}
			case "Assist":
				root = setupRepo(t, metricsEnabledConfigTOML)
			case "corrupt state", "corrupt lock":
				path := state.Path
				if name == "corrupt lock" {
					path = lockfile.Path
				}
				if err := os.WriteFile(filepath.Join(root, path), []byte("{broken"), 0o644); err != nil {
					t.Fatal(err)
				}
			case "missing lock":
				if err := os.Remove(filepath.Join(root, lockfile.Path)); err != nil {
					t.Fatal(err)
				}
			}
			if doc, err := inspectReadiness(t, root, number); err == nil || doc != nil {
				t.Fatalf("report=%+v err=%v", doc, err)
			}
		})
	}
	for _, name := range []string{"config drift", "effective drift", "legacy contract", "stopped", "blocked", "missing work"} {
		t.Run(name, func(t *testing.T) {
			issue := fixtureIssue("a", 1, state.PhaseInReview)
			issue.LastReviewVerdict, issue.ReviewCycles = VerdictApprove, 1
			if name == "blocked" {
				issue.Phase, issue.BlockedReason = state.PhaseBlocked, "human decision required"
			}
			if name == "missing work" {
				issue.Objective = ""
			}
			root := setupDeliveryRepoWithConfig(t, metricsEnabledConfigTOML, "r1", []state.Issue{issue})
			st := loadRun(t, root)
			switch name {
			case "config drift":
				st.Run.Plan.ConfigRevision = "other"
			case "effective drift":
				if err := os.WriteFile(filepath.Join(root, ".orchestrator", "config.local.toml"), []byte("[concurrency]\nmax_subagents = 2\n"), 0o644); err != nil {
					t.Fatal(err)
				}
			case "legacy contract":
				st.Run.Plan.ContractVersion = 0
			case "stopped":
				st.Run.StoppedReason = "remediation required"
			}
			if err := state.Save(root, st); err != nil {
				t.Fatal(err)
			}
			body := readinessBody(t, readinessManifest())
			doc, err := inspectReadiness(t, root, 1, readinessCalls(t, issue, readinessHead, "OPEN", body, body, "no-checks")...)
			if err != nil || doc.MergeReport.PrerequisitesMet || len(doc.Requirements) == 0 || doc.Merge.Authorized {
				t.Fatalf("report=%+v err=%v", doc, err)
			}
		})
	}
}
