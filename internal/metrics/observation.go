package metrics

import (
	"errors"
	"fmt"
	"math"
	"reflect"
	"strings"
	"time"
	"unicode"
)

const ObservationVersion = 1

// Profile is reported evidence, not a routing Selection. Each field may be
// unknown; an explicit empty variant means the host used no variant.
type Profile struct {
	Model   string  `json:"model,omitempty"`
	Effort  string  `json:"effort,omitempty"`
	Variant *string `json:"variant,omitempty"`
}

// Counters keeps native fields independent. Nil is unknown, including a total
// that a host did not report; no total is synthesized from input/output/cache.
// Every present field has the enclosing observation's source and stream.
type Counters struct {
	InputTokens         *int64 `json:"input_tokens,omitempty"`
	OutputTokens        *int64 `json:"output_tokens,omitempty"`
	CacheReadTokens     *int64 `json:"cache_read_tokens,omitempty"`
	CacheCreationTokens *int64 `json:"cache_creation_tokens,omitempty"`
	TotalTokens         *int64 `json:"total_tokens,omitempty"`
}

func (c Counters) fields() [5]*int64 {
	return [5]*int64{c.InputTokens, c.OutputTokens, c.CacheReadTokens, c.CacheCreationTokens, c.TotalTokens}
}

func counters(fields [5]*int64) Counters {
	return Counters{fields[0], fields[1], fields[2], fields[3], fields[4]}
}

type CounterSample struct {
	Stream   string   `json:"stream"`
	Mode     string   `json:"mode"` // cumulative or delta; fixed for a stream
	Sequence int64    `json:"sequence"`
	Counters Counters `json:"counters"`
}

type Interval struct {
	Kind  string `json:"kind"` // active-agent, verification, ci-waiting, human-waiting
	Start string `json:"start"`
	End   string `json:"end"`
}

// Observation is the JSON recording request and the persisted evidence. ID is
// unique within RunID. Exactly one sample, interval, or outcome is required.
// Attempts are caller-assigned identifiers; review cycles are positive ordinals.
// Neither is inferred from the current lifecycle state.
type Observation struct {
	SchemaVersion int            `json:"schema_version"`
	RunID         string         `json:"run_id"`
	ID            string         `json:"id"`
	At            string         `json:"at"`
	Source        string         `json:"source"`
	IssueNumber   int            `json:"issue_number,omitempty"`
	Role          string         `json:"role,omitempty"`
	Attempt       string         `json:"attempt,omitempty"`
	ReviewCycle   int            `json:"review_cycle,omitempty"`
	Host          string         `json:"host,omitempty"`
	Session       string         `json:"session,omitempty"`
	Requested     *Profile       `json:"requested,omitempty"`
	Observed      *Profile       `json:"observed,omitempty"`
	Sample        *CounterSample `json:"sample,omitempty"`
	Interval      *Interval      `json:"interval,omitempty"`
	Outcome       string         `json:"outcome,omitempty"`
}

// identifier permits opaque native identifiers, but not empty/whitespace or
// control characters. Only RunID becomes a path and uses validateRunID instead.
func identifier(s string) bool {
	return s != "" && len(s) <= 512 && strings.IndexFunc(s, func(r rune) bool {
		return unicode.IsSpace(r) || unicode.IsControl(r)
	}) == -1
}

func ParseObservation(data []byte) (Observation, error) {
	var o Observation
	if err := strictDecode(data, &o); err != nil {
		return o, fmt.Errorf("parse observation: %w", err)
	}
	return o, o.Validate()
}

func (o Observation) Validate() error {
	if o.SchemaVersion != ObservationVersion {
		return fmt.Errorf("unsupported observation schema_version %d (supports %d)", o.SchemaVersion, ObservationVersion)
	}
	if err := validateRunID(o.RunID); err != nil {
		return err
	}
	if !strings.HasPrefix(o.RunID, "run-") {
		return errors.New("observation run_id must start with run-")
	}
	if !identifier(o.ID) || !identifier(o.Source) {
		return errors.New("observation id and source must be nonempty identifiers")
	}
	for _, s := range []string{o.Attempt, o.Session} {
		if s != "" && !identifier(s) {
			return errors.New("invalid observation attempt or session identifier")
		}
	}
	if o.IssueNumber < 0 || o.ReviewCycle < 0 || (o.IssueNumber == 0 && (o.Attempt != "" || o.ReviewCycle != 0)) {
		return errors.New("attempt/review_cycle requires an issue; issue_number and review_cycle must be positive when supplied")
	}
	switch o.Role {
	case "", "architect", "scout", "implementer", "specialist", "reviewer":
	default:
		return fmt.Errorf("unknown observation role %q", o.Role)
	}
	switch o.Host {
	case "", "claude", "codex", "opencode":
	default:
		return fmt.Errorf("unknown observation host %q", o.Host)
	}
	for _, p := range []*Profile{o.Requested, o.Observed} {
		if p == nil {
			continue
		}
		if (p.Model != "" && !identifier(p.Model)) || (p.Effort != "" && !identifier(p.Effort)) || (p.Variant != nil && *p.Variant != "" && !identifier(*p.Variant)) {
			return errors.New("invalid observation profile identifier")
		}
		if p.Effort != "" && p.Variant != nil {
			return errors.New("observation profile cannot have both effort and variant")
		}
	}
	at, err := time.Parse(time.RFC3339Nano, o.At)
	if err != nil {
		return fmt.Errorf("invalid observation at: %w", err)
	}
	kinds := 0
	if s := o.Sample; s != nil {
		kinds++
		if o.Host == "" || o.Session == "" || !identifier(s.Stream) || s.Sequence <= 0 {
			return errors.New("sample requires host, session, stream and positive sequence")
		}
		if s.Mode != "cumulative" && s.Mode != "delta" {
			return errors.New("sample mode must be cumulative or delta")
		}
		present := false
		for _, v := range s.Counters.fields() {
			if v != nil {
				present = true
				if *v < 0 {
					return errors.New("negative sample counter")
				}
			}
		}
		if !present {
			return errors.New("sample requires at least one present counter")
		}
	}
	if i := o.Interval; i != nil {
		kinds++
		switch i.Kind {
		case "active-agent", "verification", "ci-waiting", "human-waiting":
		default:
			return fmt.Errorf("unknown interval kind %q", i.Kind)
		}
		start, err1 := time.Parse(time.RFC3339Nano, i.Start)
		end, err2 := time.Parse(time.RFC3339Nano, i.End)
		if err1 != nil || err2 != nil || end.Before(start) || end.After(at) || !start.Add(end.Sub(start)).Equal(end) {
			return errors.New("invalid interval: require start <= end <= at and duration within int64 nanoseconds")
		}
	}
	if o.Outcome != "" {
		kinds++
		switch o.Outcome {
		case "implementation-failure", "infrastructure-failure", "evidence-correction", "wrong-requirement", "escalation", "approval":
		default:
			return fmt.Errorf("unknown reported outcome %q", o.Outcome)
		}
	}
	if kinds != 1 {
		return errors.New("observation requires exactly one sample, interval or outcome")
	}
	return nil
}

// CounterContributions returns one counter delta per observation (empty for
// intervals/outcomes). Streams are scoped by run, host, session, source and
// stream identifier; never combine differently defined streams as native totals.
// Replaying accepted history rebuilds baselines without a second state store.
func CounterContributions(observations []Observation) ([]Counters, error) {
	type streamKey struct{ run, host, session, source, stream string }
	type baseline struct {
		mode     string
		sequence int64
		at       time.Time
		values   [5]int64
	}
	streams := map[streamKey]baseline{}
	ids := map[[2]string]bool{}
	result := make([]Counters, len(observations))
	for n, o := range observations {
		if err := o.Validate(); err != nil {
			return nil, fmt.Errorf("observation %q: %w", o.ID, err)
		}
		id := [2]string{o.RunID, o.ID}
		if ids[id] {
			return nil, fmt.Errorf("duplicate stored observation id %q", o.ID)
		}
		ids[id] = true
		s := o.Sample
		if s == nil {
			continue
		}
		key := streamKey{o.RunID, o.Host, o.Session, o.Source, s.Stream}
		b, exists := streams[key]
		at, _ := time.Parse(time.RFC3339Nano, o.At)
		if exists && (b.mode != s.Mode || s.Sequence <= b.sequence || at.Before(b.at)) {
			return nil, fmt.Errorf("observation %q: counter stream mode changed or sample out of order", o.ID)
		}
		var delta [5]*int64
		for i, value := range s.Counters.fields() {
			if value == nil {
				continue
			}
			d := *value
			if s.Mode == "cumulative" {
				if d < b.values[i] {
					return nil, fmt.Errorf("observation %q: regressing cumulative counter", o.ID)
				}
				d -= b.values[i]
			}
			if d > math.MaxInt64-b.values[i] {
				return nil, fmt.Errorf("observation %q: counter stream overflow", o.ID)
			}
			b.values[i] += d
			delta[i] = &d
		}
		b.mode, b.sequence, b.at = s.Mode, s.Sequence, at
		streams[key] = b
		result[n] = counters(delta)
	}
	return result, nil
}

func (doc Document) validate() error {
	if doc.SchemaVersion != 1 && doc.SchemaVersion != SchemaVersion {
		return fmt.Errorf("unsupported schema_version %d (this build understands 1 and %d)", doc.SchemaVersion, SchemaVersion)
	}
	if err := validateRunID(doc.RunID); err != nil {
		return err
	}
	if doc.SchemaVersion == 1 && len(doc.Observations) != 0 {
		return errors.New("schema-1 document cannot contain observations")
	}
	for _, ev := range doc.Events {
		if err := ev.validate(); err != nil {
			return err
		}
	}
	for _, o := range doc.Observations {
		if o.RunID != doc.RunID {
			return errors.New("observation run_id does not match document")
		}
	}
	_, err := CounterContributions(doc.Observations)
	return err
}

// Record atomically accepts new evidence or returns false for an exact replay.
// Like Append, it requires external serialization and caller-validated run/issue
// association. Neither storage writer owns a lock or checks lifecycle state.
func Record(repoRoot string, o Observation) (bool, error) {
	if err := o.Validate(); err != nil {
		return false, err
	}
	doc, err := load(repoRoot, o.RunID)
	if err != nil {
		return false, err
	}
	for _, old := range doc.Observations {
		if old.ID == o.ID {
			if reflect.DeepEqual(old, o) {
				return false, nil
			}
			return false, fmt.Errorf("conflicting observation id %q", o.ID)
		}
	}
	doc.SchemaVersion = SchemaVersion
	doc.Observations = append(doc.Observations, o)
	// ponytail: replay is linear in run history; index only if measured run
	// sizes make validation expensive, retaining the document as authority.
	if err := doc.validate(); err != nil {
		return false, err
	}
	if err := save(repoRoot, doc); err != nil {
		return false, err
	}
	return true, nil
}
