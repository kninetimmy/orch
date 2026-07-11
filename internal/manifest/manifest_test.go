package manifest

import (
	"strings"
	"testing"
)

// minimalManifest is a valid record with no escalations or
// verifications — the smallest thing Render accepts.
func minimalManifest() Manifest {
	return Manifest{
		SchemaVersion:    SchemaVersion,
		Role:             RoleImplementer,
		Executor:         Selection{Model: "opus-4-8", Effort: "high"},
		RoutingRationale: "Selected implementer for a bounded single-file change.",
		Reviewer:         Selection{Model: "gpt-5.6-sol", Effort: "medium"},
		ConfigRevision:   "cfg-2026-07-10",
	}
}

// fullManifest exercises every optional field: two escalations (an
// escalation with a role and At, a substitution without) and three
// verifications, the last a CI-state entry with no command.
func fullManifest() Manifest {
	m := minimalManifest()
	m.Escalations = []Escalation{
		{
			Kind:   "escalation",
			Role:   RoleImplementer,
			From:   Selection{Model: "sonnet-4-8", Effort: "medium"},
			To:     Selection{Model: "opus-4-8", Effort: "high"},
			Reason: "Repeated test failures on the concurrency path.",
			At:     "2026-07-10T14:03:00Z",
		},
		{
			Kind:   "substitution",
			From:   Selection{Model: "opus-4-8", Effort: "high"},
			To:     Selection{Model: "opus-4-8", Effort: "medium"},
			Reason: "Downgraded effort after the fix landed.",
		},
	}
	m.Verifications = []Verification{
		{Name: "targeted-tests", Command: "go test ./internal/manifest/...", Result: "pass", At: "2026-07-10T14:20:00Z"},
		{Name: "vet", Command: "go vet ./...", Result: "pass", Detail: "no findings"},
		{Name: "ci", Result: "CLEAN", Detail: "required checks green", At: "2026-07-10T14:45:00Z"},
	}
	return m
}

// hostileManifest packs marker-forging and markdown-breaking content
// into every free-text field: a rationale containing "-->", a line
// equal to EndMarker, a CRLF pair, and a <script> tag; a command with
// backticks, pipes, and an ampersand; a model with a pipe; a config
// revision with all three entity characters; an escalation whose reason
// is a data-close and whose At smuggles a begin-marker line; and a
// verification whose name, result, and At each smuggle a marker or
// data-comment line. Render must escape all of it so the region still
// contains exactly one of each structural line and parses back.
func hostileManifest() Manifest {
	return Manifest{
		SchemaVersion: SchemaVersion,
		Role:          RoleReviewer,
		Executor:      Selection{Model: "weird|model", Effort: "high"},
		RoutingRationale: "Rationale with a marker attempt -->\r\n" +
			EndMarker + "\n" +
			"and a <script>alert(1)</script> tag.",
		Reviewer:       Selection{Model: "opus-4-8", Effort: "low"},
		ConfigRevision: "rev&<>",
		Escalations: []Escalation{
			{
				Kind:   "escalation",
				From:   Selection{Model: "sonnet-4-8", Effort: "low"},
				To:     Selection{Model: "opus-4-8", Effort: "high"},
				Reason: "reason ending in " + dataClose,
				At:     "2026-07-10T00:00:00Z\n" + BeginMarker,
			},
		},
		Verifications: []Verification{
			{
				Name:    "targeted-tests\n" + EndMarker,
				Command: "go test -run 'A|B' ./... `backtick` && echo done",
				Result:  "pass\n" + dataOpen,
				At:      "2026-07-10T14:20:00Z\n" + dataClose,
			},
		},
	}
}

func TestValidateRejects(t *testing.T) {
	tests := map[string]struct {
		mutate func(*Manifest)
		want   string
	}{
		"bad schema version":        {func(m *Manifest) { m.SchemaVersion = 2 }, "schema_version 2 is unsupported"},
		"absent schema version":     {func(m *Manifest) { m.SchemaVersion = 0 }, "schema_version 0 is unsupported"},
		"unknown role":              {func(m *Manifest) { m.Role = "wizard" }, `role "wizard" is not one of`},
		"empty executor model":      {func(m *Manifest) { m.Executor.Model = "" }, "executor.model is empty"},
		"empty executor effort":     {func(m *Manifest) { m.Executor.Effort = "" }, "executor.effort is empty"},
		"empty reviewer model":      {func(m *Manifest) { m.Reviewer.Model = "" }, "reviewer.model is empty"},
		"empty reviewer effort":     {func(m *Manifest) { m.Reviewer.Effort = "" }, "reviewer.effort is empty"},
		"empty rationale":           {func(m *Manifest) { m.RoutingRationale = "" }, "routing_rationale is empty"},
		"empty config revision":     {func(m *Manifest) { m.ConfigRevision = "" }, "config_revision is empty"},
		"bad escalation kind":       {func(m *Manifest) { m.Escalations[0].Kind = "demotion" }, `escalations[0]: kind "demotion" is not one of`},
		"escalation empty from":     {func(m *Manifest) { m.Escalations[0].From.Model = "" }, "escalations[0]: from.model is empty"},
		"escalation empty to":       {func(m *Manifest) { m.Escalations[1].To.Effort = "" }, "escalations[1]: to.effort is empty"},
		"escalation empty reason":   {func(m *Manifest) { m.Escalations[0].Reason = "" }, "escalations[0]: reason is empty"},
		"verification empty name":   {func(m *Manifest) { m.Verifications[2].Name = "" }, "verifications[2]: name is empty"},
		"verification empty result": {func(m *Manifest) { m.Verifications[0].Result = "" }, "verifications[0]: result is empty"},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			m := fullManifest()
			tt.mutate(&m)
			_, err := Render(m)
			if err == nil {
				t.Fatalf("Render succeeded, want validation error mentioning %q", tt.want)
			}
			if !strings.Contains(err.Error(), tt.want) {
				t.Errorf("error %q does not mention %q", err, tt.want)
			}
		})
	}
}

func TestValidateAcceptsFixtures(t *testing.T) {
	for name, m := range map[string]Manifest{
		"minimal": minimalManifest(),
		"full":    fullManifest(),
		"hostile": hostileManifest(),
	} {
		t.Run(name, func(t *testing.T) {
			if err := m.validate(); err != nil {
				t.Fatalf("validate: %v", err)
			}
		})
	}
}
