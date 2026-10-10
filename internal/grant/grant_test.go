package grant

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"
)

const session = "session-a"

var now = time.Date(2026, 10, 9, 12, 0, 0, 0, time.UTC)

func proposal() Proposal {
	return Proposal{
		SchemaVersion:       SchemaVersion,
		Scope:               []ScopeItem{{Name: "issue-351", Description: "Add autonomy grants"}},
		Gates:               []string{"plan", "merge"},
		ExpiresAt:           now.Add(8 * time.Hour),
		RunLimit:            2,
		MergeLimit:          3,
		RelayPermissionMode: "acceptEdits",
	}
}

func approved(t *testing.T, p Proposal) *CreateRequest {
	t.Helper()
	pv, err := MakePreview(p, session, now)
	if err != nil {
		t.Fatal(err)
	}
	return &CreateRequest{SchemaVersion: SchemaVersion, Terms: pv.Terms, Approval: Approval{
		GrantDigest: pv.Digest, ApprovedBy: "kninetimmy", ApprovedAt: now, Statement: ApprovalStatement,
	}}
}

// snapshot captures every file under dir so a refusal can prove it wrote nothing.
func snapshot(t *testing.T, dir string) map[string]string {
	t.Helper()
	out := map[string]string{}
	err := filepath.WalkDir(dir, func(p string, d os.DirEntry, err error) error {
		if errors.Is(err, os.ErrNotExist) {
			return filepath.SkipAll
		}
		if err != nil || d.IsDir() {
			return err
		}
		data, err := os.ReadFile(p)
		out[p] = string(data)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func newStore(t *testing.T) *Store {
	return &Store{dir: filepath.Join(t.TempDir(), DirName)}
}

func TestPreviewCompletesTheGrant(t *testing.T) {
	pv, err := MakePreview(proposal(), session, now)
	if err != nil {
		t.Fatal(err)
	}
	tm := pv.Terms
	if tm.FixCycleLimit != 2 || tm.ContextThreshold != 450000 || tm.SessionID != session || !idPattern.MatchString(tm.ID) {
		t.Fatalf("terms = %+v", tm)
	}
	if d, _ := Digest(tm); d != pv.Digest {
		t.Fatalf("digest %s, recomputed %s", pv.Digest, d)
	}
	five, big := 5, 1000
	p := proposal()
	p.FixCycleLimit, p.ContextThreshold = &five, &big
	pv, err = MakePreview(p, session, now)
	if err != nil || pv.Terms.FixCycleLimit != 5 || pv.Terms.ContextThreshold != 1000 {
		t.Fatalf("explicit limits: %+v, %v", pv, err)
	}
}

// Every Terms field changes the digest; a new field without a mutator fails.
func TestDigestCoversEveryField(t *testing.T) {
	pv, err := MakePreview(proposal(), session, now)
	if err != nil {
		t.Fatal(err)
	}
	mutators := map[string]func(*Terms){
		"ID":                  func(t *Terms) { t.ID += "x" },
		"Scope":               func(t *Terms) { t.Scope = []ScopeItem{{Name: "other", Description: "Add autonomy grants"}} },
		"Gates":               func(t *Terms) { t.Gates = []string{"plan"} },
		"ExpiresAt":           func(t *Terms) { t.ExpiresAt = t.ExpiresAt.Add(time.Second) },
		"RunLimit":            func(t *Terms) { t.RunLimit++ },
		"MergeLimit":          func(t *Terms) { t.MergeLimit++ },
		"FixCycleLimit":       func(t *Terms) { t.FixCycleLimit++ },
		"ContextThreshold":    func(t *Terms) { t.ContextThreshold++ },
		"RelayPermissionMode": func(t *Terms) { t.RelayPermissionMode = "plan" },
		"SessionID":           func(t *Terms) { t.SessionID = "session-b" },
	}
	if n := reflect.TypeFor[Terms]().NumField(); n != len(mutators) {
		t.Fatalf("Terms has %d fields, test mutates %d", n, len(mutators))
	}
	for name, mutate := range mutators {
		tm := pv.Terms
		tm.Scope = append([]ScopeItem(nil), tm.Scope...)
		mutate(&tm)
		if d, _ := Digest(tm); d == pv.Digest {
			t.Errorf("changing %s kept the digest", name)
		}
	}
	tm := pv.Terms
	tm.Scope = []ScopeItem{{Name: "issue-351", Description: "changed"}}
	if d, _ := Digest(tm); d == pv.Digest {
		t.Error("changing a scope description kept the digest")
	}
}

func TestPreviewRefusesInvalidTerms(t *testing.T) {
	zero, neg := 0, -1
	cases := map[string]struct {
		mutate  func(*Proposal)
		session string
		want    string
	}{
		"bypass":         {func(p *Proposal) { p.RelayPermissionMode = "bypassPermissions" }, session, "never delegated"},
		"unknown mode":   {func(p *Proposal) { p.RelayPermissionMode = "yolo" }, session, "not one of"},
		"run limit":      {func(p *Proposal) { p.RunLimit = 0 }, session, "run_limit"},
		"merge limit":    {func(p *Proposal) { p.MergeLimit = -2 }, session, "merge_limit"},
		"fix cycles":     {func(p *Proposal) { p.FixCycleLimit = &zero }, session, "fix_cycle_limit"},
		"threshold":      {func(p *Proposal) { p.ContextThreshold = &neg }, session, "context_threshold_tokens"},
		"no session":     {func(*Proposal) {}, "", SessionEnv},
		"past expiry":    {func(p *Proposal) { p.ExpiresAt = now }, session, "not in the future"},
		"no gates":       {func(p *Proposal) { p.Gates = nil }, session, "gates"},
		"bad gate":       {func(p *Proposal) { p.Gates = []string{"deploy"} }, session, "deploy"},
		"no scope":       {func(p *Proposal) { p.Scope = nil }, session, "scope"},
		"multiline":      {func(p *Proposal) { p.Scope[0].Description = "a\nb" }, session, "one-line"},
		"schema version": {func(p *Proposal) { p.SchemaVersion = 2 }, session, "schema_version"},
	}
	for name, c := range cases {
		p := proposal()
		c.mutate(&p)
		if _, err := MakePreview(p, c.session, now); err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: err = %v, want %q", name, err, c.want)
		}
	}
	if _, err := DecodeProposal([]byte(`{"schema_version":1,"run_limit":1.5}`)); err == nil {
		t.Error("a fractional limit decoded")
	}
}

func TestCreateRefusalsWriteNothing(t *testing.T) {
	s := newStore(t)
	cases := map[string]struct {
		mutate  func(*CreateRequest)
		session string
	}{
		"digest":       {func(r *CreateRequest) { r.Approval.GrantDigest = "sha256:00" }, session},
		"statement":    {func(r *CreateRequest) { r.Approval.Statement = "approve-and-enter-delivery" }, session},
		"approver":     {func(r *CreateRequest) { r.Approval.ApprovedBy = " " }, session},
		"approval at":  {func(r *CreateRequest) { r.Approval.ApprovedAt = time.Time{} }, session},
		"changed term": {func(r *CreateRequest) { r.Terms.RunLimit = 99 }, session},
		"no session":   {func(*CreateRequest) {}, ""},
		"other":        {func(*CreateRequest) {}, "session-b"},
		"bypass":       {func(r *CreateRequest) { r.Terms.RelayPermissionMode = "bypassPermissions" }, session},
		"zero limit":   {func(r *CreateRequest) { r.Terms.MergeLimit = 0 }, session},
	}
	for name, c := range cases {
		req := approved(t, proposal())
		c.mutate(req)
		if _, err := s.Create(req, c.session, now); err == nil {
			t.Errorf("%s: created", name)
		}
		if snap := snapshot(t, s.dir); len(snap) != 0 {
			t.Errorf("%s: wrote %v", name, snap)
		}
	}
	// An approved expiry that has since passed is refused too.
	if _, err := s.Create(approved(t, proposal()), session, now.Add(9*time.Hour)); err == nil {
		t.Error("expired grant created")
	}
	if _, err := DecodeCreate([]byte(`{"schema_version":2}`)); err == nil {
		t.Error("schema 2 create request decoded")
	}
}

func TestLifecycle(t *testing.T) {
	s := newStore(t)
	if g, err := s.Active(now); g != nil || err != nil {
		t.Fatalf("empty store: %v, %v", g, err)
	}
	if _, err := os.Stat(s.dir); !os.IsNotExist(err) {
		t.Fatalf("reading an empty store created %s: %v", s.dir, err)
	}
	if r, err := s.Revoke(now); r != nil || err != nil {
		t.Fatalf("revoke on an empty store: %v, %v", r, err)
	}
	if _, err := s.RecordRun("grant-20260101T000000Z-00000000", "run-1", now); !errors.Is(err, ErrNoActiveGrant) {
		t.Fatalf("record on an empty store: %v", err)
	}
	if _, err := os.Stat(s.dir); !os.IsNotExist(err) {
		t.Fatalf("no-op operations created %s: %v", s.dir, err)
	}
	req := approved(t, proposal())
	g, err := s.Create(req, session, now)
	if err != nil {
		t.Fatal(err)
	}
	if g.SessionID != session || g.ApprovedBy != "kninetimmy" || !g.ApprovedAt.Equal(now) || g.Digest != req.Approval.GrantDigest {
		t.Fatalf("grant = %+v", g)
	}
	got, err := s.Active(now)
	if err != nil || got == nil || got.Terms.ID != g.Terms.ID {
		t.Fatalf("active = %+v, %v", got, err)
	}

	// Another active grant blocks creation and writes nothing.
	before := snapshot(t, s.dir)
	if _, err := s.Create(approved(t, proposal()), session, now); err == nil || !strings.Contains(err.Error(), "already active") {
		t.Fatalf("second grant: %v", err)
	}
	if !reflect.DeepEqual(before, snapshot(t, s.dir)) {
		t.Fatal("refused second grant changed the store")
	}

	revokedAt := now.Add(time.Minute)
	r, err := s.Revoke(revokedAt)
	if err != nil || r == nil || r.RevokedAt == nil || !r.RevokedAt.Equal(revokedAt) {
		t.Fatalf("revoke = %+v, %v", r, err)
	}
	if got, err := s.Active(revokedAt); got != nil || err != nil {
		t.Fatalf("revoked grant still active: %+v, %v", got, err)
	}
	before = snapshot(t, s.dir)
	if r, err := s.Revoke(revokedAt); r != nil || err != nil {
		t.Fatalf("second revoke = %+v, %v", r, err)
	}
	if !reflect.DeepEqual(before, snapshot(t, s.dir)) {
		t.Fatal("revoke with no active grant changed the store")
	}
	// The ended grant's own approval cannot bring it back.
	if _, err := s.Create(req, session, revokedAt); err == nil || !strings.Contains(err.Error(), "already created") {
		t.Fatalf("recreate revoked grant: %v", err)
	}
	if _, err := s.RecordRun(g.Terms.ID, "run-1", revokedAt); !errors.Is(err, ErrNoActiveGrant) {
		t.Fatalf("record against revoked grant: %v", err)
	}

	// Expiry ends a grant; a new grant is the only way to delegate again.
	g2, err := s.Create(approved(t, proposal()), session, revokedAt)
	if err != nil {
		t.Fatal(err)
	}
	expired := g2.Terms.ExpiresAt
	if got, _ := s.Active(expired); got != nil {
		t.Fatal("grant active at its expiry")
	}
	if got, _ := s.Active(expired.Add(-time.Second)); got == nil || got.Terms.ID != g2.Terms.ID {
		t.Fatal("grant inactive before its expiry")
	}
	p := proposal()
	p.ExpiresAt = expired.Add(time.Hour)
	pv, err := MakePreview(p, session, expired)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Create(&CreateRequest{SchemaVersion: 1, Terms: pv.Terms, Approval: Approval{pv.Digest, "kninetimmy", expired, ApprovalStatement}}, session, expired); err != nil {
		t.Fatalf("new grant after expiry: %v", err)
	}
}

func TestUnknownSchemaVersionRefused(t *testing.T) {
	s := newStore(t)
	g, err := s.Create(approved(t, proposal()), session, now)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(s.dir, g.Terms.ID+".json")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	data = bytes.Replace(data, []byte(`"schema_version": 1`), []byte(`"schema_version": 2`), 1)
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Active(now); err == nil || !strings.Contains(err.Error(), "schema_version 2") {
		t.Fatalf("schema 2 record: %v", err)
	}
	if _, err := s.Create(approved(t, proposal()), session, now); err == nil {
		t.Fatal("created beside an unreadable record")
	}
}

func TestRecordLimitsPersist(t *testing.T) {
	s := newStore(t)
	g, err := s.Create(approved(t, proposal()), session, now)
	if err != nil {
		t.Fatal(err)
	}
	id := g.Terms.ID
	for i := range 2 {
		if _, err := s.RecordRun(id, fmt.Sprintf("run-%d", i), now); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := s.RecordRun(id, "run-3", now); !errors.Is(err, ErrLimitReached) {
		t.Fatalf("third run: %v", err)
	}
	if _, err := s.RecordMerge(id, "pr-1", now); err != nil {
		t.Fatal(err)
	}
	if _, err := s.RecordMerge("grant-20260101T000000Z-00000000", "pr-2", now); !errors.Is(err, ErrNoActiveGrant) {
		t.Fatalf("record against another id: %v", err)
	}
	fresh := &Store{dir: s.dir}
	got, err := fresh.Active(now)
	if err != nil || len(got.Runs) != 2 || len(got.Merges) != 1 || got.Runs[1].Ref != "run-1" {
		t.Fatalf("persisted = %+v, %v", got, err)
	}
}

// Many same-process recorders race for the last two merge units; each opens its
// own lock handle, so they contend exactly as separate processes do.
func TestRecordConcurrentGoroutines(t *testing.T) {
	s := newStore(t)
	g, err := s.Create(approved(t, proposal()), session, now)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.RecordMerge(g.Terms.ID, "pr-0", now); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	var mu sync.Mutex
	ok := 0
	for i := range 12 {
		wg.Go(func() {
			_, err := s.RecordMerge(g.Terms.ID, fmt.Sprintf("pr-%d", i+1), now)
			mu.Lock()
			defer mu.Unlock()
			if err == nil {
				ok++
			} else if !errors.Is(err, ErrLimitReached) {
				t.Error(err)
			}
		})
	}
	wg.Wait()
	got, err := s.Active(now)
	if err != nil || ok != 2 || len(got.Merges) != 3 {
		t.Fatalf("ok = %d, merges = %d, err = %v", ok, len(got.Merges), err)
	}
}

// TestRecordRunProcess is the helper body for TestRecordConcurrentProcesses.
func TestRecordRunProcess(t *testing.T) {
	dir, id := os.Getenv("ORCH_TEST_GRANT_DIR"), os.Getenv("ORCH_TEST_GRANT_ID")
	if dir == "" {
		return
	}
	if _, err := (&Store{dir: dir}).RecordRun(id, os.Getenv("ORCH_TEST_GRANT_REF"), now); err != nil {
		fmt.Println("refused:", err)
		os.Exit(3)
	}
	fmt.Println("recorded")
	os.Exit(0)
}

// Two processes record against the last run unit at the same time: the lock is
// held while both start, so both wait on it, and exactly one records.
func TestRecordConcurrentProcesses(t *testing.T) {
	s := newStore(t)
	g, err := s.Create(approved(t, proposal()), session, now)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.RecordRun(g.Terms.ID, "run-0", now); err != nil {
		t.Fatal(err)
	}
	var cmds []*exec.Cmd
	var outs []*bytes.Buffer
	release := make(chan struct{})
	held := make(chan struct{})
	lockDone := make(chan error, 1)
	go func() {
		lockDone <- s.withLock(func() error { close(held); <-release; return nil })
	}()
	<-held
	for i := range 2 {
		cmd := exec.Command(os.Args[0], "-test.run=^TestRecordRunProcess$")
		cmd.Env = append(os.Environ(), "ORCH_TEST_GRANT_DIR="+s.dir, "ORCH_TEST_GRANT_ID="+g.Terms.ID, fmt.Sprintf("ORCH_TEST_GRANT_REF=run-p%d", i))
		out := new(bytes.Buffer)
		cmd.Stdout, cmd.Stderr = out, out
		if err := cmd.Start(); err != nil {
			close(release)
			t.Fatal(err)
		}
		cmds, outs = append(cmds, cmd), append(outs, out)
	}
	close(release)
	if err := <-lockDone; err != nil {
		t.Fatal(err)
	}
	recorded := 0
	for i, cmd := range cmds {
		err := cmd.Wait()
		switch {
		case err == nil:
			recorded++
		case strings.Contains(outs[i].String(), ErrLimitReached.Error()):
		default:
			t.Fatalf("process %d: %v: %s", i, err, outs[i])
		}
	}
	got, err := s.Active(now)
	if err != nil || recorded != 1 || len(got.Runs) != 2 {
		t.Fatalf("recorded = %d, runs = %+v, err = %v", recorded, got, err)
	}
}

// A recorded approval spends its unit once: recording the same run or merge
// again (a verb re-run after a crash) spends nothing, and no approval is
// recorded past a limit. Get still reads the grant after it ends.
func TestRecordApproval(t *testing.T) {
	s := newStore(t)
	p := proposal()
	p.MergeLimit = 1
	g, err := s.Create(approved(t, p), session, now)
	if err != nil {
		t.Fatal(err)
	}
	id := g.Terms.ID
	merge := RecordedApproval{Gate: "merge", RunID: "run-1", Issue: 7, PR: 9, Head: "abc"}
	for range 2 {
		if _, err := s.RecordApproval(id, RecordedApproval{Gate: "plan", RunID: "run-1"}, now); err != nil {
			t.Fatal(err)
		}
		if _, err := s.RecordApproval(id, merge, now); err != nil {
			t.Fatal(err)
		}
	}
	merge.Issue = 8
	if _, err := s.RecordApproval(id, merge, now); !errors.Is(err, ErrLimitReached) {
		t.Fatalf("second merge: %v", err)
	}
	if _, err := s.RecordApproval(id, RecordedApproval{Gate: "wrap-up", RunID: "run-1"}, now); err == nil {
		t.Fatal("recorded a wrap-up approval, which spends no unit")
	}
	if _, err := s.Revoke(now); err != nil {
		t.Fatal(err)
	}
	got, err := s.Get(id)
	if err != nil {
		t.Fatal(err)
	}
	want := []RecordedApproval{{Gate: "plan", RunID: "run-1", At: now}, {Gate: "merge", RunID: "run-1", Issue: 7, PR: 9, Head: "abc", At: now}}
	if got.RevokedAt == nil || len(got.Runs) != 1 || got.Merges[0].Ref != MergeRef("run-1", 7) || len(got.Merges) != 1 || !reflect.DeepEqual(got.Approvals, want) {
		t.Fatalf("grant = %+v", got)
	}
	if _, err := s.Get("grant-20260101T000000Z-00000000"); err == nil {
		t.Fatal("got a grant that was never recorded")
	}
}
