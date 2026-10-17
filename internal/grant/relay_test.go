package grant

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/kninetimmy/orch/internal/execx"
)

// liveShort and liveSession are the ids Claude Code 2.1.301 reported for a
// relay launch on 2026-10-17; it ignored the --session-id it was passed.
const (
	liveShort   = "20b2927b"
	liveSession = "20b2927b-b189-4cf5-825f-a78cd2fcfea8"
)

// launchOutput is what `claude --bg` printed on 2.1.301, for short.
func launchOutput(short string) string {
	return "backgrounded · " + short + "\n  claude agents             list sessions\n  claude attach " + short +
		"    open in this terminal\n  claude logs " + short + "      show recent output\n  claude stop " + short + "      stop this session"
}

// agentsOutput is a `claude agents --json` listing shaped like 2.1.301's: an
// interactive entry without an id, then one background entry per id pair.
func agentsOutput(background ...string) string {
	entries := []string{`{"pid":1,"cwd":"C:\\repo","kind":"interactive","sessionId":"aaaaaaaa-0000-4000-8000-000000000000","status":"idle"}`}
	for i := 0; i+1 < len(background); i += 2 {
		entries = append(entries, `{"pid":13352,"id":"`+background[i]+`","cwd":"C:\\Users\\Kninetimmy\\Orch","kind":"background",`+
			`"startedAt":1792253779006,"sessionId":"`+background[i+1]+`","name":"orch grant autonomy handoff","status":"busy","state":"working"}`)
	}
	return "[" + strings.Join(entries, ",") + "]"
}

func prints(stdout string) func() (execx.Result, error) {
	return func() (execx.Result, error) { return execx.Result{Stdout: stdout}, nil }
}

// fakeClaude records each call and answers `claude --bg` and `claude agents
// --json` like 2.1.301 did, unless told otherwise.
type fakeClaude struct {
	cmds   []execx.Cmd
	launch func() (execx.Result, error)
	agents func() (execx.Result, error)
	hang   string // first argument of the call that blocks until its context ends
}

func (f *fakeClaude) Run(ctx context.Context, c execx.Cmd) (execx.Result, error) {
	f.cmds = append(f.cmds, c)
	if c.Args[0] == f.hang {
		<-ctx.Done()
		return execx.Result{}, ctx.Err()
	}
	answer := f.launch
	if c.Args[0] == "agents" {
		answer = f.agents
		if answer == nil {
			answer = prints(agentsOutput(liveShort, liveSession))
		}
	} else if answer == nil {
		answer = prints(launchOutput(liveShort))
	}
	return answer()
}

func createdStore(t *testing.T) (*Store, *Grant) {
	t.Helper()
	s := newStore(t)
	g, err := s.Create(approved(t, proposal()), session, now)
	if err != nil {
		t.Fatal(err)
	}
	return s, g
}

func TestRelayHandsOffTheHolder(t *testing.T) {
	s, g := createdStore(t)
	f := &fakeClaude{}
	at := now.Add(time.Minute)
	got, err := s.Relay(context.Background(), f, "/repo", session, at)
	if err != nil {
		t.Fatal(err)
	}
	want := [][]string{{
		"--bg", "--remote-control", g.Terms.ID, "--permission-mode", "acceptEdits",
		"You continue autonomy grant " + g.Terms.ID + ", relayed to you by the previous holder so this work proceeds in a clean context. Follow the Orch grant guidance before acting.",
	}, {"agents", "--json"}}
	if len(f.cmds) != len(want) {
		t.Fatalf("claude calls = %d", len(f.cmds))
	}
	for i, c := range f.cmds {
		if c.Name != "claude" || c.Dir != "/repo" || c.Stdin != nil || c.Env != nil || !reflect.DeepEqual(c.Args, want[i]) {
			t.Fatalf("call %d = %+v\nwant args %q", i, c, want[i])
		}
	}
	if got.SessionID != liveSession {
		t.Fatalf("successor id = %q, want %q", got.SessionID, liveSession)
	}
	if !reflect.DeepEqual(got.Relays, []Relay{{From: session, To: liveSession, At: at}}) {
		t.Fatalf("relays = %+v", got.Relays)
	}
	if got.Terms.SessionID != session {
		t.Fatalf("creator changed: %q", got.Terms.SessionID)
	}
	stored, err := s.Active(at)
	if err != nil || stored.SessionID != liveSession || len(stored.Relays) != 1 {
		t.Fatalf("stored = %+v, %v", stored, err)
	}
	// The previous holder can no longer relay, and the chain extends.
	before := snapshot(t, s.dir)
	if _, err := s.Relay(context.Background(), f, "/repo", session, at); err == nil || !strings.Contains(err.Error(), "does not hold") {
		t.Fatalf("old holder relay: %v", err)
	}
	if len(f.cmds) != 2 || !reflect.DeepEqual(before, snapshot(t, s.dir)) {
		t.Fatal("refused relay launched or wrote")
	}
	const short, third = "0c1d2e3f", "0c1d2e3f-0000-4000-8000-000000000001"
	f = &fakeClaude{launch: prints(launchOutput(short)), agents: prints(agentsOutput(liveShort, liveSession, short, third))}
	later := at.Add(time.Minute)
	again, err := s.Relay(context.Background(), f, "/repo", liveSession, later)
	if err != nil || again.SessionID != third || len(again.Relays) != 2 || again.Relays[1] != (Relay{From: liveSession, To: third, At: later}) {
		t.Fatalf("second relay = %+v, %v", again, err)
	}
}

func TestRelayRefusalsAndFailuresLeaveTheHolder(t *testing.T) {
	empty := newStore(t)
	f := &fakeClaude{}
	if _, err := empty.Relay(context.Background(), f, "/repo", session, now); !errors.Is(err, ErrNoActiveGrant) || len(f.cmds) != 0 {
		t.Fatalf("no grant: %v, launches %d", err, len(f.cmds))
	}
	failed := func(err error) func() (execx.Result, error) {
		return func() (execx.Result, error) { return execx.Result{}, err }
	}
	exited := func(code int, stderr string) func() (execx.Result, error) {
		return func() (execx.Result, error) { return execx.Result{ExitCode: code, Stderr: stderr}, nil }
	}
	stop := "claude stop " + liveShort
	for _, tc := range []struct {
		name   string
		holder string
		launch func() (execx.Result, error)
		agents func() (execx.Result, error)
		want   []string
		runs   int
	}{
		{"absent session", "", nil, nil, []string{SessionEnv}, 0},
		{"other session", "session-b", nil, nil, []string{"does not hold"}, 0},
		{"cannot run", session, failed(errors.New("claude not found")), nil, []string{"claude not found"}, 1},
		{"non-zero exit", session, exited(1, "invalid permission mode"), nil, []string{"invalid permission mode"}, 1},
		{"unrecognized output", session, prints("ok"), nil, []string{"did not report"}, 1},
		{"no short id on its line", session, prints("backgrounded · \n  deadbeef\n"), nil, []string{"did not report"}, 1},
		{"agents cannot run", session, nil, failed(errors.New("agents broke")), []string{"agents broke", stop}, 2},
		{"agents non-zero exit", session, nil, exited(2, "no daemon"), []string{"exited 2", "no daemon", stop}, 2},
		{"agents not JSON", session, nil, prints("sessions: none"), []string{"decode", stop}, 2},
		{"agents lists no background entry", session, nil, prints(agentsOutput()), []string{"lists 0", stop}, 2},
		{"agents lists another id", session, nil, prints(agentsOutput("ffffffff", "ffffffff-0000-4000-8000-000000000000")), []string{"lists 0", stop}, 2},
		{"agents lists the id as interactive", session, nil, prints(strings.Replace(agentsOutput(liveShort, liveSession), `"background"`, `"interactive"`, 1)), []string{"lists 0", stop}, 2},
		{"agents lists the id twice", session, nil, prints(agentsOutput(liveShort, liveSession, liveShort, "20b2927b-0000-4000-8000-000000000000")), []string{"lists 2", stop}, 2},
		{"agents entry without a sessionId", session, nil, prints(agentsOutput(liveShort, "")), []string{"without a sessionId", stop}, 2},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, _ := createdStore(t)
			before := snapshot(t, s.dir)
			f := &fakeClaude{launch: tc.launch, agents: tc.agents}
			_, err := s.Relay(context.Background(), f, "/repo", tc.holder, now)
			for _, want := range tc.want {
				if err == nil || !strings.Contains(err.Error(), want) {
					t.Fatalf("err = %v, want %q", err, want)
				}
			}
			if len(f.cmds) != tc.runs || !reflect.DeepEqual(before, snapshot(t, s.dir)) {
				t.Fatalf("claude calls %d (want %d) or the record changed", len(f.cmds), tc.runs)
			}
		})
	}
}

func TestRelayTimesOutWithTheHolderUnchanged(t *testing.T) {
	old := relayTimeout
	relayTimeout = 20 * time.Millisecond
	t.Cleanup(func() { relayTimeout = old })
	for _, tc := range []struct {
		hang, want string
		runs       int
	}{
		{"--bg", "claude agents", 1},
		{"agents", "claude stop " + liveShort, 2},
	} {
		t.Run(tc.hang, func(t *testing.T) {
			s, _ := createdStore(t)
			before := snapshot(t, s.dir)
			f := &fakeClaude{hang: tc.hang}
			_, err := s.Relay(context.Background(), f, "/repo", session, now)
			if err == nil || !errors.Is(err, context.DeadlineExceeded) || !strings.Contains(err.Error(), tc.want) || len(f.cmds) != tc.runs {
				t.Fatalf("err = %v, calls %d", err, len(f.cmds))
			}
			if !reflect.DeepEqual(before, snapshot(t, s.dir)) {
				t.Fatal("timed-out relay changed the record")
			}
			// The lock was released: the user's kill switch still works.
			if r, err := s.Revoke(now); err != nil || r == nil {
				t.Fatalf("revoke after timeout = %+v, %v", r, err)
			}
		})
	}
}

func TestRelayRefusesATamperedRecord(t *testing.T) {
	for _, tc := range []struct {
		name   string
		tamper func(*Grant)
		want   string
	}{
		{"terms edited", func(g *Grant) { g.Terms.RelayPermissionMode = "plan" }, "approved digest"},
		{"bypassPermissions with a matching digest", func(g *Grant) {
			g.Terms.RelayPermissionMode = "bypassPermissions"
			g.Digest, _ = Digest(g.Terms)
		}, "never uses"},
		{"unknown mode with a matching digest", func(g *Grant) {
			g.Terms.RelayPermissionMode = "--dangerously-skip-permissions"
			g.Digest, _ = Digest(g.Terms)
		}, "never uses"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, g := createdStore(t)
			tc.tamper(g)
			if err := s.write(g); err != nil {
				t.Fatal(err)
			}
			before := snapshot(t, s.dir)
			f := &fakeClaude{}
			if _, err := s.Relay(context.Background(), f, "/repo", session, now); err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err = %v, want %q", err, tc.want)
			}
			if len(f.cmds) != 0 || !reflect.DeepEqual(before, snapshot(t, s.dir)) {
				t.Fatal("tampered relay launched or wrote")
			}
		})
	}
}
