package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/kninetimmy/orch/internal/lockfile"
	"github.com/kninetimmy/orch/internal/manifest"
	"github.com/kninetimmy/orch/internal/paths"
	"github.com/kninetimmy/orch/internal/run"
	"github.com/kninetimmy/orch/internal/state"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func mcpIssueContextExercise(t *testing.T, binary string) {
	t.Helper()
	root, cwd := t.TempDir(), t.TempDir()
	writeConfig(t, root, validTOML+"\n[metrics]\nenabled = true\n")
	writeConfig(t, cwd, "invalid launching repository [")
	mcpWrite(t, filepath.Join(cwd, state.Path), []byte("{corrupt cwd"))
	cwdBefore := mcpTree(t, cwd)
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()
	session, closeSession := mcpConnect(t, ctx, binary, cwd, root, "--repo", root)
	mcpDiscover(t, ctx, session, root)
	mcpIssueContextError(t, ctx, root, session, 2) // Assist is not invented work.
	st, err := state.EnterDelivery(root, "claude", testApprovedPlanRef(t, root), []state.Issue{{PlanID: "A", Phase: state.PhasePlanned}, {PlanID: "b", Phase: state.PhasePlanned}})
	if err != nil {
		t.Fatal(err)
	}
	for i := range st.Run.Issues {
		issue := &st.Run.Issues[i]
		issue.Number, issue.Phase = i+1, state.PhaseWorktreeReady
		issue.Branch = fmt.Sprintf("orch/issue-%d", issue.Number)
		issue.Worktree = fmt.Sprintf(".orchestrator/worktrees/issue-%d", issue.Number)
		issue.Objective = "Keep approved context exact: café / 日本語."
		issue.AcceptanceCriteria = []string{"No inspection changes repository files", "No dispatch authorization"}
		issue.RequiredTests = []string{"go test ./...", "approved manual check"}
		issue.TestsCIDoesNotRun = []string{"approved manual check"}
		issue.Decision = &state.Decision{Role: manifest.RoleImplementer,
			Executor: manifest.Selection{Model: "claude-sonnet-5", Effort: "xhigh"},
			Reviewer: manifest.Selection{Model: "claude-opus-4-8", Effort: "high"}, Rationale: "fixture"}
	}
	st.Run.Issues = append(st.Run.Issues, state.Issue{PlanID: "uncreated", Phase: state.PhasePlanned})
	st.Run.Issues[1].DependsOn, st.Run.Issues[1].Wave = []string{"a"}, 2
	save := func() {
		t.Helper()
		if err := state.Save(root, st); err != nil {
			t.Fatal(err)
		}
	}
	save()
	for _, args := range []any{
		nil, json.RawMessage("null"), []any{}, "not an object", 1, true, map[string]any{},
		map[string]any{"issue_number": 0}, map[string]any{"issue_number": -1}, map[string]any{"issue_number": 1.5},
		map[string]any{"issue_number": "2"}, map[string]any{"issue_number": true}, map[string]any{"issue_number": nil},
		map[string]any{"issue_number": 2, "repo": cwd}, map[string]any{"issue_number": 2, "repo_root": cwd},
		map[string]any{"issue_number": 2, "unexpected": true}, map[string]any{"issue_number": 1e100},
	} {
		result := mcpCall(t, ctx, session, root, "orch_issue_context", args)
		if !result.IsError || result.StructuredContent != nil {
			t.Fatalf("issue context accepted invalid arguments %#v: %+v", args, result)
		}
	}
	doc := mcpIssueContextParity(t, ctx, binary, root, session, 2)
	if doc.Guidance.Actions[0].Action != "wait" || len(doc.Dependencies) != 1 || doc.Dependencies[0].Number != 1 {
		t.Fatalf("dependency wait context = %+v", doc)
	}
	st.Run.Issues[0].Phase = state.PhaseCleaned
	save()
	if doc := mcpIssueContextParity(t, ctx, binary, root, session, 2); doc.Guidance.Actions[0].Action != "dispatch" {
		t.Fatalf("worktree-ready context = %+v", doc)
	}
	// Change current routing and phase while the same connection stays open.
	issue := &st.Run.Issues[1]
	issue.Attempts = []state.Attempt{{Role: issue.Decision.Role, Selection: issue.Decision.Executor, Failed: true, Reason: "needs specialist"}}
	issue.Decision.Role = manifest.RoleSpecialist
	issue.Decision.Executor = manifest.Selection{Model: "claude-opus-4-8", Effort: "high"}
	issue.Decision.Rationale, issue.Phase = "escalated", state.PhaseDispatched
	save()
	doc = mcpIssueContextParity(t, ctx, binary, root, session, 2)
	if !reflect.DeepEqual(doc.Issue.Decision, issue.Decision) || !reflect.DeepEqual(doc.Issue.Attempts, issue.Attempts) || doc.Guidance.Actions[0].Action != "implement" {
		t.Fatalf("stale routing/phase context = %+v", doc)
	}
	issue.Phase, issue.BlockedReason = state.PhaseBlocked, "human scope decision"
	issue.Blocks = []state.Block{{ID: 1, Cause: state.BlockHuman, Reason: issue.BlockedReason}}
	save()
	if doc := mcpIssueContextParity(t, ctx, binary, root, session, 2); doc.Guidance.Actions[0].Action != "resolve-block" || doc.Issue.Blocks[0].Resolution != nil {
		t.Fatalf("pending decision context = %+v", doc)
	}
	st.Run.StoppedReason = "secret remediation"
	save()
	if doc := mcpIssueContextParity(t, ctx, binary, root, session, 2); doc.Guidance.Actions[0].Action != "recover-stopped-run" {
		t.Fatalf("lost run-level stop = %+v", doc)
	}
	st.Run.StoppedReason, issue.Phase, issue.BlockedReason, issue.Blocks = "", state.PhaseWorktreeReady, "", nil
	issue.Objective, issue.Decision = "", nil
	save()
	if doc := mcpIssueContextParity(t, ctx, binary, root, session, 2); doc.Issue.Objective != "" || doc.Issue.Decision != nil || doc.Guidance.Actions[0].Action != "replan" {
		t.Fatalf("fabricated missing work/routing = %+v", doc)
	}
	local := filepath.Join(root, ".orchestrator", "config.local.toml")
	mcpWrite(t, local, []byte("[concurrency]\nmax_subagents = 2\n"))
	if doc := mcpIssueContextParity(t, ctx, binary, root, session, 2); doc.Guidance.Actions[0].Action != "restore-or-replan" || len(doc.Guidance.Actions[0].Blockers) == 0 {
		t.Fatalf("lost run-level drift = %+v", doc)
	}
	if err := os.Remove(local); err != nil {
		t.Fatal(err)
	}
	mcpIssueContextError(t, ctx, root, session, 99)
	st.Run.Issues[0].Number = 2
	save()
	mcpIssueContextError(t, ctx, root, session, 2)
	st.Run.Issues[0].Number = 1
	st.Run.Issues[0].PlanID = "B"
	save()
	mcpIssueContextError(t, ctx, root, session, 2)
	st.Run.Issues[0].PlanID = "A"
	save()
	if err := os.Remove(filepath.Join(root, lockfile.Path)); err != nil {
		t.Fatal(err)
	}
	if doc := mcpIssueContextParity(t, ctx, binary, root, session, 2); doc.Consistent || doc.Warning == "" || doc.Guidance.Actions[0].Action != "recover" {
		t.Fatalf("lost inconsistent-state warning/blocker = %+v", doc)
	}
	mcpWrite(t, filepath.Join(root, lockfile.Path), []byte("{corrupt lock"))
	mcpIssueContextError(t, ctx, root, session, 2)
	if err := os.Remove(filepath.Join(root, lockfile.Path)); err != nil {
		t.Fatal(err)
	}
	mcpWrite(t, filepath.Join(root, state.Path), []byte("{corrupt state"))
	mcpIssueContextError(t, ctx, root, session, 2)
	// Recover inspection on this connection; no repaired lock is invented.
	save()
	mcpIssueContextParity(t, ctx, binary, root, session, 2)
	mcpDiscover(t, ctx, session, root)
	if mcpTree(t, cwd) != cwdBefore {
		t.Fatal("issue context changed launching repository")
	}
	closeSession()
}

func mcpIssueContextParity(t *testing.T, ctx context.Context, binary, root string, session *mcp.ClientSession, number int) *run.IssueContextDoc {
	t.Helper()
	before := mcpTree(t, root)
	status := mcpParity(t, ctx, binary, root, session)
	next := mcpNextParity(t, ctx, binary, root, session)
	doc, err := run.IssueContext(ctx, run.Env{RepoRoot: root}, number)
	if err != nil {
		t.Fatal(err)
	}
	var recorded *state.Issue
	for i := range status.Run.Issues {
		if status.Run.Issues[i].Number == number {
			recorded = &status.Run.Issues[i]
		}
	}
	actions := []run.NextAction{}
	for _, action := range next.Actions {
		if action.IssueNumber == 0 && action.PlanID == "" || action.IssueNumber == number {
			actions = append(actions, action)
		}
	}
	next.Actions = actions
	if recorded == nil || !reflect.DeepEqual(doc.Issue, *recorded) || !reflect.DeepEqual(doc.Guidance, next) || len(doc.Guidance.Actions) != 1 || doc.Run.ID != status.Run.ID || doc.Consistent != status.Consistent || doc.Warning != status.Warning {
		t.Fatalf("context diverged from CLI status/next: context=%+v status=%+v next=%+v", doc, status, next)
	}
	encoded, err := json.Marshal(doc)
	if err != nil {
		t.Fatal(err)
	}
	result := mcpCall(t, ctx, session, root, "orch_issue_context", map[string]any{"issue_number": number})
	if result.IsError || len(result.Content) != 1 {
		t.Fatalf("context result = %+v", result)
	}
	text, ok := result.Content[0].(*mcp.TextContent)
	want := mcpDecoded(t, encoded)
	if !ok || !reflect.DeepEqual(result.StructuredContent, want) || !reflect.DeepEqual(mcpDecoded(t, []byte(text.Text)), want) || mcpTree(t, root) != before {
		t.Fatalf("context shared/structured/text mismatch or write: want=%s result=%+v", encoded, result)
	}
	t.Logf("CLI status/next = context sources; shared context = structured = JSON text: schema=%d issue=%d phase=%s action=%s; unchanged files", doc.SchemaVersion, number, doc.Issue.Phase, doc.Guidance.Actions[0].Action)
	return doc
}

func mcpIssueContextError(t *testing.T, ctx context.Context, root string, session *mcp.ClientSession, number int) {
	t.Helper()
	root, err := paths.Canonical(root)
	if err != nil {
		t.Fatal(err)
	}
	_, wantErr := run.IssueContext(ctx, run.Env{RepoRoot: root}, number)
	if wantErr == nil {
		t.Fatal("invalid context fixture succeeded")
	}
	result := mcpCall(t, ctx, session, root, "orch_issue_context", map[string]any{"issue_number": number})
	if !result.IsError || result.StructuredContent != nil || len(result.Content) != 1 {
		t.Fatalf("context failure returned a report: %+v", result)
	}
	text, ok := result.Content[0].(*mcp.TextContent)
	if !ok || text.Text != wantErr.Error() {
		t.Fatalf("context error mismatch: shared=%v MCP=%+v", wantErr, result)
	}
	t.Logf("explicit context error, unchanged files: %s", wantErr)
}
