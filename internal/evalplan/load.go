package evalplan

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"reflect"
	"strings"

	"github.com/kninetimmy/orch/internal/evalcorpus"
)

// Stored records require their complete generated wire shape, including false
// claims and empty arrays. Decoding into a struct alone loses missing fields.
func strictStored(data []byte, out any) error {
	// Version-1 previews embed config.Roles with its existing Go field names
	// (Architect/Model/Effort). Keep those frozen digest bytes compatible. The
	// generated-shape comparison below rejects alternate casing everywhere.
	if err := decodeJSON(data, out, false); err != nil {
		return err
	}
	normalized, err := json.Marshal(out)
	if err != nil {
		return err
	}
	var supplied, expected any
	if err := json.Unmarshal(data, &supplied); err != nil {
		return err
	}
	if err := json.Unmarshal(normalized, &expected); err != nil {
		return err
	}
	if !reflect.DeepEqual(supplied, expected) {
		return fmt.Errorf("stored record has missing or noncanonical fields")
	}
	return nil
}

func proposal(p Plan) Proposal {
	ids := make([]string, 0, len(p.Cases))
	for _, c := range p.Cases {
		ids = append(ids, c.ID)
	}
	q := Proposal{Version: p.Version, Scope: p.Scope, Intervention: p.Intervention,
		Corpus: p.Corpus, Cases: ids, Partitions: p.Partitions, Baseline: p.Baseline.Selection,
		Repetitions: p.Repetitions, Limits: p.Limits, Measurement: p.Measurement,
		DecisionRule: p.DecisionRule, Readiness: p.Readiness, StorageRoot: p.StorageRoot,
		WorkerRoots: p.WorkerRoots, ScratchRoots: p.ScratchRoots, Instructions: p.Instructions, ProtectedRoots: p.ProtectedRoots}
	if p.Candidate != nil {
		candidate := p.Candidate.Selection
		q.Candidate = &candidate
	}
	return q
}

func digestName(digest string) (string, error) {
	hex := strings.TrimPrefix(digest, "sha256:")
	if digest != "sha256:"+hex || !digestPattern.MatchString(hex) {
		return "", fmt.Errorf("require sha256: followed by 64 lowercase hex digits")
	}
	return hex + ".json", nil
}

// validateRecord verifies the frozen wire contract without trusting preview claims.
func validateRecord(r *Record, root, digest string) error {
	name, err := digestName(digest)
	if err != nil {
		return err
	}
	data, err := json.Marshal(r.Plan)
	if err != nil {
		return err
	}
	if (r.SchemaVersion != 1 && r.SchemaVersion != 2) || r.Kind != "maintainer-preparation-record" || r.Plan.Version != r.SchemaVersion ||
		r.PlanDigest != digest || "sha256:"+evalcorpus.Digest(data) != digest ||
		r.Plan.StorageRoot != root || r.StorageDestination != filepath.Join(root, name) {
		return fmt.Errorf("saved preparation identity/schema/digest mismatch")
	}
	// Apply the preview's size boundary before regenerating a schedule from an
	// untrusted saved record. Its digest is identity, never a validation bypass.
	if len(r.Plan.Cases) < 1 || len(r.Plan.Cases) > 12 || len(r.Plan.ExcludedCases) > 12 || len(r.Plan.Partitions) > 2 {
		return fmt.Errorf("saved plan exceeds version-1 case/partition bounds")
	}
	counts, err := plannedCounts(proposal(r.Plan))
	if err != nil {
		return err
	}
	if !reflect.DeepEqual(r.Preview, evidence(r.Plan, counts)) {
		return fmt.Errorf("saved preview schedule, counts or claims differ from the regenerated preview")
	}
	return nil
}

// Load reads an immutable version-1 preview by explicit root and digest. It
// rechecks all current local inputs and exclusions; it neither rewrites records
// nor interprets readiness documents as approval or containment evidence.
func Load(ctx context.Context, repo, storageRoot, digest string) (*Record, error) {
	g, err := openGuarded(storageRoot)
	if err != nil {
		return nil, err
	}
	defer g.close()
	name, err := digestName(digest)
	if err != nil {
		return nil, err
	}
	data, err := g.read(name, maxRecordBytes)
	if err != nil {
		return nil, fmt.Errorf("load saved preparation: %w", err)
	}
	var r Record
	if err := strictStored(data, &r); err != nil {
		return nil, fmt.Errorf("saved preparation JSON: %w", err)
	}
	if err := validateRecord(&r, g.path, digest); err != nil {
		return nil, err
	}
	fresh, err := normalize(ctx, repo, g.path, proposal(r.Plan), nil)
	if err != nil {
		return nil, fmt.Errorf("saved preparation revalidation: %w", err)
	}
	if !reflect.DeepEqual(&r, fresh) {
		return nil, fmt.Errorf("saved preparation differs from current artifacts, configuration or repository exclusions")
	}
	for _, c := range r.Plan.Cases {
		if _, err := gitRead(ctx, nil, repo, "cat-file", "-e", c.SourceCommit+"^{commit}"); err != nil {
			return nil, fmt.Errorf("case %s source commit unavailable locally: %w", c.ID, err)
		}
	}
	return &r, g.check()
}
