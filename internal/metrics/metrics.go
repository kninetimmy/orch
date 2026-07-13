// Package metrics is the mechanical PRD §21 recorder: one
// schema-versioned JSON document per Delivery run at
// .orchestrator/metrics/<run-id>.json, local-only and never
// transmitted. Like gitops, ghops, and memhub it is policy-free — the
// Metrics.Enabled gate, event timestamps, and event content all belong
// to callers (internal/run, internal/cli); this package only knows how
// to validate, append to, and load documents. Disabled metrics means
// callers simply never call Append; LoadAll never creates the
// directory itself, so a repository that has never enabled metrics
// gains no storage from merely reading (PRD §23: disabled metrics
// create no storage).
//
// Exactly one Delivery run owns a repository at a time (the Delivery
// lock, internal/lockfile), so writes to any one run's document are
// serialized by construction — this package needs no locking of its
// own.
//
// PRD §21 mapping: first-pass review outcome is the first "review"
// event recorded for an issue; retries show up as ReviewCycles and
// repeated review events; model fallback is an "escalate" event;
// durations between lifecycle events fall out of consecutive Event.At
// stamps.
package metrics

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"

	"github.com/kninetimmy/orch/internal/manifest"
)

// SchemaVersion is the metrics document schema this build reads and
// writes.
const SchemaVersion = 1

// Dir is the repo-relative, slash-form location of the metrics
// directory: one file per Delivery run.
const Dir = ".orchestrator/metrics"

// runIDPattern is the closed run-id shape Append accepts. Run ids are
// already filename-safe by construction (state/transition.go's
// newRunID: "run-<UTC stamp>-<hex>"), but Append never joins
// unvalidated input into a path regardless of who calls it.
var runIDPattern = regexp.MustCompile(`^[A-Za-z0-9._-]+$`)

// Usage is adapter-reported model usage (PRD §21 "where available");
// every field is optional and omitted when zero. It is otherwise
// opaque adapter-reported data — the only content check this package
// performs is that no field is negative.
type Usage struct {
	InputTokens         int64 `json:"input_tokens,omitempty"`
	OutputTokens        int64 `json:"output_tokens,omitempty"`
	CacheReadTokens     int64 `json:"cache_read_tokens,omitempty"`
	CacheCreationTokens int64 `json:"cache_creation_tokens,omitempty"`
	DurationMS          int64 `json:"duration_ms,omitempty"`
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
	SchemaVersion int     `json:"schema_version"`
	RunID         string  `json:"run_id"`
	Events        []Event `json:"events"`
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
	if dec.More() {
		return errors.New("trailing data after document")
	}
	return nil
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

	path := docPath(repoRoot, runID)
	doc := Document{SchemaVersion: SchemaVersion, RunID: runID}
	data, err := os.ReadFile(path)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		// A fresh document: doc already holds the right schema/run id.
	case err != nil:
		return fmt.Errorf("read %s: %w", path, err)
	default:
		if decErr := strictDecode(data, &doc); decErr != nil {
			return fmt.Errorf("parse %s: %v", path, decErr)
		}
		if doc.SchemaVersion != SchemaVersion {
			return fmt.Errorf("%s: unsupported schema_version %d (this build understands %d)", path, doc.SchemaVersion, SchemaVersion)
		}
		if doc.RunID != runID {
			return fmt.Errorf("%s: run_id %q does not match %q", path, doc.RunID, runID)
		}
	}

	doc.Events = append(doc.Events, ev)

	dir := dirPath(repoRoot)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("create %s: %w", dir, err)
	}
	return write(path, dir, doc)
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
		if doc.SchemaVersion != SchemaVersion {
			return nil, fmt.Errorf("%s: unsupported schema_version %d (this build understands %d)", path, doc.SchemaVersion, SchemaVersion)
		}
		docs = append(docs, doc)
	}
	return docs, nil
}
