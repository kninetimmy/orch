package grant

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/kninetimmy/orch/internal/execx"
	"github.com/kninetimmy/orch/internal/gitops"
)

// DirName is the directory under the git common directory holding one
// <id>.json record per grant plus the lock file that serializes every access.
const DirName = "orch-grants"

const lockName = "lock"

// Store reads and writes the grant records of one clone.
type Store struct {
	dir string
}

// Open resolves the grant store of the clone whose checkout (primary or
// linked worktree) is repoRoot.
func Open(ctx context.Context, r execx.Runner, repoRoot string) (*Store, error) {
	g, err := gitops.Open(ctx, r, repoRoot)
	if err != nil {
		return nil, err
	}
	common, err := g.CommonDir(ctx)
	if err != nil {
		return nil, fmt.Errorf("resolve git common directory for autonomy grants: %w", err)
	}
	return &Store{dir: filepath.Join(common, DirName)}, nil
}

// Dir returns the directory holding the grant records.
func (s *Store) Dir() string { return s.dir }

// withLock holds the cross-process grant lock around fn. The lock belongs to
// an open file handle, not to the process, so it also serializes goroutines and
// never contends with the Delivery mutation serializer a caller may hold.
func (s *Store) withLock(fn func() error) (err error) {
	if err := os.MkdirAll(s.dir, 0o755); err != nil {
		return fmt.Errorf("create autonomy grant directory: %w", err)
	}
	f, err := os.OpenFile(filepath.Join(s.dir, lockName), os.O_RDWR|os.O_CREATE, 0o644)
	if err != nil {
		return fmt.Errorf("open autonomy grant lock: %w", err)
	}
	defer func() {
		if closeErr := f.Close(); err == nil && closeErr != nil {
			err = fmt.Errorf("close autonomy grant lock: %w", closeErr)
		}
	}()
	if err := lockFile(f); err != nil {
		return fmt.Errorf("lock autonomy grants: %w", err)
	}
	defer func() {
		if unlockErr := unlockFile(f); err == nil && unlockErr != nil {
			err = fmt.Errorf("unlock autonomy grants: %w", unlockErr)
		}
	}()
	return fn()
}

// all reads every record, failing closed on any record it cannot trust.
func (s *Store) all() ([]*Grant, error) {
	entries, err := os.ReadDir(s.dir)
	if err != nil {
		return nil, fmt.Errorf("read autonomy grants: %w", err)
	}
	var out []*Grant
	for _, e := range entries {
		if e.Name() == lockName || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		g, err := readRecord(filepath.Join(s.dir, e.Name()))
		if err != nil {
			return nil, err
		}
		out = append(out, g)
	}
	return out, nil
}

func readRecord(path string) (*Grant, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read autonomy grant: %w", err)
	}
	var head struct {
		SchemaVersion int `json:"schema_version"`
	}
	if err := json.Unmarshal(data, &head); err != nil {
		return nil, fmt.Errorf("autonomy grant %s is unreadable: %w", path, err)
	}
	if head.SchemaVersion != SchemaVersion {
		return nil, fmt.Errorf("autonomy grant %s has schema_version %d; this build understands only %d and will not guess", path, head.SchemaVersion, SchemaVersion)
	}
	var g Grant
	if err := decodeStrict(data, &g); err != nil {
		return nil, fmt.Errorf("autonomy grant %s is unreadable: %w", path, err)
	}
	if filepath.Base(path) != g.Terms.ID+".json" {
		return nil, fmt.Errorf("autonomy grant %s does not hold grant %q", path, g.Terms.ID)
	}
	return &g, nil
}

// write replaces g's record atomically; callers hold the lock.
func (s *Store) write(g *Grant) error {
	data, err := json.MarshalIndent(g, "", "  ")
	if err != nil {
		return fmt.Errorf("encode autonomy grant: %w", err)
	}
	path := filepath.Join(s.dir, g.Terms.ID+".json")
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, append(data, '\n'), 0o644); err != nil {
		return fmt.Errorf("write autonomy grant: %w", err)
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("write autonomy grant: %w", err)
	}
	return nil
}

func active(grants []*Grant, now time.Time) (*Grant, error) {
	var found *Grant
	for _, g := range grants {
		if !g.Active(now) {
			continue
		}
		if found != nil {
			return nil, fmt.Errorf("autonomy grants %s and %s are both active; refusing to choose", found.Terms.ID, g.Terms.ID)
		}
		found = g
	}
	return found, nil
}

// empty reports whether no grant was ever stored, so reads and no-op updates
// need not create the directory or its lock file.
func (s *Store) empty() bool {
	_, err := os.Stat(s.dir)
	return errors.Is(err, fs.ErrNotExist)
}

// Active returns the active grant, or nil when none is active.
func (s *Store) Active(now time.Time) (g *Grant, err error) {
	if s.empty() {
		return nil, nil
	}
	err = s.withLock(func() error {
		grants, err := s.all()
		if err != nil {
			return err
		}
		g, err = active(grants, now)
		return err
	})
	return g, err
}

// Create records the grant req approves for session. Every refusal leaves the
// store unchanged.
func (s *Store) Create(req *CreateRequest, session string, now time.Time) (*Grant, error) {
	if err := req.Terms.validate(session, now); err != nil {
		return nil, err
	}
	digest, err := Digest(req.Terms)
	if err != nil {
		return nil, err
	}
	if err := req.Approval.validate(digest); err != nil {
		return nil, err
	}
	g := &Grant{
		SchemaVersion: SchemaVersion,
		Terms:         req.Terms,
		Digest:        digest,
		ApprovedBy:    req.Approval.ApprovedBy,
		ApprovedAt:    req.Approval.ApprovedAt,
		CreatedAt:     now,
		SessionID:     req.Terms.SessionID,
		Runs:          []Use{},
		Merges:        []Use{},
		Approvals:     []RecordedApproval{},
	}
	err = s.withLock(func() error {
		grants, err := s.all()
		if err != nil {
			return err
		}
		if cur, err := active(grants, now); err != nil {
			return err
		} else if cur != nil {
			return fmt.Errorf("autonomy grant %s is already active; revoke it before creating another", cur.Terms.ID)
		}
		for _, old := range grants {
			if old.Terms.ID == g.Terms.ID {
				return fmt.Errorf("autonomy grant %s was already created; an ended grant never becomes active again, so preview a new one", g.Terms.ID)
			}
		}
		return s.write(g)
	})
	if err != nil {
		return nil, err
	}
	return g, nil
}

// Revoke ends the active grant at now and returns it, or returns nil and
// changes nothing when no grant is active.
func (s *Store) Revoke(now time.Time) (*Grant, error) {
	return s.update(now, "", func(g *Grant) error {
		g.RevokedAt = &now
		return nil
	})
}

// RecordRun records one run against grant id, refusing past its run limit.
func (s *Store) RecordRun(id, ref string, now time.Time) (*Grant, error) {
	return s.record(id, now, runs, "run", ref, nil)
}

// RecordMerge records one merge against grant id, refusing past its merge limit.
func (s *Store) RecordMerge(id, ref string, now time.Time) (*Grant, error) {
	return s.record(id, now, merges, "merge", ref, nil)
}

// MergeRef is the reference a merge approval records: one issue of one run.
func MergeRef(runID string, issue int) string {
	return fmt.Sprintf("%s#%d", runID, issue)
}

// RecordApproval records a gate approval given under grant id together with
// the unit it spends: a plan approval spends one run (ref a.RunID), a merge
// approval one merge (ref MergeRef(a.RunID, a.Issue)).
func (s *Store) RecordApproval(id string, a RecordedApproval, now time.Time) (*Grant, error) {
	switch a.Gate {
	case "plan":
		return s.record(id, now, runs, "run", a.RunID, &a)
	case "merge":
		return s.record(id, now, merges, "merge", MergeRef(a.RunID, a.Issue), &a)
	default:
		return nil, fmt.Errorf("gate %q spends no autonomy grant unit", a.Gate)
	}
}

// Get returns grant id, active or not.
func (s *Store) Get(id string) (g *Grant, err error) {
	if s.empty() {
		return nil, fmt.Errorf("autonomy grant %s is not recorded in this clone", id)
	}
	err = s.withLock(func() error {
		grants, err := s.all()
		if err != nil {
			return err
		}
		for _, c := range grants {
			if c.Terms.ID == id {
				g = c
				return nil
			}
		}
		return fmt.Errorf("autonomy grant %s is not recorded in this clone", id)
	})
	return g, err
}

func runs(g *Grant) (*[]Use, int)   { return &g.Runs, g.Terms.RunLimit }
func merges(g *Grant) (*[]Use, int) { return &g.Merges, g.Terms.MergeLimit }

func (s *Store) record(id string, now time.Time, list func(*Grant) (*[]Use, int), kind, ref string, approval *RecordedApproval) (*Grant, error) {
	if strings.TrimSpace(ref) == "" {
		return nil, fmt.Errorf("a %s recorded against an autonomy grant needs a reference", kind)
	}
	g, err := s.update(now, id, func(g *Grant) error {
		uses, limit := list(g)
		// The same ref again is the same run or merge, as when a verb re-runs
		// after a crash: it spends nothing more.
		if slices.ContainsFunc(*uses, func(u Use) bool { return u.Ref == ref }) {
			return nil
		}
		if len(*uses) >= limit {
			return fmt.Errorf("%w: grant %s has used all %d of its %ss", ErrLimitReached, g.Terms.ID, limit, kind)
		}
		*uses = append(*uses, Use{Ref: ref, At: now})
		if approval != nil {
			a := *approval
			a.At = now
			g.Approvals = append(g.Approvals, a)
		}
		return nil
	})
	if err == nil && g == nil {
		return nil, ErrNoActiveGrant
	}
	return g, err
}

// update applies change to the active grant under the lock and persists it.
// A non-empty id must name the active grant. With no active grant it returns
// nil, nil and writes nothing.
func (s *Store) update(now time.Time, id string, change func(*Grant) error) (g *Grant, err error) {
	if s.empty() {
		return nil, nil
	}
	err = s.withLock(func() error {
		grants, err := s.all()
		if err != nil {
			return err
		}
		if g, err = active(grants, now); err != nil || g == nil {
			return err
		}
		if id != "" && g.Terms.ID != id {
			return fmt.Errorf("%w: grant %s is not the active grant (%s is)", ErrNoActiveGrant, id, g.Terms.ID)
		}
		if err := change(g); err != nil {
			return err
		}
		return s.write(g)
	})
	if err != nil {
		return nil, err
	}
	return g, nil
}
