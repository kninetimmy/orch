package grant

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/kninetimmy/orch/internal/execx/execxtest"
	"github.com/kninetimmy/orch/internal/paths"
)

func observedGrant() *Grant {
	return &Grant{SchemaVersion: SchemaVersion, Terms: Terms{
		ID: "grant-20261009T120000Z-00000001", ExpiresAt: now.Add(time.Hour),
		Scope: []ScopeItem{{Name: "issue-465", Description: "approval visibility"}},
		Gates: []string{"plan", "merge"}, RunLimit: 1, MergeLimit: 1,
		FixCycleLimit: 2, ContextThreshold: 450000, RelayPermissionMode: "acceptEdits", SessionID: session,
	}, SessionID: "relayed-holder", ApprovedBy: "human", ApprovedAt: now, CreatedAt: now,
		Runs:      []Use{{Ref: "run-1", At: now}},
		Merges:    []Use{{Ref: "run-1#465", At: now}},
		Approvals: []RecordedApproval{{Gate: "merge", RunID: "run-1", Issue: 465, PR: 466, Head: "head", At: now}},
	}
}

func TestObserveActive(t *testing.T) {
	for _, name := range []string{"missing directory", "empty directory", "active", "expired", "at expiry", "revoked", "duplicate active"} {
		t.Run(name, func(t *testing.T) {
			s := newStore(t)
			g, at := observedGrant(), now
			if name != "missing directory" {
				if err := os.Mkdir(s.dir, 0o755); err != nil {
					t.Fatal(err)
				}
			}
			switch name {
			case "expired":
				at = g.Terms.ExpiresAt.Add(time.Second)
			case "at expiry":
				at = g.Terms.ExpiresAt
			case "revoked":
				g.RevokedAt = &now
			}
			if name != "missing directory" && name != "empty directory" {
				if err := s.write(g); err != nil {
					t.Fatal(err)
				}
			}
			if name == "duplicate active" {
				other := *g
				other.Terms.ID = "grant-20261009T120000Z-00000002"
				if err := s.write(&other); err != nil {
					t.Fatal(err)
				}
			}
			before := snapshot(t, filepath.Dir(s.dir))
			got, err := s.ObserveActive(at)
			if name == "duplicate active" {
				if err == nil || !strings.Contains(err.Error(), "both active; refusing to choose") || got != nil {
					t.Fatalf("ambiguous observation: %+v, %v", got, err)
				}
			} else if err != nil || (name == "active" && !reflect.DeepEqual(got, g)) || (name != "active" && got != nil) {
				t.Fatalf("observation: %+v, %v", got, err)
			}
			if !reflect.DeepEqual(before, snapshot(t, filepath.Dir(s.dir))) {
				t.Fatal("observation wrote files")
			}
			if _, err := os.Stat(filepath.Join(s.dir, lockName)); !os.IsNotExist(err) {
				t.Fatalf("observation created a lock file: %v", err)
			}
			if name == "missing directory" {
				if _, err := os.Stat(s.dir); !os.IsNotExist(err) {
					t.Fatalf("observation created the grant directory: %v", err)
				}
			}
		})
	}
}

func TestObserveActiveStrictRecordsAndFreshness(t *testing.T) {
	s := newStore(t)
	if err := os.Mkdir(s.dir, 0o755); err != nil {
		t.Fatal(err)
	}
	g := observedGrant()
	path := filepath.Join(s.dir, g.Terms.ID+".json")
	valid, err := json.Marshal(g)
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct{ data, want string }{
		{"{broken", "unreadable"},
		{`null`, "schema_version 0"},
		{`[]`, "unreadable"},
		{`{"schema_version":99}`, "schema_version 99"},
		{strings.Replace(string(valid), `"schema_version":1`, `"schema_version":1,"unknown":true`, 1), "unknown field"},
		{strings.Replace(string(valid), `"terms":{`, `"terms":{"unknown":true,`, 1), "unknown field"},
		{strings.Replace(string(valid), g.Terms.ID, "wrong-id", 1), "does not hold grant"},
		{string(valid) + ` {}`, "trailing data"},
	} {
		if err := os.WriteFile(path, []byte(tc.data), 0o644); err != nil {
			t.Fatal(err)
		}
		before := snapshot(t, s.dir)
		got, err := s.ObserveActive(now)
		if got != nil || err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Fatalf("%s: got=%+v err=%v, want %s", tc.data, got, err, tc.want)
		}
		if !reflect.DeepEqual(before, snapshot(t, s.dir)) {
			t.Fatal("failed observation changed the store")
		}
	}
	// Recover on this same Store, then observe a changed holder and revocation.
	for _, holder := range []string{"holder-a", "holder-b"} {
		g.SessionID = holder
		if err := s.write(g); err != nil {
			t.Fatal(err)
		}
		got, err := s.ObserveActive(now)
		if err != nil || !reflect.DeepEqual(got, g) {
			t.Fatalf("fresh observation: %+v, %v", got, err)
		}
	}
	g.RevokedAt = &now
	if err := s.write(g); err != nil {
		t.Fatal(err)
	}
	if got, err := s.ObserveActive(now); got != nil || err != nil {
		t.Fatalf("fresh revocation: %+v, %v", got, err)
	}
	// Even an ended record is parsed strictly, not silently skipped.
	if err := os.WriteFile(path, []byte(strings.TrimSuffix(string(valid), "}")+`,"unknown":true}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ObserveActive(g.Terms.ExpiresAt); err == nil {
		t.Fatal("ignored a malformed expired record")
	}
	if _, err := os.Stat(filepath.Join(s.dir, lockName)); !os.IsNotExist(err) {
		t.Fatalf("observation created a lock: %v", err)
	}
}

func TestObserveActiveUnreadableStore(t *testing.T) {
	s := newStore(t)
	if err := os.WriteFile(s.dir, []byte("not a directory"), 0o644); err != nil {
		t.Fatal(err)
	}
	if g, err := s.ObserveActive(now); err == nil || g != nil {
		t.Fatalf("unreadable store became absence: %+v, %v", g, err)
	}
}

func TestObserveActiveDoesNotTakeLifecycleLock(t *testing.T) {
	s := newStore(t)
	err := s.withLock(func() error {
		if err := s.write(observedGrant()); err != nil {
			return err
		}
		done := make(chan error, 1)
		go func() {
			g, err := s.ObserveActive(now)
			if err == nil && g == nil {
				err = errors.New("active grant missing")
			}
			done <- err
		}()
		select {
		case err := <-done:
			return err
		case <-time.After(5 * time.Second):
			return errors.New("observation waited for the lifecycle lock")
		}
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestOpenGrantStoreGitFailures(t *testing.T) {
	root, err := paths.Canonical(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	common := t.TempDir()
	file := filepath.Join(common, "file")
	if err := os.WriteFile(file, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"valid", "top execution", "top exit", "root mismatch", "common execution", "common exit", "empty common", "relative common", "missing common", "file common"} {
		t.Run(name, func(t *testing.T) {
			calls := []execxtest.Call{
				{Name: "git", Args: []string{"rev-parse", "--show-toplevel"}, Dir: root, Stdout: root},
				{Name: "git", Args: []string{"rev-parse", "--path-format=absolute", "--git-common-dir"}, Dir: root, Stdout: common},
			}
			switch name {
			case "top execution":
				calls[0].Err = errors.New("git unavailable")
				calls = calls[:1]
			case "top exit":
				calls[0].Exit, calls[0].Stderr = 1, "top probe failed"
				calls = calls[:1]
			case "root mismatch":
				calls[0].Stdout = common
				calls = calls[:1]
			case "common execution":
				calls[1].Err = errors.New("common probe failed")
			case "common exit":
				calls[1].Exit, calls[1].Stderr = 1, "common probe failed"
			case "empty common":
				calls[1].Stdout = ""
			case "relative common":
				calls[1].Stdout = ".git"
			case "missing common":
				calls[1].Stdout = filepath.Join(common, "missing")
			case "file common":
				calls[1].Stdout = file
			}
			before := snapshot(t, common)
			script := &execxtest.Script{T: t, Calls: calls}
			s, err := Open(t.Context(), script, root)
			script.AssertExhausted()
			if name == "valid" {
				if err != nil || s.Dir() != filepath.Join(common, DirName) {
					t.Fatalf("Open: %+v, %v", s, err)
				}
			} else if err == nil || s != nil {
				t.Fatalf("invalid Git probe succeeded: %+v, %v", s, err)
			}
			if !reflect.DeepEqual(before, snapshot(t, common)) {
				t.Fatal("opening a grant store wrote files")
			}
		})
	}
}
