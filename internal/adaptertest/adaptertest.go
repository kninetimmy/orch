// Package adaptertest is the PRD §23 shared parity layer both host
// adapters' plugin tests consume: fixtures (the committed §10 role
// profiles) and assertion helpers (skill/manifest drift pins) so
// cross-host invariants — the run-verb allowlist, the four
// anti-forgery statement literals, the plan/merge gate option text, the
// setup interview's terminal forms, hook command portability, and
// matcher/guard parity — have exactly one source instead of a copy per
// adapter that can silently drift apart.
//
// This package carries no Test functions and tests nothing of its own;
// it is test-support only, imported by adapters/claude/plugin_test.go
// and adapters/codex/plugin_test.go. Every exported Check* helper takes
// a *testing.T and calls t.Helper(), and reads files relative to the
// caller's own working directory — the same assumption each adapter's
// plugin_test.go already makes, since `go test` runs a package's tests
// with that package's directory as the process cwd.
//
// It may import internal/run (the four statement constants) and
// internal/guard (a caller may pass guard.ClaudeTools()/CodexTools()
// into CheckMatcherEqualsGuardTools) and nothing else in this module —
// it is a leaf test-support package, not a place for adapter-specific
// or engine policy code.
package adaptertest

import (
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"

	"github.com/kninetimmy/orch/internal/run"
)

// RoleSpec is one role's committed host profile: the exact model and
// effort string an installed agent definition must carry.
type RoleSpec struct {
	Model  string
	Effort string
}

// Profile returns the committed §10 profile for host ("claude" or
// "codex"), keyed by role: "scout", "implementer", "specialist",
// "reviewer", "reviewer-safe". Every adapter's agent roster is asserted
// equal to this map so the two hosts' plugin tests and the committed
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
			"scout":         {Model: "claude-sonnet-5", Effort: "low"},
			"implementer":   {Model: "claude-sonnet-5", Effort: "xhigh"},
			"specialist":    {Model: "claude-opus-4-8", Effort: "high"},
			"reviewer":      {Model: "claude-opus-4-8", Effort: "high"},
			"reviewer-safe": {Model: "claude-sonnet-5", Effort: "high"},
		}
	case "codex":
		return map[string]RoleSpec{
			"scout":         {Model: "gpt-5.6-terra", Effort: "low"},
			"implementer":   {Model: "gpt-5.6-terra", Effort: "high"},
			"specialist":    {Model: "gpt-5.6-sol", Effort: "medium"},
			"reviewer":      {Model: "gpt-5.6-sol", Effort: "medium"},
			"reviewer-safe": {Model: "gpt-5.6-terra", Effort: "high"},
		}
	default:
		panic("adaptertest: unknown host " + host)
	}
}

// runVerbTokens is the closed set every `orch run <word>` token found in
// a skill must belong to: the 13 document-taking verbs internal/cli/run.go
// dispatches, plus "status" (orch run status --json, dispatched
// separately but still spelled "orch run status"). Moved verbatim from
// adapters/claude/plugin_test.go so both hosts pin against the same set.
var runVerbTokens = map[string]bool{
	"plan": true, "activate": true, "dispatch": true, "pr-open": true,
	"review": true, "escalate": true, "ci": true, "merge-report": true,
	"merge": true, "block": true, "abandon": true, "cleanup": true,
	"complete": true, "status": true,
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
				t.Errorf("%s: mentions `orch run %s`, which is not one of the 13 verbs or status", path, verb)
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
// question options against the exact PRD §8 four-option set.
func CheckPlanGateOptions(t *testing.T, deliverySkillPath string) {
	t.Helper()
	content := readFile(t, deliverySkillPath)
	for _, opt := range planGateOptions {
		if !strings.Contains(content, opt) {
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
// question options against the exact two-option set.
func CheckMergeGateOptions(t *testing.T, deliverySkillPath string) {
	t.Helper()
	content := readFile(t, deliverySkillPath)
	for _, opt := range mergeGateOptions {
		if !strings.Contains(content, opt) {
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
// forms against the exact three commands each interview ends with.
func CheckSetupTerminalForms(t *testing.T, setupSkillPath string) {
	t.Helper()
	content := readFile(t, setupSkillPath)
	for _, form := range setupTerminalForms {
		if !strings.Contains(content, form) {
			t.Errorf("%s does not contain terminal form %q", setupSkillPath, form)
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
