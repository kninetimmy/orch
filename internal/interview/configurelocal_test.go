package interview

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/kninetimmy/orch/internal/config"
	"github.com/kninetimmy/orch/internal/question"
)

// writeCommittedConfigLocal writes a valid both-hosts committed
// configuration to root/.orchestrator/config.toml — the on-disk fixture
// configure-local's own tests seed from (unlike Next's tests, which
// pass Facts and answers directly; NextConfigureLocal reads this file
// itself).
func writeCommittedConfigLocal(t *testing.T, root string) *config.Config {
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
			Codex: &config.Host{Roles: config.Roles{
				Architect:       config.RoleProfile{Model: "gpt-5.6-sol", Effort: "high"},
				Scout:           config.RoleProfile{Model: "gpt-5.6-terra", Effort: "low"},
				Implementer:     config.RoleProfile{Model: "gpt-5.6-terra", Effort: "high"},
				Specialist:      config.RoleProfile{Model: "gpt-5.6-sol", Effort: "medium"},
				Reviewer:        config.RoleProfile{Model: "gpt-5.6-sol", Effort: "medium"},
				ReviewDowngrade: config.RoleProfile{Model: "gpt-5.6-terra", Effort: "high"},
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

// writeLocalOverrideFile writes content verbatim to root's
// config.local.toml, bypassing config.RenderLocal — configure-local's
// own tests need to write both well-formed and deliberately invalid
// local files.
func writeLocalOverrideFile(t *testing.T, root, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(root, filepath.FromSlash(config.LocalOverridePath)), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// answerAllLocalWithDefaults walks NextConfigureLocal from an empty
// answer set, answering every "questions"-kind document with each
// question's Default, and returns the accumulated answers once a
// non-questions document is reached.
func answerAllLocalWithDefaults(t *testing.T, root string) map[string]string {
	t.Helper()
	answers := map[string]string{}
	for i := 0; i < 100; i++ {
		doc, err := NextConfigureLocal(answers, root)
		if err != nil {
			t.Fatalf("NextConfigureLocal: %v", err)
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
	t.Fatal("NextConfigureLocal did not reach a non-questions document within 100 steps")
	return nil
}

// walkConfigureLocal drives NextConfigureLocal from an empty answer set
// to its terminal document, answering every question with its Default
// unless overrides names a specific answer for that question id, and
// approving the summary once reached. Each step's document is checked
// against a golden file under testdata/transcript_local/<name>/.
func walkConfigureLocal(t *testing.T, root, name string, overrides map[string]string) question.Document {
	t.Helper()
	answers := map[string]string{}
	for step := 1; step <= 100; step++ {
		doc, err := NextConfigureLocal(answers, root)
		if err != nil {
			t.Fatalf("%s step %d: NextConfigureLocal: %v", name, step, err)
		}
		path := filepath.Join("testdata", "transcript_local", name, fmt.Sprintf("step_%02d.json", step))
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
	t.Fatalf("%s: NextConfigureLocal did not reach a terminal document within 100 steps", name)
	return question.Document{}
}

// TestGoldenTranscriptLocalFlagship is the flagship configure-local
// walk: both hosts picked, one pre-existing override left untouched,
// and one model changed — proving a fresh override is recorded
// alongside a preserved one.
func TestGoldenTranscriptLocalFlagship(t *testing.T) {
	root := t.TempDir()
	writeCommittedConfigLocal(t, root)
	writeLocalOverrideFile(t, root, "[hosts.claude.roles.architect]\nmodel = \"claude-fable-5\"\n")

	overrides := map[string]string{
		idPickCodex:                         "yes",
		localRoleModelID("claude", "scout"): "claude-opus-4-8",
	}
	doc := walkConfigureLocal(t, root, "flagship", overrides)
	if doc.Kind != question.DocComplete {
		t.Fatalf("Kind = %q, want %q", doc.Kind, question.DocComplete)
	}
	if !doc.Complete.BootstrapReady {
		t.Error("BootstrapReady = false, want true (documented: nothing external is load-bearing)")
	}
	if doc.Complete.Detection != nil {
		t.Errorf("Detection = %v, want nil (configure-local reads no environment facts)", doc.Complete.Detection)
	}
	if len(doc.Complete.Summary.Files) != 1 {
		t.Fatalf("Files = %v, want exactly one", doc.Complete.Summary.Files)
	}
	change := doc.Complete.Summary.Files[0]
	if change.Delete {
		t.Error("Delete = true, want false (overrides remain)")
	}
	if !strings.Contains(change.NewContent, "claude-fable-5") {
		t.Errorf("NewContent dropped the preserved override:\n%s", change.NewContent)
	}
	if !strings.Contains(change.NewContent, "claude-opus-4-8") {
		t.Errorf("NewContent is missing the new override:\n%s", change.NewContent)
	}
	if change.Diff == "" {
		t.Error("Diff is empty, want a diff showing the new override")
	}
}

// TestGoldenTranscriptLocalClearAll answers the only existing override
// back to its committed value — clearing it — and expects the
// resulting summary to propose deleting config.local.toml outright.
func TestGoldenTranscriptLocalClearAll(t *testing.T) {
	root := t.TempDir()
	writeCommittedConfigLocal(t, root)
	writeLocalOverrideFile(t, root, "[hosts.claude.roles.architect]\nmodel = \"claude-fable-5\"\n")

	overrides := map[string]string{
		localRoleModelID("claude", "architect"): "claude-opus-4-8", // the committed value: clears the override
	}
	doc := walkConfigureLocal(t, root, "clear_all", overrides)
	if doc.Kind != question.DocComplete {
		t.Fatalf("Kind = %q, want %q", doc.Kind, question.DocComplete)
	}
	change := doc.Complete.Summary.Files[0]
	if !change.Delete {
		t.Error("Delete = false, want true (every override was cleared)")
	}
	if change.NewContent != "" {
		t.Errorf("NewContent = %q, want empty on delete", change.NewContent)
	}
	if doc.Complete.Summary.ConfigTOML != "" {
		t.Errorf("Summary.ConfigTOML = %q, want empty on delete", doc.Complete.Summary.ConfigTOML)
	}
}

// TestNextConfigureLocalNoChangeBlocked proves an interview that picks
// nothing (no existing overrides, every picker default accepted as
// "no") ends in the no-change blocker, and that submitting approval
// anyway is ErrApprovalBlocked.
func TestNextConfigureLocalNoChangeBlocked(t *testing.T) {
	root := t.TempDir()
	writeCommittedConfigLocal(t, root)

	answers := answerAllLocalWithDefaults(t, root)
	doc, err := NextConfigureLocal(answers, root)
	if err != nil {
		t.Fatalf("NextConfigureLocal: %v", err)
	}
	if doc.Kind != question.DocSummary || len(doc.Summary.Blockers) == 0 {
		t.Fatalf("expected a blocked summary document, got %+v", doc)
	}
	if !strings.Contains(doc.Summary.Blockers[0], "nothing to write") {
		t.Errorf("blocker = %q, want the no-change message", doc.Summary.Blockers[0])
	}
	if len(doc.Questions) != 0 {
		t.Errorf("Questions = %v, want none while blocked (approval withheld)", doc.Questions)
	}

	answers[idApproval] = "approve"
	_, err = NextConfigureLocal(answers, root)
	if !errors.Is(err, ErrApprovalBlocked) {
		t.Fatalf("NextConfigureLocal err = %v, want ErrApprovalBlocked", err)
	}
}

// TestNextConfigureLocalUnknownAnswerRejected proves a role answer
// submitted before its host is even picked is unreachable.
func TestNextConfigureLocalUnknownAnswerRejected(t *testing.T) {
	root := t.TempDir()
	writeCommittedConfigLocal(t, root)

	answers := map[string]string{localRoleModelID("claude", "architect"): "claude-opus-4-8"}
	_, err := NextConfigureLocal(answers, root)
	if !errors.Is(err, ErrUnknownAnswer) {
		t.Fatalf("NextConfigureLocal err = %v, want ErrUnknownAnswer", err)
	}
}

// TestNextConfigureLocalInvalidFileSeeding proves contract call 5:
// an existing config.local.toml carrying a policy-bearing key, an
// unknown key, and an out-of-domain preference value still seeds
// whatever else decodes and classifies as a valid preference, and an
// unchanged session's summary proposes rewriting the file down to just
// that valid part.
func TestNextConfigureLocalInvalidFileSeeding(t *testing.T) {
	root := t.TempDir()
	writeCommittedConfigLocal(t, root)
	writeLocalOverrideFile(t, root, `bogus_key = "x"

[merge]
strategy = "rebase"

[hosts.claude.roles.architect]
model = "claude-fable-5"

[hosts.claude.roles.scout]
effort = "ultra"
`)

	answers := answerAllLocalWithDefaults(t, root)
	doc, err := NextConfigureLocal(answers, root)
	if err != nil {
		t.Fatalf("NextConfigureLocal: %v", err)
	}
	if doc.Kind != question.DocSummary {
		t.Fatalf("Kind = %q, want %q", doc.Kind, question.DocSummary)
	}
	if len(doc.Summary.Blockers) != 0 {
		t.Fatalf("unexpected blockers: %v", doc.Summary.Blockers)
	}
	change := doc.Summary.Files[0]
	if !strings.Contains(change.NewContent, "claude-fable-5") {
		t.Errorf("NewContent dropped the one valid override:\n%s", change.NewContent)
	}
	for _, bad := range []string{"bogus_key", "rebase", "ultra"} {
		if strings.Contains(change.NewContent, bad) {
			t.Errorf("NewContent retained invalid content %q:\n%s", bad, change.NewContent)
		}
	}
	if change.Diff == "" {
		t.Error("Diff is empty, want the repair diff dropping the invalid content")
	}
}

// TestConfigureLocalLeafIDsMatchPreferenceKeys walks the full
// both-hosts-and-settings-picked sequence and collects every role/
// settings question id — the drift guard pinning configure-local's
// question IDs to config.PreferenceKeys' closed set exactly.
func TestConfigureLocalLeafIDsMatchPreferenceKeys(t *testing.T) {
	root := t.TempDir()
	writeCommittedConfigLocal(t, root)

	answers := map[string]string{}
	seen := map[string]bool{}
	for i := 0; i < 100; i++ {
		doc, err := NextConfigureLocal(answers, root)
		if err != nil {
			t.Fatalf("NextConfigureLocal: %v", err)
		}
		if doc.Kind != question.DocQuestions {
			break
		}
		for _, q := range doc.Questions {
			if q.ID == idPickClaude || q.ID == idPickCodex || q.ID == idPickSettings {
				answers[q.ID] = "yes"
				continue
			}
			if err := question.SpecCheck(q); err != nil {
				t.Errorf("SpecCheck(%s): %v", q.ID, err)
			}
			seen[q.ID] = true
			answers[q.ID] = q.Default
		}
	}

	got := make([]string, 0, len(seen))
	for id := range seen {
		got = append(got, id)
	}
	sort.Strings(got)
	want := config.PreferenceKeys()
	if len(got) != len(want) {
		t.Fatalf("saw %d distinct leaf question ids, want %d\ngot:  %v\nwant: %v", len(got), len(want), got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("leaf ids[%d] = %q, want %q", i, got[i], want[i])
		}
	}
}

// TestHostLocalModelsPinsFableFive drift-pins hostLocalModels against
// hostModels: codex carries no local-only model, and claude carries
// exactly hostModels["claude"] plus the literal "claude-fable-5".
func TestHostLocalModelsPinsFableFive(t *testing.T) {
	if len(hostLocalModels["codex"]) != len(hostModels["codex"]) {
		t.Fatalf("hostLocalModels[codex] = %v, want exactly hostModels[codex] (codex has no local-only model)", hostLocalModels["codex"])
	}
	for i, m := range hostModels["codex"] {
		if hostLocalModels["codex"][i] != m {
			t.Errorf("hostLocalModels[codex][%d] = %q, want %q", i, hostLocalModels["codex"][i], m)
		}
	}

	wantClaude := append(append([]string{}, hostModels["claude"]...), "claude-fable-5")
	if len(hostLocalModels["claude"]) != len(wantClaude) {
		t.Fatalf("hostLocalModels[claude] = %v, want %v", hostLocalModels["claude"], wantClaude)
	}
	for i, m := range wantClaude {
		if hostLocalModels["claude"][i] != m {
			t.Errorf("hostLocalModels[claude][%d] = %q, want %q", i, hostLocalModels["claude"][i], m)
		}
	}
}
