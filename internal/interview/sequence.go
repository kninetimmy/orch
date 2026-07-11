package interview

import (
	"fmt"
	"strings"

	"github.com/kninetimmy/orch/internal/question"
)

// Question IDs are the exact TOML keys the answer materializes onto
// (materialize.go), so an adapter's answers map doubles as a preview
// of the committed configuration's shape.
const (
	idHostClaudeEnabled = "host.claude.enabled"
	idHostCodexEnabled  = "host.codex.enabled"
	idMaxSubagents      = "concurrency.max_subagents"
	idMergeStrategy     = "merge.strategy"
	idMemhubMode        = "memhub.mode"
	idMetricsEnabled    = "metrics.enabled"
	idApproval          = "approval"
)

// roleModelID and roleEffortID build a role's two question ids.
func roleModelID(host, role string) string  { return fmt.Sprintf("host.%s.role.%s.model", host, role) }
func roleEffortID(host, role string) string { return fmt.Sprintf("host.%s.role.%s.effort", host, role) }

// roleSpec is one PRD §9 role's fixed display and explanatory text.
type roleSpec struct {
	key     string // the TOML role key
	header  string // Question.Header (<= 12 chars)
	label   string // human-readable role name for Prompt text
	explain string // §18 step 4 role explanation, used as the model question's Preamble
}

// roleSpecs lists every role in config.Roles' field order — the same
// order validate.go's validateHost and render.go's roleOrder walk.
var roleSpecs = []roleSpec{
	{
		key: "architect", header: "Architect", label: "Architect",
		explain: "Architect: requirements discovery, architecture and design, issue decomposition, routing and escalation, the plan gate, and final review judgment. Never edits tracked files directly.",
	},
	{
		key: "scout", header: "Scout", label: "Scout",
		explain: "Scout: read-only file/symbol discovery, code-path tracing, documentation lookup, and concise summaries for the Architect. Scouts may run concurrently.",
	},
	{
		key: "implementer", header: "Implementer", label: "Implementer",
		explain: "Implementer: executes narrow, approved, normally difficult changes, following the approved architecture, only inside its assigned worktree.",
	},
	{
		key: "specialist", header: "Specialist", label: "Specialist",
		explain: "Specialist: executes approved changes where implementation itself is unusually difficult — concurrency, auth, migrations, performance, cross-cutting refactors.",
	},
	{
		key: "reviewer", header: "Reviewer", label: "Reviewer",
		explain: "Reviewer: independently verifies acceptance criteria, scope discipline, correctness, test evidence, CI state, and security/data-safety concerns.",
	},
	{
		key: "review_downgrade", header: "Downgrade", label: "Safe review downgrade",
		explain: "Safe review downgrade: a weaker model may review only when a change is affirmatively mechanical, low-risk, fully specified, and unsurprising.",
	},
}

// profile pairs an exact model version with a reasoning effort.
type profile struct{ model, effort string }

// defaultProfiles hardcodes the PRD §10 default model profiles, keyed
// by host then role. defaultProfilesTest asserts these against the
// PRD table verbatim.
var defaultProfiles = map[string]map[string]profile{
	"codex": {
		"architect":        {"gpt-5.6-sol", "high"},
		"scout":            {"gpt-5.6-terra", "low"},
		"implementer":      {"gpt-5.6-terra", "high"},
		"specialist":       {"gpt-5.6-sol", "medium"},
		"reviewer":         {"gpt-5.6-sol", "medium"},
		"review_downgrade": {"gpt-5.6-terra", "high"},
	},
	"claude": {
		"architect":        {"claude-opus-4-8", "xhigh"},
		"scout":            {"claude-sonnet-5", "low"},
		"implementer":      {"claude-sonnet-5", "xhigh"},
		"specialist":       {"claude-opus-4-8", "high"},
		"reviewer":         {"claude-opus-4-8", "high"},
		"review_downgrade": {"claude-sonnet-5", "high"},
	},
}

// hostModels lists each host's distinct committed-config model
// choices, in a fixed display order, so model select options are
// deterministic. This is deliberately not the local-override-only
// third Claude model (claude-fable-5, PRD §10): the committed
// configuration this interview writes never defaults to it.
var hostModels = map[string][]string{
	"codex":  {"gpt-5.6-sol", "gpt-5.6-terra"},
	"claude": {"claude-opus-4-8", "claude-sonnet-5"},
}

// hostEfforts lists each host's closed effort enum, in the same order
// validate.go's effortList documents.
var hostEfforts = map[string][]string{
	"codex":  {"low", "medium", "high"},
	"claude": {"low", "medium", "high", "xhigh"},
}

// hostLabels is each host key's human-readable display name.
var hostLabels = map[string]string{
	"codex":  "Codex CLI",
	"claude": "Claude Code",
}

// assistDeliveryExplanation is the §18 step 3 explanation, shown as
// the Preamble of the very first question in the interview.
const assistDeliveryExplanation = "Orch has two modes: Assist (read-only; the default — the Architect and Scouts may inspect and explain, but repository mutation is mechanically denied) and Delivery (a mutation request is investigated read-only, then approved work happens in an isolated per-issue worktree with its own branch and PR; merges are always human-approved). Enable each host CLI you want to drive Orch through; a host can be added later through Delivery."

// docSpec is one question.Document's fixed set of grouped, mutually
// independent Questions — the unit buildSequence assembles and Next
// walks one at a time.
type docSpec struct {
	questions []question.Question
}

// allAnswered reports whether every one of d's questions has a key in
// answers. It does not itself judge the value's validity — Next
// validates every present answer before this scan ever runs, so by the
// time allAnswered is consulted, a present key is already known legal.
func (d docSpec) allAnswered(answers map[string]string) bool {
	for _, q := range d.questions {
		if _, ok := answers[q.ID]; !ok {
			return false
		}
	}
	return true
}

// buildSequence derives the ordered list of "questions"-kind documents
// that apply given facts and the host-toggle answers already present
// in answers (PRD §18 steps 3-6, fixed order, claude before codex).
// Role documents for a host are included only once that host's
// enabled toggle is answered "yes"; the settings document (doc 14) is
// included only once both toggles are answered at all. It returns
// ErrNoHostEnabled as soon as both toggles are known and both are
// "no" — the same rule config.validate enforces on the committed file,
// checked here before any role question is ever asked.
func buildSequence(facts Facts, answers map[string]string) ([]docSpec, error) {
	docs := []docSpec{hostToggleDoc(facts)}

	claudeVal, claudeKnown := answers[idHostClaudeEnabled]
	codexVal, codexKnown := answers[idHostCodexEnabled]
	claudeEnabled := claudeKnown && claudeVal == "yes"
	codexEnabled := codexKnown && codexVal == "yes"

	if claudeKnown && codexKnown && !claudeEnabled && !codexEnabled {
		return nil, ErrNoHostEnabled
	}

	if claudeEnabled {
		docs = append(docs, roleDocSpecs("claude", true)...)
	}
	if codexEnabled {
		docs = append(docs, roleDocSpecs("codex", !claudeEnabled)...)
	}
	if claudeKnown && codexKnown {
		docs = append(docs, settingsDoc(facts))
	}
	return docs, nil
}

// hostToggleDoc is doc 1: whether to enable Claude Code and Codex CLI,
// defaulted from Facts and carrying the §18 step 3 explanation on its
// first question.
func hostToggleDoc(facts Facts) docSpec {
	claudeDefault := boolValue(facts.ClaudeCLI)
	codexDefault := boolValue(facts.CodexCLI)

	claudeQ := question.Question{
		ID:       idHostClaudeEnabled,
		Header:   "Claude",
		Prompt:   "Enable Claude Code as a host?",
		Preamble: assistDeliveryExplanation,
		Hint:     detectionHint("claude CLI", facts.ClaudeCLI),
		Kind:     question.KindSelect,
		Default:  claudeDefault,
		Options:  yesNoOptions(claudeDefault),
	}
	codexQ := question.Question{
		ID:      idHostCodexEnabled,
		Header:  "Codex",
		Prompt:  "Enable Codex CLI as a host?",
		Hint:    detectionHint("codex CLI", facts.CodexCLI),
		Kind:    question.KindSelect,
		Default: codexDefault,
		Options: yesNoOptions(codexDefault),
	}
	return docSpec{questions: []question.Question{claudeQ, codexQ}}
}

// roleDocSpecs builds host's six per-role documents (model + effort,
// grouped), in roleSpecs order. showExplain controls whether each
// model question carries its role's §18 step 4 explanation as a
// Preamble: claude's docs always show it; codex's docs show it only
// when claude was not enabled (so a solo-Codex interview still walks
// the role explanations once, and a both-hosts interview does not
// repeat them).
func roleDocSpecs(host string, showExplain bool) []docSpec {
	docs := make([]docSpec, 0, len(roleSpecs))
	for _, rs := range roleSpecs {
		def := defaultProfiles[host][rs.key]
		hostLabel := hostLabels[host]

		modelQ := question.Question{
			ID:       roleModelID(host, rs.key),
			Header:   rs.header,
			Prompt:   fmt.Sprintf("%s model (%s)", rs.label, hostLabel),
			Kind:     question.KindSelect,
			Options:  modelOptions(host, def.model),
			FreeText: true,
			Default:  def.model,
		}
		if showExplain {
			modelQ.Preamble = rs.explain
		}

		effortQ := question.Question{
			ID:      roleEffortID(host, rs.key),
			Header:  rs.header,
			Prompt:  fmt.Sprintf("%s reasoning effort (%s)", rs.label, hostLabel),
			Kind:    question.KindSelect,
			Options: effortOptions(host, def.effort),
			Default: def.effort,
		}

		docs = append(docs, docSpec{questions: []question.Question{modelQ, effortQ}})
	}
	return docs
}

// modelOptions lists host's committed-config model choices, marking
// def as Recommended.
func modelOptions(host, def string) []question.Option {
	models := hostModels[host]
	opts := make([]question.Option, len(models))
	for i, m := range models {
		opts[i] = question.Option{Value: m, Label: m, Recommended: m == def}
	}
	return opts
}

// effortOptions lists host's closed effort enum, marking def as
// Recommended.
func effortOptions(host, def string) []question.Option {
	efforts := hostEfforts[host]
	opts := make([]question.Option, len(efforts))
	for i, e := range efforts {
		opts[i] = question.Option{Value: e, Label: effortLabel(e), Recommended: e == def}
	}
	return opts
}

// effortLabel title-cases an effort enum value for display.
func effortLabel(effort string) string {
	if effort == "xhigh" {
		return "Extra high"
	}
	return strings.ToUpper(effort[:1]) + effort[1:]
}

// settingsDoc is doc 14: concurrency, merge strategy, memhub mode, and
// metrics — the four independent PRD §14/§16/§20/§21 settings,
// grouped in one document since a Document carries up to four
// questions. memhub.mode defaults to "best-effort" when memhub is
// detected and healthy, else "off" (contract call 5; "required" is one
// arrow-key away, described as the memhub-first-repo choice).
func settingsDoc(facts Facts) docSpec {
	memhubDefault := "off"
	if facts.MemhubHealthy {
		memhubDefault = "best-effort"
	}

	concurrency := question.Question{
		ID:       idMaxSubagents,
		Header:   "Concurrency",
		Prompt:   "Maximum concurrent subagents",
		Preamble: "Concurrency caps how many subagents run at once across both hosts (PRD §14; default 3).",
		Kind:     question.KindSelect,
		FreeText: true,
		Default:  "3",
		Options: []question.Option{
			{Value: "1", Label: "1"},
			{Value: "2", Label: "2"},
			{Value: "3", Label: "3", Recommended: true},
			{Value: "4", Label: "4"},
		},
	}
	merge := question.Question{
		ID:       idMergeStrategy,
		Header:   "Merge",
		Prompt:   "Merge strategy for approved PRs",
		Preamble: "Merge strategy applies once a human gives explicit merge approval (PRD §16; default squash).",
		Kind:     question.KindSelect,
		Default:  "squash",
		Options: []question.Option{
			{Value: "squash", Label: "Squash", Recommended: true},
			{Value: "rebase", Label: "Rebase"},
			{Value: "merge-commit", Label: "Merge commit"},
		},
	}
	memhub := question.Question{
		ID:       idMemhubMode,
		Header:   "Memhub",
		Prompt:   "Memhub integration mode",
		Preamble: "Memhub mode controls how Delivery planning treats memhub health (PRD §20): required blocks planning on failure, best-effort records it, off skips the probe.",
		Kind:     question.KindSelect,
		Default:  memhubDefault,
		Options: []question.Option{
			{Value: "required", Label: "Required", Description: "The memhub-first-repo choice: blocks Delivery planning if memhub health or recall fails.", Recommended: memhubDefault == "required"},
			{Value: "best-effort", Label: "Best effort", Recommended: memhubDefault == "best-effort"},
			{Value: "off", Label: "Off", Recommended: memhubDefault == "off"},
		},
	}
	metrics := question.Question{
		ID:       idMetricsEnabled,
		Header:   "Metrics",
		Prompt:   "Enable local metrics",
		Preamble: "Metrics record local, gitignored routing/outcome data for future evaluation (PRD §21); off by default and never transmitted externally.",
		Kind:     question.KindSelect,
		Default:  "no",
		Options: []question.Option{
			{Value: "yes", Label: "Yes"},
			{Value: "no", Label: "No", Recommended: true},
		},
	}
	return docSpec{questions: []question.Question{concurrency, merge, memhub, metrics}}
}

// approvalQuestion is doc 15: the summary document's embedded approval
// choice. It carries no Default — approval is never assumed.
func approvalQuestion() question.Question {
	return question.Question{
		ID:     idApproval,
		Header: "Approve",
		Prompt: "Approve this configuration and bootstrap plan?",
		Kind:   question.KindSelect,
		Options: []question.Option{
			{Value: "approve", Label: "Approve"},
			{Value: "abort", Label: "Abort"},
		},
	}
}

// yesNoOptions returns the standard yes/no Options for a boolean
// question, marking def as Recommended.
func yesNoOptions(def string) []question.Option {
	return []question.Option{
		{Value: "yes", Label: "Yes", Recommended: def == "yes"},
		{Value: "no", Label: "No", Recommended: def == "no"},
	}
}

// boolValue renders a Go bool as the wire "yes"/"no" the sequence uses
// throughout (contract: "booleans are yes/no").
func boolValue(b bool) string {
	if b {
		return "yes"
	}
	return "no"
}

// detectionHint renders a one-line detection status for a host
// toggle's Hint field.
func detectionHint(name string, found bool) string {
	if found {
		return name + " detected on PATH"
	}
	return name + " not detected on PATH"
}
