package evalplan

import (
	"errors"
	"fmt"
	"io/fs"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"time"

	"github.com/kninetimmy/orch/internal/evalcorpus"
)

type GradeReceipt struct {
	SchemaVersion    int            `json:"schema_version"`
	Sequence         int            `json:"sequence"`
	PreviousSHA256   string         `json:"previous_sha256,omitempty"`
	ID               string         `json:"id"`
	EvaluationID     string         `json:"evaluation_id"`
	EvaluationSHA256 string         `json:"evaluation_sha256"`
	PlanDigest       string         `json:"plan_digest"`
	SubmittedAt      string         `json:"submitted_at"`
	SubmissionSHA256 string         `json:"submission_sha256"`
	Files            []DigestedFile `json:"files"`
}

type retainedGrade struct {
	receipt    GradeReceipt
	submission GradeSubmission
	rubric     Rubric
}

type gradeJournal struct {
	entries []retainedGrade
	pending bool
}

func gradeRecordName(n int) string      { return fmt.Sprintf("record-%06d.json", n) }
func gradeBundleName(sha string) string { return "bundle-" + sha }

func gradeFiles(s *GradeSubmission) []DigestedFile {
	files := []DigestedFile{{"submission.json", storedDigest(s)}, {"rubric.json", s.Rubric.SHA256}}
	for _, e := range s.Evidence {
		files = append(files, DigestedFile{"evidence-" + e.ID, e.Artifact.SHA256})
	}
	return files
}

func selectedAttempt(g *guardedDir, e *Evaluation, p *Progress, unit, attempt int) (*AttemptRecord, AttemptRef, evalcorpus.Case, error) {
	var c evalcorpus.Case
	if unit < 1 || unit > len(p.Slots) || attempt < 1 || attempt > len(p.Slots[unit-1].Attempts) {
		return nil, AttemptRef{}, c, fmt.Errorf("explicit unit/attempt does not name retained evidence")
	}
	slot := p.Slots[unit-1]
	ref := slot.Attempts[attempt-1]
	if ref.SHA256 == "" {
		return nil, ref, c, fmt.Errorf("unfinished attempt cannot be graded")
	}
	prefix := attemptName(unit, attempt)
	data, err := g.read(prefix+"/attempt.json", maxRecordBytes)
	if err != nil || evalcorpus.Digest(data) != ref.SHA256 {
		return nil, ref, c, fmt.Errorf("grading attempt digest conflict: %v", err)
	}
	var a AttemptRecord
	if err := strictStored(data, &a); err != nil {
		return nil, ref, c, err
	}
	data, err = g.read(prefix+"/case.json", maxRecordBytes)
	if err != nil || evalcorpus.Digest(data) != a.CaseSHA256 {
		return nil, ref, c, fmt.Errorf("grading case digest conflict: %v", err)
	}
	if err := strictStored(data, &c); err != nil {
		return nil, ref, c, err
	}
	index := slices.IndexFunc(e.Preparation.Plan.Cases, func(c PublicCase) bool { return c.ID == slot.CaseID })
	if index < 0 || a.Unit != slot.Unit || a.Number != attempt || a.Kind != ref.Kind ||
		a.EvaluationID != e.ID || a.PlanDigest != e.Preparation.PlanDigest || c.ID != slot.CaseID || c.Version != slot.CaseVersion ||
		c.PacketSHA256 != a.PacketSHA256 || c.PacketSHA256 != e.Preparation.Plan.Cases[index].PacketSHA256 ||
		c.Role != e.Preparation.Plan.Cases[index].Role || c.Classification != e.Preparation.Plan.Cases[index].Classification || storedDigest(c) != a.CaseSHA256 {
		return nil, ref, c, fmt.Errorf("grading retained case/attempt identity conflict")
	}
	return &a, ref, c, nil
}

func openGradeArea(storageRoot, id string, create bool) (*guardedDir, error) {
	root, err := openGuarded(storageRoot)
	if err != nil {
		return nil, err
	}
	defer root.close()
	var grades *guardedDir
	if create {
		grades, err = directoryForPublication(root, "grades")
	} else {
		grades, err = root.child("grades")
	}
	if err != nil {
		return nil, err
	}
	defer grades.close()
	var area *guardedDir
	if create {
		area, err = directoryForPublication(grades, id)
	} else {
		area, err = grades.child(id)
	}
	if err == nil {
		area.depth = 0 // separate capacity; controller stop bytes are untouched
	}
	return area, err
}

func readGradeJournal(area, controller *guardedDir, e *Evaluation, hash string, p *Progress, ownedClaim bool) (*gradeJournal, error) {
	j := &gradeJournal{entries: []retainedGrade{}}
	if area == nil {
		return j, nil
	}
	_, _, pending, err := inventory(area)
	if err != nil {
		return nil, fmt.Errorf("grading namespace safety/capacity failure: %w", err)
	}
	j.pending = len(pending) != 0
	entries, err := area.entries()
	if err != nil {
		return nil, err
	}
	names, bundles := []string{}, map[string]bool{}
	for _, entry := range entries {
		name := entry.Name()
		switch {
		case strings.HasPrefix(name, "record-") && !entry.IsDir():
			names = append(names, name)
		case strings.HasPrefix(name, "bundle-") && entry.IsDir() && digestPattern.MatchString(strings.TrimPrefix(name, "bundle-")):
			bundles[name] = false
		case name == "publication" && entry.IsDir():
			j.pending = j.pending || !ownedClaim
		case strings.HasPrefix(name, ".pending-"):
			j.pending = true
		default:
			return nil, fmt.Errorf("unknown grading namespace identity: %s", name)
		}
	}
	slices.Sort(names)
	if len(names) > maxGradeRecords {
		return nil, fmt.Errorf("grading journal exceeds %d records", maxGradeRecords)
	}
	previous := ""
	for i, name := range names {
		if name != gradeRecordName(i+1) {
			return nil, fmt.Errorf("grading journal sequence gap/conflict")
		}
		data, err := area.read(name, MaxGradeBytes)
		if err != nil {
			return nil, err
		}
		var entry retainedGrade
		receipt := &entry.receipt
		if err := strictStored(data, receipt); err != nil {
			return nil, err
		}
		if receipt.SchemaVersion != 1 || receipt.Sequence != i+1 || receipt.PreviousSHA256 != previous || receipt.EvaluationID != e.ID ||
			receipt.EvaluationSHA256 != hash || receipt.PlanDigest != e.Preparation.PlanDigest || storedDigest(receipt) != evalcorpus.Digest(data) ||
			!digestPattern.MatchString(receipt.SubmissionSHA256) {
			return nil, fmt.Errorf("grading receipt identity/digest conflict")
		}
		if _, err := time.Parse(time.RFC3339Nano, receipt.SubmittedAt); err != nil {
			return nil, err
		}
		bundleName := gradeBundleName(receipt.SubmissionSHA256)
		if used, exists := bundles[bundleName]; !exists || used {
			return nil, fmt.Errorf("missing or repeated grading evidence bundle")
		}
		bundle, err := area.child(bundleName)
		if err != nil {
			return nil, err
		}
		err = readGradeBundle(bundle, &entry)
		bundle.close()
		if err != nil {
			return nil, fmt.Errorf("grading evidence conflict: %w", err)
		}
		if receipt.ID != entry.submission.ID || receipt.SubmissionSHA256 != storedDigest(&entry.submission) {
			return nil, fmt.Errorf("grading submission identity/digest conflict")
		}
		a, ref, c, err := selectedAttempt(controller, e, p, entry.submission.Unit.Ordinal, entry.submission.Attempt)
		if err != nil {
			return nil, err
		}
		if err := validateGradeInput(&entry.submission, e, hash, a, ref, c, &entry.rubric); err != nil {
			return nil, err
		}
		if finished, err := time.Parse(time.RFC3339Nano, a.FinishedAt); err != nil {
			return nil, err
		} else if submitted, _ := time.Parse(time.RFC3339Nano, receipt.SubmittedAt); submitted.Before(finished) {
			return nil, fmt.Errorf("retrospective grading receipt predates completed attempt")
		}
		j.entries = append(j.entries, entry)
		bundles[bundleName] = true
		previous = evalcorpus.Digest(data)
	}
	for _, complete := range bundles {
		j.pending = j.pending || !complete
	}
	if _, err := gradingState(j); err != nil {
		return nil, err
	}
	return j, area.check()
}

func readGradeBundle(bundle *guardedDir, entry *retainedGrade) error {
	data, err := bundle.read("submission.json", MaxGradeBytes)
	if err != nil {
		return err
	}
	if err := strictJSON(data, &entry.submission); err != nil {
		return err
	}
	if err := strictStored(data, &entry.submission); err != nil {
		return err
	}
	files := gradeFiles(&entry.submission)
	if !reflect.DeepEqual(files, entry.receipt.Files) {
		return fmt.Errorf("grading evidence manifest identity mismatch")
	}
	data, err = bundle.read("complete.json", MaxGradeBytes)
	if err != nil {
		return err
	}
	var completed []DigestedFile
	if err := strictStored(data, &completed); err != nil || !reflect.DeepEqual(completed, files) {
		return fmt.Errorf("grading bundle incomplete or conflicting: %v", err)
	}
	entries, err := bundle.entries()
	if err != nil || len(entries) != len(files)+1 {
		return fmt.Errorf("grading bundle has unexpected/pending artifacts: %v", err)
	}
	for _, f := range files {
		data, err := bundle.read(f.Path, MaxArtifactBytes)
		if err != nil || evalcorpus.Digest(data) != f.SHA256 {
			return fmt.Errorf("grade evidence missing/changed: %s: %v", f.Path, err)
		}
		if f.Path == "rubric.json" {
			if err := strictJSON(data, &entry.rubric); err != nil {
				return err
			}
			if err := strictStored(data, &entry.rubric); err != nil {
				return err
			}
		}
	}
	return bundle.check()
}

// SubmitGrade retains evaluator assertions without changing any execution byte.
// It accepts no executable seam: artifact and command contents remain data.
func SubmitGrade(repo, storageRoot, id string, unit, attempt int, file string) (*GradeReceipt, error) {
	name, data, err := readLocal(repo, file, MaxGradeBytes)
	if err != nil {
		return nil, err
	}
	var s GradeSubmission
	if err := strictJSON(data, &s); err != nil {
		return nil, fmt.Errorf("grading JSON: %w", err)
	}
	if err := strictStored(data, &s); err != nil {
		return nil, fmt.Errorf("grading JSON: %w", err)
	}
	g, e, hash, err := openEvaluation(storageRoot, id)
	if err != nil {
		return nil, err
	}
	defer g.close()
	// Reject an unrelated submission before scanning the retained controller's
	// complete evidence. The shared validation below still checks every binding.
	if s.SchemaVersion != 1 || s.EvaluationID != e.ID || s.EvaluationSHA256 != hash || s.PlanDigest != e.Preparation.PlanDigest ||
		unit < 1 || unit > len(e.Preparation.Preview.Schedule) || s.Unit != e.Preparation.Preview.Schedule[unit-1] || s.Attempt != attempt || attempt < 1 {
		return nil, fmt.Errorf("grading identity differs from retained evaluation or explicit unit/attempt selection")
	}
	p, err := readProgress(g, e, hash)
	if err != nil {
		return nil, err
	}
	a, ref, c, err := selectedAttempt(g, e, p, unit, attempt)
	if err != nil {
		return nil, err
	}
	if s.Unit.Ordinal != unit || s.Attempt != attempt {
		return nil, fmt.Errorf("submission differs from explicit CLI unit/attempt selection")
	}
	base := filepath.Dir(name)
	_, rubricData, err := readArtifact(base, s.Rubric)
	if err != nil {
		return nil, err
	}
	var r Rubric
	if err := strictJSON(rubricData, &r); err != nil {
		return nil, err
	}
	if err := strictStored(rubricData, &r); err != nil {
		return nil, err
	}
	if err := validateGradeInput(&s, e, hash, a, ref, c, &r); err != nil {
		return nil, err
	}
	payload := map[string][]byte{"submission.json": data, "rubric.json": rubricData}
	// Canonical submission identity makes whitespace-only replay idempotent.
	payload["submission.json"], err = storedBytes(&s)
	if err != nil {
		return nil, err
	}
	total := len(payload["submission.json"]) + len(rubricData)
	for _, evidence := range s.Evidence {
		_, bytes, err := readArtifact(base, evidence.Artifact)
		if err != nil {
			return nil, err
		}
		payload["evidence-"+evidence.ID] = bytes
		total += len(bytes)
		if total > maxGradeBundleBytes {
			return nil, fmt.Errorf("grading evidence bundle exceeds 16 MiB")
		}
	}
	area, err := openGradeArea(storageRoot, id, true)
	if err != nil {
		return nil, err
	}
	defer area.close()
	journal, err := readGradeJournal(area, g, e, hash, p, false)
	if err != nil {
		return nil, err
	}
	if receipt, err := replayGrade(journal, &s); receipt != nil || err != nil {
		return receipt, err
	}
	var claim *guardedDir
	for tries := 0; tries < 50; tries++ {
		claim, err = area.createDir("publication")
		if !errors.Is(err, fs.ErrExist) {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if err != nil {
		return nil, fmt.Errorf("grade publisher active or interrupted; no takeover: %w", err)
	}
	receipt, publishErr := publishGrade(area, g, e, hash, p, &s, &r, payload)
	checkErr := claim.check()
	claim.close()
	var cleanupErr error
	if checkErr == nil {
		cleanupErr = area.root.Remove("publication")
	}
	return receipt, errors.Join(publishErr, checkErr, cleanupErr, area.check())
}

func replayGrade(j *gradeJournal, s *GradeSubmission) (*GradeReceipt, error) {
	for _, prior := range j.entries {
		if prior.submission.ID == s.ID {
			if prior.receipt.SubmissionSHA256 != storedDigest(s) {
				return nil, fmt.Errorf("grading submission ID already bound to conflicting bytes; prior evidence preserved")
			}
			receipt := prior.receipt
			return &receipt, nil
		}
	}
	return nil, nil
}

func publishGrade(area, controller *guardedDir, e *Evaluation, hash string, p *Progress, s *GradeSubmission, r *Rubric, payload map[string][]byte) (*GradeReceipt, error) {
	j, err := readGradeJournal(area, controller, e, hash, p, true)
	if err != nil {
		return nil, err
	}
	if receipt, err := replayGrade(j, s); receipt != nil || err != nil {
		return receipt, err
	}
	if j.pending || len(j.entries) >= maxGradeRecords {
		return nil, fmt.Errorf("grading publication incomplete or capacity exhausted; evidence preserved, no takeover")
	}
	receipt := GradeReceipt{SchemaVersion: 1, Sequence: len(j.entries) + 1, ID: s.ID, EvaluationID: e.ID,
		EvaluationSHA256: hash, PlanDigest: e.Preparation.PlanDigest, SubmittedAt: now(), SubmissionSHA256: storedDigest(s), Files: gradeFiles(s)}
	if len(j.entries) != 0 {
		receipt.PreviousSHA256 = storedDigest(j.entries[len(j.entries)-1].receipt)
	}
	j.entries = append(j.entries, retainedGrade{receipt, *s, *r})
	if _, err := gradingState(j); err != nil {
		return nil, err
	}
	size, entries, _, err := inventory(area)
	if err != nil {
		return nil, err
	}
	area.budget = &writeBudget{used: size, entries: entries}
	bundle, err := area.createDir(gradeBundleName(receipt.SubmissionSHA256))
	if err != nil {
		return nil, fmt.Errorf("grade evidence bundle already exists or is interrupted; no overwrite: %w", err)
	}
	defer bundle.close()
	for _, f := range receipt.Files {
		if err := bundle.publishBytes(f.Path, payload[f.Path]); err != nil {
			return nil, fmt.Errorf("grading publication incomplete; evidence preserved: %w", err)
		}
	}
	if err := bundle.publish("complete.json", receipt.Files); err != nil {
		return nil, err
	}
	if err := area.publish(gradeRecordName(receipt.Sequence), &receipt); err != nil {
		return nil, err
	}
	return &receipt, nil
}

type rubricState struct {
	rubric     Rubric
	sha        string
	validation *retainedGrade
	invalid    bool
	disputes   map[string]retainedGrade
}

type gradeState struct {
	rubrics map[string]*rubricState
	latest  map[string]retainedGrade
	byID    map[string]retainedGrade
}

func gradeAttemptKey(s *GradeSubmission) string { return attemptName(s.Unit.Ordinal, s.Attempt) }

func disputedGrade(s *GradeSubmission) bool {
	if slices.ContainsFunc(s.Judgments, func(j Judgment) bool { return j.State == "disputed" }) {
		return true
	}
	if s.Review == nil {
		return false
	}
	seen := map[string]string{}
	for _, f := range s.Review.Findings {
		key := f.DefectID + "/" + f.Severity
		if f.State == "disputed" || f.State == "additional-real" || seen[key] != "" && seen[key] != f.State {
			return true
		}
		seen[key] = f.State
	}
	return false
}

func gradingState(j *gradeJournal) (*gradeState, error) {
	state := &gradeState{rubrics: map[string]*rubricState{}, latest: map[string]retainedGrade{}, byID: map[string]retainedGrade{}}
	for _, entry := range j.entries {
		s, r := &entry.submission, &entry.rubric
		if _, exists := state.byID[s.ID]; exists {
			return nil, fmt.Errorf("duplicate grading submission identity")
		}
		current := state.rubrics[s.Unit.CaseID]
		if current == nil {
			if s.PreviousRubricSHA256 != "" {
				return nil, fmt.Errorf("initial rubric cannot replace an absent version")
			}
			current = &rubricState{rubric: *r, sha: s.Rubric.SHA256, disputes: map[string]retainedGrade{}}
			state.rubrics[s.Unit.CaseID] = current
		} else if current.sha != s.Rubric.SHA256 {
			if r.Version <= current.rubric.Version || s.PreviousRubricSHA256 != current.sha ||
				!slices.Contains([]string{"grade", "validate-rubric"}, s.Operation) {
				return nil, fmt.Errorf("replacement rubric requires a higher version, exact previous digest and retained correction reason")
			}
			current.rubric, current.sha, current.validation, current.invalid = *r, s.Rubric.SHA256, nil, false
		} else if s.PreviousRubricSHA256 != "" {
			return nil, fmt.Errorf("previous rubric digest applies only to a versioned replacement")
		}
		key := gradeAttemptKey(s)
		switch s.Operation {
		case "grade":
			previous, exists := state.latest[key]
			if exists && s.Supersedes != previous.submission.ID || !exists && s.Supersedes != "" {
				return nil, fmt.Errorf("correction must supersede the exact latest grade for this attempt")
			}
			state.latest[key] = entry
			if disputedGrade(s) {
				current.disputes[s.ID] = entry
			}
		case "dispute":
			target, exists := state.byID[s.Target]
			if !exists || target.submission.Operation != "grade" || gradeAttemptKey(&target.submission) != key || target.submission.Rubric.SHA256 != current.sha {
				return nil, fmt.Errorf("dispute must name a retained grade for this attempt and current rubric")
			}
			current.disputes[s.ID] = entry
		case "invalidate-rubric":
			current.invalid = true
		case "validate-rubric":
			for _, id := range s.Validation.Resolves {
				dispute, exists := current.disputes[id]
				if !exists || r.Version <= dispute.rubric.Version || s.Evaluator.Identity == dispute.submission.Evaluator.Identity ||
					!slices.Contains(s.Evaluator.IndependentOf, dispute.submission.Evaluator.Identity) {
					return nil, fmt.Errorf("dispute resolution requires an independent adjudicator and versioned rubric correction")
				}
				if dispute.submission.Target != "" {
					target := state.byID[dispute.submission.Target]
					if s.Evaluator.Identity == target.submission.Evaluator.Identity || !slices.Contains(s.Evaluator.IndependentOf, target.submission.Evaluator.Identity) {
						return nil, fmt.Errorf("adjudicator must assert independence from the disputed grade evaluator")
					}
				}
				if judgmentResult(s.Validation.Checks) != "pass" {
					return nil, fmt.Errorf("failed/unknown validation cannot resolve a dispute")
				}
				delete(current.disputes, id)
			}
			current.validation = &entry
		}
		state.byID[s.ID] = entry
	}
	return state, nil
}

func judgmentResult(items []Judgment) string {
	result := "pass"
	for _, j := range items {
		if j.State == "unknown" || j.State == "disputed" {
			return "unknown"
		}
		if j.State == "violated" {
			result = "fail"
		}
	}
	return result
}
