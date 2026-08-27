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
	writeCommittedConfig(t, root, cfg)
	return cfg
}

func writeCommittedConfigOpenCodeOnly(t *testing.T, root string) *config.Config {
	t.Helper()
	cfg, err := materialize(openCodeOnlyAnswers())
	if err != nil {
		t.Fatal(err)
	}
	writeCommittedConfig(t, root, cfg)
	return cfg
}

// writeCommittedConfig revisions, renders, and writes cfg to root's
// committed configuration path — the tail writeCommittedConfigLocal
// shares with any test that needs the same fixture carrying one edited
// value.
func writeCommittedConfig(t *testing.T, root string, cfg *config.Config) {
	t.Helper()
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

func TestGoldenTranscriptLocalOpenCodeRoleModels(t *testing.T) {
	root := t.TempDir()
	writeCommittedConfigOpenCodeOnly(t, root)
	doc, err := NextConfigureLocalWithFacts(withOpenCodeTestCatalog(Facts{}), map[string]string{
		idPickOpenCode: "yes",
		idPickSettings: "no",
	}, root)
	if err != nil {
		t.Fatalf("NextConfigureLocal: %v", err)
	}
	checkGoldenDocument(t, filepath.Join("testdata", "transcript_local", "opencode_role", "step_01.json"), doc)
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

// answerLocalWithOverrides walks NextConfigureLocal from an empty
// answer set, answering every "questions"-kind document with overrides'
// value where the question id has one, else its Default, and returns
// the first non-questions document reached (answerAllLocalWithDefaults'
// shape, but for a session that must pick a non-default path to reach a
// summary worth inspecting — unlike walkConfigureLocal, it never
// auto-approves, since a blocked summary makes that an error).
func answerLocalWithOverrides(t *testing.T, root string, overrides map[string]string) (question.Document, map[string]string) {
	t.Helper()
	answers := map[string]string{}
	for i := 0; i < 100; i++ {
		doc, err := NextConfigureLocal(answers, root)
		if err != nil {
			t.Fatalf("NextConfigureLocal: %v", err)
		}
		if doc.Kind != question.DocQuestions {
			return doc, answers
		}
		for _, q := range doc.Questions {
			if v, ok := overrides[q.ID]; ok {
				answers[q.ID] = v
				continue
			}
			if q.Default == "" {
				t.Fatalf("question %s has no default to answer with", q.ID)
			}
			answers[q.ID] = q.Default
		}
	}
	t.Fatal("NextConfigureLocal did not reach a non-questions document within 100 steps")
	return question.Document{}, nil
}

// TestNextConfigureLocalRejectsOutOfDomainFreeTextEffort proves issue
// #124 criterion 4 for configure-local: an effort value the effort
// question's FreeText escape hatch admits past question.ValidateAnswer,
// but internal/config would reject for that host, never reaches a
// written config.local.toml — config.RenderLocal's own domain check
// (inside materializeLocal) rejects it first, naming the host's full
// accepted domain (effortList) in the returned error, mirroring
// answerLocalWithOverrides' walk shape but stopping to inspect the
// error instead of treating it as fatal.
func TestNextConfigureLocalRejectsOutOfDomainFreeTextEffort(t *testing.T) {
	root := t.TempDir()
	writeCommittedConfigLocal(t, root)

	effortKey := localRoleEffortID("codex", "architect")
	overrides := map[string]string{idPickCodex: "yes", effortKey: "minimal"}
	answers := map[string]string{}
	var lastErr error
	for i := 0; i < 100; i++ {
		doc, err := NextConfigureLocal(answers, root)
		if err != nil {
			lastErr = err
			break
		}
		if doc.Kind != question.DocQuestions {
			t.Fatalf("expected an error before reaching a clean %q document", doc.Kind)
		}
		for _, q := range doc.Questions {
			if v, ok := overrides[q.ID]; ok {
				answers[q.ID] = v
				continue
			}
			if q.Default == "" {
				t.Fatalf("question %s has no default to answer with", q.ID)
			}
			answers[q.ID] = q.Default
		}
	}
	if lastErr == nil {
		t.Fatal("NextConfigureLocal succeeded, want an error for codex effort=minimal")
	}
	if !strings.Contains(lastErr.Error(), "low, medium, high, xhigh, max, ultra") {
		t.Errorf("error %q does not name codex's full accepted effort domain", lastErr)
	}
}

// TestNextConfigureLocalMetricsTrapBlocked proves configure-local
// refuses, through the same summary-blocker mechanism the no-change
// case uses, any change whose resulting effective configuration would
// enable metrics while .gitignore does not already carry the metrics
// ignore line — naming `orch configure` as the flow that adds it — and
// that submitting approval anyway is still ErrApprovalBlocked.
func TestNextConfigureLocalMetricsTrapBlocked(t *testing.T) {
	root := t.TempDir()
	writeCommittedConfigLocal(t, root) // committed metrics.enabled = false; no .gitignore at all

	doc, answers := answerLocalWithOverrides(t, root, map[string]string{idPickSettings: "yes", idMetricsEnabled: "yes"})
	if doc.Kind != question.DocSummary || len(doc.Summary.Blockers) == 0 {
		t.Fatalf("expected a blocked summary document, got %+v", doc)
	}
	var found bool
	for _, b := range doc.Summary.Blockers {
		if strings.Contains(b, "orch configure") {
			found = true
			if !strings.Contains(b, ".orchestrator/metrics/") {
				t.Errorf("blocker %q does not name the missing .gitignore line", b)
			}
		}
	}
	if !found {
		t.Errorf("Blockers = %v, want one naming orch configure as the flow that adds the .gitignore line", doc.Summary.Blockers)
	}

	answers[idApproval] = "approve"
	_, err := NextConfigureLocal(answers, root)
	if !errors.Is(err, ErrApprovalBlocked) {
		t.Fatalf("NextConfigureLocal err = %v, want ErrApprovalBlocked", err)
	}
}

// TestNextConfigureLocalMetricsTrapClearedByGitignore proves the same
// enabling session is unblocked once .gitignore already carries the
// metrics ignore line — the trap is about the line's absence, not about
// enabling metrics itself.
func TestNextConfigureLocalMetricsTrapClearedByGitignore(t *testing.T) {
	root := t.TempDir()
	writeCommittedConfigLocal(t, root)
	if err := os.WriteFile(filepath.Join(root, ".gitignore"), []byte(".orchestrator/metrics/\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	doc, _ := answerLocalWithOverrides(t, root, map[string]string{idPickSettings: "yes", idMetricsEnabled: "yes"})
	if doc.Kind != question.DocSummary || len(doc.Summary.Blockers) != 0 {
		t.Fatalf("expected an unblocked summary document, got %+v", doc)
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

// TestConfigureLocalLeafIDsMatchEditablePreferenceKeys walks the full
// both-hosts-and-settings-picked sequence and collects every role/
// settings question id — the drift guard pinning configure-local's
// question IDs to config.EditablePreferenceKeys' closed set exactly.
func TestConfigureLocalLeafIDsMatchEditablePreferenceKeys(t *testing.T) {
	root := t.TempDir()
	writeCommittedConfigLocal(t, root)
	raw, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(config.Path)))
	if err != nil {
		t.Fatal(err)
	}
	committed, err := config.Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	openCode, err := materialize(openCodeOnlyAnswers())
	if err != nil {
		t.Fatal(err)
	}
	committed.Hosts.OpenCode = openCode.Hosts.OpenCode
	committed.ConfigRevision, err = config.Revision(committed)
	if err != nil {
		t.Fatal(err)
	}
	raw, err = config.Render(committed)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, filepath.FromSlash(config.Path)), raw, 0o644); err != nil {
		t.Fatal(err)
	}

	answers := map[string]string{}
	seen := map[string]bool{}
	for i := 0; i < 100; i++ {
		doc, err := NextConfigureLocalWithFacts(withOpenCodeTestCatalog(Facts{}), answers, root)
		if err != nil {
			t.Fatalf("NextConfigureLocal: %v", err)
		}
		if doc.Kind != question.DocQuestions {
			break
		}
		for _, q := range doc.Questions {
			if q.ID == idPickClaude || q.ID == idPickCodex || q.ID == idPickOpenCode || q.ID == idPickSettings {
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
	want := config.EditablePreferenceKeys()
	if len(got) != len(want) {
		t.Fatalf("saw %d distinct leaf question ids, want %d\ngot:  %v\nwant: %v", len(got), len(want), got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("leaf ids[%d] = %q, want %q", i, got[i], want[i])
		}
	}
}

func TestVariantOptionsKeepCommittedAndEffectiveValues(t *testing.T) {
	opts := openCodeVariantQuestion(localRoleVariantID("architect"), roleSpecs[0], []string{"high", "machine-choice"}, "machine-choice", "high").Options
	seen := map[string]bool{}
	recommended := ""
	committedLabel := ""
	for _, opt := range opts {
		seen[opt.Value] = true
		if opt.Value == "high" {
			committedLabel = opt.Label
		}
		if opt.Recommended {
			recommended = opt.Value
		}
	}
	if !seen["high"] || !seen["machine-choice"] || recommended != "machine-choice" || !strings.Contains(committedLabel, "(committed)") {
		t.Errorf("options = %+v", opts)
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

// TestNextConfigureLocalRejectsNearMissModel proves issue #207 for
// `orch configure-local`: a typed model shortening a known id — here
// the local-override-only claude-fable-5 — is rejected with that full
// id suggested, and the corrected id then reaches the summary, so no
// config.local.toml is ever written from the near miss.
func TestNextConfigureLocalRejectsNearMissModel(t *testing.T) {
	root := t.TempDir()
	writeCommittedConfigLocal(t, root)

	modelKey := localRoleModelID("claude", "architect")
	walk := func(model string) (question.Document, error) {
		overrides := map[string]string{idPickClaude: "yes", modelKey: model}
		answers := map[string]string{}
		for i := 0; i < 100; i++ {
			doc, err := NextConfigureLocal(answers, root)
			if err != nil {
				return question.Document{}, err
			}
			if doc.Kind != question.DocQuestions {
				return doc, nil
			}
			for _, q := range doc.Questions {
				if v, ok := overrides[q.ID]; ok {
					answers[q.ID] = v
					continue
				}
				if q.Default == "" {
					t.Fatalf("question %s has no default to answer with", q.ID)
				}
				answers[q.ID] = q.Default
			}
		}
		t.Fatal("NextConfigureLocal did not reach a non-questions document within 100 steps")
		return question.Document{}, nil
	}

	_, err := walk("fable-5")
	if !errors.Is(err, ErrBadAnswer) {
		t.Fatalf("NextConfigureLocal err = %v, want ErrBadAnswer", err)
	}
	if !strings.Contains(err.Error(), "claude-fable-5") {
		t.Errorf("error %q does not suggest the full id claude-fable-5", err)
	}

	doc, err := walk("claude-fable-5")
	if err != nil {
		t.Fatalf("NextConfigureLocal after correction: %v", err)
	}
	if doc.Kind != question.DocSummary {
		t.Fatalf("Kind = %q, want %q", doc.Kind, question.DocSummary)
	}
}

// TestNextConfigureLocalPreservesNearMissOverride proves the near-miss
// rule leaves an existing config.local.toml alone: a near-miss model
// override in an area this session does not pick is preserved in the
// proposed file, exactly as materializeLocal promises for every valid
// override of an unpicked area.
func TestNextConfigureLocalPreservesNearMissOverride(t *testing.T) {
	root := t.TempDir()
	writeCommittedConfigLocal(t, root)
	writeLocalOverrideFile(t, root, `[hosts.claude.roles.architect]
model = "opus-5"
`)

	codexModel := localRoleModelID("codex", "architect")
	doc, _ := answerLocalWithOverrides(t, root, map[string]string{idPickCodex: "yes", codexModel: "gpt-5.6-luna"})
	if doc.Kind != question.DocSummary {
		t.Fatalf("Kind = %q, want %q", doc.Kind, question.DocSummary)
	}
	if len(doc.Summary.Files) == 0 {
		t.Fatalf("Summary.Files is empty, want the proposed config.local.toml")
	}
	if !strings.Contains(doc.Summary.Files[0].NewContent, `model = "opus-5"`) {
		t.Errorf("proposed file dropped the unpicked area's existing override:\n%s", doc.Summary.Files[0].NewContent)
	}
}

// TestNextConfigureLocalAcceptsNearMissCommittedDefault proves a
// repository initialized before the near-miss rule existed still walks
// configure-local end to end: the model question defaults to the
// committed near-miss id, and accepting that default reaches the
// summary instead of failing the interview.
func TestNextConfigureLocalAcceptsNearMissCommittedDefault(t *testing.T) {
	root := t.TempDir()
	cfg := writeCommittedConfigLocal(t, root)
	cfg.Hosts.Claude.Roles.Architect.Model = "opus-5"
	writeCommittedConfig(t, root, cfg)

	doc, answers := answerLocalWithOverrides(t, root, map[string]string{idPickClaude: "yes"})
	if got := answers[localRoleModelID("claude", "architect")]; got != "opus-5" {
		t.Fatalf("architect model default = %q, want the committed opus-5", got)
	}
	if doc.Kind != question.DocSummary {
		t.Fatalf("Kind = %q, want %q", doc.Kind, question.DocSummary)
	}
}
