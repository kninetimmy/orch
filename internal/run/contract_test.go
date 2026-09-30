package run

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kninetimmy/orch/internal/config"
	"github.com/kninetimmy/orch/internal/execx/execxtest"
	"github.com/kninetimmy/orch/internal/state"
)

func TestApprovalRejectsEffectiveLocalDrift(t *testing.T) {
	root := setupRepo(t, testConfigTOML)
	env := Env{RepoRoot: root}
	plan := twoIssuePlanJSON()
	gate, err := Plan(context.Background(), env, []byte(plan))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, ".orchestrator", "config.local.toml"), []byte("[hosts.claude.roles.implementer]\neffort = \"high\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	changed, err := Plan(context.Background(), env, []byte(plan))
	if err != nil {
		t.Fatal(err)
	}
	if gate.PlanDigest == changed.PlanDigest {
		t.Error("effective profile changed but approval digest did not")
	}
	_, err = Activate(context.Background(), env, buildActivationJSON(t, plan, gate.PlanDigest, ApprovalStatement))
	if !errors.Is(err, ErrBadApproval) {
		t.Errorf("activation = %v, want approval rejection before preflights", err)
	}
	assertNoDeliveryState(t, root)
}

func approvedPlanRef(t *testing.T, root, revision string) state.PlanRef {
	t.Helper()
	cfg, err := config.Load(root)
	if err != nil {
		t.Fatal(err)
	}
	execution, err := executionConfig(cfg, "claude")
	if err != nil {
		t.Fatal(err)
	}
	digest, err := contentDigest(execution)
	if err != nil {
		t.Fatal(err)
	}
	return state.PlanRef{Title: "t", Digest: "sha256:x", ConfigRevision: revision, ContractVersion: ContractVersion, ExecutionDigest: digest}
}

func TestApprovalContractBindsEveryInput(t *testing.T) {
	for _, tc := range []struct {
		name    string
		plan    func(*PlanDoc)
		config  string
		rewrite func(string) string
	}{
		{name: "title", plan: func(p *PlanDoc) { p.Title += " changed" }},
		{name: "summary", plan: func(p *PlanDoc) { p.Summary += " changed" }},
		{name: "host", plan: func(p *PlanDoc) { p.Host = "codex" }},
		{name: "objective", plan: func(p *PlanDoc) { p.Issues[0].Objective += " changed" }},
		{name: "criteria", plan: func(p *PlanDoc) { p.Issues[0].AcceptanceCriteria[0] += " changed" }},
		{name: "checks", plan: func(p *PlanDoc) { p.Issues[0].RequiredTests = append(p.Issues[0].RequiredTests, "go vet ./...") }},
		{name: "ci declaration", plan: func(p *PlanDoc) { p.Issues[0].TestsCIDoesNotRun = p.Issues[0].RequiredTests[:1] }},
		{name: "routing facts", plan: func(p *PlanDoc) { p.Issues[0].Facts.UnusuallyDifficult = true }},
		{name: "engine criterion trigger", plan: func(p *PlanDoc) { p.Issues[0].Facts.RiskDomains = []string{"authorization"} }},
		{name: "concurrency", config: "[concurrency]\nmax_subagents = 2\n"},
		{name: "merge", config: "[merge]\nstrategy = \"merge-commit\"\n"},
		{name: "metrics", config: "[metrics]\nenabled = true\n"},
		{name: "memhub", rewrite: func(s string) string { return strings.Replace(s, `mode = "off"`, `mode = "best-effort"`, 1) }},
		{name: "revision", rewrite: func(s string) string {
			return strings.Replace(s, `config_revision = "r1"`, `config_revision = "r2"`, 1)
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := setupRepo(t, testConfigTOMLBothHosts)
			script := &execxtest.Script{T: t}
			env := Env{RepoRoot: root, Runner: script}
			gate, err := Plan(context.Background(), env, []byte(twoIssuePlanJSON()))
			if err != nil {
				t.Fatal(err)
			}
			p, err := DecodePlan([]byte(twoIssuePlanJSON()))
			if err != nil {
				t.Fatal(err)
			}
			if tc.plan != nil {
				tc.plan(p)
			}
			if tc.config != "" || tc.rewrite != nil {
				content := testConfigTOMLBothHosts + tc.config
				if tc.rewrite != nil {
					content = tc.rewrite(content)
				}
				if err := os.WriteFile(filepath.Join(root, ".orchestrator", "config.toml"), []byte(content), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			plan, err := json.Marshal(p)
			if err != nil {
				t.Fatal(err)
			}
			_, err = Activate(context.Background(), env, buildActivationJSON(t, string(plan), gate.PlanDigest, ApprovalStatement))
			if !errors.Is(err, ErrBadApproval) {
				t.Fatalf("activation = %v, want ErrBadApproval", err)
			}
			script.AssertExhausted()
			assertNoDeliveryState(t, root)
			for _, path := range []string{state.Path, ".orchestrator/delivery.lock", WorktreeContainer} {
				if _, err := os.Stat(filepath.Join(root, path)); !os.IsNotExist(err) {
					t.Errorf("refusal wrote %s: %v", path, err)
				}
			}
		})
	}
}

func TestContractBindsEveryProfileAndEffectiveCriterion(t *testing.T) {
	root := setupRepo(t, testConfigTOML)
	cfg, err := config.Load(root)
	if err != nil {
		t.Fatal(err)
	}
	p, err := DecodePlan([]byte(twoIssuePlanJSON()))
	if err != nil {
		t.Fatal(err)
	}
	contract, err := effectiveContract(p, cfg)
	if err != nil {
		t.Fatal(err)
	}
	before, err := contentDigest(contract)
	if err != nil {
		t.Fatal(err)
	}
	for _, role := range []string{"architect", "scout", "implementer", "specialist", "reviewer", "review_downgrade"} {
		t.Run(role, func(t *testing.T) {
			for _, field := range []string{"model", "effort"} {
				value := "claude-opus-4-9"
				if field == "effort" {
					value = "max"
				}
				local := "[hosts.claude.roles." + role + "]\n" + field + " = \"" + value + "\"\n"
				if err := os.WriteFile(filepath.Join(root, ".orchestrator", "config.local.toml"), []byte(local), 0o644); err != nil {
					t.Fatal(err)
				}
				changed, err := config.Load(root)
				if err != nil {
					t.Fatal(err)
				}
				c, err := effectiveContract(p, changed)
				if err != nil {
					t.Fatal(err)
				}
				after, err := contentDigest(c)
				if err != nil {
					t.Fatal(err)
				}
				if before == after {
					t.Errorf("%s.%s not bound", role, field)
				}
				executionDigest, err := contract.Execution.Digest()
				if err != nil {
					t.Fatal(err)
				}
				active := &state.Run{Host: "claude", Plan: state.PlanRef{ContractVersion: ContractVersion, ExecutionDigest: executionDigest}}
				if err := checkExecutionConfig(changed, &state.State{SchemaVersion: state.SchemaVersion, Run: active}); !errors.Is(err, ErrConfigDrift) {
					t.Fatalf("post-activation %s.%s drift: %v", role, field, err)
				}
			}
		})
	}
	contract.Issues[0].AcceptanceCriteria = append(contract.Issues[0].AcceptanceCriteria, "new engine contribution")
	after, err := contentDigest(contract)
	if err != nil {
		t.Fatal(err)
	}
	if before == after {
		t.Fatal("effective engine criteria not bound")
	}
}

func TestContractLegacyApprovalAndFormatting(t *testing.T) {
	root := setupRepo(t, testConfigTOML)
	env := Env{RepoRoot: root, Runner: &execxtest.Script{T: t}}
	plan := twoIssuePlanJSON()
	gate, err := Plan(context.Background(), env, []byte(plan))
	if err != nil {
		t.Fatal(err)
	}
	var object map[string]any
	if err := json.Unmarshal([]byte(plan), &object); err != nil {
		t.Fatal(err)
	}
	reformatted, err := json.MarshalIndent(object, "", "    ")
	if err != nil {
		t.Fatal(err)
	}
	again, err := Plan(context.Background(), env, reformatted)
	if err != nil {
		t.Fatal(err)
	}
	if gate.PlanDigest != again.PlanDigest {
		t.Fatal("whitespace/key order changed approval")
	}
	p, err := DecodePlan([]byte(plan))
	if err != nil {
		t.Fatal(err)
	}
	legacy, err := p.Digest()
	if err != nil {
		t.Fatal(err)
	}
	_, err = Activate(context.Background(), env, buildActivationJSON(t, plan, legacy, ApprovalStatement))
	if !errors.Is(err, ErrBadApproval) {
		t.Fatalf("legacy digest = %v", err)
	}
	for _, version := range []int{1, 999} {
		req := ActivationRequest{SchemaVersion: version, Plan: *p, Approval: Approval{PlanDigest: gate.PlanDigest, Statement: ApprovalStatement}}
		data, err := json.Marshal(req)
		if err != nil {
			t.Fatal(err)
		}
		_, err = Activate(context.Background(), env, data)
		if !errors.Is(err, ErrBadApproval) || !strings.Contains(err.Error(), "fresh approval") {
			t.Fatalf("version %d: %v", version, err)
		}
	}
	assertNoDeliveryState(t, root)
}

func TestExecutionDriftAndLegacyRunAreReadOnly(t *testing.T) {
	for _, legacy := range []bool{false, true} {
		root := setupDeliveryRepo(t, "r1", []state.Issue{fixtureIssue("a", 1, state.PhaseWorktreeReady)})
		if legacy {
			st, err := state.Load(root)
			if err != nil {
				t.Fatal(err)
			}
			st.SchemaVersion = 4
			st.Run.Plan.ContractVersion = 0
			st.Run.Plan.ExecutionDigest = ""
			if err := state.Save(root, st); err != nil {
				t.Fatal(err)
			}
		} else {
			if err := os.WriteFile(filepath.Join(root, ".orchestrator", "config.local.toml"), []byte("[concurrency]\nmax_subagents = 2\n"), 0o644); err != nil {
				t.Fatal(err)
			}
		}
		before := stateBytes(t, root)
		script := &execxtest.Script{T: t}
		env := Env{RepoRoot: root, Runner: script}
		if _, err := Status(context.Background(), env); err != nil {
			t.Fatalf("inspection: %v", err)
		}
		_, err := loadVerb(env, 1, []state.Phase{state.PhaseWorktreeReady}, false)
		want := ErrConfigDrift
		if legacy {
			want = ErrBadApproval
		}
		if !errors.Is(err, want) {
			t.Fatalf("loadVerb legacy=%v: %v", legacy, err)
		}
		_, err = resumeLoad(env)
		if !errors.Is(err, want) {
			t.Fatalf("resumeLoad legacy=%v: %v", legacy, err)
		}
		if string(before) != string(stateBytes(t, root)) {
			t.Fatal("refusal mutated state")
		}
		script.AssertExhausted()
	}
}
