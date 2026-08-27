package manifest

import (
	"strings"
	"testing"
)

// fixtureObjective is the approved objective every fixture but the
// hostile one carries, pinned as a constant so a drift case can rewrite
// it by exact bytes without guessing at the fixture's text.
const fixtureObjective = "Ship the manifest round trip."

// minimalManifest is a valid record with no escalations or
// verifications — the smallest thing Render accepts.
func minimalManifest() Manifest {
	return Manifest{
		SchemaVersion:      SchemaVersion,
		Objective:          fixtureObjective,
		AcceptanceCriteria: []string{"Render and Parse agree on every field."},
		RequiredTests:      []string{"go test ./internal/manifest/..."},
		Role:               RoleImplementer,
		Executor:           Selection{Model: "opus-4-8", Effort: "high"},
		RoutingRationale:   "Selected implementer for a bounded single-file change.",
		Reviewer:           Selection{Model: "gpt-5.6-sol", Effort: "medium"},
		EffortDelivery:     EffortDeliveryParameter,
		ConfigRevision:     "cfg-2026-07-10",
	}
}

func TestOpenCodeVariantAndNoVariantRoundTrip(t *testing.T) {
	m := minimalManifest()
	m.Executor = OpenCodeSelection("openai/gpt-5.6-sol", "xhigh")
	m.Reviewer = OpenCodeSelection("github-copilot/gpt-5-mini", "")
	m.EffortDelivery = EffortDeliveryModelVariant
	rendered, err := Render(m)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"openai/gpt-5.6-sol#xhigh", "variant `xhigh`", "github-copilot/gpt-5-mini", "no variant", `"no_variant": true`} {
		if !strings.Contains(rendered, want) {
			t.Errorf("rendered manifest missing %q:\n%s", want, rendered)
		}
	}
	got, err := Parse(rendered)
	if err != nil {
		t.Fatal(err)
	}
	if got.Executor != m.Executor || got.Reviewer != m.Reviewer {
		t.Errorf("round trip selections = %+v / %+v", got.Executor, got.Reviewer)
	}
}

// fixtureCommitOID is the head the fixture's stamped verifications were
// gathered at, and fixtureFixOID a later head one of them was re-run on,
// so a golden shows two entries that disagree about which commit they
// speak for — the distinction the field exists to make.
const (
	fixtureCommitOID = "3f6a1c9d8b2e4057a1c3e5079b2d4f6081a3c5e7"
	fixtureFixOID    = "c5e70981a3f6b2d4079b1c3e52e40578b2e4a1c3"
)

// fixtureLocalOnlyTest is the required test the full fixture declares the
// repository's CI does not run, pinned as a constant so a test can assert
// the annotated bullet by exact command.
const fixtureLocalOnlyTest = "go vet ./..."

// fullManifest exercises every optional field and every repeatable one:
// multi-entry acceptance criteria and required tests, one of them
// declared as a test CI does not run and the other not (so the golden
// pins the annotated and the plain bullet side by side), two escalations
// (an escalation with a role and At, a substitution without), and three
// verifications, the last a CI-state entry with no command. Two carry a
// commit OID and disagree about which one; the middle entry carries none,
// so the golden pins both the stamped and the unstamped render.
func fullManifest() Manifest {
	m := minimalManifest()
	m.AcceptanceCriteria = []string{
		"Render and Parse agree on every field.",
		"A hand-edited region fails closed.",
	}
	m.RequiredTests = []string{"go test ./internal/manifest/...", fixtureLocalOnlyTest}
	m.TestsCIDoesNotRun = []string{fixtureLocalOnlyTest}
	m.EffortDelivery = EffortDeliveryPromptCue
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
		{Name: "targeted-tests", Command: "go test ./internal/manifest/...", Result: "pass", CommitOID: fixtureCommitOID, At: "2026-07-10T14:20:00Z"},
		{Name: "vet", Command: "go vet ./...", Result: "pass", Detail: "no findings"},
		{Name: "ci", Result: "CLEAN", Detail: "required checks green", CommitOID: fixtureFixOID, At: "2026-07-10T14:45:00Z"},
	}
	return m
}

// hostileManifest packs marker-forging and markdown-breaking content
// into every free-text field: an objective and an acceptance criterion
// each smuggling a marker line; a required test that is exactly the
// data-close, declared as one CI does not run so its annotation lands
// after a hostile code span; a rationale containing "-->", a line equal
// to EndMarker, a CRLF pair, and a <script> tag; a command with
// backticks, pipes, and an ampersand; a model with a pipe; a config
// revision with all three entity characters; an escalation whose reason
// is a data-close and whose At smuggles a begin-marker line; and a
// verification whose name, result, commit OID, and At each smuggle a
// marker or data-comment line. Render must escape all of it so the
// region still contains exactly one of each structural line and parses
// back.
func hostileManifest() Manifest {
	return Manifest{
		SchemaVersion: SchemaVersion,
		Objective:     "Objective ending in " + dataClose + "\n" + BeginMarker + "\nand more.",
		AcceptanceCriteria: []string{
			"Criterion with a marker line\n" + EndMarker,
			"Criterion with &<> entities and a | pipe.",
		},
		RequiredTests:     []string{dataClose, "go test -run 'A|B' ./..."},
		TestsCIDoesNotRun: []string{dataClose},
		Role:              RoleReviewer,
		Executor:          Selection{Model: "weird|model", Effort: "high"},
		RoutingRationale: "Rationale with a marker attempt -->\r\n" +
			EndMarker + "\n" +
			"and a <script>alert(1)</script> tag.",
		Reviewer:       Selection{Model: "opus-4-8", Effort: "low"},
		EffortDelivery: EffortDeliveryPromptCue,
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
				Name:      "targeted-tests\n" + EndMarker,
				Command:   "go test -run 'A|B' ./... `backtick` && echo done",
				Result:    "pass\n" + dataOpen,
				CommitOID: "deadbeef\n" + BeginMarker,
				At:        "2026-07-10T14:20:00Z\n" + dataClose,
			},
		},
	}
}

func TestValidateRejects(t *testing.T) {
	tests := map[string]struct {
		mutate func(*Manifest)
		want   string
	}{
		"original schema version":   {func(m *Manifest) { m.SchemaVersion = 1 }, "schema_version 1 is unsupported"},
		"superseded schema version": {func(m *Manifest) { m.SchemaVersion = 2 }, "schema_version 2 is unsupported"},
		"prior schema version":      {func(m *Manifest) { m.SchemaVersion = 3 }, "schema_version 3 is unsupported"},
		"v4 schema version":         {func(m *Manifest) { m.SchemaVersion = 4 }, "schema_version 4 is unsupported"},
		"future schema version":     {func(m *Manifest) { m.SchemaVersion = 6 }, "schema_version 6 is unsupported"},
		"absent schema version":     {func(m *Manifest) { m.SchemaVersion = 0 }, "schema_version 0 is unsupported"},
		"empty objective":           {func(m *Manifest) { m.Objective = "" }, "objective is empty"},
		"no acceptance criteria":    {func(m *Manifest) { m.AcceptanceCriteria = nil }, "acceptance_criteria is empty"},
		"empty acceptance criteria": {func(m *Manifest) { m.AcceptanceCriteria[1] = "" }, "acceptance_criteria[1] is empty"},
		"no required tests":         {func(m *Manifest) { m.RequiredTests = nil }, "required_tests is empty"},
		"empty required test":       {func(m *Manifest) { m.RequiredTests[0] = "" }, "required_tests[0] is empty"},
		"empty ci declaration": {func(m *Manifest) { m.TestsCIDoesNotRun = []string{""} },
			"tests_ci_does_not_run[0] is empty"},
		"ci declaration names no required test": {func(m *Manifest) { m.TestsCIDoesNotRun = []string{"go test -tags golden ./..."} },
			`tests_ci_does_not_run[0] "go test -tags golden ./..." does not name one of required_tests`},
		"unknown role":              {func(m *Manifest) { m.Role = "wizard" }, `role "wizard" is not one of`},
		"empty executor model":      {func(m *Manifest) { m.Executor.Model = "" }, "executor.model is empty"},
		"empty executor effort":     {func(m *Manifest) { m.Executor.Effort = "" }, "executor must carry exactly one of"},
		"empty reviewer model":      {func(m *Manifest) { m.Reviewer.Model = "" }, "reviewer.model is empty"},
		"empty reviewer effort":     {func(m *Manifest) { m.Reviewer.Effort = "" }, "reviewer must carry exactly one of"},
		"absent effort delivery":    {func(m *Manifest) { m.EffortDelivery = "" }, `effort_delivery "" is not one of`},
		"unknown effort delivery":   {func(m *Manifest) { m.EffortDelivery = "telepathy" }, `effort_delivery "telepathy" is not one of`},
		"empty rationale":           {func(m *Manifest) { m.RoutingRationale = "" }, "routing_rationale is empty"},
		"empty config revision":     {func(m *Manifest) { m.ConfigRevision = "" }, "config_revision is empty"},
		"bad escalation kind":       {func(m *Manifest) { m.Escalations[0].Kind = "demotion" }, `escalations[0]: kind "demotion" is not one of`},
		"escalation empty from":     {func(m *Manifest) { m.Escalations[0].From.Model = "" }, "escalations[0]: from.model is empty"},
		"escalation empty to":       {func(m *Manifest) { m.Escalations[1].To.Effort = "" }, "escalations[1]: to must carry exactly one of"},
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

// TestUnsupportedVersionNamesARealRemediation pins the half of the
// remediation fix that lives in validate. The message an operator meets
// on a superseded record must name a command that exists and can
// actually recover: nothing "regenerates" a posted record, and every
// body-reading verb fails closed on this same check, so an operator who
// followed the old advice had nothing to run.
func TestUnsupportedVersionNamesARealRemediation(t *testing.T) {
	m := fullManifest()
	m.SchemaVersion = SchemaVersion - 1
	_, err := Render(m)
	if err == nil {
		t.Fatal("Render accepted a superseded schema version")
	}
	if !strings.Contains(err.Error(), "orch abort") {
		t.Errorf("error %q does not name the abort remediation", err)
	}
	if strings.Contains(err.Error(), "regenerate the record") {
		t.Errorf("error %q still names a remediation that does not exist", err)
	}
}

// TestVerificationCommitOIDIsOptional pins the deliberate choice that an
// empty commit OID validates. One engine site genuinely has no head to
// read — an issue abandoned before its PR ever opened — and a required
// field would fail Render there after that verb's GitHub mutations had
// already landed, leaving a half-written record and no remedy.
func TestVerificationCommitOIDIsOptional(t *testing.T) {
	m := fullManifest()
	for i := range m.Verifications {
		m.Verifications[i].CommitOID = ""
	}
	if err := m.validate(); err != nil {
		t.Fatalf("validate rejected verifications carrying no commit OID: %v", err)
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
