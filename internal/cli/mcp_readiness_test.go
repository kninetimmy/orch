package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/kninetimmy/orch/internal/manifest"
	"github.com/kninetimmy/orch/internal/paths"
	"github.com/kninetimmy/orch/internal/run"
	"github.com/kninetimmy/orch/internal/state"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func mcpReadinessExercise(t *testing.T, binary string) {
	t.Helper()
	root, cwd, probes := t.TempDir(), t.TempDir(), t.TempDir()
	writeConfig(t, root, validTOML+"\n[metrics]\nenabled = true\n")
	writeConfig(t, cwd, "invalid launching repository [")
	mcpWrite(t, filepath.Join(cwd, state.Path), []byte("{corrupt cwd"))
	canonical, err := paths.Canonical(root)
	if err != nil {
		t.Fatal(err)
	}
	gh := filepath.Join(probes, "gh")
	if runtime.GOOS == "windows" {
		gh += ".exe"
	}
	build := exec.CommandContext(t.Context(), "go", "build", "-o", gh, "./testdata/readiness-gh")
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build scripted gh: %v: %s", err, out)
	}
	fixturePath, logPath := filepath.Join(probes, "fixture.json"), filepath.Join(probes, "commands.log")
	ctx, cancel := context.WithTimeout(t.Context(), 60*time.Second)
	defer cancel()
	process := func(args ...string) *exec.Cmd {
		cmd := mcpProcess(ctx, binary, root, args...)
		cmd.Env = append(cmd.Env, "PATH="+probes, "ORCH_TEST_GH_FIXTURE="+fixturePath)
		return cmd
	}
	cmd := process("mcp", "--repo", root)
	cmd.Dir = cwd
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	client := mcp.NewClient(&mcp.Implementation{Name: "readiness-test", Version: "1"}, nil)
	before, cwdBefore := mcpTree(t, root), mcpTree(t, cwd)
	session, err := client.Connect(ctx, &mcp.CommandTransport{Command: cmd}, nil)
	if err != nil {
		t.Fatalf("connect: %v: %s", err, stderr.String())
	}
	closed := false
	closeSession := func() {
		t.Helper()
		if closed {
			return
		}
		closed = true
		before, cwdBefore := mcpTree(t, root), mcpTree(t, cwd)
		if err := session.Close(); err != nil {
			t.Error(err)
		}
		if cmd.ProcessState == nil || !cmd.ProcessState.Success() || stderr.Len() != 0 || mcpTree(t, root) != before || mcpTree(t, cwd) != cwdBefore {
			t.Errorf("shutdown: state=%v stderr=%s or repository changed", cmd.ProcessState, stderr.String())
		}
	}
	t.Cleanup(closeSession)
	mcpDiscover(t, ctx, session, root)
	if mcpTree(t, root) != before || mcpTree(t, cwd) != cwdBefore {
		t.Fatal("initialization/discovery changed repositories")
	}
	if _, err := os.Stat(logPath); !os.IsNotExist(err) {
		t.Fatalf("initialization/discovery launched external commands: %v", err)
	}
	st, err := state.EnterDelivery(root, "claude", testApprovedPlanRef(t, root), []state.Issue{{PlanID: "a", Phase: state.PhasePlanned}})
	if err != nil {
		t.Fatal(err)
	}
	const head = "0123456789abcdef0123456789abcdef01234567"
	const oldHead = "abcdef0123456789abcdef0123456789abcdef0123"
	selection := manifest.Selection{Model: "claude-sonnet-5", Effort: "xhigh"}
	issue := state.Issue{
		PlanID: "a", Title: "Inspect evidence", Number: 1, Phase: state.PhaseInReview,
		Branch: "orch/issue-1", Worktree: ".orchestrator/worktrees/issue-1", PRNumber: 10, PRURL: "https://github.com/o/r/pull/10",
		Objective: "Read only", AcceptanceCriteria: []string{"no writes"}, RequiredTests: []string{"go test ./..."},
		Decision:     &state.Decision{Role: manifest.RoleImplementer, Executor: selection, Reviewer: selection, Rationale: "fixture"},
		ReviewCycles: 1, LastReviewVerdict: run.VerdictApprove,
	}
	m := manifest.Manifest{
		SchemaVersion: manifest.SchemaVersion, Objective: issue.Objective, AcceptanceCriteria: issue.AcceptanceCriteria, RequiredTests: issue.RequiredTests,
		Role: issue.Decision.Role, Executor: selection, Reviewer: selection, RoutingRationale: "fixture", ConfigRevision: "r1", EffortDelivery: manifest.EffortDeliveryParameter,
		Verifications: []manifest.Verification{
			{Name: "acceptance-criterion-1", Result: run.JudgmentSatisfied, CommitOID: head, Detail: "checked"},
			{Name: "review-cycle-1", Result: run.VerdictApprove, CommitOID: head, Detail: "reviewed"},
		},
	}
	for _, name := range []string{"current", "stale", "missing evidence", "contradictory evidence", "recorded gate", "stale gate", "pending", "failing", "no-checks", "probe failure", "missing PR", "unknown issue"} {
		t.Run(name, func(t *testing.T) {
			one := issue
			liveHead, bucket, checkState := head, "pass", "SUCCESS"
			body, err := manifest.Upsert("", m)
			if err != nil {
				t.Fatal(err)
			}
			mirror, fail, failText := body, "", ""
			rollup, checks := json.RawMessage(`{"statusCheckRollup":[{}]}`), json.RawMessage(`[{"name":"test","state":"SUCCESS","bucket":"pass","link":"https://checks/test"}]`)
			switch name {
			case "stale":
				liveHead = oldHead
			case "missing evidence":
				body, mirror = "no audit record", "no audit record"
			case "contradictory evidence":
				changed := m
				changed.Verifications = append([]manifest.Verification(nil), m.Verifications...)
				changed.Verifications[1].Result = run.VerdictRequestChanges
				mirror, err = manifest.Upsert("", changed)
				if err != nil {
					t.Fatal(err)
				}
			case "recorded gate":
				one.Phase, one.ApprovedHeadOID = state.PhaseAwaitingMerge, head
			case "stale gate":
				one.Phase, one.ApprovedHeadOID = state.PhaseAwaitingMerge, oldHead
			case "pending":
				bucket, checkState = "pending", "IN_PROGRESS"
			case "failing":
				bucket, checkState = "fail", "FAILURE"
			case "no-checks":
				rollup = json.RawMessage(`{"statusCheckRollup":[]}`)
			case "probe failure":
				fail, failText = "pr checks", "required CI unavailable"
			case "missing PR":
				one.Phase, one.PRNumber, one.PRURL, one.LastReviewVerdict, one.ReviewCycles = state.PhaseDispatched, 0, "", "", 0
			}
			if name == "pending" || name == "failing" {
				checks, err = json.Marshal([]map[string]string{{"name": "test", "state": checkState, "bucket": bucket, "link": "https://checks/test"}})
				if err != nil {
					t.Fatal(err)
				}
			}
			st.Run.Issues = []state.Issue{one}
			if err := state.Save(root, st); err != nil {
				t.Fatal(err)
			}
			fixture := map[string]any{
				"Root": canonical, "Log": logPath, "Fail": fail, "FailText": failText,
				"PR":    map[string]any{"number": 10, "state": "OPEN", "url": issue.PRURL, "headRefName": issue.Branch, "headRefOid": liveHead, "body": mirror},
				"Issue": map[string]any{"number": 1, "state": "OPEN", "body": body}, "Rollup": rollup, "Checks": checks,
			}
			data, err := json.Marshal(fixture)
			if err != nil {
				t.Fatal(err)
			}
			mcpWrite(t, fixturePath, data)
			if name == "current" {
				// Validate while the issue is inspectable: a schema regression
				// must not hide behind a no-run/unknown-issue engine failure.
				for _, args := range []any{
					map[string]any{}, map[string]any{"issue_number": 0}, map[string]any{"issue_number": -1}, map[string]any{"issue_number": 1.5}, map[string]any{"issue_number": "1"}, map[string]any{"issue_number": nil},
					map[string]any{"issue_number": 1, "repo": cwd}, map[string]any{"issue_number": 1, "repo_root": cwd}, map[string]any{"issue_number": 1, "unexpected": true}, []any{}, 1,
				} {
					result := mcpCall(t, ctx, session, root, "orch_run_readiness", args)
					if !result.IsError || result.StructuredContent != nil {
						t.Fatalf("invalid readiness input accepted: %#v => %+v", args, result)
					}
				}
				if _, err := os.Stat(logPath); !os.IsNotExist(err) {
					t.Fatalf("invalid arguments launched external commands: %v", err)
				}
			}
			number, numberArg := 1, "1"
			if name == "unknown issue" {
				number, numberArg = 99, "99"
			}
			before, cwdBefore := mcpTree(t, root), mcpTree(t, cwd)
			cliOut, cliErr := process("run", "readiness", "--issue", numberArg, "--json").CombinedOutput()
			result := mcpCall(t, ctx, session, root, "orch_run_readiness", map[string]any{"issue_number": number})
			if mcpTree(t, root) != before || mcpTree(t, cwd) != cwdBefore {
				t.Fatal("inspection changed bound or launching repository")
			}
			if name == "probe failure" || name == "unknown issue" {
				want := "required CI unavailable"
				if name == "unknown issue" {
					want = "issue_number 99"
				}
				if cliErr == nil || !strings.Contains(string(cliOut), want) || !result.IsError || result.StructuredContent != nil || len(result.Content) != 1 {
					t.Fatalf("CLI=%s err=%v MCP=%+v", cliOut, cliErr, result)
				}
				text, ok := result.Content[0].(*mcp.TextContent)
				if !ok || !strings.Contains(string(cliOut), text.Text) {
					t.Fatalf("failure parity: CLI=%s MCP=%+v", cliOut, result)
				}
				if name == "probe failure" && !strings.Contains(text.Text, "exited 1") {
					t.Fatalf("missing exit-1 empty-stdout regression: %s", text.Text)
				}
				t.Logf("CLI = MCP explicit failure: %s; repositories unchanged", text.Text)
				return
			}
			if cliErr != nil || result.IsError || len(result.Content) != 1 {
				t.Fatalf("CLI=%s err=%v MCP=%+v", cliOut, cliErr, result)
			}
			text, ok := result.Content[0].(*mcp.TextContent)
			if !ok || !reflect.DeepEqual(mcpDecoded(t, cliOut), result.StructuredContent) || !reflect.DeepEqual(mcpDecoded(t, cliOut), mcpDecoded(t, []byte(text.Text))) {
				t.Fatalf("parity mismatch: CLI=%s MCP=%+v", cliOut, result)
			}
			var doc run.ReadinessDoc
			if err := json.Unmarshal(cliOut, &doc); err != nil {
				t.Fatal(err)
			}
			wantMet := name == "current" || name == "recorded gate" || name == "no-checks"
			if doc.SchemaVersion != 1 || doc.MergeReport.PrerequisitesMet != wantMet || doc.Merge.Authorized || len(doc.Merge.Requirements) == 0 || doc.Merge.ApprovalRequired == "" {
				t.Fatalf("readiness=%s", cliOut)
			}
			if (name == "stale" || name == "stale gate") && !doc.HeadDrift {
				t.Fatalf("head drift not flagged: %s", cliOut)
			}
			if name == "current" && (doc.MergeReport.Recorded || doc.Review.Evidence == nil || doc.Review.Evidence.CommitOID != head || doc.HeadOID != head) {
				t.Fatalf("preview synthesized approval or lost exact head: %s", cliOut)
			}
			if name == "pending" || name == "failing" || name == "no-checks" {
				if doc.CI == nil || doc.CI.State != name {
					t.Fatalf("CI state lost: %s", cliOut)
				}
			}
			t.Logf("CLI = structured = JSON text: readiness %s; prerequisites_met=%t authorized=false; repositories unchanged", name, doc.MergeReport.PrerequisitesMet)
		})
	}
	log, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatal(err)
	}
	for _, line := range strings.Split(strings.TrimSpace(string(log)), "\n") {
		parts := strings.SplitN(line, "\t", 2)
		if len(parts) != 2 || !strings.EqualFold(parts[0], canonical) {
			t.Fatalf("external probe escaped binding: %s", line)
		}
		switch parts[1] {
		case "auth status", "pr view 10 --json number,state,title,url,headRefName,baseRefName,headRefOid,mergeStateStatus,mergedAt,body", "issue view 1 --json number,state,title,url,labels,body", "pr view 10 --json statusCheckRollup", "pr checks 10 --required --json name,state,bucket,link":
		default:
			t.Fatalf("mutation or unapproved external command: %s", line)
		}
	}
	closeSession()
}
