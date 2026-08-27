// Package adaptertest is the PRD §23 shared parity layer all hosts'
// adapters' plugin tests consume: fixtures (the committed §10 role
// profiles) and assertion helpers (skill/manifest drift pins) so
// cross-host invariants — the run-verb allowlist, the four
// anti-forgery statement literals, the plan/merge gate option text, the
// setup interview's terminal forms, hook command portability, matcher/
// guard parity, the routed-selection prompt cue, and the blast-radius
// acceptance criterion the engine contributes to a risk-domain issue —
// have exactly one source instead of a copy per adapter that can
// silently drift apart.
//
// This package carries no Test functions and tests nothing of its own;
// it is test-support only, imported by each adapter's plugin_test.go.
// Every exported Check* helper takes
// a *testing.T and calls t.Helper(), and reads files relative to the
// caller's own working directory — the same assumption each adapter's
// plugin_test.go already makes, since `go test` runs a package's tests
// with that package's directory as the process cwd.
//
// These helpers pin the presence of instruction text in shipped skill
// markdown; they cannot pin its meaning. Every prose-phrase comparison
// runs through NormalizeWhitespace on both sides deliberately, so a
// pinned phrase that a markdown reflow happens to hard-wrap across a
// line break still counts as present — the pins track whether the
// words are there, not how the file happens to be line-wrapped.
//
// It imports internal/run and internal/question only to derive protocol
// literals from engine constants. A caller may also pass internal/guard tool
// lists into CheckMatcherEqualsGuardTools. It remains a leaf test-support
// package, not a place for adapter-specific or engine policy code.
package adaptertest

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"

	"github.com/kninetimmy/orch/internal/question"
	"github.com/kninetimmy/orch/internal/run"
)

// RoleSpec is one role's committed host profile: the exact model and
// host-native execution profile a canonical shipped agent definition carries.
type RoleSpec struct {
	Model   string
	Effort  string
	Variant string
}

// Profile returns the committed §10 profile for host ("claude", "codex",
// or "opencode"), keyed by role: "scout", "implementer", "specialist",
// "reviewer", "reviewer-safe". Every adapter's agent roster is asserted
// equal to this map so the hosts' plugin tests and the committed
// §10 table can never silently diverge from one another.
//
// An unrecognized host is a programming error in the caller (a typo, or
// a new host added to the module without updating this fixture) —
// Profile panics rather than returning a zero value a test might
// silently pass against.
func Profile(host string) map[string]RoleSpec {
	switch host {
	case "claude":
		return map[string]RoleSpec{
			"scout":         {Model: "claude-opus-5", Effort: "low"},
			"implementer":   {Model: "claude-opus-5", Effort: "medium"},
			"specialist":    {Model: "claude-opus-5", Effort: "high"},
			"reviewer":      {Model: "claude-opus-5", Effort: "high"},
			"reviewer-safe": {Model: "claude-opus-5", Effort: "medium"},
		}
	case "codex":
		return map[string]RoleSpec{
			"scout":         {Model: "gpt-5.6-luna", Effort: "max"},
			"implementer":   {Model: "gpt-5.6-terra", Effort: "max"},
			"specialist":    {Model: "gpt-5.6-sol", Effort: "max"},
			"reviewer":      {Model: "gpt-5.6-sol", Effort: "xhigh"},
			"reviewer-safe": {Model: "gpt-5.6-sol", Effort: "high"},
		}
	case "opencode":
		return map[string]RoleSpec{
			"scout":         {Model: "openai/gpt-5.6-luna", Variant: "max"},
			"implementer":   {Model: "openai/gpt-5.6-terra", Variant: "max"},
			"specialist":    {Model: "openai/gpt-5.6-sol", Variant: "max"},
			"reviewer":      {Model: "openai/gpt-5.6-sol", Variant: "xhigh"},
			"reviewer-safe": {Model: "openai/gpt-5.6-sol", Variant: "high"},
		}
	default:
		panic("adaptertest: unknown host " + host)
	}
}

// runVerbTokens is the closed set every `orch run <word>` token found in
// a skill must belong to: the 14 document-taking verbs internal/cli/run.go
// dispatches, plus "status" (orch run status --json, dispatched
// separately but still spelled "orch run status"). Moved verbatim from
// adapters/claude/plugin_test.go so both hosts pin against the same set.
var runVerbTokens = map[string]bool{
	"plan": true, "activate": true, "dispatch": true, "pr-open": true,
	"review-worktree": true, "review": true, "escalate": true, "ci": true,
	"merge-report": true, "merge": true, "block": true, "abandon": true,
	"cleanup": true, "complete": true, "status": true,
}

var orchRunTokenPattern = regexp.MustCompile(`orch run ([a-z-]+)`)

// readFile is a small t.Fatalf-wrapping os.ReadFile, shared by the
// helpers below.
func readFile(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(data)
}

// globFiles is a small t.Fatalf-wrapping filepath.Glob, shared by the
// helpers below. It fails the test if the pattern matches nothing —
// a skill glob that suddenly matches zero files is itself a signal
// something moved or was deleted.
func globFiles(t *testing.T, pattern string) []string {
	t.Helper()
	matches, err := filepath.Glob(pattern)
	if err != nil {
		t.Fatalf("glob %s: %v", pattern, err)
	}
	if len(matches) == 0 {
		t.Fatalf("glob %s matched no files", pattern)
	}
	return matches
}

// normalizeWhitespace collapses every run of whitespace in s — including
// a hard-wrapped line break — to a single space, and trims leading and
// trailing whitespace. It is the only place this normalization logic is
// written; every prose-phrase comparison in this package, and the two
// adapters' own prose-phrase checks via NormalizeWhitespace, is written
// in terms of it, so a pinned phrase a markdown reflow happens to split
// across a line break still compares equal to the same phrase read from
// the reflowed file.
func normalizeWhitespace(s string) string {
	return strings.Join(strings.Fields(s), " ")
}

// NormalizeWhitespace exports normalizeWhitespace for adapters'
// own plugin_test.go prose-phrase checks, which live outside this
// package and so cannot call the unexported form directly. It performs
// no normalization logic of its own beyond delegating to
// normalizeWhitespace.
func NormalizeWhitespace(s string) string {
	return normalizeWhitespace(s)
}

// CheckRunVerbTokens drift-pins every `orch run <verb>` token mentioned
// in a skill (found by skillGlob, e.g. "skills/*/SKILL.md") against the
// real verb set: a renamed or removed verb that a skill still mentions
// fails the test instead of silently documenting a dead command.
func CheckRunVerbTokens(t *testing.T, skillGlob string) {
	t.Helper()
	for _, path := range globFiles(t, skillGlob) {
		content := readFile(t, path)
		for _, m := range orchRunTokenPattern.FindAllStringSubmatch(content, -1) {
			verb := m[1]
			if !runVerbTokens[verb] {
				t.Errorf("%s: mentions `orch run %s`, which is not one of the 14 verbs or status", path, verb)
			}
		}
	}
}

// statementConstants maps every internal/run approval/statement literal
// a plugin's skills may quote to the exported constant it must equal.
// Reading these live from internal/run (not hardcoding the string
// twice) means a rename of any constant breaks the build, and a value
// change makes this map's key no longer match a skill's hardcoded
// prose — either way, drift breaks the test instead of silently
// documenting a wrong statement.
var statementConstants = map[string]string{
	run.ApprovalStatement:      "run.ApprovalStatement",
	run.MergeApprovalStatement: "run.MergeApprovalStatement",
	run.AbandonStatement:       "run.AbandonStatement",
	run.CleanupStatement:       "run.CleanupStatement",
}

var statementLiteralPattern = regexp.MustCompile(`"statement":\s*"([a-z-]+)"`)

// CheckStatementLiterals asserts every `"statement": "..."` literal
// quoted in a skill (found by skillGlob) equals one of the internal/run
// approval/statement constants, and that every constant appears at
// least once across the matched skills — so a skill can never silently
// drop or misquote one of the anti-forgery statements the engine
// requires verbatim.
func CheckStatementLiterals(t *testing.T, skillGlob string) {
	t.Helper()
	seen := map[string]bool{}
	for _, path := range globFiles(t, skillGlob) {
		content := readFile(t, path)
		for _, m := range statementLiteralPattern.FindAllStringSubmatch(content, -1) {
			literal := m[1]
			constName, ok := statementConstants[literal]
			if !ok {
				t.Errorf("%s: statement literal %q does not equal any internal/run approval/statement constant", path, literal)
				continue
			}
			seen[constName] = true
		}
	}
	for _, constName := range statementConstants {
		if !seen[constName] {
			t.Errorf("no skill matched by %s quotes the statement literal for %s", skillGlob, constName)
		}
	}
}

// planGateOptions are the exact §8 four options the delivery skill must
// present at the plan gate, in order.
var planGateOptions = []string{
	"Approve and enter Delivery",
	"Adjust agent routing",
	"Revise scope",
	"Cancel and remain read-only",
}

// CheckPlanGateOptions pins deliverySkillPath's documented plan-gate
// question options against the exact PRD §8 four-option set. The
// comparison runs on whitespace-normalized text on both sides, so an
// option string a markdown reflow happens to hard-wrap across a line
// break still counts as present.
func CheckPlanGateOptions(t *testing.T, deliverySkillPath string) {
	t.Helper()
	content := normalizeWhitespace(readFile(t, deliverySkillPath))
	for _, opt := range planGateOptions {
		if !strings.Contains(content, normalizeWhitespace(opt)) {
			t.Errorf("%s does not contain plan-gate option %q", deliverySkillPath, opt)
		}
	}
}

// mergeGateOptions are the exact two options the delivery skill must
// present at the merge gate, fresh for every PR.
var mergeGateOptions = []string{
	"Approve merge",
	"Not yet",
}

// CheckMergeGateOptions pins deliverySkillPath's documented merge-gate
// question options against the exact two-option set. The comparison
// runs on whitespace-normalized text on both sides, so an option string
// a markdown reflow happens to hard-wrap across a line break still
// counts as present.
func CheckMergeGateOptions(t *testing.T, deliverySkillPath string) {
	t.Helper()
	content := normalizeWhitespace(readFile(t, deliverySkillPath))
	for _, opt := range mergeGateOptions {
		if !strings.Contains(content, normalizeWhitespace(opt)) {
			t.Errorf("%s does not contain merge-gate option %q", deliverySkillPath, opt)
		}
	}
}

// setupTerminalForms are the exact three terminal-form command strings
// the setup skill must document, one per interview.
var setupTerminalForms = []string{
	"orch init --bootstrap",
	"orch configure --deliver",
	"orch configure-local --apply",
}

// CheckSetupTerminalForms pins setupSkillPath's documented terminal
// forms against the exact three commands each interview ends with. The
// comparison runs on whitespace-normalized text on both sides, so a
// terminal form a markdown reflow happens to hard-wrap across a line
// break still counts as present.
func CheckSetupTerminalForms(t *testing.T, setupSkillPath string) {
	t.Helper()
	content := normalizeWhitespace(readFile(t, setupSkillPath))
	for _, form := range setupTerminalForms {
		if !strings.Contains(content, normalizeWhitespace(form)) {
			t.Errorf("%s does not contain terminal form %q", setupSkillPath, form)
		}
	}
}

// CheckSetupOptionPagination pins each setup skill to the deterministic paging
// document emitted by internal/question. Behavioral page invariants are tested
// in that package; this check keeps adapter consumption instructions aligned.
func CheckSetupOptionPagination(t *testing.T, setupSkillPath string) {
	t.Helper()
	raw := readFile(t, setupSkillPath)
	parts := strings.SplitN(raw, "---", 3)
	if len(parts) != 3 || !strings.Contains(normalizeWhitespace(parts[1]), "pagination page") {
		t.Errorf("%s metadata does not describe pagination-page calls", setupSkillPath)
	}
	if strings.Contains(normalizeWhitespace(parts[1]), "one batched AskUserQuestion per step") {
		t.Errorf("%s metadata still promises one batched call per step", setupSkillPath)
	}
	content := normalizeWhitespace(raw)
	answerSet := fmt.Sprintf(`{"schema_version": %d, "answers": {}}`, question.SchemaVersion)
	wireVersion := fmt.Sprintf("Question wire `schema_version` is closed at `%d` for both `AnswerSet` and `Document`", question.SchemaVersion)
	for _, phrase := range []string{
		answerSet,
		wireVersion,
		"reject any other Document before reading `kind`, `questions`, or `pagination`",
		"pagination.hosts",
		"pagination.pages[0]",
		"2–3 options exactly as emitted",
		"mention the page's `index`/`total` in the prompt",
		"Never synthesize, split, merge, reorder, or replace pages with a request to type an identifier",
		"Never submit an action as `answers[question.id]`",
	} {
		if !strings.Contains(content, normalizeWhitespace(phrase)) {
			t.Errorf("%s does not contain setup option-pagination guidance %q", setupSkillPath, phrase)
		}
	}
}

// portabilityForbidden are shell metacharacters a bare-argv hook command
// must never contain: hook commands run directly as argv on every OS,
// no shell interposed, so a shell-syntax command would work nowhere.
const portabilityForbidden = "|><&;$%\"'`()"

// CheckHookCommandPortability asserts none of commands contains a shell
// metacharacter.
func CheckHookCommandPortability(t *testing.T, commands []string) {
	t.Helper()
	for _, cmd := range commands {
		if strings.ContainsAny(cmd, portabilityForbidden) {
			t.Errorf("command %q contains a shell metacharacter", cmd)
		}
	}
}

// routedSelectionCue is the exact opening line every Delivery spawn/
// dispatch prompt must open with (fenced as its own code block in the
// skill): the only channel carrying routed effort to a Claude Code
// subagent, since subagent spawns take no effort parameter, and a plain
// statement of fact on Codex, where effort is a real host parameter.
const routedSelectionCue = "Routed selection: <model> @ <effort>"

const openCodeRoutedSelectionCue = "Routed selection: <provider/model#variant or bare provider/model>"

// CheckRoutedSelectionCue pins skillPath's documented spawn/dispatch
// prompt against the exact routed-selection opening line every issue's
// executor (and reviewer) prompt must open with. The comparison runs on
// whitespace-normalized text on both sides, so the cue would still count
// as present even if a markdown reflow hard-wrapped it across a line
// break.
func CheckRoutedSelectionCue(t *testing.T, skillPath string) {
	t.Helper()
	content := normalizeWhitespace(readFile(t, skillPath))
	if !strings.Contains(content, normalizeWhitespace(routedSelectionCue)) {
		t.Errorf("%s does not contain the routed-selection prompt cue %q", skillPath, routedSelectionCue)
	}
}

// CheckOpenCodeRoutedSelectionCue pins OpenCode's native model-reference cue;
// Claude and Codex continue using CheckRoutedSelectionCue unchanged.
func CheckOpenCodeRoutedSelectionCue(t *testing.T, skillPath string) {
	t.Helper()
	content := normalizeWhitespace(readFile(t, skillPath))
	if !strings.Contains(content, normalizeWhitespace(openCodeRoutedSelectionCue)) {
		t.Errorf("%s does not contain the routed-selection prompt cue %q", skillPath, openCodeRoutedSelectionCue)
	}
}

// CheckSelectionWireVersions pins every Selection-bearing run protocol to the
// engine constants. The skills must reject drift before reading or submitting a
// Selection; hand-synced numeric literals cannot change without this test.
func CheckSelectionWireVersions(t *testing.T, deliverySkillPath, architectSkillPath string) {
	t.Helper()
	versions := fmt.Sprintf("Selection-bearing wire versions are closed: StatusDoc `%d`, GateDoc `%d`, Dispatch `%d`, Escalate `%d`, Review `%d`.",
		run.StatusSchemaVersion, run.GateSchemaVersion, run.DispatchSchemaVersion, run.EscalateSchemaVersion, run.ReviewSchemaVersion)
	delivery := normalizeWhitespace(readFile(t, deliverySkillPath))
	phrases := []string{
		versions,
		"Reject any other `schema_version` before reading or submitting a `Selection`.",
		fmt.Sprintf("`GateDoc` (`schema_version: %d`)", run.GateSchemaVersion),
		fmt.Sprintf("`{\"schema_version\": %d, \"issue_number\": N}`. Result (`DispatchResult`)", run.DispatchSchemaVersion),
		fmt.Sprintf("{\"schema_version\": %d, \"issue_number\": N, \"reviewed_head_oid\": \"...\"", run.ReviewSchemaVersion),
		fmt.Sprintf("{\"schema_version\": %d, \"issue_number\": N, \"trigger\": \"...\", \"detail\": \"...\"}", run.EscalateSchemaVersion),
	}
	for _, phrase := range phrases {
		if !strings.Contains(delivery, normalizeWhitespace(phrase)) {
			t.Errorf("%s does not contain Selection wire contract %q", deliverySkillPath, phrase)
		}
	}
	status := fmt.Sprintf("`orch run status --json` returns `StatusDoc` schema_version `%d`; reject any other before reading its Selection-bearing run state.", run.StatusSchemaVersion)
	if content := normalizeWhitespace(readFile(t, architectSkillPath)); !strings.Contains(content, normalizeWhitespace(status)) {
		t.Errorf("%s does not contain status wire contract %q", architectSkillPath, status)
	}
}

// branchScopeGuidancePhrases are the exact phrases a delivery skill must
// state about branch-scoped verifications (memhub task 87, extended by
// task 105): a verification whose text describes the branch as a
// whole — commit counts, file counts, diff totals, or scope claims —
// goes false on the next push and must be resubmitted every review
// cycle; `branch-scope:` is the literal name-prefix convention for
// marking such an entry, and it must be chosen at pr-open — the
// verification's first submission — because the `name` is the
// identity a caller-supplied entry's replace-by-name upsert matches
// on, so a prefix added only on a later cycle appends a second entry
// instead of replacing the unprefixed original; the engine's own
// `review-cycle-<n>` entries are the exception, appended rather than
// replaced every cycle; and the `branch-scope:` prefix does not
// collide with the engine-owned names.
//
// These phrases pin the presence of this instruction, not its
// meaning: NormalizeWhitespace makes the comparison tolerant of a
// markdown reflow hard-wrapping a phrase across a line break, but a
// skill author could still reword every unpinned sentence around
// these phrases, or drift the paragraph's overall sense while every
// pinned phrase survives verbatim, without this check noticing. It
// catches deletion of the guidance, not corruption of its meaning.
// CheckBranchScopeVerificationGuidance also reads the entire skill
// file, not any one section of it, so these phrases would still pass
// if a skill relocated them out of the review step entirely — which is
// exactly what task 105 did, moving the naming instruction to the
// pr-open step where the name is first chosen; the check pins that the
// words appear somewhere in the file, never that they appear at a
// particular step.
var branchScopeGuidancePhrases = []string{
	"must be named with the `branch-scope:` prefix at this first submission, not on a later cycle",
	"the `name` is the identity `orch run review`'s replace-by-name upsert matches on, so a prefix added later appends a second entry instead of replacing the original, and the unprefixed original persists in the audit record permanently",
	"This prefix does not collide with the engine-owned names `required-ci`, `merge`, `abandoned`, and `review-cycle-<n>`",
	"describes the branch as a whole — commit counts, file counts, diff totals, or scope claims — becomes false as soon as the executor pushes a fix commit, and must be resubmitted on every subsequent review cycle",
	"caller-supplied verification entries are replace-by-name upserts: submitting the same `name` again on `orch run review` re-stamps that entry at the live head and supersedes its stale text",
	"the engine's own `review-cycle-<n>` entries are appended, not replaced, each cycle",
}

// CheckBranchScopeVerificationGuidance pins deliverySkillPath's entire
// text against the exact branch-scope naming and resubmission guidance
// above, wherever in the file each phrase appears. The comparison runs
// on whitespace-normalized text on both sides, so a phrase a markdown
// reflow happens to hard-wrap across a line break still counts as
// present.
func CheckBranchScopeVerificationGuidance(t *testing.T, deliverySkillPath string) {
	t.Helper()
	content := normalizeWhitespace(readFile(t, deliverySkillPath))
	for _, phrase := range branchScopeGuidancePhrases {
		if !strings.Contains(content, normalizeWhitespace(phrase)) {
			t.Errorf("%s does not contain the branch-scope verification guidance phrase %q", deliverySkillPath, phrase)
		}
	}
}

// blastRadiusClauses are the clauses of run.BlastRadiusCriterion every
// shipped delivery skill and reviewer definition must state verbatim:
// the criterion's provenance (the engine contributed it, the plan
// document did not) and each of the three demands it makes.
//
// CheckBlastRadiusCriterionGuidance asserts each one against
// run.BlastRadiusCriterion itself as well as against the shipped files,
// which is what makes this a two-sided pin rather than another prose
// presence check: rewording the criterion the binary contributes fails
// here just as deleting the instruction describing it does, so the
// engine's demand and the shipped prose cannot drift apart in either
// direction.
var blastRadiusClauses = []string{
	"contributed by Orch because this issue declares a risk domain, not by the plan document",
	"name every element of the structure this change touches and state, for each, whether the behavior it had before this change still holds",
	"record a behavior this change removes as a before-and-after in the same document that stated the old behavior, rather than deleting that statement",
	"where a restriction is attributed to one named symbol, establish whether it holds for that symbol alone or for every symbol of its kind, and say which",
}

// blastRadiusEvidence is the sentence naming what settles the
// contributed criterion. It is required of the shipped files only, not
// of run.BlastRadiusCriterion: the criterion states what to do, and this
// states what a reviewer accepts as having done it — a criterion is not
// its own evidence rule.
const blastRadiusEvidence = "What settles it is an enumeration in the pull request body naming each element the change touched and its before-and-after; passing tests do not settle it."

// CheckBlastRadiusCriterionGuidance pins each path's whole text against
// run.BlastRadiusCriterion's clauses and the evidence sentence above.
// Callers pass their own host's delivery skill and both reviewer agent
// definitions; the files are read raw, so a Codex definition's prose
// inside a TOML string is checked the same way a Claude definition's
// markdown body is.
//
// Comparison runs on whitespace-normalized text on both sides, so a
// clause a markdown reflow or a TOML line wrap happens to split across a
// line break still counts as present. As with every pin in this package,
// it catches deletion or rewording of the words and nothing more: no
// check here observes whether an executor or reviewer that reads the
// criterion acts on it.
func CheckBlastRadiusCriterionGuidance(t *testing.T, paths ...string) {
	t.Helper()
	criterion := normalizeWhitespace(run.BlastRadiusCriterion)
	for _, clause := range blastRadiusClauses {
		if !strings.Contains(criterion, normalizeWhitespace(clause)) {
			t.Errorf("run.BlastRadiusCriterion no longer states %q; the engine's criterion and the shipped prose pinned to it have diverged", clause)
		}
	}
	for _, path := range paths {
		content := normalizeWhitespace(readFile(t, path))
		for _, clause := range blastRadiusClauses {
			if !strings.Contains(content, normalizeWhitespace(clause)) {
				t.Errorf("%s does not state the blast-radius criterion clause %q", path, clause)
			}
		}
		if !strings.Contains(content, normalizeWhitespace(blastRadiusEvidence)) {
			t.Errorf("%s does not state what evidence settles the blast-radius criterion: %q", path, blastRadiusEvidence)
		}
	}
}

// CheckMatcherEqualsGuardTools asserts matcher (a "|"-joined hook
// matcher string) names exactly the tools in want, in both directions:
// the matcher must name every guard-handled tool, and name nothing
// else, since guard denies any other tool_name by default (broadening
// the matcher without extending guard would hard-deny at the hook,
// never reaching guard's own decision).
func CheckMatcherEqualsGuardTools(t *testing.T, matcher string, want []string) {
	t.Helper()
	got := strings.Split(matcher, "|")
	sort.Strings(got)

	wantSorted := append([]string(nil), want...)
	sort.Strings(wantSorted)

	if len(got) != len(wantSorted) {
		t.Fatalf("matcher tools = %v, want %v", got, wantSorted)
	}
	for i := range got {
		if got[i] != wantSorted[i] {
			t.Errorf("matcher tools = %v, want %v", got, wantSorted)
			break
		}
	}
}
