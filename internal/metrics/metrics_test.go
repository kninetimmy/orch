package metrics

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kninetimmy/orch/internal/manifest"
)

func writeDoc(t *testing.T, root, runID, content string) {
	t.Helper()
	dir := filepath.Join(root, filepath.FromSlash(Dir))
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, runID+".json"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestAppendCreatesDirAndFile(t *testing.T) {
	root := t.TempDir()
	if err := Append(root, "run-1", Event{At: "2026-07-13T00:00:00Z", Verb: "dispatch"}); err != nil {
		t.Fatalf("Append: %v", err)
	}
	path := filepath.Join(root, filepath.FromSlash(Dir), "run-1.json")
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("stat %s: %v", path, err)
	}
}

func TestAppendThenLoadAllRoundTrip(t *testing.T) {
	root := t.TempDir()
	executor := manifest.Selection{Model: "claude-sonnet-5", Effort: "xhigh"}
	reviewer := manifest.Selection{Model: "claude-opus-4-8", Effort: "high"}
	ev := Event{
		At:                 "2026-07-13T00:00:00Z",
		Verb:               "dispatch",
		IssueNumber:        1,
		Role:               "implementer",
		Executor:           &executor,
		Reviewer:           &reviewer,
		ReviewerDowngraded: false,
		Rationale:          "impl",
	}
	if err := Append(root, "run-1", ev); err != nil {
		t.Fatalf("Append: %v", err)
	}

	docs, err := LoadAll(root)
	if err != nil {
		t.Fatalf("LoadAll: %v", err)
	}
	if len(docs) != 1 {
		t.Fatalf("docs = %+v, want 1", docs)
	}
	doc := docs[0]
	if doc.SchemaVersion != SchemaVersion || doc.RunID != "run-1" {
		t.Errorf("doc = %+v", doc)
	}
	if len(doc.Events) != 1 {
		t.Fatalf("events = %+v, want 1", doc.Events)
	}
	got := doc.Events[0]
	if got.At != ev.At || got.Verb != ev.Verb || got.IssueNumber != ev.IssueNumber || got.Role != ev.Role || got.Rationale != ev.Rationale {
		t.Errorf("event = %+v, want %+v", got, ev)
	}
	if got.Executor == nil || *got.Executor != *ev.Executor {
		t.Errorf("executor = %+v, want %+v", got.Executor, ev.Executor)
	}
	if got.Reviewer == nil || *got.Reviewer != *ev.Reviewer {
		t.Errorf("reviewer = %+v, want %+v", got.Reviewer, ev.Reviewer)
	}
}

func TestAppendTwiceAppendsToOneDocument(t *testing.T) {
	root := t.TempDir()
	if err := Append(root, "run-1", Event{At: "t1", Verb: "dispatch"}); err != nil {
		t.Fatalf("Append 1: %v", err)
	}
	if err := Append(root, "run-1", Event{At: "t2", Verb: "pr-open"}); err != nil {
		t.Fatalf("Append 2: %v", err)
	}

	docs, err := LoadAll(root)
	if err != nil {
		t.Fatalf("LoadAll: %v", err)
	}
	if len(docs) != 1 {
		t.Fatalf("docs = %+v, want 1 document (one file, two events)", docs)
	}
	if len(docs[0].Events) != 2 {
		t.Fatalf("events = %+v, want 2", docs[0].Events)
	}
	if docs[0].Events[0].Verb != "dispatch" || docs[0].Events[1].Verb != "pr-open" {
		t.Errorf("events out of order: %+v", docs[0].Events)
	}
}

func TestAppendFailClosed(t *testing.T) {
	cases := []struct {
		name    string
		setup   func(t *testing.T, root string)
		runID   string
		ev      Event
		wantMsg string
	}{
		{
			name:    "corrupt json",
			setup:   func(t *testing.T, root string) { writeDoc(t, root, "run-1", "{broken") },
			runID:   "run-1",
			ev:      Event{At: "t", Verb: "dispatch"},
			wantMsg: "run-1.json",
		},
		{
			name: "wrong schema version",
			setup: func(t *testing.T, root string) {
				writeDoc(t, root, "run-1", `{"schema_version":99,"run_id":"run-1","events":[]}`)
			},
			runID:   "run-1",
			ev:      Event{At: "t", Verb: "dispatch"},
			wantMsg: "schema_version 99",
		},
		{
			name: "mismatched run id",
			setup: func(t *testing.T, root string) {
				writeDoc(t, root, "run-1", `{"schema_version":1,"run_id":"run-other","events":[]}`)
			},
			runID:   "run-1",
			ev:      Event{At: "t", Verb: "dispatch"},
			wantMsg: "run_id",
		},
		{
			name:    "invalid run id argument",
			setup:   func(t *testing.T, root string) {},
			runID:   "../escape",
			ev:      Event{At: "t", Verb: "dispatch"},
			wantMsg: "invalid run id",
		},
		{
			name:    "negative usage",
			setup:   func(t *testing.T, root string) {},
			runID:   "run-1",
			ev:      Event{At: "t", Verb: "pr-open", Usage: &Usage{InputTokens: -1}},
			wantMsg: "input_tokens",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			tc.setup(t, root)
			err := Append(root, tc.runID, tc.ev)
			if err == nil {
				t.Fatal("Append succeeded, want error")
			}
			if !strings.Contains(err.Error(), tc.wantMsg) {
				t.Errorf("err = %v, want it to mention %q", err, tc.wantMsg)
			}
		})
	}
}

func TestLoadAllMissingDirReturnsEmptyAndCreatesNothing(t *testing.T) {
	root := t.TempDir()
	docs, err := LoadAll(root)
	if err != nil {
		t.Fatalf("LoadAll: %v", err)
	}
	if docs != nil {
		t.Errorf("docs = %+v, want nil", docs)
	}
	if _, err := os.Stat(filepath.Join(root, filepath.FromSlash(Dir))); !os.IsNotExist(err) {
		t.Errorf("metrics dir exists after LoadAll on an empty repo (err = %v), want ErrNotExist", err)
	}
}

func TestLoadAllFailClosedOnCorruptFile(t *testing.T) {
	root := t.TempDir()
	writeDoc(t, root, "run-1", `{"schema_version":1,"run_id":"run-1","unknown_field":true,"events":[]}`)
	_, err := LoadAll(root)
	if err == nil {
		t.Fatal("LoadAll succeeded over an unknown field, want error")
	}
	if !strings.Contains(err.Error(), "run-1.json") {
		t.Errorf("err = %v, want it to name the file", err)
	}
}

func TestUsageValidate(t *testing.T) {
	var nilUsage *Usage
	if err := nilUsage.Validate(); err != nil {
		t.Errorf("nil Usage.Validate() = %v, want nil", err)
	}
	if err := (&Usage{}).Validate(); err != nil {
		t.Errorf("zero Usage.Validate() = %v, want nil", err)
	}
	cases := []Usage{
		{InputTokens: -1},
		{OutputTokens: -1},
		{CacheReadTokens: -1},
		{CacheCreationTokens: -1},
		{DurationMS: -1},
	}
	for _, u := range cases {
		if err := u.Validate(); err == nil {
			t.Errorf("Usage%+v.Validate() = nil, want an error", u)
		}
	}
}
