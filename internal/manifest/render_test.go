package manifest

import (
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// update regenerates the golden files instead of comparing against them.
// The goldens are written with LF only; .gitattributes pins *.md to
// eol=lf so they check out identically on every OS and the byte compare
// below holds.
var update = flag.Bool("update", false, "regenerate golden files")

func goldenCases() map[string]Manifest {
	return map[string]Manifest{
		"minimal": minimalManifest(),
		"full":    fullManifest(),
		"hostile": hostileManifest(),
	}
}

func TestRenderGolden(t *testing.T) {
	for name, m := range goldenCases() {
		t.Run(name, func(t *testing.T) {
			got, err := Render(m)
			if err != nil {
				t.Fatalf("Render: %v", err)
			}
			path := filepath.Join("testdata", name+".golden.md")
			if *update {
				if err := os.WriteFile(path, []byte(got), 0o644); err != nil {
					t.Fatalf("write golden: %v", err)
				}
			}
			want, err := os.ReadFile(path)
			if err != nil {
				t.Fatalf("read golden (run `go test ./internal/manifest -update`): %v", err)
			}
			if got != string(want) {
				t.Errorf("Render(%s) does not match %s\n--- got ---\n%s\n--- want ---\n%s", name, path, got, want)
			}
		})
	}
}

// TestRenderShowsCommitOIDInBothViews pins the commit OID into the human
// markdown as well as the canonical JSON. A field emitted only into the
// data comment is invisible to anyone reading the record on GitHub —
// where the comment does not display at all — and the drift check would
// still pass, so nothing else would catch the omission.
func TestRenderShowsCommitOIDInBothViews(t *testing.T) {
	got, err := Render(fullManifest())
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	human, data, ok := strings.Cut(got, "\n"+dataOpen+"\n")
	if !ok {
		t.Fatal("could not split the region into its human and JSON views")
	}
	for _, oid := range []string{fixtureCommitOID, fixtureFixOID} {
		if !strings.Contains(human, oid) {
			t.Errorf("the human markdown does not show commit %s", oid)
		}
		if !strings.Contains(data, `"commit_oid": "`+oid+`"`) {
			t.Errorf("the canonical JSON does not carry commit_oid %s", oid)
		}
	}
}

// TestRenderShowsCIDeclarationInBothViews pins the plan's CI declaration
// into the human markdown as well as the canonical JSON, and pins it
// beside the test it qualifies rather than as a separate list: a reviewer
// reads this record on a PR, where the data comment does not display at
// all, so a declaration living only there would tell nobody that the
// required test above it is the only thing that will ever run it. It also
// checks the record says nothing about the tests it did not name, since a
// note on every bullet would carry no information.
func TestRenderShowsCIDeclarationInBothViews(t *testing.T) {
	m := fullManifest()
	got, err := Render(m)
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	human, data, ok := strings.Cut(got, "\n"+dataOpen+"\n")
	if !ok {
		t.Fatal("could not split the region into its human and JSON views")
	}
	if want := "- " + mdCode(fixtureLocalOnlyTest) + CIDoesNotRunNote + "\n"; !strings.Contains(human, want) {
		t.Errorf("the human markdown does not annotate the declared test\nwant line: %q\n--- human ---\n%s", want, human)
	}
	if !strings.Contains(data, `"tests_ci_does_not_run": [`) {
		t.Errorf("the canonical JSON does not carry tests_ci_does_not_run\n--- data ---\n%s", data)
	}
	if n := strings.Count(human, CIDoesNotRunNote); n != 1 {
		t.Errorf("the human markdown carries %d CI notes, want exactly one: only %q was declared", n, fixtureLocalOnlyTest)
	}

	// A record declaring nothing renders no note at all and omits the
	// field, so a plan that made no statement produces the same bytes it
	// produced before the declaration existed.
	m.TestsCIDoesNotRun = nil
	silent, err := Render(m)
	if err != nil {
		t.Fatalf("Render (no declaration): %v", err)
	}
	if strings.Contains(silent, CIDoesNotRunNote) || strings.Contains(silent, "tests_ci_does_not_run") {
		t.Errorf("a record declaring nothing still mentions the declaration:\n%s", silent)
	}
}

func TestRenderDeterministic(t *testing.T) {
	for name, m := range goldenCases() {
		t.Run(name, func(t *testing.T) {
			a, err := Render(m)
			if err != nil {
				t.Fatalf("Render: %v", err)
			}
			b, err := Render(m)
			if err != nil {
				t.Fatalf("Render (second): %v", err)
			}
			if a != b {
				t.Fatal("Render is not deterministic across two calls")
			}
			if strings.Contains(a, "\r") {
				t.Error("Render emitted a carriage return")
			}
			if n := countLines(a, dataClose); n != 1 {
				t.Errorf("found %d %q lines, want exactly one (the data-close)", n, dataClose)
			}
			if n := countLines(a, BeginMarker); n != 1 {
				t.Errorf("found %d begin markers, want one", n)
			}
			if n := countLines(a, EndMarker); n != 1 {
				t.Errorf("found %d end markers, want one", n)
			}
			if n := countLines(a, dataOpen); n != 1 {
				t.Errorf("found %d data-open lines, want one", n)
			}
		})
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
