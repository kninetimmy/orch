// Package grant records machine-local autonomy grants: what the user has
// delegated (scope, covered gates, expiry, limits, the permission mode relayed
// sessions start in, and the session allowed to approve under it). A grant is
// created only from a digest-bound human approval, is active until it expires
// or is revoked, and never becomes active again after either.
//
// Records live in the clone's git common directory so every linked worktree
// sees the same grant and no working tree ever shows them in `git status`.
// The guard already denies agent-tool writes under .git; that limits accidents
// and drift, it is not a security boundary against a shell write.
package grant

import (
	"bytes"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strings"
	"time"
)

// SchemaVersion is the request, preview and record schema this build reads and
// writes. A record carrying any other version is refused, never guessed at.
const SchemaVersion = 1

// ApprovalStatement is the exact assertion a creation approval must carry.
const ApprovalStatement = "approve-autonomy-grant"

// SessionEnv names the environment variable holding the Claude Code session
// id; the creating session's value becomes the grant's first session holder.
const SessionEnv = "CLAUDE_CODE_SESSION_ID"

// Defaults applied by Preview when a proposal omits them.
const (
	DefaultFixCycleLimit    = 2
	DefaultContextThreshold = 450000
)

// Gates a grant may cover.
var gateNames = []string{"plan", "merge", "wrap-up"}

// permissionModes are the Claude Code permission modes a relayed session may
// start in. bypassPermissions is a Claude Code mode but is refused on purpose.
// `claude --help` 2.1.295 lists "manual" where earlier releases and settings
// name "default"; both are accepted.
var permissionModes = []string{"default", "manual", "acceptEdits", "plan", "auto", "dontAsk"}

var idPattern = regexp.MustCompile(`^grant-[0-9]{8}T[0-9]{6}Z-[0-9a-f]{8}$`)

// ErrNoActiveGrant reports that no grant is active.
var ErrNoActiveGrant = errors.New("no active autonomy grant")

// ErrLimitReached reports that recording would exceed a grant limit.
var ErrLimitReached = errors.New("autonomy grant limit reached")

// ScopeItem is one named item of a grant's scope.
type ScopeItem struct {
	Name        string `json:"name"`
	Description string `json:"description"`
}

// Proposal is what the user asks to delegate; Preview completes it into Terms.
// Omitted fix-cycle limit and context threshold take their defaults; an
// explicit value, including 0, is kept and validated.
type Proposal struct {
	SchemaVersion       int         `json:"schema_version"`
	Scope               []ScopeItem `json:"scope"`
	Gates               []string    `json:"gates"`
	ExpiresAt           time.Time   `json:"expires_at"`
	RunLimit            int         `json:"run_limit"`
	MergeLimit          int         `json:"merge_limit"`
	FixCycleLimit       *int        `json:"fix_cycle_limit,omitempty"`
	ContextThreshold    *int        `json:"context_threshold_tokens,omitempty"`
	RelayPermissionMode string      `json:"relay_permission_mode"`
}

// Terms is everything the user approves. The digest covers every field; the
// approver and approval time arrive with the approval itself.
type Terms struct {
	ID                  string      `json:"id"`
	Scope               []ScopeItem `json:"scope"`
	Gates               []string    `json:"gates"`
	ExpiresAt           time.Time   `json:"expires_at"`
	RunLimit            int         `json:"run_limit"`
	MergeLimit          int         `json:"merge_limit"`
	FixCycleLimit       int         `json:"fix_cycle_limit"`
	ContextThreshold    int         `json:"context_threshold_tokens"`
	RelayPermissionMode string      `json:"relay_permission_mode"`
	// SessionID is the creating session's CLAUDE_CODE_SESSION_ID.
	SessionID string `json:"session_id"`
}

// Preview is the read-only document shown to the user before approval.
type Preview struct {
	SchemaVersion int    `json:"schema_version"`
	Terms         Terms  `json:"terms"`
	Digest        string `json:"digest"`
}

// Approval is the adapter's record of the user's approval of one preview.
type Approval struct {
	GrantDigest string    `json:"grant_digest"`
	ApprovedBy  string    `json:"approved_by"`
	ApprovedAt  time.Time `json:"approved_at"`
	Statement   string    `json:"statement"`
}

// CreateRequest is the input to Create: the previewed terms and their approval.
type CreateRequest struct {
	SchemaVersion int      `json:"schema_version"`
	Terms         Terms    `json:"terms"`
	Approval      Approval `json:"approval"`
}

// Use is one run or merge recorded against a grant.
type Use struct {
	Ref string    `json:"ref"`
	At  time.Time `json:"at"`
}

// RecordedApproval is one gate approval given under a grant. No verb records
// one yet; the list stays empty until gate verbs accept grant approvals.
type RecordedApproval struct {
	Gate  string    `json:"gate"`
	RunID string    `json:"run_id"`
	Issue int       `json:"issue,omitempty"`
	PR    int       `json:"pr,omitempty"`
	Head  string    `json:"head,omitempty"`
	At    time.Time `json:"at"`
}

// Grant is the stored record.
type Grant struct {
	SchemaVersion int       `json:"schema_version"`
	Terms         Terms     `json:"terms"`
	Digest        string    `json:"digest"`
	ApprovedBy    string    `json:"approved_by"`
	ApprovedAt    time.Time `json:"approved_at"`
	CreatedAt     time.Time `json:"created_at"`
	// SessionID is the session that may currently approve under the grant.
	SessionID string             `json:"session_id"`
	RevokedAt *time.Time         `json:"revoked_at,omitempty"`
	Runs      []Use              `json:"runs"`
	Merges    []Use              `json:"merges"`
	Approvals []RecordedApproval `json:"approvals"`

	// Relays is the hand-off chain after the creating session. Optional, so records
	// written before relays existed stay readable without a schema bump.
	Relays []Relay `json:"relays,omitempty"`
}

// Active reports whether g is active at now: not revoked and not yet expired.
// shortcut: a wall clock set backwards can make an expired grant look active
// again; record an observed end if that ever matters.
func (g *Grant) Active(now time.Time) bool {
	return g.RevokedAt == nil && now.Before(g.Terms.ExpiresAt)
}

// Digest returns the sha256 digest of t's canonical JSON encoding.
func Digest(t Terms) (string, error) {
	data, err := json.Marshal(t)
	if err != nil {
		return "", fmt.Errorf("encode grant terms for digest: %w", err)
	}
	sum := sha256.Sum256(data)
	return "sha256:" + hex.EncodeToString(sum[:]), nil
}

// MakePreview completes p into Terms for session and returns them with their
// digest. It writes nothing.
func MakePreview(p Proposal, session string, now time.Time) (*Preview, error) {
	if p.SchemaVersion != SchemaVersion {
		return nil, fmt.Errorf("grant proposal schema_version %d is unsupported (this build supports %d)", p.SchemaVersion, SchemaVersion)
	}
	id, err := newID(now)
	if err != nil {
		return nil, err
	}
	t := Terms{
		ID:                  id,
		Scope:               p.Scope,
		Gates:               p.Gates,
		ExpiresAt:           p.ExpiresAt.UTC(),
		RunLimit:            p.RunLimit,
		MergeLimit:          p.MergeLimit,
		FixCycleLimit:       DefaultFixCycleLimit,
		ContextThreshold:    DefaultContextThreshold,
		RelayPermissionMode: p.RelayPermissionMode,
		SessionID:           session,
	}
	if p.FixCycleLimit != nil {
		t.FixCycleLimit = *p.FixCycleLimit
	}
	if p.ContextThreshold != nil {
		t.ContextThreshold = *p.ContextThreshold
	}
	if err := t.validate(session, now); err != nil {
		return nil, err
	}
	digest, err := Digest(t)
	if err != nil {
		return nil, err
	}
	return &Preview{SchemaVersion: SchemaVersion, Terms: t, Digest: digest}, nil
}

// validate checks every term against the creating session and now.
func (t Terms) validate(session string, now time.Time) error {
	var problems []string
	add := func(format string, args ...any) { problems = append(problems, fmt.Sprintf(format, args...)) }
	if !idPattern.MatchString(t.ID) {
		add("id %q is not a grant id", t.ID)
	}
	if len(t.Scope) == 0 {
		add("scope must name at least one item")
	}
	names := map[string]bool{}
	for i, s := range t.Scope {
		switch {
		case strings.TrimSpace(s.Name) == "" || strings.ContainsAny(s.Name, "\r\n"):
			add("scope[%d] needs a one-line name", i)
		case names[s.Name]:
			add("scope item %q is listed twice", s.Name)
		}
		names[s.Name] = true
		if strings.TrimSpace(s.Description) == "" || strings.ContainsAny(s.Description, "\r\n") {
			add("scope[%d] needs a one-line description", i)
		}
	}
	if len(t.Gates) == 0 {
		add("gates must name at least one of %s", strings.Join(gateNames, ", "))
	}
	seen := map[string]bool{}
	for _, g := range t.Gates {
		switch {
		case !slices.Contains(gateNames, g):
			add("gate %q is not one of %s", g, strings.Join(gateNames, ", "))
		case seen[g]:
			add("gate %q is listed twice", g)
		}
		seen[g] = true
	}
	if !t.ExpiresAt.After(now) {
		add("expires_at %s is not in the future", t.ExpiresAt.Format(time.RFC3339))
	}
	for _, l := range []struct {
		name  string
		value int
	}{{"run_limit", t.RunLimit}, {"merge_limit", t.MergeLimit}, {"fix_cycle_limit", t.FixCycleLimit}, {"context_threshold_tokens", t.ContextThreshold}} {
		if l.value <= 0 {
			add("%s must be a positive whole number, got %d", l.name, l.value)
		}
	}
	switch {
	case t.RelayPermissionMode == "bypassPermissions":
		add("relay_permission_mode bypassPermissions is never delegated")
	case !slices.Contains(permissionModes, t.RelayPermissionMode):
		add("relay_permission_mode %q is not one of %s", t.RelayPermissionMode, strings.Join(permissionModes, ", "))
	}
	if session == "" {
		add("%s is absent from the environment; a grant is created only from a Claude Code session", SessionEnv)
	} else if t.SessionID != session {
		add("session_id %q is not this session (%s=%q)", t.SessionID, SessionEnv, session)
	}
	if len(problems) > 0 {
		return fmt.Errorf("invalid autonomy grant: %s", strings.Join(problems, "; "))
	}
	return nil
}

// validate checks a against the recomputed digest of the terms.
func (a Approval) validate(digest string) error {
	if a.Statement != ApprovalStatement {
		return fmt.Errorf("approval statement %q does not equal %q", a.Statement, ApprovalStatement)
	}
	if a.GrantDigest != digest {
		return fmt.Errorf("approval grant_digest %q does not match the recomputed digest %q; preview again and obtain a fresh approval", a.GrantDigest, digest)
	}
	if strings.TrimSpace(a.ApprovedBy) == "" {
		return fmt.Errorf("approval needs a non-empty approved_by")
	}
	if a.ApprovedAt.IsZero() {
		return fmt.Errorf("approval needs an approved_at time")
	}
	return nil
}

// DecodeCreate strictly decodes a CreateRequest: unknown fields, trailing data
// and any other schema_version are refused.
func DecodeCreate(data []byte) (*CreateRequest, error) {
	var req CreateRequest
	if err := decodeStrict(data, &req); err != nil {
		return nil, fmt.Errorf("decode grant create request: %w", err)
	}
	if req.SchemaVersion != SchemaVersion {
		return nil, fmt.Errorf("grant create request schema_version %d is unsupported (this build supports %d)", req.SchemaVersion, SchemaVersion)
	}
	return &req, nil
}

// DecodeProposal strictly decodes a Proposal.
func DecodeProposal(data []byte) (*Proposal, error) {
	var p Proposal
	if err := decodeStrict(data, &p); err != nil {
		return nil, fmt.Errorf("decode grant proposal: %w", err)
	}
	return &p, nil
}

func decodeStrict(data []byte, v any) error {
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		return err
	}
	if dec.More() {
		return fmt.Errorf("trailing data after JSON document")
	}
	return nil
}

func newID(now time.Time) (string, error) {
	var b [4]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", fmt.Errorf("generate grant id: %w", err)
	}
	return "grant-" + now.UTC().Format("20060102T150405Z") + "-" + hex.EncodeToString(b[:]), nil
}
