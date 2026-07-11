package manifest

import (
	"errors"
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
		"schema version zero":  tamperJSON(valid, `"schema_version": 1`, `"schema_version": 0`),
		"schema version two":   tamperJSON(valid, `"schema_version": 1`, `"schema_version": 2`),
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

func TestParseDrift(t *testing.T) {
	base := mustRender(t, fullManifest())
	cases := map[string]string{
		"markdown char changed": strings.Replace(base, "| Role |", "| role |", 1),
		"blank line inserted":   strings.Replace(base, "### Orch audit record\n", "### Orch audit record\n\n", 1),
		"sentence inserted":     strings.Replace(base, "**Verification:**", "An extra human sentence.\n\n**Verification:**", 1),
		"json keys reordered": strings.Replace(base,
			"{\n  \"schema_version\": 1,\n  \"role\": \"implementer\",",
			"{\n  \"role\": \"implementer\",\n  \"schema_version\": 1,", 1),
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
