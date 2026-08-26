package manifest

import (
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"
)

// mustRender renders m or fails the test.
func mustRender(t *testing.T, m Manifest) string {
	t.Helper()
	s, err := Render(m)
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	return s
}

// equalManifest compares two manifests treating nil and empty slices as
// equal, since omitempty makes Parse(Render(m)) return nil where the
// input held empty non-nil slices.
func equalManifest(a, b Manifest) bool {
	na := a
	nb := b
	if len(na.Escalations) == 0 {
		na.Escalations = nil
	}
	if len(nb.Escalations) == 0 {
		nb.Escalations = nil
	}
	if len(na.Verifications) == 0 {
		na.Verifications = nil
	}
	if len(nb.Verifications) == 0 {
		nb.Verifications = nil
	}
	return reflect.DeepEqual(na, nb)
}

func TestParseRoundTrip(t *testing.T) {
	for name, m := range map[string]Manifest{
		"minimal": minimalManifest(),
		"full":    fullManifest(),
		"hostile": hostileManifest(),
	} {
		t.Run(name, func(t *testing.T) {
			got, err := Parse(mustRender(t, m))
			if err != nil {
				t.Fatalf("Parse: %v", err)
			}
			if !equalManifest(got, m) {
				t.Errorf("round trip mismatch\n got %+v\nwant %+v", got, m)
			}
		})
	}
}

func TestParseEmptySlicesBecomeNil(t *testing.T) {
	m := minimalManifest()
	m.Escalations = []Escalation{}
	m.Verifications = []Verification{}
	got, err := Parse(mustRender(t, m))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if got.Escalations != nil {
		t.Errorf("Escalations = %v, want nil", got.Escalations)
	}
	if got.Verifications != nil {
		t.Errorf("Verifications = %v, want nil", got.Verifications)
	}
}

func TestParseRegionPosition(t *testing.T) {
	region := mustRender(t, fullManifest())
	cases := map[string]string{
		"whole body": region,
		"at start":   region + "\n\ntrailing human notes\n",
		"at end":     "leading human notes\n\n" + region,
		"mid body":   "leading\n\n" + region + "\n\ntrailing\n",
	}
	for name, body := range cases {
		t.Run(name, func(t *testing.T) {
			got, err := Parse(body)
			if err != nil {
				t.Fatalf("Parse: %v", err)
			}
			if !equalManifest(got, fullManifest()) {
				t.Errorf("mismatch: %+v", got)
			}
		})
	}
}

func TestParseNoManifest(t *testing.T) {
	cases := map[string]string{
		"empty":           "",
		"plain text":      "Just some issue body with no audit record.\n",
		"mid-line begin":  "See <!-- orch:manifest:begin --> mentioned inline.\n",
		"mid-line end":    "prefix " + EndMarker + " suffix\n",
		"marker in words": "The manifest:begin marker is documented elsewhere.\n",
	}
	for name, body := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := Parse(body)
			if !errors.Is(err, ErrNoManifest) {
				t.Fatalf("err = %v, want ErrNoManifest", err)
			}
		})
	}
}

func TestParseBadManifest(t *testing.T) {
	valid := mustRender(t, minimalManifest())
	cases := map[string]string{
		"unpaired begin":       BeginMarker + "\n### body\n",
		"unpaired end":         "### body\n" + EndMarker + "\n",
		"duplicated begin":     BeginMarker + "\n" + valid + "\n",
		"reversed markers":     EndMarker + "\nbody\n" + BeginMarker + "\n",
		"missing data comment": BeginMarker + "\n### Orch audit record\n" + EndMarker,
		"unterminated data":    BeginMarker + "\n" + dataOpen + "\n{}\n" + EndMarker,
		"double data comment":  BeginMarker + "\n" + dataOpen + "\n{}\n" + dataClose + "\n" + dataOpen + "\n{}\n" + dataClose + "\n" + EndMarker,
		"bad json":             BeginMarker + "\n" + dataOpen + "\nthis is not json\n" + dataClose + "\n" + EndMarker,
		"schema version zero":  tamperJSON(valid, `"schema_version": 5`, `"schema_version": 0`),
		"schema version six":   tamperJSON(valid, `"schema_version": 5`, `"schema_version": 6`),
		"schema absent":        BeginMarker + "\n" + dataOpen + "\n{\"role\":\"implementer\"}\n" + dataClose + "\n" + EndMarker,
		"invalid record":       tamperJSON(valid, `"role": "implementer"`, `"role": "wizard"`),
	}
	for name, body := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := Parse(body)
			if !errors.Is(err, ErrBadManifest) {
				t.Fatalf("err = %v, want ErrBadManifest", err)
			}
		})
	}
}

// schemaOneRegion is a genuine schema-1 record, frozen byte-for-byte as
// the v1 renderer emitted it. It is not a later render with a tampered
// version number: it is the real thing this build must refuse, missing
// exactly the objective, acceptance criteria, required tests, and
// effort-delivery fields v2 required.
const schemaOneRegion = BeginMarker + `
### Orch audit record

| Field | Value |
| --- | --- |
| Role | ` + "`implementer`" + ` |
| Executor | ` + "`opus-4-8`" + ` — effort ` + "`high`" + ` |
| Reviewer | ` + "`gpt-5.6-sol`" + ` — effort ` + "`medium`" + ` |
| Config revision | ` + "`cfg-2026-07-10`" + ` |

**Routing rationale:** Selected implementer for a bounded single-file change.

**Escalations:** _none_

**Verification:** _none_

` + dataOpen + `
{
  "schema_version": 1,
  "role": "implementer",
  "executor": {
    "model": "opus-4-8",
    "effort": "high"
  },
  "routing_rationale": "Selected implementer for a bounded single-file change.",
  "reviewer": {
    "model": "gpt-5.6-sol",
    "effort": "medium"
  },
  "config_revision": "cfg-2026-07-10"
}
` + dataClose + `
` + EndMarker

// TestParseRejectsSchemaOneRecord proves a v1 record fails closed through
// the unsupported-version path — named as such, before the drift compare
// — rather than decoding into this build's struct with approved work
// that would read as empty. There is no migration: the missing fields
// cannot be invented.
func TestParseRejectsSchemaOneRecord(t *testing.T) {
	_, err := Parse(schemaOneRegion)
	if !errors.Is(err, ErrBadManifest) {
		t.Fatalf("err = %v, want ErrBadManifest", err)
	}
	if !strings.Contains(err.Error(), "schema_version 1 is unsupported") {
		t.Errorf("err %q does not name the unsupported version", err)
	}
	if errors.Is(err, ErrDrift) {
		t.Error("a v1 record was reported as drift; the version check must come first")
	}
}

// schemaTwoRegion is a genuine schema-2 record, frozen byte-for-byte as
// the v2 renderer emitted it: the minimal fixture plus one verification,
// which — being v2 — names no commit it was gathered at.
const schemaTwoRegion = BeginMarker + `
### Orch audit record

**Objective:** Ship the manifest round trip.

**Acceptance criteria:**
- Render and Parse agree on every field.

**Required tests:**
- ` + "`go test ./internal/manifest/...`" + `

| Field | Value |
| --- | --- |
| Role | ` + "`implementer`" + ` |
| Executor | ` + "`opus-4-8`" + ` — effort ` + "`high`" + ` |
| Reviewer | ` + "`gpt-5.6-sol`" + ` — effort ` + "`medium`" + ` |
| Effort delivery | ` + "`parameter`" + ` — the host applied the routed effort as a real model parameter |
| Config revision | ` + "`cfg-2026-07-10`" + ` |

**Routing rationale:** Selected implementer for a bounded single-file change.

**Escalations:** _none_

**Verification:**
- **targeted-tests** — pass — ` + "`go test ./internal/manifest/...`" + ` (2026-07-10T14:20:00Z)

` + dataOpen + `
{
  "schema_version": 2,
  "objective": "Ship the manifest round trip.",
  "acceptance_criteria": [
    "Render and Parse agree on every field."
  ],
  "required_tests": [
    "go test ./internal/manifest/..."
  ],
  "role": "implementer",
  "executor": {
    "model": "opus-4-8",
    "effort": "high"
  },
  "routing_rationale": "Selected implementer for a bounded single-file change.",
  "reviewer": {
    "model": "gpt-5.6-sol",
    "effort": "medium"
  },
  "effort_delivery": "parameter",
  "config_revision": "cfg-2026-07-10",
  "verifications": [
    {
      "name": "targeted-tests",
      "command": "go test ./internal/manifest/...",
      "result": "pass",
      "at": "2026-07-10T14:20:00Z"
    }
  ]
}
` + dataClose + `
` + EndMarker

// TestSchemaTwoRegionIsAGenuineRender guards the fixture above against a
// typo. No verification in it carries a commit OID and it declares no
// test CI does not run, so a v2 render and this build's render of the
// same record differ in exactly one place — the schema_version line —
// and re-rendering the frozen region's own decoded record at this
// build's version must reproduce it byte for byte. Without this check a
// mangled fixture would still be rejected, and the rejection test below
// would pass for the wrong reason.
func TestSchemaTwoRegionIsAGenuineRender(t *testing.T) {
	jsonText, err := extractData(schemaTwoRegion)
	if err != nil {
		t.Fatalf("extractData: %v", err)
	}
	var m Manifest
	if err := json.Unmarshal([]byte(jsonText), &m); err != nil {
		t.Fatalf("decode the frozen record: %v", err)
	}
	if m.SchemaVersion != 2 {
		t.Fatalf("frozen record schema_version = %d, want 2", m.SchemaVersion)
	}
	m.SchemaVersion = SchemaVersion
	got, err := Render(m)
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	want := tamperJSON(schemaTwoRegion, `"schema_version": 2`, fmt.Sprintf(`"schema_version": %d`, SchemaVersion))
	if got != want {
		t.Errorf("the frozen region is not a v2 render of its own record\n--- re-rendered ---\n%s\n--- frozen, version raised ---\n%s", got, want)
	}
}

// TestParseRejectsSchemaThreeRecord proves a v3 record — the version
// every posted body in flight when this build ships carries — fails
// closed through the unsupported-version path, before the drift compare,
// with the remediation an operator can actually run.
//
// Its body is lowered from this build's own render rather than frozen
// like schemaOneRegion and schemaTwoRegion above, because that
// construction is exact for this pair: v4's only added field is optional
// and renders nothing when the plan declared nothing, so a genuine v3
// render of a declaration-free record and a v4 render of it differ in
// exactly the schema_version line. Freezing a copy would assert the same
// bytes with more of them.
func TestParseRejectsSchemaThreeRecord(t *testing.T) {
	body := tamperJSON(mustRender(t, fullManifest()), `"schema_version": 5`, `"schema_version": 3`)
	_, err := Parse(body)
	if !errors.Is(err, ErrBadManifest) {
		t.Fatalf("err = %v, want ErrBadManifest", err)
	}
	if !strings.Contains(err.Error(), "schema_version 3 is unsupported") {
		t.Errorf("err %q does not name the unsupported version", err)
	}
	if errors.Is(err, ErrDrift) {
		t.Error("a v3 record was reported as drift; the version check must come first")
	}
	if !strings.Contains(err.Error(), "orch abort") {
		t.Errorf("err %q does not name a remediation that exists", err)
	}
}

func TestParseRejectsSchemaFourRecord(t *testing.T) {
	body := tamperJSON(mustRender(t, fullManifest()), `"schema_version": 5`, `"schema_version": 4`)
	_, err := Parse(body)
	if !errors.Is(err, ErrBadManifest) || !strings.Contains(err.Error(), "schema_version 4 is unsupported") {
		t.Fatalf("err = %v, want unsupported v4 manifest", err)
	}
	if errors.Is(err, ErrDrift) {
		t.Error("a v4 record was reported as drift; the version check must come first")
	}
}

// TestParseRejectsSchemaTwoRecord proves an earlier superseded record
// fails closed through the same unsupported-version path, before the
// drift compare, and that the error names a remediation that exists.
//
// Nothing else would catch it. A v2 record decodes cleanly into this
// build's struct and re-renders to the same bytes but for the version
// line, so its verifications would silently read as gathered at no
// commit — which from v3 on is a claim about the evidence, not the
// absence of one — and the OID they lack cannot be recovered after the
// fact.
func TestParseRejectsSchemaTwoRecord(t *testing.T) {
	_, err := Parse(schemaTwoRegion)
	if !errors.Is(err, ErrBadManifest) {
		t.Fatalf("err = %v, want ErrBadManifest", err)
	}
	if !strings.Contains(err.Error(), "schema_version 2 is unsupported") {
		t.Errorf("err %q does not name the unsupported version", err)
	}
	if errors.Is(err, ErrDrift) {
		t.Error("a v2 record was reported as drift; the version check must come first")
	}
	if !strings.Contains(err.Error(), "orch abort") {
		t.Errorf("err %q does not name a remediation that exists", err)
	}
}

func TestParseDrift(t *testing.T) {
	base := mustRender(t, fullManifest())
	cases := map[string]string{
		"markdown char changed": strings.Replace(base, "| Role |", "| role |", 1),
		"blank line inserted":   strings.Replace(base, "### Orch audit record\n", "### Orch audit record\n\n", 1),
		"sentence inserted":     strings.Replace(base, "**Verification:**", "An extra human sentence.\n\n**Verification:**", 1),
		"json keys reordered": strings.Replace(base,
			"{\n  \"schema_version\": 5,\n  \"objective\": \""+fixtureObjective+"\",",
			"{\n  \"objective\": \""+fixtureObjective+"\",\n  \"schema_version\": 5,", 1),
	}
	for name, body := range cases {
		t.Run(name, func(t *testing.T) {
			if body == base {
				t.Fatal("test setup did not change the body")
			}
			_, err := Parse(body)
			if !errors.Is(err, ErrDrift) {
				t.Fatalf("err = %v, want ErrDrift", err)
			}
		})
	}
}

func TestParseCRLF(t *testing.T) {
	base := mustRender(t, fullManifest())
	lead, trail := "intro line\n\n", "\n\ntrailing line\n"
	crlf := func(s string) string { return strings.ReplaceAll(s, "\n", "\r\n") }

	// Split the region into its human markdown and its data comment so
	// one case can carry CRLF only in the human content.
	idx := strings.Index(base, "\n"+dataOpen)
	if idx < 0 {
		t.Fatal("could not locate data comment in region")
	}
	human, data := base[:idx], base[idx:]

	pass := map[string]string{
		"whole body crlf":    crlf(lead + base + trail),
		"region only crlf":   lead + crlf(base) + trail,
		"human content crlf": lead + crlf(human) + data + trail,
	}
	for name, body := range pass {
		t.Run(name, func(t *testing.T) {
			got, err := Parse(body)
			if err != nil {
				t.Fatalf("Parse: %v", err)
			}
			if !equalManifest(got, fullManifest()) {
				t.Errorf("mismatch: %+v", got)
			}
		})
	}

	t.Run("lone carriage return fails", func(t *testing.T) {
		// A bare CR inside a human line survives normalization (it is not
		// part of a CRLF pair) and fails the drift compare.
		body := strings.Replace(base, "bounded single-file", "bounded\rsingle-file", 1)
		if body == base {
			t.Fatal("test setup did not insert a carriage return")
		}
		_, err := Parse(body)
		if !errors.Is(err, ErrDrift) {
			t.Fatalf("err = %v, want ErrDrift", err)
		}
	})
}

// tamperJSON replaces old with new in body, requiring the swap to change
// something so a test cannot silently pass on an unchanged body.
func tamperJSON(body, old, new string) string {
	out := strings.Replace(body, old, new, 1)
	if out == body {
		panic("tamperJSON: substring not found: " + old)
	}
	return out
}
