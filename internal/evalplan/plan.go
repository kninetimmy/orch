// Package evalplan retains evaluation preparation and bounded controller evidence.
// It grants no approval; execution separately verifies the native boundary.
package evalplan

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/kninetimmy/orch/internal/config"
	"github.com/kninetimmy/orch/internal/evalcorpus"
	"github.com/kninetimmy/orch/internal/execx"
)

const (
	MaxPlanBytes     = 64 * 1024
	MaxArtifactBytes = 2 * 1024 * 1024
	maxRecordBytes   = 16 * 1024 * 1024
)

type Artifact struct {
	Path   string `json:"path"`
	SHA256 string `json:"sha256"`
}

type Selection struct {
	OrchRevision string   `json:"orch_revision"`
	Profile      Artifact `json:"profile"`
}

type Limits struct {
	OverallSeconds      int64  `json:"overall_seconds"`
	AttemptSeconds      int64  `json:"attempt_seconds"`
	VerificationSeconds int64  `json:"verification_seconds"`
	CleanupSeconds      int64  `json:"cleanup_seconds"`
	MaxAttemptsPerUnit  int64  `json:"max_attempts_per_unit"`
	MaxRepairsPerUnit   *int64 `json:"max_repairs_per_unit"`
}

type Measurement struct {
	Source string `json:"source"`
	Scope  string `json:"scope"`
}

// Readiness documents are opaque digest references, never authoritative claims.
type Readiness struct {
	IndependentValidation *Artifact `json:"independent_validation,omitempty"`
	Exposure              *Artifact `json:"exposure,omitempty"`
	NativeExecution       *Artifact `json:"native_execution,omitempty"`
}

type Proposal struct {
	Version        int         `json:"version"`
	Scope          string      `json:"scope"`
	Intervention   string      `json:"intervention"`
	Corpus         Artifact    `json:"corpus"`
	Cases          []string    `json:"cases"`
	Partitions     []string    `json:"partitions"`
	Baseline       Selection   `json:"baseline"`
	Candidate      *Selection  `json:"candidate,omitempty"`
	Repetitions    int64       `json:"repetitions"`
	Limits         Limits      `json:"limits"`
	Measurement    Measurement `json:"measurement"`
	DecisionRule   Artifact    `json:"decision_rule"`
	Readiness      Readiness   `json:"readiness"`
	StorageRoot    string      `json:"storage_root"`
	WorkerRoots    []string    `json:"worker_roots"`
	ScratchRoots   []string    `json:"scratch_roots"`
	Instructions   *[]Artifact `json:"instructions,omitempty"`
	ProtectedRoots *[]string   `json:"protected_roots,omitempty"`
}

type Configuration struct {
	Revision       string                  `json:"revision"`
	SHA256         string                  `json:"sha256"`
	Profiles       map[string]config.Roles `json:"profiles"`
	LocalOverrides []string                `json:"local_overrides"`
}

type PinnedSelection struct {
	Selection
	Configuration Configuration `json:"configuration"`
}

// PublicCase deliberately excludes task/context bytes, keys, probes, controls,
// free-form author/selection prose and private readiness document contents.
type PublicCase struct {
	ID             string `json:"id"`
	Version        int    `json:"version"`
	Role           string `json:"role"`
	Partition      string `json:"partition"`
	Lineage        string `json:"lineage"`
	Difficulty     string `json:"difficulty"`
	Classification string `json:"classification,omitempty"`
	SourceCommit   string `json:"source_commit"`
	PacketSHA256   string `json:"packet_sha256"`
}

type Plan struct {
	Version                int              `json:"version"`
	Scope                  string           `json:"scope"`
	Intervention           string           `json:"intervention"`
	Corpus                 Artifact         `json:"corpus"`
	Cases                  []PublicCase     `json:"cases"`
	ExcludedCases          []PublicCase     `json:"excluded_cases"`
	Partitions             []string         `json:"partitions"`
	Baseline               PinnedSelection  `json:"baseline"`
	Candidate              *PinnedSelection `json:"candidate,omitempty"`
	EffectiveConfiguration Configuration    `json:"effective_configuration"`
	Repetitions            int64            `json:"repetitions"`
	Limits                 Limits           `json:"limits"`
	Measurement            Measurement      `json:"measurement"`
	DecisionRule           Artifact         `json:"decision_rule"`
	Readiness              Readiness        `json:"readiness"`
	StorageRoot            string           `json:"storage_root"`
	WorkerRoots            []string         `json:"worker_roots"`
	ScratchRoots           []string         `json:"scratch_roots"`
	RepositoryRoots        []string         `json:"repository_roots"`
	GitDirectories         []string         `json:"git_directories"`
	Instructions           *[]Artifact      `json:"instructions,omitempty"`
	ProtectedRoots         *[]string        `json:"protected_roots,omitempty"`
}

type Counts struct {
	BaselineUnits                   int64 `json:"baseline_units"`
	CandidateUnits                  int64 `json:"candidate_units"`
	MatchedPairs                    int64 `json:"matched_pairs"`
	Units                           int64 `json:"units"`
	MaximumRetries                  int64 `json:"maximum_retries"`
	MaximumRepairs                  int64 `json:"maximum_repairs"`
	MaximumAttemptsIncludingRepairs int64 `json:"maximum_attempts_including_repairs"`
	MaximumScheduledSeconds         int64 `json:"maximum_scheduled_seconds"`
}

type Unit struct {
	Ordinal     int    `json:"ordinal"`
	CaseID      string `json:"case_id"`
	CaseVersion int    `json:"case_version"`
	Repetition  int64  `json:"repetition"`
	Side        string `json:"side"`
}

type Check struct {
	Name   string `json:"name"`
	Status string `json:"status"`
	Detail string `json:"detail"`
}

type Evidence struct {
	ExecutionAvailable     bool     `json:"execution_available"`
	ApprovalGranted        bool     `json:"approval_granted"`
	WorkerAccessProtection string   `json:"worker_access_protection"`
	Counts                 Counts   `json:"counts"`
	Schedule               []Unit   `json:"schedule"`
	Checks                 []Check  `json:"checks"`
	MeasurementUnknowns    []string `json:"measurement_unknowns"`
	ReadinessBlockers      []string `json:"readiness_blockers"`
}

type Record struct {
	SchemaVersion      int      `json:"schema_version"`
	Kind               string   `json:"kind"`
	PlanDigest         string   `json:"plan_digest"`
	StorageDestination string   `json:"storage_destination"`
	Plan               Plan     `json:"plan"`
	Preview            Evidence `json:"preview"`
}

var (
	oidPattern        = regexp.MustCompile(`^[0-9a-f]{40}$`)
	digestPattern     = regexp.MustCompile(`^[0-9a-f]{64}$`)
	identifierPattern = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9._:/@+-]{0,127}$`)
	casePattern       = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,95}$`)
)

// strictJSON also rejects duplicate keys, nulls and excessive nesting; the
// standard decoder alone silently accepts duplicate object members.
func strictJSON(data []byte, out any) error {
	return decodeJSON(data, out, true)
}

func decodeJSON(data []byte, out any, lowercase bool) error {
	if !utf8.Valid(data) {
		return fmt.Errorf("JSON must be UTF-8")
	}
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.UseNumber()
	var scan func(int) error
	scan = func(depth int) error {
		if depth > 32 {
			return fmt.Errorf("JSON exceeds 32 nesting levels")
		}
		token, err := dec.Token()
		if err != nil {
			return err
		}
		if token == nil {
			return fmt.Errorf("null is not a plan value; omit optional fields")
		}
		if token == json.Delim('{') {
			seen := map[string]bool{}
			for dec.More() {
				key, err := dec.Token()
				if err != nil {
					return err
				}
				name, ok := key.(string)
				if !ok || seen[name] || lowercase && name != strings.ToLower(name) {
					return fmt.Errorf("duplicate or non-lowercase JSON field %q", key)
				}
				seen[name] = true
				if err := scan(depth + 1); err != nil {
					return err
				}
			}
			_, err = dec.Token()
			return err
		}
		if token == json.Delim('[') {
			for dec.More() {
				if err := scan(depth + 1); err != nil {
					return err
				}
			}
			_, err = dec.Token()
			return err
		}
		return nil
	}
	if err := scan(0); err != nil {
		return err
	}
	if _, err := dec.Token(); err != io.EOF {
		return fmt.Errorf("trailing JSON data; require one object")
	}
	dec = json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	return dec.Decode(out)
}

func checkedAdd(a, b int64) (int64, error) {
	if a < 0 || b < 0 || a > math.MaxInt64-b {
		return 0, fmt.Errorf("finite-limit arithmetic overflow")
	}
	return a + b, nil
}

func checkedMultiply(a, b int64) (int64, error) {
	if a < 0 || b < 0 || b != 0 && a > math.MaxInt64/b {
		return 0, fmt.Errorf("finite-limit arithmetic overflow")
	}
	return a * b, nil
}

func plannedCounts(p Proposal) (Counts, error) {
	l := p.Limits
	for name, n := range map[string]int64{"overall_seconds": l.OverallSeconds, "attempt_seconds": l.AttemptSeconds, "verification_seconds": l.VerificationSeconds, "cleanup_seconds": l.CleanupSeconds} {
		if n < 1 || n > math.MaxInt64/int64(time.Second) {
			return Counts{}, fmt.Errorf("limits.%s: require positive finite seconds representable as a duration", name)
		}
	}
	if p.Repetitions < 1 || p.Repetitions > 1000 || l.MaxAttemptsPerUnit < 1 || l.MaxRepairsPerUnit == nil || *l.MaxRepairsPerUnit < 0 {
		return Counts{}, fmt.Errorf("require repetitions 1..1000, max_attempts_per_unit >= 1 and explicit max_repairs_per_unit >= 0")
	}
	seconds, err := checkedAdd(l.AttemptSeconds, l.VerificationSeconds)
	if err != nil {
		return Counts{}, err
	}
	seconds, err = checkedAdd(seconds, l.CleanupSeconds)
	if err != nil || seconds > l.OverallSeconds {
		return Counts{}, fmt.Errorf("overall_seconds must cover one attempt, verification and cleanup: %v", err)
	}
	attempts, err := checkedAdd(l.MaxAttemptsPerUnit, *l.MaxRepairsPerUnit)
	if err != nil {
		return Counts{}, err
	}
	c := Counts{BaselineUnits: int64(len(p.Cases)) * p.Repetitions}
	if p.Candidate != nil {
		c.CandidateUnits, c.MatchedPairs = c.BaselineUnits, c.BaselineUnits
	}
	c.Units = c.BaselineUnits + c.CandidateUnits
	if c.MaximumAttemptsIncludingRepairs, err = checkedMultiply(c.Units, attempts); err != nil {
		return Counts{}, err
	}
	if c.MaximumScheduledSeconds, err = checkedMultiply(c.MaximumAttemptsIncludingRepairs, seconds); err != nil {
		return Counts{}, err
	}
	c.MaximumRetries, err = checkedMultiply(c.Units, l.MaxAttemptsPerUnit-1)
	if err != nil {
		return Counts{}, err
	}
	c.MaximumRepairs, err = checkedMultiply(c.Units, *l.MaxRepairsPerUnit)
	return c, err
}

func readArtifact(base string, a Artifact) (Artifact, []byte, error) {
	if !digestPattern.MatchString(a.SHA256) {
		return a, nil, fmt.Errorf("artifact sha256 must be 64 lowercase hex digits")
	}
	name, data, err := readLocal(base, a.Path, MaxArtifactBytes)
	if err != nil {
		return a, nil, err
	}
	if evalcorpus.Digest(data) != a.SHA256 {
		return a, nil, fmt.Errorf("artifact digest mismatch for %s (want %s, observed %s)", name, a.SHA256, evalcorpus.Digest(data))
	}
	a.Path = name
	return a, data, nil
}

func configuration(cfg *config.Config) (Configuration, error) {
	if !identifierPattern.MatchString(cfg.ConfigRevision) {
		return Configuration{}, fmt.Errorf("configuration revision must be a public identifier of 1..128 characters")
	}
	profiles := map[string]config.Roles{}
	for _, host := range cfg.EnabledHosts() {
		r := cfg.Host(host).Roles
		for _, p := range []config.RoleProfile{r.Architect, r.Scout, r.Implementer, r.Specialist, r.Reviewer, r.ReviewDowngrade} {
			for _, id := range []string{p.Model, p.Effort, p.Variant} {
				if id != "" && !identifierPattern.MatchString(id) {
					return Configuration{}, fmt.Errorf("profile model/effort/variant must be public identifiers of 1..128 characters")
				}
			}
		}
		profiles[host] = r
	}
	data, err := config.Render(cfg)
	if err != nil {
		return Configuration{}, err
	}
	return Configuration{Revision: cfg.ConfigRevision, SHA256: evalcorpus.Digest(data), Profiles: profiles, LocalOverrides: append([]string{}, cfg.Overrides...)}, nil
}

func pinSelection(ctx context.Context, repo, base string, s Selection, runner execx.Runner) (PinnedSelection, error) {
	if !oidPattern.MatchString(s.OrchRevision) {
		return PinnedSelection{}, fmt.Errorf("orch_revision must be a full 40-character lowercase commit OID")
	}
	if _, err := gitRead(ctx, runner, repo, "cat-file", "-e", s.OrchRevision+"^{commit}"); err != nil {
		return PinnedSelection{}, fmt.Errorf("pinned Orch commit %s unavailable locally; no fetch is attempted: %w", s.OrchRevision, err)
	}
	a, data, err := readArtifact(base, s.Profile)
	if err != nil {
		return PinnedSelection{}, err
	}
	cfg, err := config.Parse(data)
	if err != nil {
		return PinnedSelection{}, fmt.Errorf("profile artifact: %w", err)
	}
	c, err := configuration(cfg)
	return PinnedSelection{Selection: Selection{OrchRevision: s.OrchRevision, Profile: a}, Configuration: c}, err
}

func selectCases(p Proposal, manifest evalcorpus.Manifest) ([]PublicCase, []PublicCase, error) {
	if !slices.Contains([]string{"baseline-only", "screen", "matched"}, p.Scope) || len(p.Cases) == 0 || len(p.Cases) > 12 {
		return nil, nil, fmt.Errorf("scope must be baseline-only, screen or matched; cases must name 1..12 reference-v1 cases")
	}
	if p.Scope == "baseline-only" && p.Candidate != nil || p.Scope == "matched" && p.Candidate == nil {
		return nil, nil, fmt.Errorf("baseline-only forbids a candidate; matched requires a candidate")
	}
	if p.Candidate == nil && p.Intervention != "none" || p.Candidate != nil && !slices.Contains([]string{"orch-revision", "requested-profile", "combined"}, p.Intervention) {
		return nil, nil, fmt.Errorf("intervention must be none without a candidate, or orch-revision, requested-profile or combined with a candidate")
	}
	if p.Scope == "screen" && len(p.Cases) != 4 || p.Scope != "screen" && len(p.Cases) != 12 {
		return nil, nil, fmt.Errorf("screen requires exactly four cases; baseline-only and matched require all twelve")
	}
	selected := map[string]bool{}
	for _, id := range p.Cases {
		if !casePattern.MatchString(id) || selected[id] {
			return nil, nil, fmt.Errorf("invalid or duplicate case %q", id)
		}
		selected[id] = true
	}
	partitions := map[string]bool{}
	for _, partition := range p.Partitions {
		if !slices.Contains([]string{"development", "held-out"}, partition) || partitions[partition] {
			return nil, nil, fmt.Errorf("invalid or duplicate partition %q", partition)
		}
		partitions[partition] = true
	}
	cases, excluded := []PublicCase{}, []PublicCase{}
	coverage, usedPartitions := map[string]int{}, map[string]bool{}
	for _, c := range manifest.Cases {
		if !casePattern.MatchString(c.ID) || !casePattern.MatchString(c.Lineage) || !identifierPattern.MatchString(c.Difficulty) {
			return nil, nil, fmt.Errorf("case identity/lineage/difficulty must be bounded public identifiers")
		}
		public := PublicCase{ID: c.ID, Version: c.Version, Role: c.Role, Partition: c.Partition, Lineage: c.Lineage, Difficulty: c.Difficulty, Classification: c.Classification, SourceCommit: c.SourceCommit, PacketSHA256: c.PacketSHA256}
		if selected[c.ID] {
			if !partitions[c.Partition] {
				return nil, nil, fmt.Errorf("case %s is outside declared partitions", c.ID)
			}
			cases = append(cases, public)
			delete(selected, c.ID)
			coverage[c.Role+"/"+c.Classification]++
			usedPartitions[c.Partition] = true
		} else {
			excluded = append(excluded, public)
		}
	}
	if len(selected) != 0 {
		return nil, nil, fmt.Errorf("unknown case IDs in cases")
	}
	if len(partitions) == 0 || len(partitions) != len(usedPartitions) {
		return nil, nil, fmt.Errorf("partitions must exactly match included case coverage")
	}
	if p.Scope == "screen" && (coverage["scout/"] != 1 || coverage["implementation/"] != 1 || coverage["review/defective"] != 1 || coverage["review/clean"] != 1) {
		return nil, nil, fmt.Errorf("screen requires one scout, implementation, defective review and clean review")
	}
	sortCases := func(items []PublicCase) {
		slices.SortFunc(items, func(a, b PublicCase) int { return strings.Compare(a.ID, b.ID) })
	}
	sortCases(cases)
	sortCases(excluded)
	return cases, excluded, nil
}

func evidence(plan Plan, counts Counts) Evidence {
	e := Evidence{
		WorkerAccessProtection: "unverified",
		Counts:                 counts,
		Schedule:               []Unit{},
		Checks: []Check{
			{"manifest-structure", "passed", "evalcorpus.Load reference-v1 structure, coverage, partitions and declared packet digests"},
			{"supplied-artifact-digests", "passed", "bounded local manifest/profile/decision/readiness bytes match declared SHA-256"},
			{"pinned-orch-revisions", "passed", "baseline/candidate commit objects exist locally; no source export or fetch"},
			{"corpus-source-bytes", "not-performed", "historical inputs, authored worker bytes, keys, probes and controls were not exported or checked"},
			{"independent-validation", "not-performed", "supplied evidence is hashed only; semantic validation, control reproduction and disputes are unverified"},
			{"exposure", "not-performed", "exposure documents are hashed only; held-out tuning access and training familiarity are unverified"},
			{"native-execution", "unavailable", "every production model turn still requires IsolationPreflight; RunSession and Session.Resume remain refused"},
			{"worker-access-protection", "unavailable", "local placement/digests/permissions and caller claims do not prove containment"},
			{"decision-rule-semantics", "not-performed", "artifact identity is pinned; tolerances, endpoints and stopping/invalidity rules were not interpreted"},
		},
		MeasurementUnknowns: []string{
			"No observed results, grades, acceptance denominator, usage, time, human work, terminal or cleanup evidence exists in this preview.",
			"Capture compatibility, per-unit/attempt/actor/counter coverage and observed inference identity are unverified.",
			"Controller/grader usage and active time are unknown; task-agent scope cannot establish whole-Orch cost.",
		},
		ReadinessBlockers: []string{
			"Evaluation execution and protected worker-access enforcement are unimplemented; this record grants no approval.",
			"Verify pinned worker source bytes and independently validate controls/rubrics, disputes and exposure before any later trial proposal.",
			"Obtain separately reviewed native model-tool containment and protected-storage enforcement; preparation is not execution eligibility.",
			"Validate decision-rule semantics and measurement coverage, then obtain approval of the exact frozen finite trial through applicable gates.",
		},
	}
	if plan.Version == 2 {
		e.WorkerAccessProtection = "not-observed; required on actual native connection"
		for i := range e.Checks {
			switch e.Checks[i].Name {
			case "native-execution":
				e.Checks[i] = Check{"native-execution", "not-observed", "evaluation integration requires exact clean build revision, frozen Codex role/profile, declared instruction context and actual-connection admission; no turn occurs in preview"}
			case "worker-access-protection":
				e.Checks[i] = Check{"worker-access-protection", "not-observed", "native sandbox/profile/protected-path controls are checked for each attempted execution; readiness references never establish enforcement"}
			}
		}
		e.Checks = append(e.Checks, Check{"approved-instruction-inputs", "digests-checked", "declared global instruction artifacts are frozen approval inputs; native loaded sources and hashes must match at execution"})
		e.ReadinessBlockers = []string{
			"Preview observes no native execution/eligibility and grants no approval; version-2 execution requires a matching clean embedded build revision and supported none/requested-profile intervention.",
			"Independently validate selected sources, controls/rubrics, disputes and exposure; opaque readiness documents are not execution or semantic proof.",
			"Revalidate the actual native connection's protections, declared instruction sources/hashes and exact role/profile; missing or changed controls refuse.",
			"Complete separately approved bounded live completion/interruption/recovery checks, decision/measurement validation and screening before the twelve-case baseline; Phase 1 remains open.",
		}
	}
	pair := 0
	for _, c := range plan.Cases {
		for rep := int64(1); rep <= plan.Repetitions; rep++ {
			sides := []string{"baseline"}
			if plan.Candidate != nil {
				sides = append(sides, "candidate")
				if pair%2 != 0 {
					slices.Reverse(sides)
				}
			}
			for _, side := range sides {
				e.Schedule = append(e.Schedule, Unit{Ordinal: len(e.Schedule) + 1, CaseID: c.ID, CaseVersion: c.Version, Repetition: rep, Side: side})
			}
			pair++
		}
	}
	return e
}

// Preview reads local artifacts, freezes a schedule, and retains one immutable
// public preparation record. Its only subprocesses are fixed read-only Git queries.
func Preview(ctx context.Context, repo, file string, runner execx.Runner) (*Record, error) {
	name, data, err := readLocal(repo, file, MaxPlanBytes)
	if err != nil {
		return nil, fmt.Errorf("plan file: %w", err)
	}
	var p Proposal
	if err := strictJSON(data, &p); err != nil {
		return nil, fmt.Errorf("plan JSON: %w", err)
	}
	if p.Version != 1 && p.Version != 2 {
		return nil, fmt.Errorf("plan version %d unsupported; require version 1 or 2", p.Version)
	}
	base := filepath.Dir(name)
	record, err := normalize(ctx, repo, base, p, runner)
	if err != nil {
		return nil, err
	}
	if err := save(record); err != nil {
		return nil, err
	}
	return record, nil
}

// normalize is shared by preview and saved-record revalidation. It never writes.
func normalize(ctx context.Context, repo, base string, p Proposal, runner execx.Runner) (*Record, error) {
	var data []byte
	var err error
	if p.Version != 1 && p.Version != 2 || p.Version == 1 && (p.Instructions != nil || p.ProtectedRoots != nil) || p.Version == 2 && (p.Instructions == nil || len(*p.Instructions) > 1 || p.ProtectedRoots == nil) {
		return nil, fmt.Errorf("version-2 plans require explicit instructions (zero or one approved global artifact) and protected_roots; version 1 has neither declaration")
	}
	p.Corpus, data, err = readArtifact(base, p.Corpus)
	if err != nil {
		return nil, fmt.Errorf("corpus: %w", err)
	}
	// Use the same strict JSON boundary for caller-supplied manifests, then the
	// existing corpus validator for all corpus-specific rules.
	var manifest evalcorpus.Manifest
	if err := strictJSON(data, &manifest); err != nil {
		return nil, fmt.Errorf("corpus JSON: %w", err)
	}
	manifest, err = evalcorpus.Load(data)
	if err != nil {
		return nil, err
	}
	cases, excluded, err := selectCases(p, manifest)
	if err != nil {
		return nil, err
	}
	counts, err := plannedCounts(p)
	if err != nil {
		return nil, err
	}
	if !identifierPattern.MatchString(p.Measurement.Source) || !slices.Contains([]string{"task-agents", "whole-orch"}, p.Measurement.Scope) {
		return nil, fmt.Errorf("measurement requires a public source identifier and scope task-agents or whole-orch")
	}
	plan := Plan{Version: p.Version, Scope: p.Scope, Intervention: p.Intervention, Corpus: p.Corpus, Cases: cases, ExcludedCases: excluded, Partitions: slices.Clone(p.Partitions), Repetitions: p.Repetitions, Limits: p.Limits, Measurement: p.Measurement, Readiness: p.Readiness}
	if p.Instructions != nil {
		instructions := []Artifact{}
		for _, source := range *p.Instructions {
			artifact, bytes, err := readArtifact(base, source)
			if err != nil {
				return nil, fmt.Errorf("approved instruction artifact: %w", err)
			}
			if !slices.Contains([]string{"AGENTS.md", "AGENTS.override.md"}, filepath.Base(artifact.Path)) || len(bytes) > 64*1024 || !utf8.Valid(bytes) || len(bytes) == 0 {
				return nil, fmt.Errorf("approved instruction artifact must be a bounded nonempty UTF-8 global AGENTS file")
			}
			instructions = append(instructions, artifact)
		}
		plan.Instructions = &instructions
	}
	if p.ProtectedRoots != nil {
		roots, err := pathList(base, *p.ProtectedRoots, false)
		if err != nil {
			return nil, fmt.Errorf("protected_roots: %w", err)
		}
		plan.ProtectedRoots = &roots
	}
	slices.Sort(plan.Partitions)
	plan.Baseline, err = pinSelection(ctx, repo, base, p.Baseline, runner)
	if err != nil {
		return nil, fmt.Errorf("baseline: %w", err)
	}
	if p.Candidate != nil {
		selection, err := pinSelection(ctx, repo, base, *p.Candidate, runner)
		if err != nil {
			return nil, fmt.Errorf("candidate: %w", err)
		}
		plan.Candidate = &selection
	}
	plan.DecisionRule, _, err = readArtifact(base, p.DecisionRule)
	if err != nil {
		return nil, fmt.Errorf("decision_rule: %w", err)
	}
	for label, ref := range map[string]**Artifact{"independent_validation": &plan.Readiness.IndependentValidation, "exposure": &plan.Readiness.Exposure, "native_execution": &plan.Readiness.NativeExecution} {
		if *ref == nil {
			continue
		}
		artifact, _, err := readArtifact(base, **ref)
		if err != nil {
			return nil, fmt.Errorf("readiness.%s: %w", label, err)
		}
		*ref = &artifact
	}
	plan.EffectiveConfiguration, err = effectiveConfiguration(repo)
	if err != nil {
		return nil, err
	}
	if err := storagePaths(ctx, repo, base, p, &plan, runner); err != nil {
		return nil, err
	}
	normalized, err := json.Marshal(plan)
	if err != nil {
		return nil, err
	}
	digest := evalcorpus.Digest(normalized)
	return &Record{SchemaVersion: plan.Version, Kind: "maintainer-preparation-record", PlanDigest: "sha256:" + digest, StorageDestination: filepath.Join(plan.StorageRoot, digest+".json"), Plan: plan, Preview: evidence(plan, counts)}, nil
}

// Text uses exactly the JSON record's facts, including every scheduled unit and
// unknown. Individual structured sections remain JSON to avoid lossy summaries.
func WriteText(w io.Writer, r *Record) error {
	if _, err := fmt.Fprintf(w, "Maintainer preparation record: %s\nStorage: %s\nExecution/worker protection not observed in preview; no approval granted.\n", r.PlanDigest, r.StorageDestination); err != nil {
		return err
	}
	for _, section := range []struct {
		name  string
		value any
	}{{"Normalized plan", r.Plan}, {"Preview evidence and schedule", r.Preview}} {
		data, err := json.MarshalIndent(section.value, "", "  ")
		if err != nil {
			return err
		}
		if _, err := fmt.Fprintf(w, "\n%s:\n%s\n", section.name, data); err != nil {
			return err
		}
	}
	return nil
}
