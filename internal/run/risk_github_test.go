package run

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/kninetimmy/orch/internal/state"
)

func TestResidualRiskRefusesUnverifiedOwnerOrCollaborators(t *testing.T) {
	cases := []struct {
		name   string
		change func(*riskServer)
		want   string
	}{
		{"organization owner", func(g *riskServer) { g.ownerKind = "Organization" }, "verified user-owned"},
		{"unknown owner type", func(g *riskServer) { g.ownerKind = "" }, "verified user-owned"},
		{"missing owner ID", func(g *riskServer) { g.ownerID = "" }, "verified user-owned"},
		{"automation owns repo", func(g *riskServer) { g.ownerID = g.viewerID }, "non-admin automation"},
		{"GraphQL viewer is maintain", func(g *riskServer) { g.viewerPermission = "MAINTAIN" }, "non-admin automation"},
		{"missing admin row", func(g *riskServer) { g.collaborators = g.collaborators[:1] }, "both verified"},
		{"missing viewer row", func(g *riskServer) { g.collaborators = g.collaborators[1:] }, "both verified"},
		{"login without matching immutable ID", func(g *riskServer) { g.collaborators[1][0]["id"] = 99 }, "both verified"},
		{"ID and login disagree", func(g *riskServer) { g.collaborators[1][0]["login"] = "other" }, "standard admin role and flags"},
		{"non-user role row", func(g *riskServer) { g.collaborators[1][0]["type"] = "Bot" }, "standard admin role and flags"},
		{"missing permission object", func(g *riskServer) { delete(g.collaborators[0][0], "permissions") }, "standard write role and flags"},
		{"unknown admin flag", func(g *riskServer) { delete(g.collaborators[0][0]["permissions"].(map[string]any), "admin") }, "standard write role and flags"},
		{"viewer cannot push", func(g *riskServer) { g.collaborators[0][0]["permissions"].(map[string]any)["push"] = false }, "standard write role and flags"},
		{"viewer has admin flag", func(g *riskServer) { g.collaborators[0][0]["permissions"].(map[string]any)["admin"] = true }, "standard write role and flags"},
		{"viewer has maintain flag", func(g *riskServer) { g.collaborators[0][0]["permissions"].(map[string]any)["maintain"] = true }, "standard write role and flags"},
		{"admin flag disagrees with role", func(g *riskServer) { g.collaborators[1][0]["permissions"].(map[string]any)["admin"] = false }, "standard admin role and flags"},
		{"null page", func(g *riskServer) { g.collaborators = append(g.collaborators, nil) }, "incomplete page"},
		{"duplicate on a later page", func(g *riskServer) { g.collaborators = append(g.collaborators, g.collaborators[1]) }, "duplicate identity"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			env, g, req, accept := newRiskFixture(t, true)
			holdRisk(t, env, g, req)
			before := g.issueBody
			tc.change(g)
			if _, err := AcceptRisk(t.Context(), env, requestBytes(t, accept)); err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("accept-risk = %v, want %q", err, tc.want)
			}
			wantPhase(t, env.RepoRoot, 1, state.PhaseBlocked)
			if g.issueBody != before {
				t.Fatal("invalid owner or collaborator proof wrote an acceptance")
			}
		})
	}
}

func TestResidualRiskAcceptsDocumentedOptionalFieldsAndBindsTheirPresence(t *testing.T) {
	env, g, req, accept := newRiskFixture(t, true)
	for _, page := range g.collaborators {
		for _, row := range page {
			flags := row["permissions"].(map[string]any)
			delete(flags, "maintain")
			delete(flags, "triage")
		}
	}
	g.onCreate = nil
	holdRisk(t, env, g, req)
	g.onCreate = json.RawMessage(`false`)
	if _, err := AcceptRisk(t.Context(), env, requestBytes(t, accept)); !errors.Is(err, ErrReviewStale) {
		t.Fatalf("absent policy flag was conflated with explicit false: %v", err)
	}
	g.onCreate = nil
	got, err := AcceptRisk(t.Context(), env, requestBytes(t, accept))
	if err != nil || got.Phase != state.PhaseInReview {
		t.Fatalf("documented optional fields prevented valid acceptance: %+v, %v", got, err)
	}
	if strings.Contains(got.Receipt.Detail, "do_not_enforce_on_create") {
		t.Fatal("receipt promoted an omitted flag to a GitHub-observed value")
	}
}

func TestResidualRiskRejectsNonBooleanCreationPolicy(t *testing.T) {
	for _, raw := range []string{`null`, `"false"`, `0`, `{}`} {
		t.Run(raw, func(t *testing.T) {
			env, g, req, _ := newRiskFixture(t, true)
			g.onCreate = json.RawMessage(raw)
			if _, err := Review(t.Context(), env, requestBytes(t, req)); err == nil || !strings.Contains(err.Error(), "do_not_enforce_on_create must be Boolean") {
				t.Fatalf("review = %v, want malformed-policy refusal", err)
			}
			wantPhase(t, env.RepoRoot, 1, state.PhasePROpen)
		})
	}
}
