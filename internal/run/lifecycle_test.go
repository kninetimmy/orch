package run

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"testing"

	"github.com/kninetimmy/orch/internal/execx"
	"github.com/kninetimmy/orch/internal/execx/execxtest"
	"github.com/kninetimmy/orch/internal/ghops"
	"github.com/kninetimmy/orch/internal/lockfile"
	"github.com/kninetimmy/orch/internal/manifest"
	"github.com/kninetimmy/orch/internal/state"
)

// prFieldsRun mirrors ghops's unexported prFields, pinned here so PR
// reads in the scripted transcript match exactly.
const prFieldsRun = "number,state,title,url,headRefName,baseRefName,headRefOid,mergeStateStatus,mergedAt,body"

// baseManifestBody renders a minimal valid audit record on some prose, so
// a scripted issue view returns a body manifest.Parse accepts.
func baseManifestBody(t *testing.T) string {
	t.Helper()
	body, err := manifest.Upsert("**Objective**\n\ndo it\n", manifest.Manifest{
		SchemaVersion:    manifest.SchemaVersion,
		Role:             manifest.RoleImplementer,
		Executor:         manifest.Selection{Model: "claude-sonnet-5", Effort: "xhigh"},
		RoutingRationale: "impl",
		Reviewer:         manifest.Selection{Model: "claude-opus-4-8", Effort: "high"},
		ConfigRevision:   "r1",
	})
	if err != nil {
		t.Fatal(err)
	}
	return body
}

func jsonQuote(t *testing.T, s string) string {
	t.Helper()
	b, err := json.Marshal(s)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func ghAuth() execxtest.Call { return execxtest.Call{Name: "gh", Args: []string{"auth", "status"}} }

func ghIssueViewCall(t *testing.T, number int, stateStr, body string) execxtest.Call {
	return execxtest.Call{
		Name: "gh", Args: []string{"issue", "view", strconv.Itoa(number), "--json", "number,state,title,url,labels,body"},
		Stdout: fmt.Sprintf(`{"number":%d,"state":%q,"title":"t","url":"u","labels":[],"body":%s}`, number, stateStr, jsonQuote(t, body)),
	}
}

func ghPRViewCall(number int, prState, headOID string) execxtest.Call {
	return execxtest.Call{
		Name: "gh", Args: []string{"pr", "view", strconv.Itoa(number), "--json", prFieldsRun},
		Stdout: fmt.Sprintf(`{"number":%d,"state":%q,"title":"t","url":"https://github.com/o/r/pull/%d","headRefName":"b","baseRefName":"main","headRefOid":%q,"mergeStateStatus":"CLEAN","mergedAt":null,"body":"pr body"}`, number, prState, number, headOID),
	}
}

func ghPRListEmptyCall(branch string) execxtest.Call {
	return execxtest.Call{Name: "gh", Args: []string{"pr", "list", "--head", branch, "--state", "open", "--json", prFieldsRun}, Stdout: "[]"}
}

func ghRollupEmptyCall(number int) execxtest.Call {
	return execxtest.Call{Name: "gh", Args: []string{"pr", "view", strconv.Itoa(number), "--json", "statusCheckRollup"}, Stdout: `{"statusCheckRollup":[]}`}
}

func ghSetIssueBodyCall(number int) execxtest.Call {
	return execxtest.Call{Name: "gh", Args: []string{"issue", "edit", strconv.Itoa(number), "--body-file", "-"}}
}

func ghSetPRBodyCall(number int) execxtest.Call {
	return execxtest.Call{Name: "gh", Args: []string{"pr", "edit", strconv.Itoa(number), "--body-file", "-"}}
}

func ghCreatePRCall(branch, title string, number int) execxtest.Call {
	return execxtest.Call{
		Name:   "gh",
		Args:   []string{"pr", "create", "--head", branch, "--base", "main", "--title", title, "--body-file", "-"},
		Stdout: fmt.Sprintf("https://github.com/o/r/pull/%d\n", number),
	}
}

func ghMergePRCall(number int, headOID string) execxtest.Call {
	return execxtest.Call{Name: "gh", Args: []string{"pr", "merge", strconv.Itoa(number), "--squash", "--match-head-commit", headOID}}
}

func ghCloseIssueCall(number int) execxtest.Call {
	return execxtest.Call{Name: "gh", Args: []string{"issue", "close", strconv.Itoa(number)}}
}

func ghSetStatusCall(number int, to ghops.Status) execxtest.Call {
	args := []string{"issue", "edit", strconv.Itoa(number)}
	for _, s := range []ghops.Status{ghops.StatusReady, ghops.StatusInProgress, ghops.StatusBlocked, ghops.StatusNeedsHuman, ghops.StatusAwaitingReview, ghops.StatusDelivered} {
		if s != to {
			args = append(args, "--remove-label", string(s))
		}
	}
	args = append(args, "--add-label", string(to))
	return execxtest.Call{Name: "gh", Args: args}
}

func ghSetRoleCall(number int, to ghops.Role) execxtest.Call {
	args := []string{"issue", "edit", strconv.Itoa(number)}
	for _, r := range []ghops.Role{ghops.RoleImplementer, ghops.RoleSpecialist} {
		if r != to {
			args = append(args, "--remove-label", string(r))
		}
	}
	args = append(args, "--add-label", string(to))
	return execxtest.Call{Name: "gh", Args: args}
}

// newLifecycleRepo builds an activation sandbox with a bare origin remote
// and main pushed, so the dispatch/pr-open/cleanup git paths have a
// remote to work against.
func newLifecycleRepo(t *testing.T) string {
	t.Helper()
	root := newActivateRepo(t)
	origin := filepath.Join(t.TempDir(), "origin.git")
	rawGit(t, filepath.Dir(origin), "init", "--bare", origin)
	rawGit(t, root, "remote", "add", "origin", origin)
	rawGit(t, root, "push", "origin", "main")
	return root
}

// runVerb runs one lifecycle verb against a mux runner (real git,
// scripted gh) and asserts the scripted gh transcript was fully consumed.
func runVerb[T any](t *testing.T, root string, fn func(context.Context, Env, []byte) (T, error), reqJSON string, calls ...execxtest.Call) T {
	t.Helper()
	script := &execxtest.Script{T: t, Calls: calls}
	env := Env{RepoRoot: root, Runner: muxRunner{git: execx.Local{}, gh: script}, Now: fixedNow}
	out, err := fn(context.Background(), env, []byte(reqJSON))
	if err != nil {
		t.Fatalf("verb: %v", err)
	}
	script.AssertExhausted()
	return out
}

func loadRun(t *testing.T, root string) *state.State {
	t.Helper()
	st, err := state.Load(root)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	return st
}

func wantPhase(t *testing.T, root string, number int, want state.Phase) {
	t.Helper()
	st := loadRun(t, root)
	for i := range st.Run.Issues {
		if st.Run.Issues[i].Number == number {
			if st.Run.Issues[i].Phase != want {
				t.Fatalf("issue #%d phase = %s, want %s", number, st.Run.Issues[i].Phase, want)
			}
			return
		}
	}
	t.Fatalf("issue #%d not found", number)
}

// TestLifecycleWalk drives one issue from activation through cleanup and
// run completion in a real git sandbox with scripted gh, asserting the
// phase after every verb, the exact status transitions (via the scripted
// SetStatus calls), and assist with no lock at the end.
func TestLifecycleWalk(t *testing.T) {
	root := newLifecycleRepo(t)
	body := baseManifestBody(t)
	const branch = "orch/issue-1-fix-the-status-lock-race"
	const title = "Fix the status lock race"

	// Activate the single-issue plan.
	activateCalls := append(fullTaxonomyScript(), ghIssueCreateCall(title, []string{"ready", "bug", "implementer", "standard"}, 1))
	script := &execxtest.Script{T: t, Calls: activateCalls}
	env := Env{RepoRoot: root, Runner: muxRunner{git: execx.Local{}, gh: script}, Now: fixedNow}
	if _, err := Activate(context.Background(), env, activationJSON(t, validPlanJSON())); err != nil {
		t.Fatalf("Activate: %v", err)
	}
	script.AssertExhausted()
	wantPhase(t, root, 1, state.PhaseWorktreeReady)

	// dispatch.
	runVerb(t, root, Dispatch, `{"schema_version":1,"issue_number":1}`,
		ghAuth(), ghRepoViewCall("main"), ghSetStatusCall(1, ghops.StatusInProgress))
	wantPhase(t, root, 1, state.PhaseDispatched)

	// The executor commits work into the worktree.
	wtDir := filepath.Join(root, ".orchestrator", "worktrees", "issue-1")
	if err := os.WriteFile(filepath.Join(wtDir, "feature.go"), []byte("package feature\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	rawGit(t, wtDir, "add", "-A")
	rawGit(t, wtDir, "commit", "-m", "work")

	// pr-open.
	runVerb(t, root, PROpen, `{"schema_version":1,"issue_number":1,"verifications":[{"name":"go test","result":"pass"}]}`,
		ghAuth(), ghRepoViewCall("main"), ghPRListEmptyCall(branch),
		ghIssueViewCall(t, 1, "OPEN", body), ghSetIssueBodyCall(1),
		ghCreatePRCall(branch, title, 10), ghSetStatusCall(1, ghops.StatusAwaitingReview))
	wantPhase(t, root, 1, state.PhasePROpen)

	// review: request-changes.
	runVerb(t, root, Review, `{"schema_version":1,"issue_number":1,"reviewed_head_oid":"head-oid-1","verdict":"request-changes","summary":"fix things","reviewer":{"model":"claude-opus-4-8","effort":"high"}}`,
		ghAuth(), ghPRViewCall(10, "OPEN", "head-oid-1"),
		ghIssueViewCall(t, 1, "OPEN", body), ghSetIssueBodyCall(1), ghSetPRBodyCall(10),
		ghSetStatusCall(1, ghops.StatusInProgress))
	wantPhase(t, root, 1, state.PhaseInReview)

	// review: approve (PR moved to a new head).
	runVerb(t, root, Review, `{"schema_version":1,"issue_number":1,"reviewed_head_oid":"head-oid-2","verdict":"approve","summary":"looks good","reviewer":{"model":"claude-opus-4-8","effort":"high"}}`,
		ghAuth(), ghPRViewCall(10, "OPEN", "head-oid-2"),
		ghIssueViewCall(t, 1, "OPEN", body), ghSetIssueBodyCall(1), ghSetPRBodyCall(10))
	wantPhase(t, root, 1, state.PhaseInReview)

	// ci (no required checks).
	runVerb(t, root, CI, `{"schema_version":1,"issue_number":1}`,
		ghAuth(), ghRollupEmptyCall(10), ghIssueViewCall(t, 1, "OPEN", body),
		ghPRViewCall(10, "OPEN", "head-oid-2"), ghSetIssueBodyCall(1), ghSetPRBodyCall(10))
	wantPhase(t, root, 1, state.PhaseInReview)

	// merge-report.
	report := runVerb(t, root, MergeReport, `{"schema_version":1,"issue_number":1}`,
		ghAuth(), ghPRViewCall(10, "OPEN", "head-oid-2"), ghRollupEmptyCall(10),
		ghSetStatusCall(1, ghops.StatusNeedsHuman))
	wantPhase(t, root, 1, state.PhaseAwaitingMerge)
	if report.HeadOID != "head-oid-2" || report.NoCIStatement == "" {
		t.Errorf("report = %+v, want head-oid-2 and a no-CI statement", report)
	}

	// merge.
	runVerb(t, root, Merge, `{"schema_version":1,"issue_number":1,"approval":{"pr_number":10,"head_oid":"head-oid-2","approved_by":"alice","approved_at":"2026-07-11T12:00:00Z","statement":"approve-merge"}}`,
		ghAuth(), ghPRViewCall(10, "OPEN", "head-oid-2"), ghRollupEmptyCall(10),
		ghMergePRCall(10, "head-oid-2"), ghPRViewCall(10, "MERGED", "head-oid-2"),
		ghSetStatusCall(1, ghops.StatusDelivered),
		ghIssueViewCall(t, 1, "OPEN", body), ghCloseIssueCall(1), ghSetIssueBodyCall(1))
	wantPhase(t, root, 1, state.PhaseMerged)

	// cleanup (git only).
	runVerb(t, root, Cleanup, `{"schema_version":1,"issue_number":1,"statement":"cleanup-issue"}`)
	wantPhase(t, root, 1, state.PhaseCleaned)

	// complete.
	result := runVerb(t, root, Complete, `{"schema_version":1}`, ghAuth(), ghRepoViewCall("main"))
	if result.Merged != 1 || result.Abandoned != 0 || result.ReturnedTo != "assist" {
		t.Errorf("complete result = %+v, want merged 1", result)
	}

	// The run returned to assist with no lock held.
	st := loadRun(t, root)
	if st.Mode != state.ModeAssist || st.Run != nil {
		t.Errorf("state after complete = %+v, want assist", st)
	}
	if owner, err := lockfile.Inspect(root); owner != nil || err != nil {
		t.Errorf("lock present after complete: %+v, %v", owner, err)
	}
}
