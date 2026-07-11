package instructions

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

func goldenDiff(t *testing.T, name, got string) {
	t.Helper()
	path := filepath.Join("testdata", name+".golden.diff")
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
		t.Errorf("unifiedDiff does not match %s\n--- got ---\n%s\n--- want ---\n%s", path, got, string(want))
	}
}

func TestUnifiedDiffNoChange(t *testing.T) {
	if got := unifiedDiff("same\nlines\n", "same\nlines\n"); got != "" {
		t.Errorf("unifiedDiff(equal) = %q, want \"\"", got)
	}
}

func TestUnifiedDiffInsert(t *testing.T) {
	old := "a\nb\nc\n"
	newText := "a\nb\nX\nc\n"
	got := unifiedDiff(old, newText)
	goldenDiff(t, "diff_insert", got)
	if strings.Count(got, "@@") != 2 {
		t.Errorf("expected exactly one hunk (2 '@@' markers), got:\n%s", got)
	}
}

func TestUnifiedDiffDelete(t *testing.T) {
	old := "a\nb\nc\nd\n"
	newText := "a\nc\nd\n"
	got := unifiedDiff(old, newText)
	goldenDiff(t, "diff_delete", got)
}

func TestUnifiedDiffReplace(t *testing.T) {
	old := "a\nb\nc\n"
	newText := "a\nB\nc\n"
	got := unifiedDiff(old, newText)
	goldenDiff(t, "diff_replace", got)
}

// TestUnifiedDiffNoTrailingNewlineBothSides changes the last line on
// both sides (b/B) while also dropping the trailing newline on both
// sides, so the final line is a delete+insert pair rather than shared
// context: the marker must print once for each side.
func TestUnifiedDiffNoTrailingNewlineBothSides(t *testing.T) {
	old := "a\nb"
	newText := "a\nB"
	got := unifiedDiff(old, newText)
	goldenDiff(t, "diff_no_trailing_newline", got)
	if strings.Count(got, noNewlineMarker) != 2 {
		t.Errorf("expected the no-newline marker once per side, got:\n%s", got)
	}
}

// TestUnifiedDiffNoTrailingNewlineOldOnly changes a line before the
// (shared, unchanged) last line, so old's missing trailing newline is
// the only asymmetry and must produce exactly one marker.
func TestUnifiedDiffNoTrailingNewlineOldOnly(t *testing.T) {
	old := "a\nb\nc"
	newText := "a\nB\nc\n"
	got := unifiedDiff(old, newText)
	if strings.Count(got, noNewlineMarker) != 1 {
		t.Errorf("expected exactly one no-newline marker, got:\n%s", got)
	}
}

// TestUnifiedDiffTrailingNewlineOnlyDifference documents unifiedDiff's
// known limitation (see its doc comment): two inputs whose lines are
// all textually identical, differing only in whether the final line
// carries a trailing newline, compare as "no diff" even though old !=
// new.
func TestUnifiedDiffTrailingNewlineOnlyDifference(t *testing.T) {
	old := "a\nb\nc"
	newText := "a\nb\nc\n"
	if old == newText {
		t.Fatal("test setup: old and new must differ")
	}
	if got := unifiedDiff(old, newText); got != "" {
		t.Errorf("unifiedDiff = %q, want \"\" (known limitation)", got)
	}
}

func TestUnifiedDiffHunkMerge(t *testing.T) {
	// Two changes 4 equal lines apart (< 2*diffContext=6) must merge
	// into a single hunk.
	old := "1\n2\n3\nX\n5\n6\n7\n8\nY\n10\n11\n12\n"
	newText := "1\n2\n3\nx\n5\n6\n7\n8\ny\n10\n11\n12\n"
	got := unifiedDiff(old, newText)
	goldenDiff(t, "diff_merge", got)
	if n := strings.Count(got, "@@"); n != 2 {
		t.Errorf("expected changes to merge into one hunk (2 '@@' markers), got %d in:\n%s", n, got)
	}
}

func TestUnifiedDiffHunkSeparate(t *testing.T) {
	// Two changes far enough apart (well beyond 2*diffContext=6 equal
	// lines) must stay as two separate hunks.
	var oldLines, newLines []string
	oldLines = append(oldLines, "1", "2", "3", "X")
	for i := 5; i <= 20; i++ {
		oldLines = append(oldLines, strconv.Itoa(i))
	}
	oldLines = append(oldLines, "Y", "22")
	newLines = append(newLines, "1", "2", "3", "x")
	for i := 5; i <= 20; i++ {
		newLines = append(newLines, strconv.Itoa(i))
	}
	newLines = append(newLines, "y", "22")

	old := strings.Join(oldLines, "\n") + "\n"
	newText := strings.Join(newLines, "\n") + "\n"
	got := unifiedDiff(old, newText)
	goldenDiff(t, "diff_separate", got)
	if n := strings.Count(got, "@@"); n != 4 {
		t.Errorf("expected two separate hunks (4 '@@' markers), got %d in:\n%s", n, got)
	}
}

func TestUnifiedDiffDeterministic(t *testing.T) {
	old := "a\nb\nc\nd\ne\n"
	newText := "a\nx\nc\ny\ne\n"
	a := unifiedDiff(old, newText)
	b := unifiedDiff(old, newText)
	if a != b {
		t.Fatal("unifiedDiff is not deterministic across two calls")
	}
}

// TestUnifiedDiffRealBlockChangesAreSingleHunk asserts the invariant
// the diff plan calls for: a real install/upgrade/remove diff over a
// managed block touches one contiguous region and so must render as a
// single hunk.
func TestUnifiedDiffRealBlockChangesAreSingleHunk(t *testing.T) {
	install, err := Plan("")
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}
	if n := strings.Count(install.Diff, "@@"); n != 2 {
		t.Errorf("install diff has %d '@@' markers, want 2 (one hunk):\n%s", n, install.Diff)
	}

	remove, err := PlanRemove(install.New)
	if err != nil {
		t.Fatalf("PlanRemove: %v", err)
	}
	if n := strings.Count(remove.Diff, "@@"); n != 2 {
		t.Errorf("remove diff has %d '@@' markers, want 2 (one hunk):\n%s", n, remove.Diff)
	}
}
