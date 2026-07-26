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
