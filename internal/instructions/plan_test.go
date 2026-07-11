package instructions

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writeTestFile writes content to path, failing the test on error.
func writeTestFile(t *testing.T, path, content string) error {
	t.Helper()
	return os.WriteFile(path, []byte(content), 0o644)
}

func mustPlan(t *testing.T, content string) Change {
	t.Helper()
	ch, err := Plan(content)
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}
	return ch
}

// TestPlanTaxonomy walks every row of the Inspect/Plan taxonomy table.
func TestPlanTaxonomy(t *testing.T) {
	current := mustRender(t, CurrentVersion)

	t.Run("no markers installs", func(t *testing.T) {
		ch := mustPlan(t, "human notes\n")
		if ch.Action != ActionInstall {
			t.Fatalf("Action = %v, want ActionInstall", ch.Action)
		}
		if !strings.HasSuffix(ch.New, current) {
			t.Errorf("New does not end with the canonical block:\n%s", ch.New)
		}
		if ch.Diff == "" {
			t.Error("Diff is empty for an install")
		}
	})

	t.Run("current is a no-op", func(t *testing.T) {
		ch := mustPlan(t, current)
		if ch.Action != ActionNone {
			t.Fatalf("Action = %v, want ActionNone", ch.Action)
		}
		if ch.Diff != "" {
			t.Errorf("Diff = %q, want \"\" for ActionNone", ch.Diff)
		}
		if ch.Old != ch.New {
			t.Error("Old != New for ActionNone")
		}
	})

	t.Run("drifted body errors", func(t *testing.T) {
		drifted := strings.Replace(current, "This file", "THIS FILE", 1)
		_, err := Plan(drifted)
		if !errors.Is(err, ErrDrifted) {
			t.Fatalf("err = %v, want ErrDrifted", err)
		}
	})

	t.Run("newer version errors", func(t *testing.T) {
		newer := "<!-- orchestrator:managed:start version=2 -->\nfuture body\n" + EndMarker
		_, err := Plan(newer)
		if !errors.Is(err, ErrNewerVersion) {
			t.Fatalf("err = %v, want ErrNewerVersion", err)
		}
	})

	t.Run("malformed markers error", func(t *testing.T) {
		bad := current + "\n" + current // duplicated region: two begins, two ends
		_, err := Plan(bad)
		if !errors.Is(err, ErrMalformed) {
			t.Fatalf("err = %v, want ErrMalformed", err)
		}
	})
}

// TestPlanRemoveTaxonomy walks PlanRemove's taxonomy row: it removes
// on every reachable status except Absent (no-op) and Malformed
// (error), regardless of drift or version.
func TestPlanRemoveTaxonomy(t *testing.T) {
	current := mustRender(t, CurrentVersion)

	t.Run("no markers is a no-op", func(t *testing.T) {
		ch, err := PlanRemove("human notes\n")
		if err != nil {
			t.Fatalf("PlanRemove: %v", err)
		}
		if ch.Action != ActionNone {
			t.Fatalf("Action = %v, want ActionNone", ch.Action)
		}
		if ch.New != "human notes\n" {
			t.Errorf("New = %q, want input unchanged", ch.New)
		}
	})

	t.Run("current removes", func(t *testing.T) {
		ch, err := PlanRemove(current)
		if err != nil {
			t.Fatalf("PlanRemove: %v", err)
		}
		if ch.Action != ActionRemove {
			t.Fatalf("Action = %v, want ActionRemove", ch.Action)
		}
		if ch.New != "" {
			t.Errorf("New = %q, want empty (region was the whole content)", ch.New)
		}
		if !ch.DeleteWholeFile {
			t.Error("DeleteWholeFile = false, want true")
		}
	})

	t.Run("drifted still removes", func(t *testing.T) {
		drifted := strings.Replace(current, "This file", "THIS FILE", 1)
		ch, err := PlanRemove(drifted)
		if err != nil {
			t.Fatalf("PlanRemove: %v", err)
		}
		if ch.Action != ActionRemove {
			t.Fatalf("Action = %v, want ActionRemove even though the body drifted", ch.Action)
		}
	})

	t.Run("newer version still removes", func(t *testing.T) {
		newer := "<!-- orchestrator:managed:start version=2 -->\nfuture body\n" + EndMarker
		ch, err := PlanRemove(newer)
		if err != nil {
			t.Fatalf("PlanRemove: %v", err)
		}
		if ch.Action != ActionRemove {
			t.Fatalf("Action = %v, want ActionRemove even for a newer version", ch.Action)
		}
	})

	t.Run("malformed markers error", func(t *testing.T) {
		bad := current + "\n" + current
		_, err := PlanRemove(bad)
		if !errors.Is(err, ErrMalformed) {
			t.Fatalf("err = %v, want ErrMalformed", err)
		}
	})

	t.Run("remove with surrounding content preserves it", func(t *testing.T) {
		body := "intro\n\n" + current + "\n\noutro\n"
		ch, err := PlanRemove(body)
		if err != nil {
			t.Fatalf("PlanRemove: %v", err)
		}
		if ch.DeleteWholeFile {
			t.Error("DeleteWholeFile = true, want false (real content remains)")
		}
		if !strings.HasPrefix(ch.New, "intro\n\n") {
			t.Errorf("prefix not preserved: %q", ch.New)
		}
		if !strings.HasSuffix(ch.New, "\n\noutro\n") {
			t.Errorf("suffix not preserved: %q", ch.New)
		}
	})
}

func TestPlanIdempotent(t *testing.T) {
	bodies := []string{"", "human notes", "human notes\n", "intro\n\nmore notes\n"}
	for _, body := range bodies {
		t.Run(body, func(t *testing.T) {
			once := mustPlan(t, body)
			twice := mustPlan(t, once.New)
			if twice.Action != ActionNone {
				t.Fatalf("Plan over its own applied output has Action = %v, want ActionNone", twice.Action)
			}
			if once.New != twice.New {
				t.Errorf("re-planning changed content:\n once  %q\n twice %q", once.New, twice.New)
			}
		})
	}
}

func TestPlanInspectRoundTrip(t *testing.T) {
	cases := []string{"", "human notes\n", "intro\n\nnotes\n"}
	for _, body := range cases {
		t.Run(body, func(t *testing.T) {
			ch := mustPlan(t, body)
			report := Inspect(ch.New)
			if report.Status != StatusCurrent {
				t.Fatalf("Inspect(Plan(...).New) = %v, want StatusCurrent", report.Status)
			}
		})
	}
}

// TestPlanCRLFFixture asserts a CRLF prefix/suffix survive Install and
// Upgrade byte-identically while the block itself is always written
// as LF.
func TestPlanCRLFFixture(t *testing.T) {
	t.Run("install preserves crlf surroundings", func(t *testing.T) {
		body := "human intro\r\nmore notes\r\n"
		ch := mustPlan(t, body)
		if !strings.HasPrefix(ch.New, body) {
			t.Errorf("CRLF prefix not preserved verbatim: %q", ch.New)
		}
		block := ch.New[len(body):]
		if strings.Contains(block, "\r") {
			t.Errorf("installed block carries a carriage return: %q", block)
		}
	})

	// upgradeBlock's byte-splice mechanics (CRLF prefix/suffix
	// preserved, replacement always LF) are exercised directly here
	// rather than through Plan/StatusStale: a real StatusStale input
	// is unreachable until a version 2 exists (locate's begin regexp
	// accepts only version >= 1, and CurrentVersion is 1), so the
	// fixture below carries an ordinary version=1 marker — the
	// splice itself does not look at the found version at all.
	t.Run("upgrade preserves crlf prefix and suffix", func(t *testing.T) {
		// old carries no terminator of its own after EndMarker: the end
		// marker's line terminator is, per the splice formula, part of
		// the untouched suffix, so suffix here explicitly includes it
		// (the leading "\r\n") to make the expected output exact.
		old := "<!-- orchestrator:managed:start version=1 -->\r\nold body\r\n" + EndMarker
		prefix, suffix := "human intro\r\n\r\n", "\r\n\r\nhuman outro\r\n"
		body := prefix + old + suffix

		lines := splitLines(body)
		loc, err := locate(lines)
		if err != nil {
			t.Fatalf("locate: %v", err)
		}
		got, err := upgradeBlock(body, lines, loc)
		if err != nil {
			t.Fatalf("upgradeBlock: %v", err)
		}
		canonical := mustRender(t, CurrentVersion)
		want := prefix + canonical + suffix
		if got != want {
			t.Errorf("upgradeBlock() = %q, want %q", got, want)
		}
		if !strings.HasPrefix(got, prefix) {
			t.Errorf("CRLF prefix not preserved verbatim: %q", got)
		}
		if !strings.HasSuffix(got, suffix) {
			t.Errorf("CRLF suffix not preserved verbatim: %q", got)
		}
	})
}

// hostileBlockAndFence returns content with a real managed block plus
// an exact begin-marker line inside a fenced code block: line scanning
// knows nothing about fences, so the fenced line is still counted as a
// second begin marker, producing StatusMalformed even though a real,
// well-formed block is also present.
func hostileBlockAndFence(t *testing.T) string {
	t.Helper()
	real := mustRender(t, CurrentVersion)
	fence := "```\n<!-- orchestrator:managed:start version=1 -->\n```\n"
	return fence + real
}

func TestPlanHostileFixture(t *testing.T) {
	t.Run("fenced exact marker is counted and malformed", func(t *testing.T) {
		content := hostileBlockAndFence(t)
		report := Inspect(content)
		if report.Status != StatusMalformed {
			t.Fatalf("Inspect() = %v, want StatusMalformed (fenced marker line is still counted)", report.Status)
		}
		if _, err := Plan(content); !errors.Is(err, ErrMalformed) {
			t.Fatalf("Plan() err = %v, want ErrMalformed", err)
		}
	})

	t.Run("near-miss prose is ignored", func(t *testing.T) {
		content := "See <!-- orchestrator:managed:start version=1 --> mentioned in prose, " +
			"and a leading-zero variant <!-- orchestrator:managed:start version=01 --> too.\n" +
			"No real block here.\n"
		report := Inspect(content)
		if report.Status != StatusAbsent {
			t.Fatalf("Inspect() = %v, want StatusAbsent (near-miss/mid-line text is ordinary content)", report.Status)
		}
	})
}

func TestIsOtherwiseEmpty(t *testing.T) {
	cases := map[string]bool{
		"":          true,
		"   \n\t\n": true,
		"x":         false,
		"  x  \n":   false,
		"\n\n\n":    true,
	}
	for content, want := range cases {
		if got := IsOtherwiseEmpty(content); got != want {
			t.Errorf("IsOtherwiseEmpty(%q) = %v, want %v", content, got, want)
		}
	}
}

func TestPlanFileMissing(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "AGENTS.md")
	ch, err := PlanFile(path)
	if err != nil {
		t.Fatalf("PlanFile: %v", err)
	}
	if ch.FileExisted {
		t.Error("FileExisted = true for a missing file")
	}
	if ch.Action != ActionInstall {
		t.Fatalf("Action = %v, want ActionInstall", ch.Action)
	}
}

func TestPlanFileEmptyButExisting(t *testing.T) {
	// An empty file on disk exists, unlike Plan("")'s inferred
	// missing-file default; PlanFile must override FileExisted with
	// the read's truth.
	dir := t.TempDir()
	path := filepath.Join(dir, "AGENTS.md")
	if err := writeTestFile(t, path, ""); err != nil {
		t.Fatal(err)
	}
	ch, err := PlanFile(path)
	if err != nil {
		t.Fatalf("PlanFile: %v", err)
	}
	if !ch.FileExisted {
		t.Error("FileExisted = false for an empty-but-existing file")
	}
}

func TestPlanRemoveFileMissing(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "CLAUDE.md")
	ch, err := PlanRemoveFile(path)
	if err != nil {
		t.Fatalf("PlanRemoveFile: %v", err)
	}
	if ch.FileExisted {
		t.Error("FileExisted = true for a missing file")
	}
	if ch.Action != ActionNone {
		t.Fatalf("Action = %v, want ActionNone", ch.Action)
	}
}
