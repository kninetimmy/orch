package interview

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kninetimmy/orch/internal/config"
	"github.com/kninetimmy/orch/internal/instructions"
	"github.com/kninetimmy/orch/internal/question"
)

// configureFacts is the Facts fixture `orch configure` tests start
// from: both host CLIs, git, and gh detected — the bootstrap-readiness
// inputs NextConfigure's terminal Complete document carries forward
// (buildComplete is reused verbatim from init). GitRoot is the same
// fixed "/repo" literal bothHostsFacts uses, decoupled from the real
// t.TempDir() repoRoot every caller passes to NextConfigure separately
// — Detection's git_root would otherwise embed a fresh, non-reproducible
// temp path into every golden-compared Complete document.
func configureFacts() Facts {
	return Facts{ClaudeCLI: true, CodexCLI: true, Git: true, GitRoot: "/repo", Gh: true}
}

// writeCommittedConfigClaudeOnly writes a valid claude-only committed
// configuration to root/.orchestrator/config.toml — the fixture the
// "enable codex from claude-only" scenario needs.
func writeCommittedConfigClaudeOnly(t *testing.T, root string) *config.Config {
	t.Helper()
	cfg := &config.Config{
		SchemaVersion: 1,
		Concurrency:   config.Concurrency{MaxSubagents: 3},
		Merge:         config.Merge{Strategy: "squash"},
		Memhub:        config.Memhub{Mode: "off"},
		Metrics:       config.Metrics{Enabled: false},
		Hosts: config.Hosts{
			Claude: &config.Host{Roles: config.Roles{
				Architect:       config.RoleProfile{Model: "claude-opus-4-8", Effort: "xhigh"},
				Scout:           config.RoleProfile{Model: "claude-sonnet-5", Effort: "low"},
				Implementer:     config.RoleProfile{Model: "claude-sonnet-5", Effort: "xhigh"},
				Specialist:      config.RoleProfile{Model: "claude-opus-4-8", Effort: "high"},
				Reviewer:        config.RoleProfile{Model: "claude-opus-4-8", Effort: "high"},
				ReviewDowngrade: config.RoleProfile{Model: "claude-sonnet-5", Effort: "high"},
			}},
		},
	}
	rev, err := config.Revision(cfg)
	if err != nil {
		t.Fatal(err)
	}
	cfg.ConfigRevision = rev

	data, err := config.Render(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, ".orchestrator"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, filepath.FromSlash(config.Path)), data, 0o644); err != nil {
		t.Fatal(err)
	}
	return cfg
}

// writeInstalledBlock writes the current canonical managed block to
// root/name, so a PlanRemoveFile against it has an actual region to
// remove (rather than a no-op against an absent file).
func writeInstalledBlock(t *testing.T, root, name string) {
	t.Helper()
	block, err := instructions.Render(instructions.CurrentVersion)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, name), []byte(block), 0o644); err != nil {
		t.Fatal(err)
	}
}

// writeNoMissingGitignore writes root/.gitignore with every line
// baseGitignoreLines wants already present, so gitignoreLines proposes
// nothing missing — the third precondition (alongside unchanged config
// bytes and unchanged instruction files) for the no-change blocker to
// ever fire.
func writeNoMissingGitignore(t *testing.T, root string) {
	t.Helper()
	content := strings.Join(baseGitignoreLines, "\n") + "\n"
	if err := os.WriteFile(filepath.Join(root, ".gitignore"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// findFileChange returns the entry of files whose Path equals name, or
// nil if none matches.
func findFileChange(files []question.FileChange, name string) *question.FileChange {
	for i := range files {
		if files[i].Path == name {
			return &files[i]
		}
	}
	return nil
}

// answerAllConfigureWithDefaults walks NextConfigure from an empty
// answer set, answering every "questions"-kind document with each
// question's Default, and returns the accumulated answers once a
// non-questions document is reached.
func answerAllConfigureWithDefaults(t *testing.T, facts Facts, root string) map[string]string {
	t.Helper()
	answers := map[string]string{}
	for i := 0; i < 100; i++ {
		doc, err := NextConfigure(facts, answers, root)
		if err != nil {
			t.Fatalf("NextConfigure: %v", err)
		}
		if doc.Kind != question.DocQuestions {
			return answers
		}
		for _, q := range doc.Questions {
			if q.Default == "" {
				t.Fatalf("question %s has no default to answer with", q.ID)
			}
			answers[q.ID] = q.Default
		}
	}
	t.Fatal("NextConfigure did not reach a non-questions document within 100 steps")
	return nil
}

// walkConfigure drives NextConfigure from an empty answer set to its
// terminal document, answering every question with its Default unless
// overrides names a specific answer for that question id, and
// approving the summary once reached. Each step's document is checked
// against a golden file under testdata/transcript_configure/<name>/.
func walkConfigure(t *testing.T, facts Facts, root, name string, overrides map[string]string) question.Document {
	t.Helper()
	answers := map[string]string{}
	for step := 1; step <= 100; step++ {
		doc, err := NextConfigure(facts, answers, root)
		if err != nil {
			t.Fatalf("%s step %d: NextConfigure: %v", name, step, err)
		}
		path := filepath.Join("testdata", "transcript_configure", name, fmt.Sprintf("step_%02d.json", step))
		checkGoldenDocument(t, path, doc)

		switch doc.Kind {
		case question.DocQuestions:
			for _, q := range doc.Questions {
				if v, ok := overrides[q.ID]; ok {
					answers[q.ID] = v
					continue
				}
				if q.Default == "" {
					t.Fatalf("%s step %d: question %s has no default", name, step, q.ID)
				}
				answers[q.ID] = q.Default
			}
		case question.DocSummary:
			answers[idApproval] = "approve"
		case question.DocComplete, question.DocAborted:
			return doc
		default:
			t.Fatalf("%s step %d: unexpected document kind %q", name, step, doc.Kind)
		}
	}
	t.Fatalf("%s: NextConfigure did not reach a terminal document within 100 steps", name)
	return question.Document{}
}

// TestGoldenTranscriptConfigureMergeAndModel changes the merge
// strategy and one claude role's model in the same session, proving
// both a settings-only and a role-only edit land in the same
// materialized configuration and that ConfigDiff shows the change.
func TestGoldenTranscriptConfigureMergeAndModel(t *testing.T) {
	root := t.TempDir()
	writeCommittedConfigLocal(t, root)
	writeInstalledBlock(t, root, "CLAUDE.md")
	writeInstalledBlock(t, root, "AGENTS.md")
	facts := configureFacts()

	overrides := map[string]string{
		idPickRolesClaude:              "yes",
		idPickSettings:                 "yes",
		roleModelID("claude", "scout"): "claude-opus-4-8",
		idMergeStrategy:                "rebase",
	}
	doc := walkConfigure(t, facts, root, "merge_and_model", overrides)
	if doc.Kind != question.DocComplete {
		t.Fatalf("Kind = %q, want %q", doc.Kind, question.DocComplete)
	}
	if !doc.Complete.BootstrapReady {
		t.Error("BootstrapReady = false, want true (git and gh were both detected)")
	}
	toml := doc.Complete.Summary.ConfigTOML
	if !strings.Contains(toml, `strategy = "rebase"`) {
		t.Errorf("ConfigTOML does not carry the new merge strategy:\n%s", toml)
	}
	if !strings.Contains(toml, "claude-opus-4-8") || strings.Count(toml, "claude-opus-4-8") < 4 {
		// architect, specialist, and reviewer already use claude-opus-4-8
		// (writeCommittedConfigLocal); scout's model now joins them.
		t.Errorf("ConfigTOML does not carry the changed scout model:\n%s", toml)
	}
	for _, name := range []string{"CLAUDE.md", "AGENTS.md"} {
		change := findFileChange(doc.Complete.Summary.Files, name)
		if change == nil {
			t.Fatalf("Files = %v, want entries for both CLAUDE.md and AGENTS.md", doc.Complete.Summary.Files)
		}
		if change.Diff != "" {
			t.Errorf("%s Diff = %q, want empty (instruction files were already current; only settings/model changed)", name, change.Diff)
		}
	}
	if doc.Complete.Summary.ConfigDiff == "" {
		t.Error("ConfigDiff is empty, want a diff showing the merge/model changes")
	}
}

// TestGoldenTranscriptConfigureDisableCodex disables codex from a
// both-hosts committed configuration and expects AGENTS.md's managed
// block to be proposed for block-only removal (ActionRemove), never a
// whole-file deletion.
func TestGoldenTranscriptConfigureDisableCodex(t *testing.T) {
	root := t.TempDir()
	writeCommittedConfigLocal(t, root)
	writeInstalledBlock(t, root, "AGENTS.md")
	facts := configureFacts()

	overrides := map[string]string{
		idPickHosts:        "yes",
		idHostCodexEnabled: "no",
	}
	doc := walkConfigure(t, facts, root, "disable_codex", overrides)
	if doc.Kind != question.DocComplete {
		t.Fatalf("Kind = %q, want %q", doc.Kind, question.DocComplete)
	}
	if strings.Contains(doc.Complete.Summary.ConfigTOML, "hosts.codex") {
		t.Errorf("ConfigTOML still mentions hosts.codex:\n%s", doc.Complete.Summary.ConfigTOML)
	}
	change := findFileChange(doc.Complete.Summary.Files, "AGENTS.md")
	if change == nil {
		t.Fatalf("Files = %v, want an AGENTS.md removal entry", doc.Complete.Summary.Files)
	}
	if change.Diff == "" {
		t.Error("AGENTS.md Diff is empty, want a removal diff")
	}
	if strings.TrimSpace(change.NewContent) != "" {
		t.Errorf("NewContent = %q, want empty (the file held only the managed block)", change.NewContent)
	}
}

// TestGoldenTranscriptConfigureEnableCodexFromClaudeOnly enables codex
// on a claude-only committed configuration, proving a newly-enabled
// host's role documents default from the PRD §10 profiles
// (defaultProfiles), not the committed configuration's own values, and
// that its instruction file proposes a fresh install.
func TestGoldenTranscriptConfigureEnableCodexFromClaudeOnly(t *testing.T) {
	root := t.TempDir()
	writeCommittedConfigClaudeOnly(t, root)
	facts := configureFacts()

	overrides := map[string]string{
		idPickHosts:        "yes",
		idHostCodexEnabled: "yes",
	}
	doc := walkConfigure(t, facts, root, "enable_codex", overrides)
	if doc.Kind != question.DocComplete {
		t.Fatalf("Kind = %q, want %q", doc.Kind, question.DocComplete)
	}
	toml := doc.Complete.Summary.ConfigTOML
	for role, want := range map[string]string{"architect": "gpt-5.6-sol", "scout": "gpt-5.6-terra"} {
		if !strings.Contains(toml, want) {
			t.Errorf("ConfigTOML missing PRD default %s for %s:\n%s", want, role, toml)
		}
	}
	change := findFileChange(doc.Complete.Summary.Files, "AGENTS.md")
	if change == nil {
		t.Fatalf("Files = %v, want an AGENTS.md install entry", doc.Complete.Summary.Files)
	}
	if change.Existed {
		t.Error("Existed = true, want false (AGENTS.md did not exist before)")
	}
	if change.Diff == "" {
		t.Error("AGENTS.md Diff is empty, want an install diff")
	}
}

// TestNextConfigureNoChangeBlocked proves an interview that picks
// nothing (every picker default "no") ends in the no-change blocker,
// and that submitting approval anyway is ErrApprovalBlocked.
func TestNextConfigureNoChangeBlocked(t *testing.T) {
	root := t.TempDir()
	writeCommittedConfigLocal(t, root)
	writeInstalledBlock(t, root, "CLAUDE.md")
	writeInstalledBlock(t, root, "AGENTS.md")
	writeNoMissingGitignore(t, root)
	facts := configureFacts()

	answers := answerAllConfigureWithDefaults(t, facts, root)
	doc, err := NextConfigure(facts, answers, root)
	if err != nil {
		t.Fatalf("NextConfigure: %v", err)
	}
	if doc.Kind != question.DocSummary || len(doc.Summary.Blockers) == 0 {
		t.Fatalf("expected a blocked summary document, got %+v", doc)
	}
	if !strings.Contains(doc.Summary.Blockers[0], "nothing to deliver") {
		t.Errorf("blocker = %q, want the no-change message", doc.Summary.Blockers[0])
	}
	if len(doc.Questions) != 0 {
		t.Errorf("Questions = %v, want none while blocked (approval withheld)", doc.Questions)
	}

	answers[idApproval] = "approve"
	_, err = NextConfigure(facts, answers, root)
	if !errors.Is(err, ErrApprovalBlocked) {
		t.Fatalf("NextConfigure err = %v, want ErrApprovalBlocked", err)
	}
}

// TestNextConfigureSeedingIgnoresLocalOverride proves NextConfigure
// seeds every default from the committed configuration alone: a
// config.local.toml override sitting on disk must not change a role
// document's Default or Recommended option.
func TestNextConfigureSeedingIgnoresLocalOverride(t *testing.T) {
	root := t.TempDir()
	writeCommittedConfigLocal(t, root)
	writeLocalOverrideFile(t, root, "[hosts.claude.roles.architect]\nmodel = \"claude-fable-5\"\n")
	facts := configureFacts()

	answers := map[string]string{idPickHosts: "no", idPickRolesClaude: "yes", idPickRolesCodex: "no", idPickSettings: "no"}
	doc, err := NextConfigure(facts, answers, root)
	if err != nil {
		t.Fatalf("NextConfigure: %v", err)
	}
	if doc.Kind != question.DocQuestions {
		t.Fatalf("Kind = %q, want %q", doc.Kind, question.DocQuestions)
	}
	architectModelID := roleModelID("claude", "architect")
	found := false
	for _, q := range doc.Questions {
		if q.ID == architectModelID {
			found = true
			if q.Default != "claude-opus-4-8" {
				t.Errorf("Default = %q, want the committed value claude-opus-4-8 (local override must be ignored)", q.Default)
			}
			for _, opt := range q.Options {
				if opt.Value == "claude-fable-5" {
					t.Errorf("Options carries the local-override-only model claude-fable-5: %+v", q.Options)
				}
			}
		}
	}
	if !found {
		t.Fatalf("architect model question %s not found in %+v", architectModelID, doc.Questions)
	}
}

// TestNextConfigureBothHostsDisabledIsAnError proves toggling both
// committed-enabled hosts off in the same session is rejected the same
// way init's own "at least one host" rule is.
func TestNextConfigureBothHostsDisabledIsAnError(t *testing.T) {
	root := t.TempDir()
	writeCommittedConfigLocal(t, root)
	facts := configureFacts()

	answers := map[string]string{
		idPickHosts:         "yes",
		idHostClaudeEnabled: "no",
		idHostCodexEnabled:  "no",
	}
	_, err := NextConfigure(facts, answers, root)
	if !errors.Is(err, ErrNoHostEnabled) {
		t.Fatalf("NextConfigure err = %v, want ErrNoHostEnabled", err)
	}
}

// TestNextConfigureStaleRoleAnswerAfterDisableRejected proves a
// codex role answer submitted alongside a session that disables codex
// is unreachable (ErrUnknownAnswer), the same "existing walk" every
// no-longer-applicable answer already goes through.
func TestNextConfigureStaleRoleAnswerAfterDisableRejected(t *testing.T) {
	root := t.TempDir()
	writeCommittedConfigLocal(t, root)
	facts := configureFacts()

	answers := map[string]string{
		idPickHosts:                        "yes",
		idHostClaudeEnabled:                "yes",
		idHostCodexEnabled:                 "no",
		roleModelID("codex", "architect"):  "gpt-5.6-sol",
		roleEffortID("codex", "architect"): "high",
	}
	_, err := NextConfigure(facts, answers, root)
	if !errors.Is(err, ErrUnknownAnswer) {
		t.Fatalf("NextConfigure err = %v, want ErrUnknownAnswer", err)
	}
}

// TestNextConfigureUninitializedPropagatesError proves NextConfigure
// propagates config.ErrNotInitialized when no committed configuration
// exists yet.
func TestNextConfigureUninitializedPropagatesError(t *testing.T) {
	root := t.TempDir()
	facts := configureFacts()

	_, err := NextConfigure(facts, map[string]string{}, root)
	if !errors.Is(err, config.ErrNotInitialized) {
		t.Fatalf("NextConfigure err = %v, want config.ErrNotInitialized", err)
	}
}

// TestInstructionFilePinned drift-pins the exported InstructionFile
// against the committed root-instruction-file names contract call 4
// fixes.
func TestInstructionFilePinned(t *testing.T) {
	want := map[string]string{"claude": "CLAUDE.md", "codex": "AGENTS.md"}
	for host, file := range want {
		if got := InstructionFile(host); got != file {
			t.Errorf("InstructionFile(%q) = %q, want %q", host, got, file)
		}
	}
	if got := InstructionFile("bogus"); got != "" {
		t.Errorf(`InstructionFile("bogus") = %q, want ""`, got)
	}
}
