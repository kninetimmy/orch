package instructions

import (
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// update regenerates the golden files instead of comparing against
// them. The goldens are written with LF only; .gitattributes pins
// *.md/*.diff to eol=lf so they check out identically on every OS and
// the byte compare below holds.
var update = flag.Bool("update", false, "regenerate golden files")

func mustRender(t *testing.T, version int) string {
	t.Helper()
	s, err := Render(version)
	if err != nil {
		t.Fatalf("Render(%d): %v", version, err)
	}
	return s
}

func TestRenderGoldenV1(t *testing.T) {
	got := mustRender(t, CurrentVersion)
	path := filepath.Join("testdata", "block_v1.golden.md")
	if *update {
		if err := os.WriteFile(path, []byte(got), 0o644); err != nil {
			t.Fatalf("write golden: %v", err)
		}
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read golden (run `go test ./internal/instructions -update`): %v", err)
	}
	if got != string(want) {
		t.Errorf("Render(%d) does not match %s\n--- got ---\n%s\n--- want ---\n%s", CurrentVersion, path, got, want)
	}
}

func TestRenderDeterministic(t *testing.T) {
	a := mustRender(t, CurrentVersion)
	b := mustRender(t, CurrentVersion)
	if a != b {
		t.Fatal("Render is not deterministic across two calls")
	}
	if strings.Contains(a, "\r") {
		t.Error("Render emitted a carriage return")
	}
	if n := countLines(a, EndMarker); n != 1 {
		t.Errorf("found %d end markers, want exactly one", n)
	}
	beginCount := 0
	for _, l := range strings.Split(a, "\n") {
		if beginPattern.MatchString(l) {
			beginCount++
		}
	}
	if beginCount != 1 {
		t.Errorf("found %d begin markers, want exactly one", beginCount)
	}
	if strings.HasSuffix(a, "\n") {
		t.Error("Render carries a trailing newline")
	}
}

func TestRenderUnknownVersion(t *testing.T) {
	if _, err := Render(2); err == nil {
		t.Fatal("Render(2) succeeded, want an error (no canonicalBody entry yet)")
	}
}

// TestBodyForgesNoMarker asserts the engine-owned body can never forge
// a marker on its own: no line of bodyV1 matches the begin regexp or
// equals EndMarker, so the block needs no escaping machinery the way
// manifest's free-text fields do.
func TestBodyForgesNoMarker(t *testing.T) {
	for _, l := range strings.Split(bodyV1, "\n") {
		if l == EndMarker {
			t.Errorf("body line %q equals EndMarker", l)
		}
		if beginPattern.MatchString(l) {
			t.Errorf("body line %q matches the begin marker pattern", l)
		}
	}
}

func countLines(s, want string) int {
	n := 0
	for _, l := range strings.Split(s, "\n") {
		if l == want {
			n++
		}
	}
	return n
}

// TestClassifyStaleSynthetic exercises StatusStale, which no real
// content can reach today (locate's begin regexp accepts only
// version >= 1, and CurrentVersion is 1): it calls classify directly
// with a synthetic location{version: 0}. This is the one test in the
// package that fabricates a location rather than deriving it from
// locate, precisely because the real path is unreachable until a
// version 2 exists.
func TestClassifyStaleSynthetic(t *testing.T) {
	content := "<!-- orchestrator:managed:start version=0 -->\nold body\n" + EndMarker
	lines := splitLines(content)
	loc := location{begin: 0, end: 2, version: 0}
	report := classify(content, lines, loc)
	if report.Status != StatusStale {
		t.Fatalf("classify() status = %v, want StatusStale", report.Status)
	}
	if report.Version != 0 {
		t.Errorf("classify() version = %d, want 0", report.Version)
	}
	if report.Detail == "" {
		t.Error("classify() Detail is empty for StatusStale")
	}
}

func TestClassifyNewerVersion(t *testing.T) {
	content := "<!-- orchestrator:managed:start version=2 -->\nnew body\n" + EndMarker
	lines := splitLines(content)
	loc := location{begin: 0, end: 2, version: 2}
	report := classify(content, lines, loc)
	if report.Status != StatusNewerVersion {
		t.Fatalf("classify() status = %v, want StatusNewerVersion", report.Status)
	}
	if !report.Blocking() {
		t.Error("StatusNewerVersion should be Blocking")
	}
}

// TestClassifyCRLFRegion asserts a checkout-time CRLF conversion of a
// current block is not misreported as a hand edit (classify normalizes
// CRLF within the region before its drift compare, manifest.Parse's
// idiom), while a lone \r that is not part of a CRLF pair still fails
// closed as Drifted.
func TestClassifyCRLFRegion(t *testing.T) {
	region := mustRender(t, CurrentVersion)

	crlf := strings.ReplaceAll(region, "\n", "\r\n")
	lines := splitLines(crlf)
	loc, err := locate(lines)
	if err != nil {
		t.Fatalf("locate: %v", err)
	}
	if got := classify(crlf, lines, loc); got.Status != StatusCurrent {
		t.Errorf("classify(CRLF-converted region) status = %v, want StatusCurrent", got.Status)
	}

	lone := strings.Replace(region, "This file", "This\rfile", 1)
	lLines := splitLines(lone)
	lLoc, err := locate(lLines)
	if err != nil {
		t.Fatalf("locate: %v", err)
	}
	if got := classify(lone, lLines, lLoc); got.Status != StatusDrifted {
		t.Errorf("classify(lone-\\r region) status = %v, want StatusDrifted", got.Status)
	}
}

func TestClassifyCurrentAndDrifted(t *testing.T) {
	region := mustRender(t, CurrentVersion)
	content := region
	lines := splitLines(content)
	loc, err := locate(lines)
	if err != nil {
		t.Fatalf("locate: %v", err)
	}
	if got := classify(content, lines, loc); got.Status != StatusCurrent {
		t.Fatalf("classify() status = %v, want StatusCurrent", got.Status)
	}

	drifted := strings.Replace(content, "This file", "THIS FILE", 1)
	dLines := splitLines(drifted)
	dLoc, err := locate(dLines)
	if err != nil {
		t.Fatalf("locate: %v", err)
	}
	got := classify(drifted, dLines, dLoc)
	if got.Status != StatusDrifted {
		t.Fatalf("classify() status = %v, want StatusDrifted", got.Status)
	}
	if !got.Blocking() {
		t.Error("StatusDrifted should be Blocking")
	}
}
