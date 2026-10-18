// Package metrics is the mechanical PRD §21 recorder: one
// schema-versioned JSON document per Delivery run at
// .orchestrator/metrics/<run-id>.json, local-only and never
// transmitted. Like gitops, ghops, and memhub it is policy-free — the
// Metrics.Enabled gate, event timestamps, and event content all belong
// to callers (internal/run, internal/cli); this package only knows how
// to validate, append to, and load documents. Disabled metrics means
// callers simply never call a writer (Append, Record, SetHost or
// AddArchitectSession); LoadAll never creates the
// directory itself, so a repository that has never enabled metrics
// gains no storage from merely reading (PRD §23: disabled metrics
// create no storage).
//
// Before command-level mutation serialization, exactly one Delivery run
// owned a repository but multiple command invocations inside it could still
// overlap Append's read-modify-write and lose an event. Now the production
// CLI's process-scoped Delivery mutation lock covers each final Append. This
// package remains policy-free and has no lock of its own; direct callers must
// provide the same serialization.
//
// PRD §21 mapping: first-pass review outcome is the first "review"
// event recorded for an issue; retries show up as ReviewCycles and
// repeated review events; model fallback is an "escalate" event;
// Before observations, durations between lifecycle events were described as
// falling out of consecutive Event.At stamps. Those gaps remain available,
// but are not evidence of active, verification, CI, or human time; only
// explicitly recorded intervals describe those measurements.
package metrics

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"sort"

	"github.com/kninetimmy/orch/internal/manifest"
)

// SchemaVersion adds observations beside unchanged legacy lifecycle events.
// Schema 1 remains readable; only Record upgrades an existing schema-1 file.
const SchemaVersion = 2

// Dir is the repo-relative, slash-form location of the metrics
// directory: one file per Delivery run.
const Dir = ".orchestrator/metrics"

// runIDPattern is the closed run-id shape Append accepts. Run ids are
// already filename-safe by construction (state/transition.go's
// newRunID: "run-<UTC stamp>-<hex>"), but Append never joins
// unvalidated input into a path regardless of who calls it.
var runIDPattern = regexp.MustCompile(`^[A-Za-z0-9._-]+$`)

// Usage is adapter-reported model usage (PRD §21 "where available");
// Before schema 2, every field was omitted when zero. Existing Go callers
// retain that behavior; decoded explicit zeros now survive a rewrite, and
// Counters exposes absent fields as unknown. It is otherwise
// opaque adapter-reported data — the only content check this package
// performs is that no field is negative.
type Usage struct {
	InputTokens         int64 `json:"input_tokens,omitempty"`
	OutputTokens        int64 `json:"output_tokens,omitempty"`
	CacheReadTokens     int64 `json:"cache_read_tokens,omitempty"`
	CacheCreationTokens int64 `json:"cache_creation_tokens,omitempty"`
	// TotalTokens is for a host that reports one aggregate token count
	// with no input/output/cache split; it is independent of the four
	// fields above; a caller reporting both is not reconciled against
	// each other by this package.
	TotalTokens int64 `json:"total_tokens,omitempty"`
	DurationMS  int64 `json:"duration_ms,omitempty"`
	zeroFields  uint8
}

// Validate reports the first negative field in u. A nil u is valid —
// usage is optional everywhere it appears, so the nil receiver must
// not panic.
func (u *Usage) Validate() error {
	if u == nil {
		return nil
	}
	switch {
	case u.InputTokens < 0:
		return fmt.Errorf("usage.input_tokens is negative (%d)", u.InputTokens)
	case u.OutputTokens < 0:
		return fmt.Errorf("usage.output_tokens is negative (%d)", u.OutputTokens)
	case u.CacheReadTokens < 0:
		return fmt.Errorf("usage.cache_read_tokens is negative (%d)", u.CacheReadTokens)
	case u.CacheCreationTokens < 0:
		return fmt.Errorf("usage.cache_creation_tokens is negative (%d)", u.CacheCreationTokens)
	case u.TotalTokens < 0:
		return fmt.Errorf("usage.total_tokens is negative (%d)", u.TotalTokens)
	case u.DurationMS < 0:
		return fmt.Errorf("usage.duration_ms is negative (%d)", u.DurationMS)
	default:
		return nil
	}
}

// Event is one lifecycle observation. At and Verb are required;
// everything else is verb-specific and left zero/empty when the verb
// does not carry it — see internal/run/metrics.go for the per-verb
// field mapping.
type Event struct {
	At                 string              `json:"at"`
	Verb               string              `json:"verb"`
	IssueNumber        int                 `json:"issue_number,omitempty"`
	Role               string              `json:"role,omitempty"`
	Executor           *manifest.Selection `json:"executor,omitempty"`
	Reviewer           *manifest.Selection `json:"reviewer,omitempty"`
	ReviewerDowngraded bool                `json:"reviewer_downgraded,omitempty"`
	Rationale          string              `json:"rationale,omitempty"`
	Verdict            string              `json:"verdict,omitempty"`
	ReviewCycles       int                 `json:"review_cycles,omitempty"`
	CIState            string              `json:"ci_state,omitempty"`
	BlockClass         string              `json:"block_class,omitempty"`
	RunStopped         bool                `json:"run_stopped,omitempty"`
	EscalateKind       string              `json:"escalate_kind,omitempty"`
	Reason             string              `json:"reason,omitempty"`
	Merged             int                 `json:"merged,omitempty"`
	Abandoned          int                 `json:"abandoned,omitempty"`
	Usage              *Usage              `json:"usage,omitempty"`
	// ApprovalSource is who approved an activate or merge event: "human",
	// or "grant:<id>" for an autonomy grant. Events written before it
	// existed carry none and still load.
	ApprovalSource string `json:"approval_source,omitempty"`
	// RoutingFacts is the plan's routing facts for an activate event. A nil
	// pointer means the event predates them (unknown), which is not the same
	// as every fact being false.
	RoutingFacts *RoutingFacts `json:"routing_facts,omitempty"`
	// PRNumber is the issue's PR number on a pr-open or merge event.
	PRNumber int `json:"pr_number,omitempty"`
	// ReviewBlockCause is set on a review event whose review blocked its
	// issue: BlockCauseWrongCriterion or BlockCauseFixCycleLimit. A review
	// that blocked nothing records none.
	ReviewBlockCause string `json:"review_block_cause,omitempty"`
	// Fixes is the merged PR numbers an activate event's issue declares it
	// fixes, so the report can attribute each escaped defect to the issue
	// whose merge event carries that PR number.
	Fixes []int `json:"fixes,omitempty"`
}

// The causes a review event can record in Event.ReviewBlockCause.
const (
	BlockCauseWrongCriterion = "wrong_criterion"
	BlockCauseFixCycleLimit  = "fix_cycle_limit"
)

// RoutingFacts mirrors the routing facts a plan declared for one issue.
// Every field is recorded explicitly, so a false is a recorded false.
type RoutingFacts struct {
	RiskDomains        []string `json:"risk_domains,omitempty"`
	UnusuallyDifficult bool     `json:"unusually_difficult"`
	Mechanical         bool     `json:"mechanical"`
	LowRisk            bool     `json:"low_risk"`
	FullySpecified     bool     `json:"fully_specified"`
	Unsurprising       bool     `json:"unsurprising"`
}

// validate reports the first violation in ev: At and Verb are
// required, and any usage it carries must be non-negative.
func (ev Event) validate() error {
	if ev.At == "" {
		return errors.New("event.at is empty")
	}
	if ev.Verb == "" {
		return errors.New("event.verb is empty")
	}
	return ev.Usage.Validate()
}

// Document is one Delivery run's metrics history, schema-versioned
// like internal/state and internal/manifest.
type Document struct {
	SchemaVersion int    `json:"schema_version"`
	RunID         string `json:"run_id"`
	// Host is the run's host (claude, codex or opencode). Documents written
	// before it was recorded carry none.
	Host string `json:"host,omitempty"`
	// ArchitectSessions are the Claude Code session ids that ran the run's
	// verbs, for orch run complete to read their usage. Documents written
	// before it was recorded carry none.
	ArchitectSessions []string      `json:"architect_sessions,omitempty"`
	Events            []Event       `json:"events"`
	Observations      []Observation `json:"observations,omitempty"`
}

func dirPath(repoRoot string) string {
	return filepath.Join(repoRoot, filepath.FromSlash(Dir))
}

func docPath(repoRoot, runID string) string {
	return filepath.Join(dirPath(repoRoot), runID+".json")
}

// validateRunID fails closed on anything that is not a plain
// filename-safe token: Append never joins unvalidated input into a
// path.
func validateRunID(runID string) error {
	if !runIDPattern.MatchString(runID) {
		return fmt.Errorf("invalid run id %q", runID)
	}
	return nil
}

// strictDecode decodes data into v, rejecting unknown fields and
// trailing data, mirroring internal/run's decodeRequest and
// internal/state's Load discipline for schema-versioned documents.
func strictDecode(data []byte, v any) error {
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		return err
	}
	if err := dec.Decode(new(any)); err != io.EOF {
		return errors.New("trailing data after document")
	}
	return nil
}

// SetHost records the run's host in its metrics document, creating the
// document if needed. It requires the same external serialization as Append.
func SetHost(repoRoot, runID, host string) error {
	if err := validateRunID(runID); err != nil {
		return err
	}
	switch host {
	case "claude", "codex", "opencode":
	default:
		return fmt.Errorf("invalid metrics host %q", host)
	}
	doc, err := load(repoRoot, runID)
	if err != nil {
		return err
	}
	doc.Host = host
	return save(repoRoot, doc)
}

// AddArchitectSession adds session to the run's ArchitectSessions, creating
// the document if needed and writing nothing when it is already there. It
// requires the same external serialization as Append.
func AddArchitectSession(repoRoot, runID, session string) error {
	if err := validateRunID(runID); err != nil {
		return err
	}
	if !identifier(session) {
		return fmt.Errorf("invalid architect session %q", session)
	}
	doc, err := load(repoRoot, runID)
	if err != nil {
		return err
	}
	if slices.Contains(doc.ArchitectSessions, session) {
		return nil
	}
	doc.ArchitectSessions = append(doc.ArchitectSessions, session)
	return save(repoRoot, doc)
}

// ArchitectSessions returns the run's recorded ArchitectSessions; a run with
// no document has none.
func ArchitectSessions(repoRoot, runID string) ([]string, error) {
	if err := validateRunID(runID); err != nil {
		return nil, err
	}
	doc, err := load(repoRoot, runID)
	return doc.ArchitectSessions, err
}

// Append records ev under repoRoot's run-scoped metrics document
// (.orchestrator/metrics/<runID>.json): it validates runID and ev,
// loads and validates the existing document if one is present (a
// missing file starts a fresh one at the current SchemaVersion),
// appends ev, creates the metrics directory if needed, and writes the
// document back atomically.
func Append(repoRoot, runID string, ev Event) error {
	if err := validateRunID(runID); err != nil {
		return err
	}
	if err := ev.validate(); err != nil {
		return err
	}

	doc, err := load(repoRoot, runID)
	if err != nil {
		return err
	}
	doc.Events = append(doc.Events, ev)
	return save(repoRoot, doc)
}

func load(repoRoot, runID string) (Document, error) {
	path := docPath(repoRoot, runID)
	doc := Document{SchemaVersion: SchemaVersion, RunID: runID}
	data, err := os.ReadFile(path)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		// A fresh document: doc already holds the right schema/run id.
	case err != nil:
		return doc, fmt.Errorf("read %s: %w", path, err)
	default:
		if decErr := strictDecode(data, &doc); decErr != nil {
			return doc, fmt.Errorf("parse %s: %v", path, decErr)
		}
		if err := doc.validate(); err != nil {
			return doc, fmt.Errorf("%s: %w", path, err)
		}
		if doc.RunID != runID {
			return doc, fmt.Errorf("%s: run_id %q does not match %q", path, doc.RunID, runID)
		}
	}

	return doc, nil
}

func save(repoRoot string, doc Document) error {
	dir := dirPath(repoRoot)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("create %s: %w", dir, err)
	}
	return write(docPath(repoRoot, doc.RunID), dir, doc)
}

// write atomically replaces path with doc's JSON encoding: temp file
// in dir (path's directory), sync, then rename — state.go's write
// shape exactly (state.go:315-341), so a crash mid-write never
// corrupts a document an earlier Append already committed, and the
// rename replaces on Windows too.
func write(path, dir string, doc Document) error {
	data, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return fmt.Errorf("encode %s: %w", path, err)
	}
	f, err := os.CreateTemp(dir, "metrics-*.tmp")
	if err != nil {
		return fmt.Errorf("write %s: %w", path, err)
	}
	tmp := f.Name()
	_, err = f.Write(append(data, '\n'))
	if err == nil {
		err = f.Sync()
	}
	if closeErr := f.Close(); err == nil {
		err = closeErr
	}
	if err == nil {
		err = os.Rename(tmp, path)
	}
	if err != nil {
		_ = os.Remove(tmp) // best effort; the prior document is untouched
		return fmt.Errorf("write %s: %w", path, err)
	}
	return nil
}

// LoadAll reads every run-*.json file in repoRoot's metrics directory,
// sorted by filename (run ids are UTC-stamp-prefixed and sort
// chronologically, state/transition.go:168-176's newRunID). A missing
// directory is not an error — it reports (nil, nil) and, critically,
// never creates the directory itself: `orch metrics` is a read path,
// and disabled metrics must never gain storage merely from running it
// (PRD §23). Every file is strict-decoded and fails closed on a
// corrupt file or an unsupported schema_version, naming the file.
func LoadAll(repoRoot string) ([]Document, error) {
	pattern := filepath.Join(dirPath(repoRoot), "run-*.json")
	matches, err := filepath.Glob(pattern)
	if err != nil {
		return nil, fmt.Errorf("list %s: %w", pattern, err)
	}
	sort.Strings(matches)

	var docs []Document
	for _, path := range matches {
		data, err := os.ReadFile(path)
		if err != nil {
			return nil, fmt.Errorf("read %s: %w", path, err)
		}
		var doc Document
		if err := strictDecode(data, &doc); err != nil {
			return nil, fmt.Errorf("parse %s: %v", path, err)
		}
		if err := doc.validate(); err != nil {
			return nil, fmt.Errorf("%s: %w", path, err)
		}
		if filepath.Base(path) != doc.RunID+".json" {
			return nil, fmt.Errorf("%s: run_id does not match filename", path)
		}
		docs = append(docs, doc)
	}
	return docs, nil
}
