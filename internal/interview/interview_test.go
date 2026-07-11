package interview

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kninetimmy/orch/internal/instructions"
	"github.com/kninetimmy/orch/internal/question"
)

// update regenerates the golden files instead of comparing against
// them (instructions.Render's -update convention).
var update = flag.Bool("update", false, "regenerate golden files")

// checkGoldenBytes compares got against the golden file at path,
// rewriting it first when -update is passed.
func checkGoldenBytes(t *testing.T, path string, got []byte) {
	t.Helper()
	if *update {
		if err := os.WriteFile(path, got, 0o644); err != nil {
			t.Fatalf("write golden: %v", err)
		}
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read golden (run `go test ./internal/interview -update`): %v", err)
	}
	if string(got) != string(want) {
		t.Errorf("does not match %s\n--- got ---\n%s\n--- want ---\n%s", path, got, want)
	}
}

// checkGoldenDocument marshals doc the same way cli's JSON verbs do
// (json.MarshalIndent, two-space indent) and compares it against the
// golden file at path.
func checkGoldenDocument(t *testing.T, path string, doc question.Document) {
	t.Helper()
	data, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		t.Fatalf("MarshalIndent: %v", err)
	}
	checkGoldenBytes(t, path, append(data, '\n'))
}

// TestGoldenTranscriptBothHosts is the flagship walk: from an empty
// answer set, with both host CLIs and a healthy memhub detected,
// answer every question with its Default through repeated Next calls.
// Each emitted Document must byte-equal its numbered golden transcript
// file, and the final materialized configuration must byte-equal
// config_both_hosts.golden.toml.
func TestGoldenTranscriptBothHosts(t *testing.T) {
	facts := bothHostsFacts()
	root := t.TempDir()
	answers := map[string]string{}

	for step := 1; step <= 100; step++ {
		doc, err := Next(facts, answers, root)
		if err != nil {
			t.Fatalf("step %d: Next: %v", step, err)
		}
		path := filepath.Join("testdata", "transcript", fmt.Sprintf("step_%02d.json", step))
		checkGoldenDocument(t, path, doc)

		switch doc.Kind {
		case question.DocQuestions:
			for _, q := range doc.Questions {
				if q.Default == "" {
					t.Fatalf("step %d: question %s has no default", step, q.ID)
				}
				answers[q.ID] = q.Default
			}
		case question.DocSummary:
			if len(doc.Summary.Blockers) != 0 {
				t.Fatalf("step %d: unexpected blockers: %v", step, doc.Summary.Blockers)
			}
			if len(doc.Questions) != 1 || doc.Questions[0].ID != idApproval {
				t.Fatalf("step %d: summary document did not embed the approval question: %+v", step, doc.Questions)
			}
			answers[idApproval] = "approve"
		case question.DocComplete:
			checkGoldenBytes(t, filepath.Join("testdata", "config_both_hosts.golden.toml"), []byte(doc.Complete.Summary.ConfigTOML))
			if !doc.Complete.BootstrapReady {
				t.Errorf("BootstrapReady = false, want true (git and gh were both detected)")
			}
			return
		default:
			t.Fatalf("step %d: unexpected document kind %q", step, doc.Kind)
		}
	}
	t.Fatal("transcript did not reach a complete document within 100 steps")
}

// TestGoldenTranscriptAbort re-walks the same both-hosts transcript up
// to the summary document, then aborts instead of approving.
func TestGoldenTranscriptAbort(t *testing.T) {
	facts := bothHostsFacts()
	root := t.TempDir()
	answers := answerAllWithDefaults(t, facts, root)

	answers[idApproval] = "abort"
	doc, err := Next(facts, answers, root)
	if err != nil {
		t.Fatalf("Next: %v", err)
	}
	if doc.Kind != question.DocAborted {
		t.Fatalf("Kind = %q, want %q", doc.Kind, question.DocAborted)
	}
}

func TestNextClaudeOnly(t *testing.T) {
	facts := Facts{ClaudeCLI: true, Git: true, GitRoot: "/repo", Gh: true}
	root := t.TempDir()
	answers := answerAllWithDefaults(t, facts, root)

	doc, err := Next(facts, answers, root)
	if err != nil {
		t.Fatalf("Next: %v", err)
	}
	if doc.Kind != question.DocSummary {
		t.Fatalf("Kind = %q, want %q", doc.Kind, question.DocSummary)
	}
	if len(doc.Summary.Files) != 1 || doc.Summary.Files[0].Path != "CLAUDE.md" {
		t.Errorf("Files = %v, want exactly [CLAUDE.md]", doc.Summary.Files)
	}
	if strings.Contains(doc.Summary.ConfigTOML, "hosts.codex") {
		t.Error("ConfigTOML mentions hosts.codex for a claude-only interview")
	}
	for id := range answers {
		if strings.Contains(id, "host.codex.role") {
			t.Errorf("answers carries a codex role id %q for a claude-only interview", id)
		}
	}
}

func TestNextCodexOnly(t *testing.T) {
	facts := Facts{CodexCLI: true, Git: true, GitRoot: "/repo", Gh: true}
	root := t.TempDir()
	answers := answerAllWithDefaults(t, facts, root)

	doc, err := Next(facts, answers, root)
	if err != nil {
		t.Fatalf("Next: %v", err)
	}
	if doc.Kind != question.DocSummary {
		t.Fatalf("Kind = %q, want %q", doc.Kind, question.DocSummary)
	}
	if len(doc.Summary.Files) != 1 || doc.Summary.Files[0].Path != "AGENTS.md" {
		t.Errorf("Files = %v, want exactly [AGENTS.md]", doc.Summary.Files)
	}
}

func TestNextBothHostsDisabledIsAnError(t *testing.T) {
	facts := Facts{ClaudeCLI: true, CodexCLI: true}
	answers := map[string]string{idHostClaudeEnabled: "no", idHostCodexEnabled: "no"}
	_, err := Next(facts, answers, t.TempDir())
	if !errors.Is(err, ErrNoHostEnabled) {
		t.Fatalf("Next err = %v, want ErrNoHostEnabled", err)
	}
}

func TestNextUnknownAnswerRejected(t *testing.T) {
	facts := bothHostsFacts()
	answers := map[string]string{"bogus.key": "x"}
	_, err := Next(facts, answers, t.TempDir())
	if !errors.Is(err, ErrUnknownAnswer) {
		t.Fatalf("Next err = %v, want ErrUnknownAnswer", err)
	}
}

func TestNextNotYetApplicableAnswerRejected(t *testing.T) {
	facts := bothHostsFacts()
	// host.claude.enabled has not been answered yet, so a claude role
	// answer is not yet reachable.
	answers := map[string]string{roleModelID("claude", "architect"): "claude-opus-4-8"}
	_, err := Next(facts, answers, t.TempDir())
	if !errors.Is(err, ErrUnknownAnswer) {
		t.Fatalf("Next err = %v, want ErrUnknownAnswer", err)
	}
}

func TestNextCodexRoleAnswerWhileCodexDisabledRejected(t *testing.T) {
	facts := bothHostsFacts()
	answers := map[string]string{
		idHostClaudeEnabled:               "yes",
		idHostCodexEnabled:                "no",
		roleModelID("codex", "architect"): "gpt-5.6-sol",
	}
	_, err := Next(facts, answers, t.TempDir())
	if !errors.Is(err, ErrUnknownAnswer) {
		t.Fatalf("Next err = %v, want ErrUnknownAnswer", err)
	}
}

func TestNextFreeTextModelLandsInSummaryTOML(t *testing.T) {
	facts := bothHostsFacts()
	root := t.TempDir()
	answers := answerAllWithDefaults(t, facts, root)
	answers[roleModelID("claude", "architect")] = "claude-fable-5"

	doc, err := Next(facts, answers, root)
	if err != nil {
		t.Fatalf("Next: %v", err)
	}
	if doc.Kind != question.DocSummary {
		t.Fatalf("Kind = %q, want %q", doc.Kind, question.DocSummary)
	}
	if !strings.Contains(doc.Summary.ConfigTOML, "claude-fable-5") {
		t.Errorf("ConfigTOML does not carry the free-text model:\n%s", doc.Summary.ConfigTOML)
	}
}

func TestNextConcurrencyValidation(t *testing.T) {
	tests := []struct {
		value   string
		wantErr bool
	}{
		{value: "7"},
		{value: "0", wantErr: true},
		{value: "x", wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.value, func(t *testing.T) {
			facts := bothHostsFacts()
			root := t.TempDir()
			answers := answerAllWithDefaults(t, facts, root)
			answers[idMaxSubagents] = tt.value

			doc, err := Next(facts, answers, root)
			if tt.wantErr {
				if err == nil {
					t.Fatal("Next succeeded, want error")
				}
				return
			}
			if err != nil {
				t.Fatalf("Next: %v", err)
			}
			if !strings.Contains(doc.Summary.ConfigTOML, "max_subagents = 7") {
				t.Errorf("ConfigTOML does not carry max_subagents = 7:\n%s", doc.Summary.ConfigTOML)
			}
		})
	}
}

func TestNextApprovalWhileBlockedIsAnError(t *testing.T) {
	facts := bothHostsFacts()
	root := t.TempDir()
	block, err := instructions.Render(instructions.CurrentVersion)
	if err != nil {
		t.Fatal(err)
	}
	drifted := strings.Replace(block, "This file", "THIS FILE", 1)
	if err := os.WriteFile(filepath.Join(root, "CLAUDE.md"), []byte(drifted), 0o644); err != nil {
		t.Fatal(err)
	}

	answers := answerAllWithDefaults(t, facts, root)
	doc, err := Next(facts, answers, root)
	if err != nil {
		t.Fatalf("Next: %v", err)
	}
	if doc.Kind != question.DocSummary || len(doc.Summary.Blockers) == 0 {
		t.Fatalf("expected a blocked summary document, got %+v", doc)
	}
	if len(doc.Questions) != 0 {
		t.Errorf("Questions = %v, want none while blocked (approval withheld)", doc.Questions)
	}

	answers[idApproval] = "approve"
	_, err = Next(facts, answers, root)
	if !errors.Is(err, ErrApprovalBlocked) {
		t.Fatalf("Next err = %v, want ErrApprovalBlocked", err)
	}
}

// TestStatelessnessReAsksExactlyOneQuestion asserts the revision UX:
// deleting any single non-toggle answer from a fully answered
// transcript makes Next re-ask exactly the document that answer
// belongs to (grouped with its still-answered siblings), never an
// error. Host-toggle keys are excluded from this sweep on purpose:
// deleting one invalidates every dependent role answer at once (a
// different, intentionally-error-producing scenario — see
// TestNextCodexRoleAnswerWhileCodexDisabledRejected), so it is not a
// "delete one answer, get exactly that question back" case.
func TestStatelessnessReAsksExactlyOneQuestion(t *testing.T) {
	facts := bothHostsFacts()
	root := t.TempDir()
	full := answerAllWithDefaults(t, facts, root)

	for id := range full {
		if id == idHostClaudeEnabled || id == idHostCodexEnabled {
			continue
		}
		t.Run(id, func(t *testing.T) {
			answers := map[string]string{}
			for k, v := range full {
				if k != id {
					answers[k] = v
				}
			}
			doc, err := Next(facts, answers, root)
			if err != nil {
				t.Fatalf("Next: %v", err)
			}
			if doc.Kind != question.DocQuestions {
				t.Fatalf("Kind = %q, want %q", doc.Kind, question.DocQuestions)
			}
			found := false
			for _, q := range doc.Questions {
				if q.ID == id {
					found = true
				}
			}
			if !found {
				t.Errorf("re-asked document %v does not include the deleted question %q", doc.Questions, id)
			}
		})
	}
}
