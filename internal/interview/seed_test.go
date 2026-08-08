package interview

import (
	"context"
	"errors"
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

	if got := facts.Instructions["claude"]; !got.Exists || !got.Conventions || got.BlockOnly {
		t.Errorf("claude = %+v, want a file that exists and carries conventions", got)
	}
	if got := facts.Instructions["codex"]; !got.Exists || got.Conventions || !got.BlockOnly {
		t.Errorf("codex = %+v, want a file that exists holding only the managed block", got)
	}

	bare := Detect(context.Background(), Deps{RepoRoot: t.TempDir(), LookPath: fakeLookPath()})
	for host, got := range bare.Instructions {
		if got.Exists || got.Conventions || got.BlockOnly {
			t.Errorf("%s = %+v, want absent in a bare directory", host, got)
		}
	}
}

// driftedBlock is the current managed block with one body line
// hand-edited: PlanRemove still finds its markers (removal needs only
// those), so it is "block-only" by `orch doctor`'s reading, while
// Inspect classifies it StatusDrifted.
func driftedBlock(t *testing.T) string {
	t.Helper()
	block := managedBlock(t)
	drifted := strings.Replace(block, "This file is partially managed by Orch", "This file is entirely mine", 1)
	if drifted == block {
		t.Fatal("the drift fixture changed nothing; the canonical body must have moved")
	}
	return drifted
}

// newerBlock is a block declaring a version this build cannot render.
func newerBlock(t *testing.T) string {
	t.Helper()
	block := managedBlock(t)
	newer := strings.Replace(block, "version=1", "version=2", 1)
	if newer == block {
		t.Fatal("the newer-version fixture changed nothing; the marker grammar must have moved")
	}
	return newer
}

// TestBlockOnlyIsNarrowerThanNoConventions pins the classification the
// repair offer keys on, as Detect reports it and as isBlockOnly
// re-derives it at plan time. Only a file whose entire content is the
// current managed block qualifies: every state this build cannot fully
// vouch for reports Exists with neither Conventions nor BlockOnly, so
// "carries no conventions" alone can never license replacing a file's
// bytes.
func TestBlockOnlyIsNarrowerThanNoConventions(t *testing.T) {
	block := managedBlock(t)
	cases := []struct {
		name          string
		content       string
		wantBlockOnly bool
	}{
		{name: "the managed block alone", content: block, wantBlockOnly: true},
		{name: "the managed block with surrounding blank lines", content: "\n" + block + "\n\n", wantBlockOnly: true},
		{name: "conventions above the block", content: seedConventions + "\n" + block},
		{name: "conventions below the block", content: block + "\n\n" + seedConventions},
		{name: "a drifted block alone", content: driftedBlock(t)},
		{name: "a newer-versioned block alone", content: newerBlock(t)},
		{name: "malformed markers", content: block + "\n" + block},
		{name: "an empty file", content: ""},
		{name: "whitespace alone", content: "\n\n"},
		{name: "no managed block at all", content: seedConventions},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			writeInstructionFile(t, root, "AGENTS.md", tc.content)

			got := classifyInstructionFile(root, "AGENTS.md")
			if !got.Exists {
				t.Fatalf("Exists = false, want true (the file was written)")
			}
			if got.BlockOnly != tc.wantBlockOnly {
				t.Errorf("Detect BlockOnly = %v, want %v (%+v)", got.BlockOnly, tc.wantBlockOnly, got)
			}
			if isBlockOnly(tc.content) != tc.wantBlockOnly {
				t.Errorf("isBlockOnly = %v, want %v", isBlockOnly(tc.content), tc.wantBlockOnly)
			}
			if got.BlockOnly && got.Conventions {
				t.Error("BlockOnly and Conventions are both set; a block-only file carries no conventions")
			}
		})
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

// TestSeedNotOfferedForStillEnabledConfigureHost proves the CREATE
// offer is scoped to a host this change newly enables: a
// committed-enabled host whose file happens to be missing does not get
// one created behind the human's back — `orch doctor` reports that state
// instead.
//
// Before the repair offer existed that restriction covered seeding as a
// whole: a still-enabled host was never offered anything. After it, a
// still-enabled host IS offered a seed when its file exists holding
// nothing but the managed block (TestConfigureRepairsBlockOnlyFileFromSibling),
// so the restriction this test pins now holds for the create offer
// alone — which is exactly why the fixture leaves AGENTS.md absent
// rather than block-only.
func TestSeedNotOfferedForStillEnabledConfigureHost(t *testing.T) {
	root := t.TempDir()
	writeCommittedConfigLocal(t, root)
	writeInstructionFile(t, root, "CLAUDE.md", seedConventions+"\n"+managedBlock(t))

	overrides := map[string]string{idPickHosts: "yes"}
	if _, asked := walkSeed(t, NextConfigure, withInstructions(configureFacts(), root), root, "yes", overrides); asked {
		t.Error("the seed question was asked for a host that was already committed-enabled")
	}
}

// TestSeedNeverOverwritesAFileWithConventionsOfItsOwn pins planSeeded's
// fail-safe: if the target file exists carrying content of its own — the
// interview's facts and the summary's root having disagreed — the
// ordinary plan runs and the file's own content is preserved, rather
// than being replaced by a copy of the sibling's.
//
// Before the repair offer existed this test was named
// TestSeedNeverOverwritesAnExistingFile, and its comment said an
// existing target file meant the ordinary plan runs and its content is
// preserved — of ANY existing file, with no qualification. That is the
// before. The after is narrower: a file whose entire content is the
// current managed block IS overwritten, by a proposal a human approves
// first (TestPlanSeededOnlySeedsOverAnAbsentOrBlockOnlyFile pins both
// halves of the new contract). So the invariant this test pins holds for
// a file carrying conventions of its own, and the name says that rather
// than restating an invariant seeding no longer has.
func TestSeedNeverOverwritesAFileWithConventionsOfItsOwn(t *testing.T) {
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

// TestPlanSeededOnlySeedsOverAnAbsentOrBlockOnlyFile is the destructive
// half of the repair carve-out, checked at the layer that actually
// composes the bytes: planSeeded seeds over an absent file and over one
// holding nothing but the current managed block, and preserves every
// other file's own content verbatim — including the states Detect also
// refuses (drifted, newer-versioned, malformed), so a fact that somehow
// said "block-only" for one of them still could not destroy it.
func TestPlanSeededOnlySeedsOverAnAbsentOrBlockOnlyFile(t *testing.T) {
	block := managedBlock(t)
	cases := []struct {
		name string
		// content "" writes no AGENTS.md at all.
		content  string
		wantSeed bool
	}{
		{name: "absent", wantSeed: true},
		{name: "the managed block alone", content: block, wantSeed: true},
		{name: "conventions of its own above the block", content: "# Codex only\n\n" + block},
		{name: "conventions of its own and no block", content: "# Codex only\n"},
		{name: "a drifted block alone", content: driftedBlock(t)},
		{name: "a newer-versioned block alone", content: newerBlock(t)},
		{name: "an empty file", content: "\n"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			writeInstructionFile(t, root, "CLAUDE.md", seedConventions+"\n"+block)
			if tc.content != "" {
				writeInstructionFile(t, root, "AGENTS.md", tc.content)
			}
			target := filepath.Join(root, "AGENTS.md")

			ch, err := planSeeded(filepath.Join(root, "CLAUDE.md"), target)
			switch {
			case tc.wantSeed:
				if err != nil {
					t.Fatalf("planSeeded: %v", err)
				}
				if !strings.HasPrefix(ch.New, seedConventions) {
					t.Errorf("New does not open with the sibling's conventions:\n%s", ch.New)
				}
				if ch.FileExisted != (tc.content != "") {
					t.Errorf("FileExisted = %v, want %v — the record the PR body renders must say which", ch.FileExisted, tc.content != "")
				}
			case err != nil:
				// A blocking structural error is a refusal too: the
				// summary names it as a blocker and writes nothing.
				if !isBlockingPlanError(err) {
					t.Fatalf("planSeeded: %v, want a blocking plan error or a preserved file", err)
				}
			default:
				if strings.Contains(ch.New, seedConventions) {
					t.Errorf("New carries the sibling's conventions, overwriting content this build does not own:\n%s", ch.New)
				}
				if !strings.Contains(ch.New, strings.TrimSpace(tc.content)) {
					t.Errorf("New does not preserve the file's own content:\n%s", ch.New)
				}
			}

			// planSeeded proposes; it never writes.
			after, readErr := os.ReadFile(target)
			if tc.content == "" {
				if readErr == nil {
					t.Errorf("AGENTS.md was created on disk by planning alone: %q", after)
				}
				return
			}
			if readErr != nil {
				t.Fatalf("read back AGENTS.md: %v", readErr)
			}
			if string(after) != tc.content {
				t.Errorf("AGENTS.md on disk = %q, want it untouched by planning", after)
			}
		})
	}
}

// TestConfigureRepairsBlockOnlyFileFromSibling is the repair flagship:
// a repository whose CLAUDE.md states its conventions while AGENTS.md
// holds nothing but the Orch managed block gets one offer to repair
// AGENTS.md from CLAUDE.md, the whole resulting file is shown as the
// summary's diff, and nothing is written until that summary is
// approved.
func TestConfigureRepairsBlockOnlyFileFromSibling(t *testing.T) {
	block := managedBlock(t)
	root := t.TempDir()
	writeCommittedConfigLocal(t, root)
	writeInstructionFile(t, root, "CLAUDE.md", seedConventions+"\n"+block)
	writeInstructionFile(t, root, "AGENTS.md", block)

	summary, asked := walkSeed(t, NextConfigure, withInstructions(configureFacts(), root), root, "yes", nil)
	if !asked {
		t.Fatal("the repair question was never asked")
	}

	ch := findFileChange(summary.Files, "AGENTS.md")
	if ch == nil {
		t.Fatalf("Files = %v, want an AGENTS.md entry", summary.Files)
	}
	if !ch.Existed {
		t.Error("Existed = false, want true (AGENTS.md was already there, holding the block)")
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

	// Every line of the file the human is approving is visible in the
	// diff, the block it already held included — not the seeded lines
	// against three lines of borrowed context.
	for _, line := range strings.Split(strings.TrimSuffix(ch.NewContent, "\n"), "\n") {
		if !strings.Contains(ch.Diff, "\n+"+line+"\n") && !strings.HasPrefix(ch.Diff, "+"+line+"\n") {
			t.Errorf("Diff does not show %q as an added line:\n%s", line, ch.Diff)
		}
	}

	// The sibling it was seeded from is itself unchanged, and neither
	// file has been written: the summary is a proposal.
	sibling := findFileChange(summary.Files, "CLAUDE.md")
	if sibling == nil {
		t.Fatalf("Files = %v, want a CLAUDE.md entry", summary.Files)
	}
	if sibling.Diff != "" {
		t.Errorf("CLAUDE.md Diff = %q, want empty (it already carries a current block)", sibling.Diff)
	}
	for name, want := range map[string]string{"AGENTS.md": block, "CLAUDE.md": seedConventions + "\n" + block} {
		got, err := os.ReadFile(filepath.Join(root, name))
		if err != nil {
			t.Fatalf("read back %s: %v", name, err)
		}
		if string(got) != want {
			t.Errorf("%s on disk = %q, want it untouched until the summary is approved", name, got)
		}
	}
}

// TestConfigureRepairDeclinedChangesNothing proves answering no leaves
// the block-only file exactly as it was — the same proposal `orch
// configure` made for it before the repair offer existed.
func TestConfigureRepairDeclinedChangesNothing(t *testing.T) {
	block := managedBlock(t)
	root := t.TempDir()
	writeCommittedConfigLocal(t, root)
	writeInstructionFile(t, root, "CLAUDE.md", seedConventions+"\n"+block)
	writeInstructionFile(t, root, "AGENTS.md", block)

	summary, asked := walkSeed(t, NextConfigure, withInstructions(configureFacts(), root), root, "no", nil)
	if !asked {
		t.Fatal("the repair question was never asked")
	}
	ch := findFileChange(summary.Files, "AGENTS.md")
	if ch == nil {
		t.Fatalf("Files = %v, want an AGENTS.md entry", summary.Files)
	}
	if ch.NewContent != block || ch.Diff != "" {
		t.Errorf("NewContent/Diff = %q/%q, want the block alone and no diff", ch.NewContent, ch.Diff)
	}
}

// TestConfigureRepairNotOffered walks every condition under which the
// repair offer must stay silent. The golden configure transcripts pin
// the "no facts at all" case by running unmodified.
func TestConfigureRepairNotOffered(t *testing.T) {
	block := managedBlock(t)
	cases := []struct {
		name  string
		files map[string]string
	}{
		{"the block-only file carries conventions of its own", map[string]string{
			"CLAUDE.md": seedConventions + "\n" + block,
			"AGENTS.md": "# Codex only\n\n" + block,
		}},
		{"the sibling holds only the managed block too", map[string]string{
			"CLAUDE.md": block,
			"AGENTS.md": block,
		}},
		{"the sibling is absent", map[string]string{"AGENTS.md": block}},
		{"the block-only file's block has drifted", map[string]string{
			"CLAUDE.md": seedConventions + "\n" + block,
			"AGENTS.md": driftedBlock(t),
		}},
		{"the block-only file's block is newer than this build", map[string]string{
			"CLAUDE.md": seedConventions + "\n" + block,
			"AGENTS.md": newerBlock(t),
		}},
		{"the block-only file's markers are malformed", map[string]string{
			"CLAUDE.md": seedConventions + "\n" + block,
			"AGENTS.md": block + "\n" + block,
		}},
		{"the file holds no managed block at all", map[string]string{
			"CLAUDE.md": seedConventions + "\n" + block,
			"AGENTS.md": "",
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			writeCommittedConfigLocal(t, root)
			for name, content := range tc.files {
				writeInstructionFile(t, root, name, content)
			}
			if _, asked := walkSeed(t, NextConfigure, withInstructions(configureFacts(), root), root, "yes", nil); asked {
				t.Error("the repair question was asked")
			}
		})
	}
}

// TestRepairNotOfferedByInit pins the offer to `orch configure`: init
// runs in an uninitialized repository, and a block-only file there is
// left exactly as init has always left it.
func TestRepairNotOfferedByInit(t *testing.T) {
	block := managedBlock(t)
	root := t.TempDir()
	writeInstructionFile(t, root, "CLAUDE.md", seedConventions+"\n"+block)
	writeInstructionFile(t, root, "AGENTS.md", block)

	summary, asked := walkSeed(t, Next, withInstructions(bothHostsFacts(), root), root, "yes", nil)
	if asked {
		t.Error("init asked a seed question for an existing block-only file")
	}
	ch := findFileChange(summary.Files, "AGENTS.md")
	if ch == nil {
		t.Fatalf("Files = %v, want an AGENTS.md entry", summary.Files)
	}
	if ch.Diff != "" || ch.NewContent != block {
		t.Errorf("NewContent/Diff = %q/%q, want the existing block untouched", ch.NewContent, ch.Diff)
	}
}

// TestConfigureRepairStaleYesRejectedOnceTheFileHasConventions is the
// repair case's leg of the anti-forgery boundary PR #192 established: a
// yes answer replayed after the file stopped being block-only is an
// answer to a question the sequence no longer derives, and fails closed
// like every other unreachable id.
func TestConfigureRepairStaleYesRejectedOnceTheFileHasConventions(t *testing.T) {
	block := managedBlock(t)
	root := t.TempDir()
	writeCommittedConfigLocal(t, root)
	writeInstructionFile(t, root, "CLAUDE.md", seedConventions+"\n"+block)
	writeInstructionFile(t, root, "AGENTS.md", block)

	answers := map[string]string{
		idPickHosts:       "no",
		idPickRolesClaude: "no",
		idPickRolesCodex:  "no",
		idPickSettings:    "no",
		seedID("codex"):   "yes",
	}
	if _, err := NextConfigure(withInstructions(configureFacts(), root), answers, root); err != nil {
		t.Fatalf("NextConfigure with the offer still applicable: %v", err)
	}

	writeInstructionFile(t, root, "AGENTS.md", "# Codex only\n\n"+block)
	_, err := NextConfigure(withInstructions(configureFacts(), root), answers, root)
	if !errors.Is(err, ErrUnknownAnswer) {
		t.Fatalf("NextConfigure err = %v, want ErrUnknownAnswer", err)
	}
}

// TestConfigureRepairNeverOverwritesContentWrittenAfterTheAnswer is the
// same race the other way round: the facts still say block-only (they
// were snapshotted before), the answer still says yes, and the file has
// since been given conventions of its own. planSeeded re-derives the
// classification from disk, so the summary proposes the file's own
// content untouched rather than a copy of the sibling's.
func TestConfigureRepairNeverOverwritesContentWrittenAfterTheAnswer(t *testing.T) {
	block := managedBlock(t)
	root := t.TempDir()
	writeCommittedConfigLocal(t, root)
	writeInstructionFile(t, root, "CLAUDE.md", seedConventions+"\n"+block)
	writeInstructionFile(t, root, "AGENTS.md", block)
	staleFacts := withInstructions(configureFacts(), root)

	const own = "# Codex only\n\nUse tabs.\n"
	writeInstructionFile(t, root, "AGENTS.md", own+"\n"+block)

	answers := map[string]string{
		idPickHosts:       "no",
		idPickRolesClaude: "no",
		idPickRolesCodex:  "no",
		idPickSettings:    "no",
		seedID("codex"):   "yes",
	}
	doc, err := NextConfigure(staleFacts, answers, root)
	if err != nil {
		t.Fatalf("NextConfigure: %v", err)
	}
	if doc.Summary == nil {
		t.Fatalf("document %q carries no summary", doc.Kind)
	}
	ch := findFileChange(doc.Summary.Files, "AGENTS.md")
	if ch == nil {
		t.Fatalf("Files = %v, want an AGENTS.md entry", doc.Summary.Files)
	}
	if !strings.HasPrefix(ch.NewContent, own) {
		t.Errorf("NewContent does not open with AGENTS.md's own content:\n%s", ch.NewContent)
	}
	if strings.Contains(ch.NewContent, seedConventions) {
		t.Errorf("NewContent carries the sibling's conventions, overwriting content written since the answer:\n%s", ch.NewContent)
	}
	if ch.Diff != "" {
		t.Errorf("Diff = %q, want empty (the file already carries a current block)", ch.Diff)
	}
}
