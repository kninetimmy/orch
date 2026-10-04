package evalplan

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"path/filepath"
	"reflect"
	"time"
)

// ApprovalStatement follows the existing digest-bound human assertion convention.
// It is specific to one evaluation, never Delivery, configuration or merge authority.
const ApprovalStatement = "approve-evaluation"

var errNoApproval = errors.New("no retained evaluation approval")

type Approval struct {
	SchemaVersion int    `json:"schema_version"`
	PlanDigest    string `json:"plan_digest"`
	ApprovedBy    string `json:"approved_by"`
	ApprovedAt    string `json:"approved_at"`
	Statement     string `json:"statement"`
}

type ApprovalScope struct {
	Preparation       Record `json:"preparation"`
	ReportDestination string `json:"report_destination"`
}

// ApprovalRecord is a sidecar: existing schema-1 evaluations remain inspectable.
// The same receipt is also retained in an exclusive assertion claim at the root.
type ApprovalRecord struct {
	SchemaVersion int           `json:"schema_version"`
	EvaluationID  string        `json:"evaluation_id"`
	Approval      Approval      `json:"approval"`
	Scope         ApprovalScope `json:"scope"`
}

func Scope(r *Record) ApprovalScope {
	return ApprovalScope{*r, filepath.Join(r.Plan.StorageRoot, "reports", "<evaluation-id>", "<snapshot-sha256>")}
}

// ReadApproval uses the same bounded, alias-rejecting local reader as proposals.
func ReadApproval(repo, file string) (*Approval, error) {
	_, data, err := readLocal(repo, file, MaxPlanBytes)
	if err != nil {
		return nil, fmt.Errorf("approval file: %w", err)
	}
	var a Approval
	if err := strictStored(data, &a); err != nil {
		return nil, fmt.Errorf("approval JSON: %w", err)
	}
	return &a, nil
}

func (a Approval) validate(digest string, at time.Time) error {
	if a.SchemaVersion != 1 || a.PlanDigest != digest || a.Statement != ApprovalStatement || !identifierPattern.MatchString(a.ApprovedBy) {
		return fmt.Errorf("require schema-1 human approval with matching plan_digest, public approved_by identifier and statement %q", ApprovalStatement)
	}
	approved, err := time.Parse(time.RFC3339Nano, a.ApprovedAt)
	if err != nil || approved.After(at) || at.Sub(approved) > 24*time.Hour {
		return fmt.Errorf("approval requires an RFC3339 timestamp within the preceding 24 hours, never in the future")
	}
	return nil
}

// PrepareApproved revalidates inputs, consumes one explicit assertion and retains
// its exact scope. A failure after claiming preserves that claim; no silent replay.
func PrepareApproved(ctx context.Context, repo, storageRoot, digest string, a Approval) (*Evaluation, error) {
	if err := a.validate(digest, time.Now()); err != nil {
		return nil, err
	}
	return prepare(ctx, repo, storageRoot, digest, &a)
}

func directoryForPublication(g *guardedDir, name string) (*guardedDir, error) {
	child, err := g.createDir(name)
	if errors.Is(err, fs.ErrExist) {
		return g.child(name)
	}
	return child, err
}

func claimApproval(parent *guardedDir, receipt *ApprovalRecord) error {
	claims, err := directoryForPublication(parent, "approvals")
	if err != nil {
		return err
	}
	defer claims.close()
	claim, err := claims.createDir(storedDigest(receipt.Approval))
	if err != nil {
		return fmt.Errorf("approval assertion already consumed or its claim is incomplete; obtain a new explicit approval: %w", err)
	}
	defer claim.close()
	return claim.publish("assertion.json", receipt)
}

func readApproval(g *guardedDir, e *Evaluation) (*ApprovalRecord, error) {
	data, err := g.read("approval.json", maxRecordBytes)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, fmt.Errorf("%w: %v", errNoApproval, err)
		}
		return nil, err
	}
	var a ApprovalRecord
	if err := strictStored(data, &a); err != nil {
		return nil, err
	}
	if a.SchemaVersion != 1 || a.EvaluationID != e.ID || a.Approval.SchemaVersion != 1 ||
		a.Approval.PlanDigest != e.Preparation.PlanDigest || a.Approval.Statement != ApprovalStatement ||
		!identifierPattern.MatchString(a.Approval.ApprovedBy) || !reflect.DeepEqual(a.Scope, Scope(&e.Preparation)) {
		return nil, fmt.Errorf("retained approval identity or frozen scope mismatch")
	}
	if _, err := time.Parse(time.RFC3339Nano, a.Approval.ApprovedAt); err != nil {
		return nil, err
	}
	parent, err := openGuarded(e.Preparation.Plan.StorageRoot)
	if err != nil {
		return nil, err
	}
	defer parent.close()
	data, err = parent.read("approvals/"+storedDigest(a.Approval)+"/assertion.json", maxRecordBytes)
	if err != nil {
		return nil, fmt.Errorf("approval claim unavailable: %w", err)
	}
	var claim ApprovalRecord
	if err := strictStored(data, &claim); err != nil || !reflect.DeepEqual(claim, a) {
		return nil, fmt.Errorf("approval claim does not bind this evaluation: %v", err)
	}
	return &a, nil
}

func executionApproval(storageRoot, id string) error {
	g, e, _, err := openEvaluation(storageRoot, id)
	if err != nil {
		return err
	}
	defer g.close()
	a, err := readApproval(g, e)
	if err != nil {
		return fmt.Errorf("evaluation requires its retained explicit human approval: %w", err)
	}
	if err := a.Approval.validate(e.Preparation.PlanDigest, time.Now()); err != nil {
		return err
	}
	return nil
}
