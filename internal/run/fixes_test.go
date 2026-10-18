package run

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/kninetimmy/orch/internal/execx"
	"github.com/kninetimmy/orch/internal/execx/execxtest"
)

// Gate plan_digest values as builds before the fixes field computed them,
// taken from a scratch run of Plan on unmodified main (setupRepo with
// testConfigTOML), pinned as literals: a plan listing no fixes must stay
// digest-identical, or every previously approved plan fails approval.
const (
	preFixesValidGateDigest    = "sha256:4ed88f5b107353974e9e18281322429df075f4ee69938f36c64b40b0821a4263"
	preFixesTwoIssueGateDigest = "sha256:d56dadabb150868bf8e93203029a220fc457bf7f0cf33c3fca5d957e46112e84"
)

func TestGateDigestUnchangedForAPlanListingNoFixes(t *testing.T) {
	root := setupRepo(t, testConfigTOML)
	for plan, want := range map[string]string{validPlanJSON(): preFixesValidGateDigest, twoIssuePlanJSON(): preFixesTwoIssueGateDigest} {
		doc, err := Plan(context.Background(), Env{RepoRoot: root}, []byte(plan))
		if err != nil {
			t.Fatal(err)
		}
		if doc.PlanDigest != want {
			t.Errorf("plan_digest = %q, want the pre-fixes digest %q", doc.PlanDigest, want)
		}
		gateJSON, err := json.Marshal(doc)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(gateJSON), "fixes") {
			t.Errorf("gate document for a plan listing no fixes mentions fixes:\n%s", gateJSON)
		}
	}
}

// fixesPlanJSON is validPlanJSON with its one issue fixing the given PRs.
func fixesPlanJSON(t *testing.T, fixes ...int) string {
	t.Helper()
	p := mustDecodePlan(t, validPlanJSON())
	p.Issues[0].Fixes = fixes
	data, err := json.Marshal(p)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func TestPlanGateShowsFixes(t *testing.T) {
	root := setupRepo(t, testConfigTOML)
	doc, err := Plan(context.Background(), Env{RepoRoot: root}, []byte(fixesPlanJSON(t, 12, 7)))
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}
	if got := doc.Issues[0].Fixes; len(got) != 2 || got[0] != 12 || got[1] != 7 {
		t.Errorf("gate fixes = %v, want [12 7]", got)
	}
	if doc.PlanDigest == preFixesValidGateDigest {
		t.Error("listing fixes left the plan_digest unchanged")
	}
}

// activateWithFixes scripts an activation of fixesPlanJSON(12) up to and
// including the fixes check, then the given tail.
func activateWithFixes(t *testing.T, root string, prCall execxtest.Call, tail ...execxtest.Call) (*execxtest.Script, error) {
	t.Helper()
	calls := []execxtest.Call{ghOpenCall(), ghRepoViewCall("main"), prCall}
	calls = append(calls, tail...)
	script := &execxtest.Script{T: t, Calls: calls}
	env := Env{RepoRoot: root, Runner: muxRunner{git: execx.Local{}, gh: script}, Now: fixedNow}
	_, err := Activate(context.Background(), env, activationJSON(t, root, fixesPlanJSON(t, 12)))
	return script, err
}

func TestActivateRecordsAndShowsMergedFixes(t *testing.T) {
	root := newActivateRepoWithConfig(t, metricsEnabledConfigTOML)
	tail := append([]execxtest.Call{ghLabelListEmptyCall()}, ghLabelCreateCalls()...)
	tail = append(tail, ghIssueCreateCall("Fix the status lock race", []string{"ready", "bug", "implementer", "standard"}, 1))
	script, err := activateWithFixes(t, root, ghPRViewCall(12, "MERGED", "h"), tail...)
	if err != nil {
		t.Fatalf("Activate: %v", err)
	}
	script.AssertExhausted()

	body := script.StdinAt(len(script.Calls) - 1)
	if !strings.Contains(body, "**Fixes**\n\n- #12\n") {
		t.Errorf("issue body does not list the fixed PR:\n%s", body)
	}
	docs := loadMetricsDocs(t, root)
	if got := docs[0].Events[0].Fixes; len(got) != 1 || got[0] != 12 {
		t.Errorf("activate event fixes = %v, want [12]", got)
	}
}

func TestActivateIssueBodyOmitsFixesWhenNoneListed(t *testing.T) {
	if strings.Contains(issueBody(mustDecodePlan(t, validPlanJSON()).Issues[0], nil, "sha256:x"), "Fixes") {
		t.Error("issue body for an issue listing no fixes has a fixes entry")
	}
}

func TestActivateRefusesUnmergedOrMissingFixesBeforeCreatingAnything(t *testing.T) {
	for name, call := range map[string]execxtest.Call{
		"open":    ghPRViewCall(12, "OPEN", "h"),
		"closed":  ghPRViewCall(12, "CLOSED", "h"),
		"missing": {Name: "gh", Args: ghPRViewCall(12, "", "").Args, Exit: 1, Stderr: "Could not resolve to a PullRequest"},
	} {
		t.Run(name, func(t *testing.T) {
			root := newActivateRepo(t)
			script, err := activateWithFixes(t, root, call)
			if !errors.Is(err, ErrFixesPRNotMerged) {
				t.Fatalf("err = %v, want ErrFixesPRNotMerged", err)
			}
			if !strings.Contains(err.Error(), "#12") {
				t.Errorf("err = %v, want it to name PR #12", err)
			}
			script.AssertExhausted()
			assertNoDeliveryState(t, root)
		})
	}
}
