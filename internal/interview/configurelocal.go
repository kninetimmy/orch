package interview

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/BurntSushi/toml"

	"github.com/kninetimmy/orch/internal/config"
	"github.com/kninetimmy/orch/internal/instructions"
	"github.com/kninetimmy/orch/internal/question"
)

// Question IDs for the picker document. Every other configure-local
// question ID is the exact overlay dotted key it materializes onto
// (localRoleModelID/localRoleEffortID below, and idMaxSubagents/
// idMetricsEnabled reused verbatim from sequence.go — those two already
// equal their own overlay keys) — deliberately unlike init's
// host.<host>.role.<role>.* ids, so an adapter's answers map doubles as
// a preview of the resulting config.local.toml (contract: "configure-local
// question IDs are the true overlay dotted keys").
const (
	idPickClaude   = "pick.claude"
	idPickCodex    = "pick.codex"
	idPickSettings = "pick.settings"
)

// localRoleModelID and localRoleEffortID build a role's two
// configure-local question ids, identical in shape to the dotted keys
// config's leafClasses table classifies (TestConfigureLocalLeafIDsMatchPreferenceKeys
// pins this).
func localRoleModelID(host, role string) string {
	return fmt.Sprintf("hosts.%s.roles.%s.model", host, role)
}
func localRoleEffortID(host, role string) string {
	return fmt.Sprintf("hosts.%s.roles.%s.effort", host, role)
}

// hostLocalModels lists each host's local-override-selectable models:
// hostModels, plus — for claude only — claude-fable-5, the PRD §10
// local-override-only third model (decision 7). The committed
// configuration never defaults to it; only a machine-local override
// may select it.
var hostLocalModels = buildHostLocalModels()

func buildHostLocalModels() map[string][]string {
	m := make(map[string][]string, len(hostModels))
	for host, models := range hostModels {
		copied := append([]string{}, models...)
		if host == "claude" {
			copied = append(copied, "claude-fable-5")
		}
		m[host] = copied
	}
	return m
}

// preferenceKeySet is config.PreferenceKeys' sorted list turned into a
// set, so seedOverrides can classify an arbitrary local-file leaf key
// in O(1) without config exporting its unexported leafClasses table.
var preferenceKeySet = buildPreferenceKeySet()

func buildPreferenceKeySet() map[string]bool {
	set := map[string]bool{}
	for _, k := range config.PreferenceKeys() {
		set[k] = true
	}
	return set
}

// NextConfigureLocal derives the next document for the `orch
// configure-local` interview (PRD §17). Unlike Next it carries no
// Facts: every default comes from the committed configuration and the
// current config.local.toml on disk, never the environment, so both
// are read up front — before any question is asked, not only once the
// sequence completes the way Next's own repoRoot use is scoped
// (documented deviation from the init precedent).
//
// Question IDs equal the exact overlay dotted keys config.PreferenceKeys
// enumerates (the picker's own pick.* ids aside), so an adapter's
// answers map is itself a preview of the resulting config.local.toml.
//
// NextConfigureLocal propagates config.ErrNotInitialized when no
// committed configuration exists yet — configure-local overlays onto
// it and has nothing to seed from otherwise.
func NextConfigureLocal(answers map[string]string, repoRoot string) (question.Document, error) {
	if answers == nil {
		answers = map[string]string{}
	}

	committed, err := readCommittedForLocal(repoRoot)
	if err != nil {
		return question.Document{}, err
	}
	localRaw, err := readLocalRaw(repoRoot)
	if err != nil {
		return question.Document{}, err
	}
	seeded := seedOverrides(committed, localRaw)

	seq := buildSequenceLocal(committed, seeded, answers)
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

	return nextAfterSequenceLocal(committed, seeded, answers, repoRoot)
}

// nextAfterSequenceLocal handles NextConfigureLocal's tail, mirroring
// nextAfterSequence's approval/summary/complete shape.
func nextAfterSequenceLocal(committed *config.Config, seeded map[string]string, answers map[string]string, repoRoot string) (question.Document, error) {
	overrides, err := materializeLocal(committed, seeded, answers)
	if err != nil {
		return question.Document{}, err
	}
	summary, err := buildSummaryLocal(committed, overrides, repoRoot)
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
			Questions:     []question.Question{approvalQuestionLocal()},
			Summary:       &summary,
		}, nil
	}

	if approvalVal == "approve" {
		return question.Document{
			SchemaVersion: question.SchemaVersion,
			Kind:          question.DocComplete,
			Complete:      buildCompleteLocal(summary),
		}, nil
	}
	// ValidateAnswer already restricted approvalVal to "approve" or
	// "abort" before this point is reached.
	return question.Document{SchemaVersion: question.SchemaVersion, Kind: question.DocAborted}, nil
}

// readCommittedForLocal reads and parses repoRoot's committed
// configuration through config.Parse — never config.Load, which would
// layer in the very local overrides this interview is about to
// re-derive from scratch. A missing committed file is
// config.ErrNotInitialized.
func readCommittedForLocal(repoRoot string) (*config.Config, error) {
	data, err := os.ReadFile(filepath.Join(repoRoot, filepath.FromSlash(config.Path)))
	if errors.Is(err, fs.ErrNotExist) {
		return nil, config.ErrNotInitialized
	}
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", config.Path, err)
	}
	cfg, err := config.Parse(data)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", config.Path, err)
	}
	return cfg, nil
}

// readLocalRaw reads repoRoot's current config.local.toml bytes, or nil
// if the file does not exist yet.
func readLocalRaw(repoRoot string) ([]byte, error) {
	data, err := os.ReadFile(filepath.Join(repoRoot, filepath.FromSlash(config.LocalOverridePath)))
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", config.LocalOverridePath, err)
	}
	return data, nil
}

// seedOverrides leniently extracts every currently-valid preference
// override from localRaw, config.local.toml's current bytes (contract
// call 5: configure-local is the repair tool for the file it owns). A
// TOML syntax error throws away the whole file — committed alone is
// the seed. Once the document parses, each leaf is classified and
// value-checked independently: a policy-bearing key, a key naming a
// host not enabled in committed, an unknown key, or a value that fails
// its own semantic check drops only that one leaf, never the file's
// other, good overrides.
func seedOverrides(committed *config.Config, localRaw []byte) map[string]string {
	seeded := map[string]string{}
	if len(localRaw) == 0 {
		return seeded
	}

	var generic map[string]any
	md, err := toml.Decode(string(localRaw), &generic)
	if err != nil {
		return seeded // total parse failure: committed alone is the seed.
	}

	for _, k := range md.Keys() {
		key := k.String()
		if !preferenceKeySet[key] {
			continue // policy-bearing, unknown, or a table header, not a leaf.
		}
		if host, isHostLeaf := hostOfPreferenceKey(key); isHostLeaf && committedHostConfig(committed, host) == nil {
			continue // host not enabled in committed.
		}
		val, ok := leafValue(generic, k)
		if !ok {
			continue
		}
		str, ok := stringifyLeafValue(key, val)
		if !ok {
			continue // wrong type or out of domain for this leaf.
		}
		seeded[key] = str
	}
	return seeded
}

// hostOfPreferenceKey reports the host name for a "hosts.<host>...."
// leaf key, and false for any other key (config's overlay.go
// hostOfLeaf, duplicated so this pre-classification walk need not
// reach into config's unexported table directly).
func hostOfPreferenceKey(key string) (string, bool) {
	const prefix = "hosts."
	if !strings.HasPrefix(key, prefix) {
		return "", false
	}
	rest := key[len(prefix):]
	if i := strings.IndexByte(rest, '.'); i >= 0 {
		return rest[:i], true
	}
	return rest, true
}

// leafValue walks generic (toml.Decode's map[string]any form of the
// local file) along k's parts and returns the value at that path, or
// false if any intermediate segment is missing or not itself a table.
func leafValue(generic map[string]any, k toml.Key) (any, bool) {
	var cur any = generic
	for _, part := range k {
		m, ok := cur.(map[string]any)
		if !ok {
			return nil, false
		}
		cur, ok = m[part]
		if !ok {
			return nil, false
		}
	}
	return cur, true
}

// stringifyLeafValue converts val (as generically decoded by
// toml.Decode) to the canonical string form materializeLocal/RenderLocal
// use for key, or false if val's type or domain is wrong for key —
// seedOverrides' per-leaf semantic check.
func stringifyLeafValue(key string, val any) (string, bool) {
	switch key {
	case idMaxSubagents:
		n, ok := val.(int64)
		if !ok || n < 1 {
			return "", false
		}
		return strconv.FormatInt(n, 10), true
	case idMetricsEnabled:
		b, ok := val.(bool)
		if !ok {
			return "", false
		}
		return strconv.FormatBool(b), true
	default:
		s, ok := val.(string)
		if !ok {
			return "", false
		}
		switch {
		case strings.HasSuffix(key, ".model"):
			if validateModelFreeText(key, s) != nil {
				return "", false
			}
			return s, true
		case strings.HasSuffix(key, ".effort"):
			host, _ := hostOfPreferenceKey(key)
			if !validEffort(host, s) {
				return "", false
			}
			return s, true
		default:
			return "", false
		}
	}
}

// validEffort reports whether value is one of host's closed effort
// enum (hostEfforts, sequence.go).
func validEffort(host, value string) bool {
	for _, e := range hostEfforts[host] {
		if e == value {
			return true
		}
	}
	return false
}

// committedHostConfig returns committed's *config.Host for the named
// host, or nil if that host is not enabled.
func committedHostConfig(committed *config.Config, host string) *config.Host {
	switch host {
	case "claude":
		return committed.Hosts.Claude
	case "codex":
		return committed.Hosts.Codex
	default:
		return nil
	}
}

// committedProfile returns h's RoleProfile for role — a small local
// switch mirroring materialize.go's setRoleProfile, read direction
// instead of write, since config's own equivalent (render.go's
// roleProfileOf) is unexported.
func committedProfile(h *config.Host, role string) config.RoleProfile {
	switch role {
	case "architect":
		return h.Roles.Architect
	case "scout":
		return h.Roles.Scout
	case "implementer":
		return h.Roles.Implementer
	case "specialist":
		return h.Roles.Specialist
	case "reviewer":
		return h.Roles.Reviewer
	case "review_downgrade":
		return h.Roles.ReviewDowngrade
	default:
		return config.RoleProfile{}
	}
}

// pickIDForHost maps a host name to its picker-document question id.
func pickIDForHost(host string) string {
	switch host {
	case "claude":
		return idPickClaude
	case "codex":
		return idPickCodex
	default:
		return ""
	}
}

// buildSequenceLocal derives the ordered list of "questions"-kind
// documents for the current answers: the picker (doc 1, always
// present), then each picked host's six role docs (roleSpecs order),
// then the settings doc when picked. Unlike buildSequence there is no
// error case here — configure-local never disables a host, so no
// "at least one host enabled" rule is ever at risk.
func buildSequenceLocal(committed *config.Config, seeded map[string]string, answers map[string]string) []docSpec {
	docs := []docSpec{pickerDocLocal(committed, seeded)}

	if committed.Hosts.Claude != nil && answers[idPickClaude] == "yes" {
		docs = append(docs, localRoleDocSpecs("claude", committed, seeded)...)
	}
	if committed.Hosts.Codex != nil && answers[idPickCodex] == "yes" {
		docs = append(docs, localRoleDocSpecs("codex", committed, seeded)...)
	}
	if answers[idPickSettings] == "yes" {
		docs = append(docs, localSettingsDoc(committed, seeded))
	}
	return docs
}

// pickerDocLocal is doc 1: whether to review/change each
// committed-enabled host's overrides, plus the settings area, each
// defaulted to "yes" iff a valid override already exists there.
func pickerDocLocal(committed *config.Config, seeded map[string]string) docSpec {
	var qs []question.Question
	if committed.Hosts.Claude != nil {
		def := boolValue(hasHostOverride(seeded, "claude"))
		qs = append(qs, question.Question{
			ID:      idPickClaude,
			Header:  "Claude",
			Prompt:  "Review or change Claude Code's local overrides?",
			Kind:    question.KindSelect,
			Default: def,
			Options: yesNoOptions(def),
		})
	}
	if committed.Hosts.Codex != nil {
		def := boolValue(hasHostOverride(seeded, "codex"))
		qs = append(qs, question.Question{
			ID:      idPickCodex,
			Header:  "Codex",
			Prompt:  "Review or change Codex CLI's local overrides?",
			Kind:    question.KindSelect,
			Default: def,
			Options: yesNoOptions(def),
		})
	}
	settingsDef := boolValue(hasSettingsOverride(seeded))
	qs = append(qs, question.Question{
		ID:      idPickSettings,
		Header:  "Settings",
		Prompt:  "Review or change local concurrency/metrics overrides?",
		Kind:    question.KindSelect,
		Default: settingsDef,
		Options: yesNoOptions(settingsDef),
	})
	return docSpec{questions: qs}
}

// hasHostOverride reports whether seeded carries any valid override
// under host's hosts.<host>.* prefix.
func hasHostOverride(seeded map[string]string, host string) bool {
	prefix := "hosts." + host + "."
	for k := range seeded {
		if strings.HasPrefix(k, prefix) {
			return true
		}
	}
	return false
}

// hasSettingsOverride reports whether seeded carries a concurrency or
// metrics override.
func hasSettingsOverride(seeded map[string]string) bool {
	_, hasConcurrency := seeded[idMaxSubagents]
	_, hasMetrics := seeded[idMetricsEnabled]
	return hasConcurrency || hasMetrics
}

// effectiveValue returns seeded[key] if present, else committedValue —
// "override else committed", the effective-current rule every
// configure-local question default and Recommended option follows.
func effectiveValue(seeded map[string]string, key, committedValue string) string {
	if v, ok := seeded[key]; ok {
		return v
	}
	return committedValue
}

// localRoleDocSpecs builds host's six per-role documents (model+effort
// paired, roleSpecs order) for the configure-local interview: question
// IDs are the true overlay dotted keys, options mark the committed
// value's option "(committed)" in its Label and the effective-current
// value Recommended, and Default is always the effective-current value
// (override else committed) — never a PRD default profile, since this
// interview only ever edits an already-committed host.
func localRoleDocSpecs(host string, committed *config.Config, seeded map[string]string) []docSpec {
	h := committedHostConfig(committed, host)
	hostLabel := hostLabels[host]
	docs := make([]docSpec, 0, len(roleSpecs))
	for _, rs := range roleSpecs {
		cp := committedProfile(h, rs.key)
		modelKey := localRoleModelID(host, rs.key)
		effortKey := localRoleEffortID(host, rs.key)
		effectiveModel := effectiveValue(seeded, modelKey, cp.Model)
		effectiveEffort := effectiveValue(seeded, effortKey, cp.Effort)

		modelQ := question.Question{
			ID:       modelKey,
			Header:   rs.header,
			Prompt:   fmt.Sprintf("%s model (%s)", rs.label, hostLabel),
			Kind:     question.KindSelect,
			Options:  modelOptionsLocal(host, cp.Model, effectiveModel),
			FreeText: true,
			Default:  effectiveModel,
		}
		effortQ := question.Question{
			ID:      effortKey,
			Header:  rs.header,
			Prompt:  fmt.Sprintf("%s reasoning effort (%s)", rs.label, hostLabel),
			Kind:    question.KindSelect,
			Options: effortOptionsLocal(host, cp.Effort, effectiveEffort),
			Default: effectiveEffort,
		}
		docs = append(docs, docSpec{questions: []question.Question{modelQ, effortQ}})
	}
	return docs
}

// modelOptionsLocal lists host's local-override-selectable models,
// marking committedVal's option "(committed)" in its Label and
// effectiveVal Recommended.
func modelOptionsLocal(host, committedVal, effectiveVal string) []question.Option {
	models := hostLocalModels[host]
	opts := make([]question.Option, len(models))
	for i, m := range models {
		label := m
		if m == committedVal {
			label += " (committed)"
		}
		opts[i] = question.Option{Value: m, Label: label, Recommended: m == effectiveVal}
	}
	return opts
}

// effortOptionsLocal lists host's closed effort enum, marking
// committedVal's option "(committed)" in its Label and effectiveVal
// Recommended.
func effortOptionsLocal(host, committedVal, effectiveVal string) []question.Option {
	efforts := hostEfforts[host]
	opts := make([]question.Option, len(efforts))
	for i, e := range efforts {
		label := effortLabel(e)
		if e == committedVal {
			label += " (committed)"
		}
		opts[i] = question.Option{Value: e, Label: label, Recommended: e == effectiveVal}
	}
	return opts
}

// localSettingsDoc is configure-local's settings document:
// concurrency.max_subagents and metrics.enabled, the only two
// preference-classified settings keys (merge.strategy and memhub.mode
// are policy-bearing and never appear here). Defaults are always the
// effective-current value.
func localSettingsDoc(committed *config.Config, seeded map[string]string) docSpec {
	committedConcurrency := strconv.Itoa(committed.Concurrency.MaxSubagents)
	effectiveConcurrency := effectiveValue(seeded, idMaxSubagents, committedConcurrency)

	committedMetrics := boolValue(committed.Metrics.Enabled)
	effectiveMetrics := committedMetrics
	if v, ok := seeded[idMetricsEnabled]; ok {
		if b, err := strconv.ParseBool(v); err == nil {
			effectiveMetrics = boolValue(b)
		}
	}

	concurrency := question.Question{
		ID:       idMaxSubagents,
		Header:   "Concurrency",
		Prompt:   "Maximum concurrent subagents (this machine)",
		Kind:     question.KindSelect,
		FreeText: true,
		Default:  effectiveConcurrency,
		Options:  concurrencyOptionsLocal(committedConcurrency, effectiveConcurrency),
	}
	metrics := question.Question{
		ID:      idMetricsEnabled,
		Header:  "Metrics",
		Prompt:  "Enable local metrics (this machine)",
		Kind:    question.KindSelect,
		Default: effectiveMetrics,
		Options: yesNoOptionsLocal(committedMetrics, effectiveMetrics),
	}
	return docSpec{questions: []question.Question{concurrency, metrics}}
}

// concurrencyOptionsLocal lists the fixed 1-4 concurrency choices,
// marking committedVal's option "(committed)" in its Label and
// effectiveVal Recommended.
func concurrencyOptionsLocal(committedVal, effectiveVal string) []question.Option {
	opts := make([]question.Option, 0, 4)
	for i := 1; i <= 4; i++ {
		v := strconv.Itoa(i)
		label := v
		if v == committedVal {
			label += " (committed)"
		}
		opts = append(opts, question.Option{Value: v, Label: label, Recommended: v == effectiveVal})
	}
	return opts
}

// yesNoOptionsLocal is yesNoOptions with committedVal's option also
// marked "(committed)" in its Label.
func yesNoOptionsLocal(committedVal, effectiveVal string) []question.Option {
	return []question.Option{
		{Value: "yes", Label: yesNoLabelLocal("Yes", "yes", committedVal), Recommended: effectiveVal == "yes"},
		{Value: "no", Label: yesNoLabelLocal("No", "no", committedVal), Recommended: effectiveVal == "no"},
	}
}

func yesNoLabelLocal(label, value, committedVal string) string {
	if value == committedVal {
		return label + " (committed)"
	}
	return label
}

// materializeLocal turns seeded (every currently valid override the
// on-disk file carries) and answers (this session's picked-area
// answers) into the final override map: it starts from seeded in full
// — an unpicked area's valid overrides are preserved untouched — then
// applies every picked host's role answers and, if picked, the settings
// answers, clearing an override whenever the answer equals the
// committed value (contract call 5: "no sentinel option"; clearing is
// answering the committed value back). Once assembled, a non-empty
// result round-trips through config.RenderLocal and config.MergeLocal
// as a fail-closed self-check — the same anti-forgery discipline
// materialize's Render/Parse round trip applies to init.
func materializeLocal(committed *config.Config, seeded map[string]string, answers map[string]string) (map[string]string, error) {
	overrides := make(map[string]string, len(seeded))
	for k, v := range seeded {
		overrides[k] = v
	}

	for _, host := range []string{"claude", "codex"} {
		if answers[pickIDForHost(host)] != "yes" {
			continue
		}
		if err := applyRoleAnswers(committed, host, answers, overrides); err != nil {
			return nil, err
		}
	}

	if answers[idPickSettings] == "yes" {
		if err := applySettingsAnswers(committed, answers, overrides); err != nil {
			return nil, err
		}
	}

	if len(overrides) > 0 {
		rendered, err := config.RenderLocal(overrides)
		if err != nil {
			return nil, fmt.Errorf("render local overrides: %w", err)
		}
		if _, err := config.MergeLocal(committed, rendered); err != nil {
			return nil, fmt.Errorf("materialized local overrides failed their own merge check: %w", err)
		}
	}
	return overrides, nil
}

// applyRoleAnswers applies host's six answered role questions onto
// overrides, clearing (setOrClear) each leaf whose answer equals
// committed's own value.
func applyRoleAnswers(committed *config.Config, host string, answers, overrides map[string]string) error {
	h := committedHostConfig(committed, host)
	for _, rs := range roleSpecs {
		modelKey := localRoleModelID(host, rs.key)
		effortKey := localRoleEffortID(host, rs.key)
		modelVal := answers[modelKey]
		effortVal := answers[effortKey]
		if err := validateModelFreeText(modelKey, modelVal); err != nil {
			return err
		}
		cp := committedProfile(h, rs.key)
		setOrClear(overrides, modelKey, modelVal, cp.Model)
		setOrClear(overrides, effortKey, effortVal, cp.Effort)
	}
	return nil
}

// applySettingsAnswers applies the two settings answers onto overrides.
func applySettingsAnswers(committed *config.Config, answers, overrides map[string]string) error {
	n, err := parseConcurrency(answers[idMaxSubagents])
	if err != nil {
		return err
	}
	setOrClear(overrides, idMaxSubagents, strconv.Itoa(n), strconv.Itoa(committed.Concurrency.MaxSubagents))

	enabled := answers[idMetricsEnabled] == "yes"
	setOrClear(overrides, idMetricsEnabled, strconv.FormatBool(enabled), strconv.FormatBool(committed.Metrics.Enabled))
	return nil
}

// setOrClear records value under key in overrides, unless value equals
// committedValue: clearing an override is defined as answering the
// committed value back (contract call 5's "no sentinel option" rule),
// so an unchanged answer removes any existing seeded override for key
// instead of re-recording it verbatim.
func setOrClear(overrides map[string]string, key, value, committedValue string) {
	if value == committedValue {
		delete(overrides, key)
		return
	}
	overrides[key] = value
}

// buildSummaryLocal materializes overrides into the proposed
// config.local.toml content (or a deletion) and its one-FileChange
// question.Summary. committed is never itself touched by
// configure-local; ConfigRevision is carried forward unchanged so the
// summary's shared shape with init's still names the configuration
// this override applies to.
func buildSummaryLocal(committed *config.Config, overrides map[string]string, repoRoot string) (question.Summary, error) {
	oldRaw, err := readLocalRaw(repoRoot)
	if err != nil {
		return question.Summary{}, err
	}
	old := string(oldRaw)
	existed := oldRaw != nil

	var newContent string
	if len(overrides) > 0 {
		rendered, err := config.RenderLocal(overrides)
		if err != nil {
			return question.Summary{}, fmt.Errorf("render local overrides: %w", err)
		}
		newContent = string(rendered)
	}

	change := question.FileChange{
		Path:       filepath.ToSlash(config.LocalOverridePath),
		Existed:    existed,
		Diff:       instructions.UnifiedDiff(old, newContent),
		NewContent: newContent,
		Delete:     len(overrides) == 0,
	}

	var blockers []string
	if old == newContent {
		blockers = append(blockers, "no local override changes; nothing to write")
	}

	return question.Summary{
		ConfigTOML:     newContent,
		ConfigRevision: committed.ConfigRevision,
		Files:          []question.FileChange{change},
		Blockers:       blockers,
	}, nil
}

// approvalQuestionLocal is approvalQuestion with configure-local's own
// prompt: the terminal act here is a machine-local file write, not
// init's configuration-and-bootstrap plan. The ID and options are
// identical, so applicableQuestions' shared registration (which uses
// approvalQuestion) validates the same answer values either way.
func approvalQuestionLocal() question.Question {
	q := approvalQuestion()
	q.Prompt = "Apply these machine-local overrides?"
	return q
}

// buildCompleteLocal assembles configure-local's terminal Complete
// document. Detection is nil (configure-local reads no environment
// facts) and BootstrapReady is always true: unlike init's bootstrap
// handoff, nothing external (git, gh) is load-bearing for the apply
// step, which is a plain local file write (documented deviation).
func buildCompleteLocal(summary question.Summary) *question.Complete {
	return &question.Complete{
		Summary:        summary,
		BootstrapReady: true,
	}
}
