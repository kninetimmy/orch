package interview

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kninetimmy/orch/internal/instructions"
	"github.com/kninetimmy/orch/internal/question"
)

// seedConventions is a fixture repository's conventions: the content
// that lives outside a root instruction file's managed block and that a
// newly created sibling file would otherwise be missing entirely.
const seedConventions = "# Fixture\n\nRun `go test ./...` before every push.\n"

// managedBlock is the current Orch managed block, rendered exactly as
// instructions.Plan installs it.
func managedBlock(t *testing.T) string {
	t.Helper()
	block, err := instructions.Render(instructions.CurrentVersion)
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	return block
}

// writeInstructionFile writes name into root.
func writeInstructionFile(t *testing.T, root, name, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(root, name), []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", name, err)
	}
}

// withInstructions classifies root's two root instruction files onto
// facts the way Detect does, so a walk over a fixture directory sees
// the same instruction facts a real run would.
func withInstructions(facts Facts, root string) Facts {
	facts.Instructions = map[string]InstructionFileState{
		"claude": classifyInstructionFile(root, InstructionFile("claude")),
		"codex":  classifyInstructionFile(root, InstructionFile("codex")),
	}
	return facts
}

// checkSeedQuestion asserts q is a well-formed seed question: it passes
// the shared spec check, names both files in its prompt, and defaults
// to yes.
func checkSeedQuestion(t *testing.T, q question.Question, file, sibling string) {
	t.Helper()
	if err := question.SpecCheck(q); err != nil {
		t.Errorf("SpecCheck(%s): %v", q.ID, err)
	}
	if q.Default != "yes" {
		t.Errorf("%s: Default = %q, want %q", q.ID, q.Default, "yes")
	}
	if !strings.Contains(q.Prompt, file) || !strings.Contains(q.Prompt, sibling) {
		t.Errorf("%s: Prompt = %q, want it to name both %s and %s", q.ID, q.Prompt, file, sibling)
	}
}

// walkSeed drives next (Next or NextConfigure) from an empty answer set
// to its first non-questions document, answering every question with
// its Default unless overrides names it, and answering any seed
// question with seedAnswer. It returns that document's summary and
// whether a seed question was asked at all.
func walkSeed(t *testing.T, next func(Facts, map[string]string, string) (question.Document, error), facts Facts, root, seedAnswer string, overrides map[string]string) (question.Summary, bool) {
	t.Helper()
	answers := map[string]string{}
	asked := false
	for step := 1; step <= 100; step++ {
		doc, err := next(facts, answers, root)
		if err != nil {
			t.Fatalf("step %d: %v", step, err)
		}
		if doc.Kind != question.DocQuestions {
			if doc.Summary == nil {
				t.Fatalf("step %d: document %q carries no summary", step, doc.Kind)
			}
			return *doc.Summary, asked
		}
		for _, q := range doc.Questions {
			switch {
			case q.ID == seedID("claude"):
				asked = true
				checkSeedQuestion(t, q, InstructionFile("claude"), InstructionFile("codex"))
				answers[q.ID] = seedAnswer
			case q.ID == seedID("codex"):
				asked = true
				checkSeedQuestion(t, q, InstructionFile("codex"), InstructionFile("claude"))
				answers[q.ID] = seedAnswer
			default:
				if v, ok := overrides[q.ID]; ok {
					answers[q.ID] = v
					continue
				}
				if q.Default == "" {
					t.Fatalf("step %d: question %s has no default", step, q.ID)
				}
				answers[q.ID] = q.Default
			}
		}
	}
	t.Fatal("the interview did not reach a summary within 100 steps")
	return question.Summary{}, false
}

// TestDetectClassifiesInstructionFiles proves Detect reports the three
// states the seed question keys on — absent, block-only, and carrying
// conventions — reading them from the repository root it was pointed
// at (git undetected here, so the fallback to Deps.RepoRoot applies).
func TestDetectClassifiesInstructionFiles(t *testing.T) {
	block := managedBlock(t)
	root := t.TempDir()
	writeInstructionFile(t, root, "CLAUDE.md", seedConventions+"\n"+block)
	writeInstructionFile(t, root, "AGENTS.md", block)

	facts := Detect(context.Background(), Deps{RepoRoot: root, LookPath: fakeLookPath()})

	if got := facts.Instructions["claude"]; !got.Exists || !got.Conventions {
		t.Errorf("claude = %+v, want a file that exists and carries conventions", got)
	}
	if got := facts.Instructions["codex"]; !got.Exists || got.Conventions {
		t.Errorf("codex = %+v, want a file that exists with no conventions", got)
	}

	bare := Detect(context.Background(), Deps{RepoRoot: t.TempDir(), LookPath: fakeLookPath()})
	for host, got := range bare.Instructions {
		if got.Exists || got.Conventions {
			t.Errorf("%s = %+v, want absent in a bare directory", host, got)
		}
	}
}

// TestSeedAcceptedCarriesSiblingConventions is the flagship walk: a
// repository whose conventions live in CLAUDE.md enables both hosts,
// and the new AGENTS.md is proposed as those conventions followed by a
// single fresh managed block.
func TestSeedAcceptedCarriesSiblingConventions(t *testing.T) {
	root := t.TempDir()
	writeInstructionFile(t, root, "CLAUDE.md", seedConventions+"\n"+managedBlock(t))

	summary, asked := walkSeed(t, Next, withInstructions(bothHostsFacts(), root), root, "yes", nil)
	if !asked {
		t.Fatal("the seed question was never asked")
	}

	ch := findFileChange(summary.Files, "AGENTS.md")
	if ch == nil {
		t.Fatalf("Files = %v, want an AGENTS.md entry", summary.Files)
	}
	if ch.Existed {
		t.Error("Existed = true, want false (AGENTS.md did not exist)")
	}
	if !strings.HasPrefix(ch.NewContent, seedConventions) {
		t.Errorf("NewContent does not open with CLAUDE.md's conventions:\n%s", ch.NewContent)
	}
	if n := strings.Count(ch.NewContent, "orchestrator:managed:start"); n != 1 {
		t.Errorf("NewContent carries %d managed blocks, want exactly 1:\n%s", n, ch.NewContent)
	}
	if report := instructions.Inspect(ch.NewContent); report.Status != instructions.StatusCurrent {
		t.Errorf("Inspect(NewContent).Status = %v, want StatusCurrent (%v): %s", report.Status, instructions.StatusCurrent, report.Detail)
	}

	// Every line of the proposed file is visible as an addition in the
	// diff a human approves, seeded lines included.
	for _, line := range strings.Split(strings.TrimSuffix(ch.NewContent, "\n"), "\n") {
		if !strings.Contains(ch.Diff, "\n+"+line+"\n") && !strings.HasPrefix(ch.Diff, "+"+line+"\n") {
			t.Errorf("Diff does not show %q as an added line:\n%s", line, ch.Diff)
		}
	}

	// The sibling it was seeded from is itself unchanged.
	sibling := findFileChange(summary.Files, "CLAUDE.md")
	if sibling == nil {
		t.Fatalf("Files = %v, want a CLAUDE.md entry", summary.Files)
	}
	if sibling.Diff != "" {
		t.Errorf("CLAUDE.md Diff = %q, want empty (it already carries a current block)", sibling.Diff)
	}
}

// TestSeedDeclinedKeepsBlockOnlyProposal proves answering no leaves the
// proposal exactly what it was before seeding existed.
func TestSeedDeclinedKeepsBlockOnlyProposal(t *testing.T) {
	root := t.TempDir()
	writeInstructionFile(t, root, "CLAUDE.md", seedConventions+"\n"+managedBlock(t))

	summary, asked := walkSeed(t, Next, withInstructions(bothHostsFacts(), root), root, "no", nil)
	if !asked {
		t.Fatal("the seed question was never asked")
	}
	ch := findFileChange(summary.Files, "AGENTS.md")
	if ch == nil {
		t.Fatalf("Files = %v, want an AGENTS.md entry", summary.Files)
	}
	if ch.NewContent != managedBlock(t) {
		t.Errorf("NewContent = %q, want the managed block alone", ch.NewContent)
	}
}

// TestSeedNotOffered walks every condition under which the question
// must stay silent. The golden transcripts pin the fourth (a bare
// repository) by running unmodified.
func TestSeedNotOffered(t *testing.T) {
	block := managedBlock(t)
	cases := []struct {
		name  string
		files map[string]string
	}{
		{"sibling absent", nil},
		{"sibling holds only the managed block", map[string]string{"CLAUDE.md": block}},
		{"the host's own file already exists", map[string]string{
			"CLAUDE.md": seedConventions + "\n" + block,
			"AGENTS.md": "# Codex\n",
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			for name, content := range tc.files {
				writeInstructionFile(t, root, name, content)
			}
			if _, asked := walkSeed(t, Next, withInstructions(bothHostsFacts(), root), root, "yes", nil); asked {
				t.Error("the seed question was asked")
			}
		})
	}
}

// TestSeedOfferedForConfigureNewlyEnabledHost proves an `orch
// configure` change that newly enables Codex gets the same offer, and
// that the newly created AGENTS.md carries CLAUDE.md's conventions.
func TestSeedOfferedForConfigureNewlyEnabledHost(t *testing.T) {
	root := t.TempDir()
	writeCommittedConfigClaudeOnly(t, root)
	writeInstructionFile(t, root, "CLAUDE.md", seedConventions+"\n"+managedBlock(t))

	overrides := map[string]string{idPickHosts: "yes", idHostCodexEnabled: "yes"}
	summary, asked := walkSeed(t, NextConfigure, withInstructions(configureFacts(), root), root, "yes", overrides)
	if !asked {
		t.Fatal("the seed question was never asked")
	}
	ch := findFileChange(summary.Files, "AGENTS.md")
	if ch == nil {
		t.Fatalf("Files = %v, want an AGENTS.md entry", summary.Files)
	}
	if !strings.HasPrefix(ch.NewContent, seedConventions) {
		t.Errorf("NewContent does not open with CLAUDE.md's conventions:\n%s", ch.NewContent)
	}
	if report := instructions.Inspect(ch.NewContent); report.Status != instructions.StatusCurrent {
		t.Errorf("Inspect(NewContent).Status = %v, want StatusCurrent (%v): %s", report.Status, instructions.StatusCurrent, report.Detail)
	}
}

// TestSeedNotOfferedForStillEnabledConfigureHost proves the offer is
// scoped to a host this change newly enables: a committed-enabled host
// whose file happens to be missing is not seeded behind the human's
// back — `orch doctor` reports that state instead.
func TestSeedNotOfferedForStillEnabledConfigureHost(t *testing.T) {
	root := t.TempDir()
	writeCommittedConfigLocal(t, root)
	writeInstructionFile(t, root, "CLAUDE.md", seedConventions+"\n"+managedBlock(t))

	overrides := map[string]string{idPickHosts: "yes"}
	if _, asked := walkSeed(t, NextConfigure, withInstructions(configureFacts(), root), root, "yes", overrides); asked {
		t.Error("the seed question was asked for a host that was already committed-enabled")
	}
}

// TestSeedNeverOverwritesAnExistingFile pins planSeeded's fail-safe: if
// the target file exists after all — the interview's facts and the
// summary's root having disagreed — the ordinary plan runs and the
// file's own content is preserved, rather than being replaced by a copy
// of the sibling's.
func TestSeedNeverOverwritesAnExistingFile(t *testing.T) {
	root := t.TempDir()
	writeInstructionFile(t, root, "CLAUDE.md", seedConventions+"\n"+managedBlock(t))
	writeInstructionFile(t, root, "AGENTS.md", "# Codex only\n")

	ch, err := planSeeded(filepath.Join(root, "CLAUDE.md"), filepath.Join(root, "AGENTS.md"))
	if err != nil {
		t.Fatalf("planSeeded: %v", err)
	}
	if !strings.HasPrefix(ch.New, "# Codex only\n") {
		t.Errorf("New = %q, want AGENTS.md's own content preserved", ch.New)
	}
	if strings.Contains(ch.New, seedConventions) {
		t.Errorf("New carries the sibling's conventions, overwriting the existing file:\n%s", ch.New)
	}
}
