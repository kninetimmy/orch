package evalplan

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"reflect"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/kninetimmy/orch/internal/codexnative"
	"github.com/kninetimmy/orch/internal/evalcorpus"
	"github.com/kninetimmy/orch/internal/metrics"
)

const (
	maxControllerAttempts = 1024
	maxControllerEntries  = 65536
	maxControllerBytes    = 256 * 1024 * 1024
	maxProgressRecords    = 2*maxControllerAttempts + 3
)

var evaluationID = regexp.MustCompile(`^eval-[a-z2-7]{26}$`)

// Evaluation is an immutable controller identity, not approval or a Delivery ID.
type Evaluation struct {
	SchemaVersion int    `json:"schema_version"`
	ID            string `json:"id"`
	Repository    string `json:"repository"`
	PreparedAt    string `json:"prepared_at"`
	Preparation   Record `json:"preparation"`
}

type AttemptRef struct {
	Number int    `json:"number"`
	Kind   string `json:"kind"`
	SHA256 string `json:"sha256,omitempty"`
}

type Slot struct {
	Unit
	Status           string       `json:"status"`
	Grade            string       `json:"grade"`
	AttemptsConsumed int64        `json:"attempts_consumed"`
	RepairsConsumed  int64        `json:"repairs_consumed"`
	Attempts         []AttemptRef `json:"attempts"`
}

// Progress is a complete immutable snapshot. Missing terminal evidence is never
// completed work. Inspection notes are computed by Status, not written back.
type Progress struct {
	SchemaVersion    int      `json:"schema_version"`
	EvaluationID     string   `json:"evaluation_id"`
	EvaluationSHA256 string   `json:"evaluation_sha256"`
	PlanDigest       string   `json:"plan_digest"`
	Sequence         int      `json:"sequence"`
	PreviousSHA256   string   `json:"previous_sha256,omitempty"`
	State            string   `json:"state"`
	At               string   `json:"at"`
	Reason           string   `json:"reason,omitempty"`
	Slots            []Slot   `json:"slots"`
	Inspection       []string `json:"inspection,omitempty"`
}

type DigestedFile struct {
	Path   string `json:"path"`
	SHA256 string `json:"sha256"`
}

// NativeEvidence preserves reports with their original counter semantics and
// missingness. It is never submitted to the current-Delivery metrics recorder.
type NativeEvidence struct {
	SchemaVersion int                              `json:"schema_version,omitempty"`
	Binding       *codexnative.EvaluationBinding   `json:"binding,omitempty"`
	TaskID        string                           `json:"task_id,omitempty"`
	ThreadID      string                           `json:"thread_id,omitempty"`
	SessionID     string                           `json:"session_id,omitempty"`
	TurnID        string                           `json:"turn_id,omitempty"`
	Status        string                           `json:"status,omitempty"`
	Requested     *metrics.Profile                 `json:"requested,omitempty"`
	Observed      *metrics.Profile                 `json:"observed,omitempty"`
	Observations  []metrics.Observation            `json:"observations,omitempty"`
	Instructions  *codexnative.InstructionEvidence `json:"instructions,omitempty"`
	Cleanup       *codexnative.SessionCleanup      `json:"cleanup,omitempty"`
	Failure       string                           `json:"failure,omitempty"`
}

// Eligibility reports checked native controls, not observed inference identity,
// native completion or semantic acceptance. Legacy evidence lacks model proof.
type Eligibility struct {
	HostVersion        string `json:"host_version,omitempty"`
	SandboxReady       bool   `json:"sandbox_ready,omitempty"`
	ProfileAllowed     bool   `json:"profile_allowed,omitempty"`
	ToolsDisabled      bool   `json:"tools_disabled,omitempty"`
	DisabledMCP        *int   `json:"disabled_mcp,omitempty"`
	ModelToolsVerified bool   `json:"model_tools_verified,omitempty"`
}

type Cleanup struct {
	Status             string `json:"status"`
	Detail             string `json:"detail"`
	InterruptionAsked  bool   `json:"interruption_asked"`
	WorkerReturned     bool   `json:"worker_returned"`
	NativeAcknowledged *bool  `json:"native_acknowledged,omitempty"`
}

type AttemptRecord struct {
	SchemaVersion   int             `json:"schema_version"`
	EvaluationID    string          `json:"evaluation_id"`
	PlanDigest      string          `json:"plan_digest"`
	Unit            Unit            `json:"unit"`
	Number          int             `json:"number"`
	Kind            string          `json:"kind"`
	PacketSHA256    string          `json:"packet_sha256"`
	CaseSHA256      string          `json:"case_sha256"`
	Packet          string          `json:"packet"`
	Scratch         string          `json:"scratch"`
	StartedAt       string          `json:"started_at"`
	FinishedAt      string          `json:"finished_at,omitempty"`
	Outcome         string          `json:"outcome"`
	ExecutionSource string          `json:"execution_source"`
	Detail          string          `json:"detail,omitempty"`
	Grade           string          `json:"grade"`
	Verification    string          `json:"verification"`
	Initial         []DigestedFile  `json:"initial"`
	Artifacts       []DigestedFile  `json:"artifacts"`
	Output          *DigestedFile   `json:"output,omitempty"`
	Native          *NativeEvidence `json:"native,omitempty"`
	InvalidNative   *DigestedFile   `json:"invalid_native,omitempty"`
	Eligibility     *Eligibility    `json:"eligibility,omitempty"`
	Cleanup         Cleanup         `json:"cleanup"`
}

func storedBytes(value any) ([]byte, error) {
	data, err := json.MarshalIndent(value, "", "  ")
	return append(data, '\n'), err
}

func storedDigest(value any) string {
	data, _ := storedBytes(value) // all retained types are JSON-safe
	return evalcorpus.Digest(data)
}

func openEvaluation(storageRoot, id string) (*guardedDir, *Evaluation, string, error) {
	if !evaluationID.MatchString(id) {
		return nil, nil, "", fmt.Errorf("invalid evaluation ID")
	}
	parent, err := openGuarded(storageRoot)
	if err != nil {
		return nil, nil, "", err
	}
	defer parent.close()
	g, err := parent.child(id)
	if err != nil {
		return nil, nil, "", err
	}
	g.depth = 0 // this evaluation is the inventory/depth boundary
	data, err := g.read("evaluation.json", maxRecordBytes)
	var e Evaluation
	if err == nil {
		err = strictStored(data, &e)
	}
	if err == nil && (e.SchemaVersion != 1 || e.ID != id) {
		err = fmt.Errorf("evaluation identity/schema mismatch")
	}
	if err == nil {
		err = validateRecord(&e.Preparation, parent.path, e.Preparation.PlanDigest)
	}
	if err == nil {
		_, err = time.Parse(time.RFC3339Nano, e.PreparedAt)
	}
	if err != nil {
		g.close()
		return nil, nil, "", fmt.Errorf("incomplete or invalid evaluation; artifacts preserved: %w", err)
	}
	return g, &e, evalcorpus.Digest(data), nil
}

// Prepare creates a distinct controller record after revalidating a saved
// preview and every selected source byte. This grants no execution authority.
func Prepare(ctx context.Context, repo, storageRoot, digest string) (*Evaluation, error) {
	return prepare(ctx, repo, storageRoot, digest, nil)
}

func prepare(ctx context.Context, repo, storageRoot, digest string, approval *Approval) (*Evaluation, error) {
	r, err := Load(ctx, repo, storageRoot, digest)
	if err != nil {
		return nil, err
	}
	if r.Preview.Counts.MaximumAttemptsIncludingRepairs > maxControllerAttempts {
		return nil, fmt.Errorf("controller capacity is %d frozen attempts; preview remains valid", maxControllerAttempts)
	}
	if _, err := loadSources(ctx, repo, r.Plan); err != nil {
		return nil, err
	}
	repo, err = localPath("", repo, true)
	if err != nil {
		return nil, err
	}
	repo, err = directory(repo, false)
	if err != nil {
		return nil, err
	}
	parent, err := openGuarded(r.Plan.StorageRoot)
	if err != nil {
		return nil, err
	}
	defer parent.close()
	id := "eval-" + strings.ToLower(rand.Text())
	var receipt *ApprovalRecord
	if approval != nil {
		receipt = &ApprovalRecord{1, id, *approval, Scope(r)}
		if err := claimApproval(parent, receipt); err != nil {
			return nil, err
		}
	}
	g, err := parent.createDir(id)
	if err != nil {
		return nil, err
	}
	defer g.close()
	g.budget = &writeBudget{used: MaxPlanBytes, entries: 16} // reserve a bounded concurrent stop record
	e := &Evaluation{SchemaVersion: 1, ID: id, Repository: repo, PreparedAt: now(), Preparation: *r}
	if err := g.publish("evaluation.json", e); err != nil {
		return nil, err
	}
	if receipt != nil {
		if err := g.publish("approval.json", receipt); err != nil {
			return nil, err
		}
	}
	p := &Progress{SchemaVersion: 1, EvaluationID: id, EvaluationSHA256: storedDigest(e), PlanDigest: digest,
		State: "prepared", At: now(), Slots: make([]Slot, 0, len(r.Preview.Schedule))}
	for _, unit := range r.Preview.Schedule {
		p.Slots = append(p.Slots, Slot{Unit: unit, Status: "unrun", Grade: "unknown", Attempts: []AttemptRef{}})
	}
	if err := g.publish(progressName(0), p); err != nil {
		return nil, err
	}
	return e, nil
}

func now() string { return time.Now().UTC().Format(time.RFC3339Nano) }

func progressName(n int) string { return fmt.Sprintf("progress-%06d.json", n) }
func attemptName(unit, attempt int) string {
	return fmt.Sprintf("unit-%06d-attempt-%06d", unit, attempt)
}

func (g *guardedDir) entries() ([]fs.DirEntry, error) {
	if err := g.check(); err != nil {
		return nil, err
	}
	f, err := g.root.Open(".")
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()
	entries, err := f.ReadDir(maxControllerEntries + 1)
	if err != nil && !errors.Is(err, io.EOF) {
		return nil, err
	}
	if len(entries) > maxControllerEntries {
		return nil, fmt.Errorf("controller directory exceeds %d entries", maxControllerEntries)
	}
	return entries, g.check()
}

// inventory bounds the entire evaluation, including unpublished/unknown files.
// It reads metadata only, and never follows an untrusted directory alias.
func inventory(g *guardedDir) (int64, int, []string, error) {
	var size int64
	var count int
	var incomplete []string
	var walk func(*guardedDir, string, int) error
	walk = func(dir *guardedDir, prefix string, depth int) error {
		if depth > 32 {
			return fmt.Errorf("controller artifacts exceed 32 directory levels")
		}
		entries, err := dir.entries()
		if err != nil {
			return err
		}
		for _, d := range entries {
			count++
			if count > maxControllerEntries {
				return fmt.Errorf("evaluation exceeds %d artifact entries", maxControllerEntries)
			}
			if err := relativeName(d.Name()); err != nil {
				return err
			}
			info, err := dir.root.Lstat(d.Name())
			if errors.Is(err, fs.ErrNotExist) && strings.HasPrefix(d.Name(), ".pending-") {
				continue // only an atomic publisher's transient name may disappear
			}
			if err != nil || reparse(info) {
				return fmt.Errorf("linked or unverifiable controller artifact: %s: %v", d.Name(), err)
			}
			name := prefix + d.Name()
			if info.IsDir() {
				child, err := dir.child(d.Name())
				if err != nil {
					return err
				}
				err = walk(child, name+"/", depth+1)
				child.close()
				if err != nil {
					return err
				}
			} else {
				if !info.Mode().IsRegular() || info.Size() < 0 || info.Size() > maxRecordBytes || info.Size() > maxControllerBytes-size {
					return fmt.Errorf("nonregular or oversized controller evidence: %s", name)
				}
				// Unknown/pending files must be independent too. Do not read their
				// contents just to verify aliases, or follow any intermediate link.
				for attempt := 0; ; attempt++ {
					file, err := dir.root.Open(d.Name())
					if errors.Is(err, fs.ErrNotExist) && strings.HasPrefix(d.Name(), ".pending-") {
						break
					}
					if err != nil {
						return err
					}
					opened, statErr := file.Stat()
					links, linkErr := linkCount(file)
					closeErr := file.Close()
					if statErr != nil || linkErr != nil || closeErr != nil || !os.SameFile(info, opened) {
						return fmt.Errorf("unverifiable controller file identity: %s: %v", name, errors.Join(statErr, linkErr, closeErr))
					}
					if links == 1 {
						break
					}
					if attempt == 49 || !strings.HasPrefix(d.Name(), ".pending-") && !strings.HasSuffix(d.Name(), ".json") {
						return fmt.Errorf("%w: %s", errLinkedFile, name)
					}
					time.Sleep(20 * time.Millisecond)
				}
				size += info.Size()
				if strings.HasPrefix(d.Name(), ".pending-") {
					incomplete = append(incomplete, name)
				}
			}
		}
		return nil
	}
	err := walk(g, "", 0)
	return size, count, incomplete, err
}

var outcomes = []string{"native-completed", "task-failure", "infrastructure-failure", "timeout", "interrupted", "disconnected", "refused", "protocol-invalid", "invalid-evidence", "safety-failure"}

// State claims must follow the retained schedule, not just use a valid enum.
// Both publication and inspection use this same semantic check.
func validateProgressState(p *Progress) error {
	if len(p.Slots) == 0 {
		return fmt.Errorf("progress lacks scheduled slots")
	}
	unrun, unfinished := false, false
	last := ""
	for _, slot := range p.Slots {
		if slot.Status == "unrun" {
			if len(slot.Attempts) != 0 || slot.AttemptsConsumed != 0 || slot.RepairsConsumed != 0 {
				return fmt.Errorf("unrun slot has consumed attempts")
			}
			unrun = true
			if p.State == "completed" {
				return fmt.Errorf("completed schedule contains unrun work")
			}
			continue
		}
		if unrun || unfinished || len(slot.Attempts) == 0 {
			return fmt.Errorf("slot progression contradicts the frozen schedule")
		}
		for i, ref := range slot.Attempts {
			if ref.SHA256 == "" && (i != len(slot.Attempts)-1 || slot.Status != "running") {
				return fmt.Errorf("terminal or earlier attempt lacks complete evidence")
			}
		}
		if slot.Status == "running" {
			if slot.Attempts[len(slot.Attempts)-1].SHA256 != "" {
				return fmt.Errorf("running slot has no unfinished attempt")
			}
			unfinished = true
		} else if !slices.Contains(outcomes, slot.Status) {
			return fmt.Errorf("invalid slot status")
		}
		if p.State == "prepared" || p.State == "completed" && !slices.Contains([]string{"native-completed", "task-failure", "infrastructure-failure", "timeout"}, slot.Status) {
			return fmt.Errorf("controller state contradicts retained slot outcomes")
		}
		last = slot.Status
		if slices.Contains([]string{"interrupted", "disconnected", "refused", "protocol-invalid", "invalid-evidence", "safety-failure"}, last) {
			unfinished = true // these outcomes must stop subsequent units
		}
	}
	if p.State == "prepared" && p.Sequence != 0 || p.State == "refused" && last != "refused" || p.State == "incomplete" && last == "" {
		return fmt.Errorf("controller state lacks its required slot evidence")
	}
	return nil
}

func validateNative(native *NativeEvidence) error {
	if native == nil {
		return nil
	}
	if len(native.Observations) > 1024 {
		return fmt.Errorf("native observation capacity exceeded")
	}
	if native.SchemaVersion != 0 && native.SchemaVersion != 2 || native.SchemaVersion == 0 && (native.Binding != nil || native.TaskID != "" || native.Instructions != nil || native.Cleanup != nil || native.Failure != "") || native.SchemaVersion == 2 && (native.Binding == nil || native.Cleanup == nil) {
		return fmt.Errorf("unsupported or mixed native evidence schema")
	}
	for _, o := range native.Observations {
		if (native.SchemaVersion == 2) != (o.SchemaVersion == metrics.EvaluationObservationVersion) {
			return fmt.Errorf("native observation/evidence versions differ")
		}
	}
	// Observation has version-specific JSON decoding as well as semantic rules.
	// A typed payload must survive the same decode used by the retained reader.
	data, err := storedBytes(native)
	if err != nil {
		return err
	}
	var decoded NativeEvidence
	if err := strictStored(data, &decoded); err != nil {
		return fmt.Errorf("native evidence wire format invalid: %w", err)
	}
	if _, err := metrics.CounterContributions(decoded.Observations); err != nil {
		return fmt.Errorf("native observations invalid: %w", err)
	}
	return nil
}

func validateProgress(p *Progress, e *Evaluation, hash string, previous *Progress) error {
	if p.SchemaVersion != 1 || p.EvaluationID != e.ID || p.PlanDigest != e.Preparation.PlanDigest ||
		p.EvaluationSHA256 != hash || len(p.Slots) != len(e.Preparation.Preview.Schedule) || len(p.Inspection) != 0 ||
		!slices.Contains([]string{"prepared", "running", "completed", "stopped", "overall-cutoff", "refused", "safety-failure", "incomplete"}, p.State) {
		return fmt.Errorf("invalid progress identity, schedule or state")
	}
	if _, err := time.Parse(time.RFC3339Nano, p.At); err != nil {
		return err
	}
	if previous == nil {
		if p.Sequence != 0 || p.State != "prepared" || p.PreviousSHA256 != "" {
			return fmt.Errorf("invalid initial progress")
		}
	} else if p.Sequence != previous.Sequence+1 || p.PreviousSHA256 != storedDigest(previous) || previous.State != "prepared" && previous.State != "running" {
		return fmt.Errorf("invalid progress chain or transition after terminal evidence")
	}
	for i, slot := range p.Slots {
		if slot.Unit != e.Preparation.Preview.Schedule[i] || slot.Grade != "unknown" || slot.Attempts == nil ||
			!slices.Contains(append([]string{"unrun", "running"}, outcomes...), slot.Status) ||
			slot.AttemptsConsumed < 0 || slot.AttemptsConsumed > e.Preparation.Plan.Limits.MaxAttemptsPerUnit ||
			slot.RepairsConsumed < 0 || slot.RepairsConsumed > *e.Preparation.Plan.Limits.MaxRepairsPerUnit ||
			int64(len(slot.Attempts)) != slot.AttemptsConsumed+slot.RepairsConsumed {
			return fmt.Errorf("invalid slot identity, grade or consumed budget")
		}
		attempts, repairs := int64(0), int64(0)
		for j, ref := range slot.Attempts {
			if ref.Number != j+1 || !slices.Contains([]string{"initial", "retry", "repair"}, ref.Kind) ||
				(j == 0) != (ref.Kind == "initial") || ref.SHA256 != "" && !digestPattern.MatchString(ref.SHA256) {
				return fmt.Errorf("invalid attempt reference")
			}
			if ref.Kind == "repair" {
				repairs++
			} else {
				attempts++
			}
		}
		if attempts != slot.AttemptsConsumed || repairs != slot.RepairsConsumed {
			return fmt.Errorf("consumed retry/repair budgets differ from retained attempts")
		}
		if previous != nil {
			old := previous.Slots[i]
			if len(slot.Attempts) < len(old.Attempts) || slot.AttemptsConsumed < old.AttemptsConsumed || slot.RepairsConsumed < old.RepairsConsumed {
				return fmt.Errorf("progress resets consumed budgets")
			}
			for j, ref := range old.Attempts {
				if ref.Number != slot.Attempts[j].Number || ref.Kind != slot.Attempts[j].Kind || ref.SHA256 != "" && ref.SHA256 != slot.Attempts[j].SHA256 {
					return fmt.Errorf("progress replaces earlier attempt evidence")
				}
			}
		}
	}
	return validateProgressState(p)
}

func readProgress(g *guardedDir, e *Evaluation, hash string) (*Progress, error) {
	_, _, pending, err := inventory(g)
	if err != nil {
		return nil, err
	}
	entries, err := g.entries()
	if err != nil {
		return nil, err
	}
	var names []string
	for _, d := range entries {
		if strings.HasPrefix(d.Name(), "progress-") {
			names = append(names, d.Name())
		}
	}
	slices.Sort(names)
	if len(names) == 0 || len(names) > maxProgressRecords {
		return nil, fmt.Errorf("missing or excessive progress records; completion unknown")
	}
	var previous *Progress
	seen := map[string]struct{ sha, outcome string }{}
	for n, name := range names {
		if name != progressName(n) {
			return nil, fmt.Errorf("incomplete progress chain: %s", name)
		}
		data, err := g.read(name, maxRecordBytes)
		if err != nil {
			return nil, err
		}
		var p Progress
		if err := strictStored(data, &p); err != nil {
			return nil, err
		}
		if storedDigest(&p) != evalcorpus.Digest(data) {
			return nil, fmt.Errorf("immutable progress bytes changed")
		}
		if err := validateProgress(&p, e, hash, previous); err != nil {
			return nil, err
		}
		for _, slot := range p.Slots {
			for _, ref := range slot.Attempts {
				if ref.SHA256 == "" {
					continue
				}
				name := attemptName(slot.Ordinal, ref.Number) + "/attempt.json"
				if retained := seen[name]; retained.sha == ref.SHA256 {
					if ref.Number == len(slot.Attempts) && slot.Status != "running" && slot.Status != retained.outcome {
						return nil, fmt.Errorf("slot status conflicts with final attempt outcome")
					}
					continue
				}
				data, err := g.read(name, maxRecordBytes)
				if err != nil || evalcorpus.Digest(data) != ref.SHA256 {
					return nil, fmt.Errorf("attempt evidence missing or changed: %s: %v", name, err)
				}
				var a AttemptRecord
				if err := strictStored(data, &a); err != nil {
					return nil, err
				}
				if (a.SchemaVersion != 1 && a.SchemaVersion != 2) || a.EvaluationID != e.ID || a.PlanDigest != e.Preparation.PlanDigest ||
					a.Unit != slot.Unit || a.Number != ref.Number || a.Kind != ref.Kind || a.Grade != "unknown" ||
					!slices.Contains(outcomes, a.Outcome) || a.FinishedAt == "" ||
					!slices.Contains([]string{"native-eligibility-only", "no-model-test-script", "codex-native-evaluation"}, a.ExecutionSource) || a.ExecutionSource == "codex-native-evaluation" && (a.SchemaVersion != 2 || e.Preparation.Plan.Version != 2) {
					return nil, fmt.Errorf("invalid attempt identity/outcome")
				}
				if ref.Number == len(slot.Attempts) && slot.Status != "running" && slot.Status != a.Outcome {
					return nil, fmt.Errorf("slot status conflicts with final attempt outcome")
				}
				if err := validateAttemptNative(&a, e); err != nil {
					return nil, err
				}
				if a.InvalidNative != nil {
					if a.Native != nil || a.Outcome != "invalid-evidence" {
						return nil, fmt.Errorf("invalid native evidence presented as usable")
					}
					bytes, err := g.read(attemptName(slot.Ordinal, ref.Number)+"/"+a.InvalidNative.Path, MaxArtifactBytes)
					if err != nil || evalcorpus.Digest(bytes) != a.InvalidNative.SHA256 {
						return nil, fmt.Errorf("invalid native artifact missing or changed: %v", err)
					}
				}
				caseBytes, err := g.read(attemptName(slot.Ordinal, ref.Number)+"/case.json", maxRecordBytes)
				if err != nil || evalcorpus.Digest(caseBytes) != a.CaseSHA256 {
					return nil, fmt.Errorf("retained case metadata missing or changed: %v", err)
				}
				for _, artifact := range append(slices.Clone(a.Artifacts), a.Initial...) {
					bytes, err := g.read(attemptName(slot.Ordinal, ref.Number)+"/"+artifact.Path, MaxArtifactBytes)
					if err != nil || evalcorpus.Digest(bytes) != artifact.SHA256 {
						return nil, fmt.Errorf("retained artifact missing or changed: %s: %v", artifact.Path, err)
					}
				}
				if a.Output != nil {
					bytes, err := g.read(attemptName(slot.Ordinal, ref.Number)+"/"+a.Output.Path, MaxArtifactBytes)
					if err != nil || evalcorpus.Digest(bytes) != a.Output.SHA256 {
						return nil, fmt.Errorf("retained output missing or changed: %v", err)
					}
				}
				seen[name] = struct{ sha, outcome string }{ref.SHA256, a.Outcome}
			}
		}
		previous = &p
	}
	previous.Inspection = pending
	for _, slot := range previous.Slots {
		for _, attempt := range slot.Attempts {
			if attempt.SHA256 == "" {
				previous.Inspection = append(previous.Inspection, fmt.Sprintf("Unit %d attempt %d has no completed attempt record; execution and cleanup remain unknown.", slot.Ordinal, attempt.Number))
			}
		}
	}
	if stop, err := stopped(g, e); err != nil {
		return nil, err
	} else if stop {
		previous.Inspection = append(previous.Inspection, "Durable stop request retained.")
	}
	if previous.State == "running" {
		previous.Inspection = append(previous.Inspection, "No terminal controller record; active or interrupted execution remains incomplete. No takeover or replay is allowed.")
	}
	if _, err := g.root.Lstat("execution"); err == nil && previous.State == "prepared" {
		previous.Inspection = append(previous.Inspection, "Exclusive execution claim exists without running progress; execution outcome is unknown.")
	} else if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return nil, err
	}
	return previous, nil
}

// Status reads complete validated snapshots and digest-linked retained bytes.
// It starts no work, does not require live source availability, and never resumes.
func Status(storageRoot, id string) (*Progress, error) {
	g, e, hash, err := openEvaluation(storageRoot, id)
	if err != nil {
		return nil, err
	}
	defer g.close()
	p, err := readProgress(g, e, hash)
	if err == nil && e.Preparation.Plan.Version == 2 {
		p.Inspection = append(p.Inspection, "Version-2 attempts retain native eligibility, declared-context and execution evidence separately; native completion is not a semantic grade or Phase 1 completion.")
	}
	return p, err
}

type stopRecord struct {
	SchemaVersion int    `json:"schema_version"`
	EvaluationID  string `json:"evaluation_id"`
	PlanDigest    string `json:"plan_digest"`
	Request       string `json:"request"`
}

// Stop durably retains one idempotent request. Concurrent identical requests
// share a no-replace record; conflicting/corrupt requests fail without removal.
func Stop(storageRoot, id string) error {
	g, e, _, err := openEvaluation(storageRoot, id)
	if err != nil {
		return err
	}
	defer g.close()
	// One exclusive stop writer uses the reserved quota. Other requesters read
	// its no-replace publication; an incomplete claim stops execution as unknown.
	g.budget = &writeBudget{used: maxControllerBytes - MaxPlanBytes, entries: maxControllerEntries - 16}
	request, err := g.createDir("stop")
	if errors.Is(err, fs.ErrExist) {
		request, err = g.child("stop")
		if err != nil {
			return err
		}
		defer request.close()
		_, err = stopped(g, e)
		return err
	}
	if err != nil {
		return err
	}
	defer request.close()
	return request.publish("request.json", stopRecord{1, id, e.Preparation.PlanDigest, "stop"})
}

func stopped(g *guardedDir, e *Evaluation) (bool, error) {
	request, err := g.child("stop")
	if errors.Is(err, fs.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	defer request.close()
	var data []byte
	for attempt := 0; attempt < 50; attempt++ {
		data, err = request.read("request.json", MaxPlanBytes)
		if !errors.Is(err, fs.ErrNotExist) {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if err != nil {
		return false, fmt.Errorf("incomplete stop request; preserve claim: %w", err)
	}
	var stop stopRecord
	if err := strictStored(data, &stop); err != nil {
		return false, err
	}
	if !reflect.DeepEqual(stop, stopRecord{1, e.ID, e.Preparation.PlanDigest, "stop"}) {
		return false, fmt.Errorf("invalid stop request identity")
	}
	return true, nil
}

// appendProgress mutates a copy so its predecessor digest covers the original
// complete record, rather than newly changed fields.
func appendProgress(g *guardedDir, p *Progress, change func(*Progress)) (*Progress, error) {
	// ponytail: complete snapshots are quadratic over at most 1,024 attempts;
	// use an indexed journal only if measured runs need a larger capacity.
	data, _ := json.Marshal(p)
	var next Progress
	_ = json.Unmarshal(data, &next)
	next.Inspection = nil
	next.PreviousSHA256 = storedDigest(p)
	next.Sequence++
	next.At = now()
	change(&next)
	if err := validateProgressState(&next); err != nil {
		return p, err
	}
	if next.Sequence >= maxProgressRecords {
		return p, fmt.Errorf("progress record capacity exceeded")
	}
	if err := g.publish(progressName(next.Sequence), &next); err != nil {
		return p, err
	}
	return &next, nil
}
