package interview

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"

	"github.com/kninetimmy/orch/internal/config"
	"github.com/kninetimmy/orch/internal/instructions"
	"github.com/kninetimmy/orch/internal/question"
)

// Question IDs for `orch configure`'s picker document (doc 1). Every
// other question id this interview asks is either idHostClaudeEnabled/
// idHostCodexEnabled, a roleModelID/roleEffortID pair (sequence.go, the
// same ids init uses), or one of the four settings ids — all reused
// verbatim so a picked, still-committed value round-trips through the
// exact same materializeHost/parseConcurrency ingestion init already
// has.
const (
	idPickHosts       = "pick.hosts"
	idPickRolesClaude = "pick.roles.claude"
	idPickRolesCodex  = "pick.roles.codex"
)

// NextConfigure derives the next document for the `orch configure`
// interview (PRD §17/§18 applied to editing an already-committed
// configuration rather than a fresh bootstrap). Like Next it keeps
// Facts — git/gh readiness and the Detection audit record the terminal
// Complete document carries forward — but, like NextConfigureLocal,
// every question default is seeded from the committed configuration,
// read via os.ReadFile + config.Parse up front (never config.Load,
// which would layer in a machine-local override: `orch configure`
// must never treat a local preference as "already committed").
// NextConfigure propagates config.ErrNotInitialized when no committed
// configuration exists yet.
func NextConfigure(facts Facts, answers map[string]string, repoRoot string) (question.Document, error) {
	if answers == nil {
		answers = map[string]string{}
	}

	committedRaw, err := readCommittedRaw(repoRoot)
	if err != nil {
		return question.Document{}, err
	}
	committed, err := config.Parse(committedRaw)
	if err != nil {
		return question.Document{}, fmt.Errorf("%s: %w", config.Path, err)
	}

	seq, err := buildSequenceConfigure(facts, committed, answers)
	if err != nil {
		return question.Document{}, err
	}
	complete := allDocsAnswered(seq, answers)

	applicable := applicableQuestions(seq, complete)
	if err := validateAnswers(applicable, answers); err != nil {
		return question.Document{}, err
	}

	for i, d := range seq {
		if !d.allAnswered(answers) {
			return question.Document{
				SchemaVersion: question.SchemaVersion,
				Kind:          question.DocQuestions,
				Progress:      &question.Progress{Index: i + 1, Total: len(seq)},
				Questions:     d.questions,
			}, nil
		}
	}

	return nextAfterSequenceConfigure(facts, committed, committedRaw, answers, repoRoot)
}

// nextAfterSequenceConfigure handles NextConfigure's tail, mirroring
// nextAfterSequence's approval/summary/complete shape.
func nextAfterSequenceConfigure(facts Facts, committed *config.Config, committedRaw []byte, answers map[string]string, repoRoot string) (question.Document, error) {
	cfg, err := materializeConfigure(committed, answers)
	if err != nil {
		return question.Document{}, err
	}
	summary, err := buildConfigureSummary(cfg, committed, committedRaw, repoRoot)
	if err != nil {
		return question.Document{}, err
	}

	approvalVal, answered := answers[idApproval]
	if len(summary.Blockers) > 0 {
		if answered {
			return question.Document{}, fmt.Errorf("%w: %s", ErrApprovalBlocked, joinBlockers(summary.Blockers))
		}
		return question.Document{
			SchemaVersion: question.SchemaVersion,
			Kind:          question.DocSummary,
			Summary:       &summary,
		}, nil
	}

	if !answered {
		return question.Document{
			SchemaVersion: question.SchemaVersion,
			Kind:          question.DocSummary,
			Questions:     []question.Question{approvalQuestionConfigure()},
			Summary:       &summary,
		}, nil
	}

	if approvalVal == "approve" {
		return question.Document{
			SchemaVersion: question.SchemaVersion,
			Kind:          question.DocComplete,
			Complete:      buildComplete(summary, facts),
		}, nil
	}
	// ValidateAnswer already restricted approvalVal to "approve" or
	// "abort" before this point is reached.
	return question.Document{SchemaVersion: question.SchemaVersion, Kind: question.DocAborted}, nil
}

// readCommittedRaw reads repoRoot's committed configuration bytes
// verbatim — never through config.Load, which would layer in the very
// local overrides `orch configure` must not seed from. NextConfigure
// both config.Parse's these bytes for its seed values and diffs them
// against the freshly materialized render for Summary.ConfigDiff. A
// missing file is config.ErrNotInitialized.
func readCommittedRaw(repoRoot string) ([]byte, error) {
	data, err := os.ReadFile(filepath.Join(repoRoot, filepath.FromSlash(config.Path)))
	if errors.Is(err, fs.ErrNotExist) {
		return nil, config.ErrNotInitialized
	}
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", config.Path, err)
	}
	return data, nil
}

// buildSequenceConfigure derives the ordered list of "questions"-kind
// documents `orch configure`'s picker doc and subsequent answers
// produce. Doc 1 is always the section picker (pickerDocConfigure, up
// to four questions: pick.hosts, pick.roles.claude/pick.roles.codex for
// each committed-enabled host, and pick.settings — every default
// "no", contract call 2). If pick.hosts is answered "yes", init's own
// host-toggle doc follows, re-asking enablement with defaults sourced
// from committed enablement rather than facts-detected CLI presence;
// ErrNoHostEnabled applies the same "at least one host" rule
// buildSequence enforces once both toggle answers are known.
//
// A host this sequence newly enables always gets its six role
// documents next, defaulted from the PRD §10 profiles
// (defaultProfileFor) exactly as init would. A host that was already
// committed-enabled and stays enabled gets them only when its
// pick.roles.<host> question was answered "yes", defaulted from its
// current committed profile instead (committedRoleDefaults). A
// disabled host's role documents are never included, so any stale role
// answer for it is rejected by the existing ErrUnknownAnswer walk
// (validateAnswers). The settings document
// (concurrency/merge/memhub/metrics) is included only when
// pick.settings is answered "yes", defaulted from the committed
// configuration's current values (committedSettingsDefaults).
func buildSequenceConfigure(facts Facts, committed *config.Config, answers map[string]string) ([]docSpec, error) {
	docs := []docSpec{pickerDocConfigure(committed)}

	claudeCommitted := committedHostConfig(committed, "claude") != nil
	codexCommitted := committedHostConfig(committed, "codex") != nil
	claudeEnabled := claudeCommitted
	codexEnabled := codexCommitted

	if answers[idPickHosts] == "yes" {
		docs = append(docs, hostToggleDoc(facts, boolValue(claudeCommitted), boolValue(codexCommitted)))

		claudeVal, claudeKnown := answers[idHostClaudeEnabled]
		codexVal, codexKnown := answers[idHostCodexEnabled]
		claudeEnabled = claudeKnown && claudeVal == "yes"
		codexEnabled = codexKnown && codexVal == "yes"

		if claudeKnown && codexKnown && !claudeEnabled && !codexEnabled {
			return nil, ErrNoHostEnabled
		}
	}

	if claudeEnabled {
		switch {
		case !claudeCommitted:
			docs = append(docs, roleDocSpecs("claude", true, defaultProfileFor("claude"))...)
		case answers[idPickRolesClaude] == "yes":
			docs = append(docs, roleDocSpecs("claude", true, committedRoleDefaults(committedHostConfig(committed, "claude")))...)
		}
	}
	if codexEnabled {
		showExplain := !claudeEnabled
		switch {
		case !codexCommitted:
			docs = append(docs, roleDocSpecs("codex", showExplain, defaultProfileFor("codex"))...)
		case answers[idPickRolesCodex] == "yes":
			docs = append(docs, roleDocSpecs("codex", showExplain, committedRoleDefaults(committedHostConfig(committed, "codex")))...)
		}
	}

	if answers[idPickSettings] == "yes" {
		docs = append(docs, settingsDoc(committedSettingsDefaults(committed)))
	}

	return docs, nil
}

// pickerDocConfigure is `orch configure`'s doc 1: whether to review or
// change host enablement, each committed-enabled host's role profiles,
// and the settings area — every default "no" (contract call 2: `orch
// configure` never assumes a change is wanted).
func pickerDocConfigure(committed *config.Config) docSpec {
	qs := []question.Question{
		{
			ID:      idPickHosts,
			Header:  "Hosts",
			Prompt:  "Review or change which hosts are enabled?",
			Kind:    question.KindSelect,
			Default: "no",
			Options: yesNoOptions("no"),
		},
	}
	if committedHostConfig(committed, "claude") != nil {
		qs = append(qs, question.Question{
			ID:      idPickRolesClaude,
			Header:  "Claude",
			Prompt:  "Review or change Claude Code's role profiles?",
			Kind:    question.KindSelect,
			Default: "no",
			Options: yesNoOptions("no"),
		})
	}
	if committedHostConfig(committed, "codex") != nil {
		qs = append(qs, question.Question{
			ID:      idPickRolesCodex,
			Header:  "Codex",
			Prompt:  "Review or change Codex CLI's role profiles?",
			Kind:    question.KindSelect,
			Default: "no",
			Options: yesNoOptions("no"),
		})
	}
	qs = append(qs, question.Question{
		ID:      idPickSettings,
		Header:  "Settings",
		Prompt:  "Review or change concurrency/merge/memhub/metrics settings?",
		Kind:    question.KindSelect,
		Default: "no",
		Options: yesNoOptions("no"),
	})
	return docSpec{questions: qs}
}

// committedRoleDefaults returns a role-defaults source drawing from
// h's current committed profiles — roleDocSpecs' defaults source for a
// host that was already committed-enabled and stays enabled, so
// re-editing its role profiles starts from what is already committed
// rather than the PRD §10 defaults.
func committedRoleDefaults(h *config.Host) func(string) profile {
	return func(role string) profile {
		p := committedProfile(h, role)
		return profile{model: p.Model, effort: p.Effort}
	}
}

// committedSettingsDefaults returns settingsDoc's defaults sourced
// from committed's current concurrency/merge/memhub/metrics values —
// `orch configure`'s counterpart to initSettingsDefaults.
func committedSettingsDefaults(committed *config.Config) settingsDefaults {
	return settingsDefaults{
		maxSubagents:   strconv.Itoa(committed.Concurrency.MaxSubagents),
		mergeStrategy:  committed.Merge.Strategy,
		memhubMode:     committed.Memhub.Mode,
		metricsEnabled: boolValue(committed.Metrics.Enabled),
	}
}

// approvalQuestionConfigure is approvalQuestion with `orch configure`'s
// own prompt: this is an edit to an already-committed configuration
// delivered through a PR (PRD §17), not init's first-time
// configuration-and-bootstrap plan. The ID and options are identical,
// so applicableQuestions' shared registration (which uses
// approvalQuestion) validates the same answer values either way.
func approvalQuestionConfigure() question.Question {
	q := approvalQuestion()
	q.Prompt = "Approve this configuration change for delivery?"
	return q
}

// hostEnabledID returns host's host-toggle question id (idHostClaudeEnabled/
// idHostCodexEnabled), or "" for an unrecognized host.
func hostEnabledID(host string) string {
	switch host {
	case "claude":
		return idHostClaudeEnabled
	case "codex":
		return idHostCodexEnabled
	default:
		return ""
	}
}

// setConfigHost sets cfg's *Host for the named host to h (nil to
// disable).
func setConfigHost(cfg *config.Config, host string, h *config.Host) {
	switch host {
	case "claude":
		cfg.Hosts.Claude = h
	case "codex":
		cfg.Hosts.Codex = h
	}
}

// deepCopyConfig returns a shallow copy of committed with its own,
// independent *Host copies for claude and codex — so materializeConfigure
// can freely overwrite cfg.Hosts.* without ever mutating committed
// in place (config's own overlay.go mergeOverride precedent).
func deepCopyConfig(committed *config.Config) *config.Config {
	cfg := *committed
	if committed.Hosts.Claude != nil {
		h := *committed.Hosts.Claude
		cfg.Hosts.Claude = &h
	}
	if committed.Hosts.Codex != nil {
		h := *committed.Hosts.Codex
		cfg.Hosts.Codex = &h
	}
	return &cfg
}

// applyHostConfigure applies host's answered keys onto cfg (already a
// deepCopyConfig of committed): a "no" toggle answer disables the
// host; a present role answer (buildSequenceConfigure only ever
// includes a host's role doc when this session means to set one)
// rebuilds it via materializeHost, reusing the exact ingestion
// (setRoleProfile/validateModelFreeText) init's own materializeHost
// already owns; otherwise host's already-deep-copied value (nil or the
// committed profile) is left untouched.
func applyHostConfigure(cfg *config.Config, host string, answers map[string]string) error {
	toggleVal, toggleKnown := answers[hostEnabledID(host)]
	_, hasRoleAnswers := answers[roleModelID(host, "architect")]

	switch {
	case toggleKnown && toggleVal == "no":
		setConfigHost(cfg, host, nil)
	case hasRoleAnswers:
		h, err := materializeHost(host, answers)
		if err != nil {
			return err
		}
		setConfigHost(cfg, host, h)
	}
	return nil
}

// materializeConfigure turns committed plus this session's answers
// into the edited *config.Config: a deep copy of committed with every
// answered key applied (applyHostConfigure per host, then the four
// settings), a recomputed Revision, and a Render/Parse round-trip
// self-check — the same discipline materialize applies to a fresh
// init configuration.
func materializeConfigure(committed *config.Config, answers map[string]string) (*config.Config, error) {
	cfg := deepCopyConfig(committed)

	for _, host := range []string{"claude", "codex"} {
		if err := applyHostConfigure(cfg, host, answers); err != nil {
			return nil, err
		}
	}

	if v, ok := answers[idMaxSubagents]; ok {
		n, err := parseConcurrency(v)
		if err != nil {
			return nil, err
		}
		cfg.Concurrency.MaxSubagents = n
	}
	if v, ok := answers[idMergeStrategy]; ok {
		cfg.Merge.Strategy = v
	}
	if v, ok := answers[idMemhubMode]; ok {
		cfg.Memhub.Mode = v
	}
	if v, ok := answers[idMetricsEnabled]; ok {
		cfg.Metrics.Enabled = v == "yes"
	}

	rev, err := config.Revision(cfg)
	if err != nil {
		return nil, fmt.Errorf("compute configuration revision: %w", err)
	}
	cfg.ConfigRevision = rev

	rendered, err := config.Render(cfg)
	if err != nil {
		return nil, fmt.Errorf("render generated configuration: %w", err)
	}
	if _, err := config.Parse(rendered); err != nil {
		return nil, fmt.Errorf("materialized configuration failed its own round-trip check: %w", err)
	}

	return cfg, nil
}

// disabledInstructionFiles lists, in claude-then-codex order, the root
// instruction file name for every host committed enables but cfg does
// not — the hosts this configure change disables.
func disabledInstructionFiles(committed, cfg *config.Config) []string {
	var names []string
	if committed.Hosts.Claude != nil && cfg.Hosts.Claude == nil {
		names = append(names, InstructionFile("claude"))
	}
	if committed.Hosts.Codex != nil && cfg.Hosts.Codex == nil {
		names = append(names, InstructionFile("codex"))
	}
	return names
}

// allFileChangesUnchanged reports whether every FileChange in files
// carries an empty Diff — Diff == "" only when the underlying
// instructions.Change was ActionNone (instructions.Change's own doc
// comment), so this is buildConfigureSummary's "all FileChanges
// ActionNone" check without needing to thread the Action value itself
// through planInstructionFiles' question.FileChange conversion.
func allFileChangesUnchanged(files []question.FileChange) bool {
	for _, f := range files {
		if f.Diff != "" {
			return false
		}
	}
	return true
}

// buildConfigureSummary materializes cfg's rendered TOML and proposed
// instruction-file changes into a question.Summary for `orch
// configure` (PRD §18 steps 7-8 applied to an edit rather than a fresh
// bootstrap): every host cfg enables gets instructions.PlanFile
// (install, or — the day a version 2 exists — an upgrade diff, PRD §19
// "upgrade blocks only through Delivery"); every host this change
// disables instead gets instructions.PlanRemoveFile, block-only
// (DeleteWholeFile is deliberately never consulted here: whole-file
// deletion / deinit is out of scope, contract call 3). ConfigDiff
// carries the committed-vs-new TOML diff for display; it is kept out
// of Files so bootstrap's writeFiles is reused unchanged and
// config.toml is never double-written. The no-change blocker compares
// bytes, not revision, so a hand-denormalized committed file can still
// be re-canonicalized through an otherwise-empty configure change.
func buildConfigureSummary(cfg, committed *config.Config, committedRaw []byte, repoRoot string) (question.Summary, error) {
	rendered, err := config.Render(cfg)
	if err != nil {
		return question.Summary{}, fmt.Errorf("render configuration for summary: %w", err)
	}

	enabledFiles, enabledBlockers, err := planInstructionFiles(repoRoot, applicableInstructionFiles(cfg), instructions.PlanFile)
	if err != nil {
		return question.Summary{}, err
	}
	removedFiles, removedBlockers, err := planInstructionFiles(repoRoot, disabledInstructionFiles(committed, cfg), instructions.PlanRemoveFile)
	if err != nil {
		return question.Summary{}, err
	}
	files := append(enabledFiles, removedFiles...)
	blockers := append(enabledBlockers, removedBlockers...)

	conflicts, err := instructions.Scan(repoRoot)
	if err != nil {
		return question.Summary{}, fmt.Errorf("scan for nested instruction conflicts: %w", err)
	}
	var conflictLines []string
	for _, c := range conflicts {
		rel, relErr := filepath.Rel(repoRoot, c.Path)
		if relErr != nil {
			rel = c.Path
		}
		conflictLines = append(conflictLines, fmt.Sprintf("%s: %s", filepath.ToSlash(rel), c.Report.Detail))
	}

	gitignore, err := gitignoreLines(repoRoot, cfg.Metrics.Enabled)
	if err != nil {
		return question.Summary{}, err
	}

	if len(blockers) == 0 && bytes.Equal(rendered, committedRaw) && allFileChangesUnchanged(files) && len(gitignore) == 0 {
		blockers = append(blockers, "no configuration changes; nothing to deliver")
	}

	return question.Summary{
		ConfigTOML:     string(rendered),
		ConfigRevision: cfg.ConfigRevision,
		Files:          files,
		GitignoreLines: gitignore,
		Conflicts:      conflictLines,
		Blockers:       blockers,
		ConfigDiff:     instructions.UnifiedDiff(string(committedRaw), string(rendered)),
	}, nil
}
