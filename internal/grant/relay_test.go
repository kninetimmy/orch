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

// fakeClaude records each launch and answers like `claude --bg` would.
type fakeClaude struct {
	cmds  []execx.Cmd
	reply func(args []string) (execx.Result, error)
}

func (f *fakeClaude) Run(_ context.Context, c execx.Cmd) (execx.Result, error) {
	f.cmds = append(f.cmds, c)
	if f.reply != nil {
		return f.reply(c.Args)
	}
	return execx.Result{Stdout: "backgrounded · " + c.Args[2][:8] + " (idle)\n"}, nil
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
	if len(f.cmds) != 1 {
		t.Fatalf("launches = %d", len(f.cmds))
	}
	c := f.cmds[0]
	next := got.SessionID
	want := []string{
		"--bg", "--session-id", next, "--remote-control", g.Terms.ID, "--permission-mode", "acceptEdits",
		"You continue autonomy grant " + g.Terms.ID + ", relayed to you by the previous holder so this work proceeds in a clean context. Follow the Orch grant guidance before acting.",
	}
	if c.Name != "claude" || c.Dir != "/repo" || c.Stdin != nil || c.Env != nil || !reflect.DeepEqual(c.Args, want) {
		t.Fatalf("launch = %+v\nwant args %q", c, want)
	}
	if next == session || len(next) != 36 || next[14] != '4' {
		t.Fatalf("successor id = %q", next)
	}
	if !reflect.DeepEqual(got.Relays, []Relay{{From: session, To: next, At: at}}) {
		t.Fatalf("relays = %+v", got.Relays)
	}
	if got.Terms.SessionID != session {
		t.Fatalf("creator changed: %q", got.Terms.SessionID)
	}
	stored, err := s.Active(at)
	if err != nil || stored.SessionID != next || len(stored.Relays) != 1 {
		t.Fatalf("stored = %+v, %v", stored, err)
	}
	// The previous holder can no longer relay, and the chain extends.
	before := snapshot(t, s.dir)
	if _, err := s.Relay(context.Background(), f, "/repo", session, at); err == nil || !strings.Contains(err.Error(), "does not hold") {
		t.Fatalf("old holder relay: %v", err)
	}
	if len(f.cmds) != 1 || !reflect.DeepEqual(before, snapshot(t, s.dir)) {
		t.Fatal("refused relay launched or wrote")
	}
	again, err := s.Relay(context.Background(), f, "/repo", next, at.Add(time.Minute))
	if err != nil || len(again.Relays) != 2 || again.Relays[1].From != next {
		t.Fatalf("second relay = %+v, %v", again, err)
	}
}

func TestRelayRefusalsAndFailuresLeaveTheHolder(t *testing.T) {
	empty := newStore(t)
	f := &fakeClaude{}
	if _, err := empty.Relay(context.Background(), f, "/repo", session, now); !errors.Is(err, ErrNoActiveGrant) || len(f.cmds) != 0 {
		t.Fatalf("no grant: %v, launches %d", err, len(f.cmds))
	}
	for _, tc := range []struct {
		name   string
		holder string
		reply  func([]string) (execx.Result, error)
		want   string
		runs   int
	}{
		{"absent session", "", nil, SessionEnv, 0},
		{"other session", "session-b", nil, "does not hold", 0},
		{"cannot run", session, func([]string) (execx.Result, error) { return execx.Result{}, errors.New("claude not found") }, "claude not found", 1},
		{"non-zero exit", session, func([]string) (execx.Result, error) {
			return execx.Result{ExitCode: 1, Stderr: "invalid permission mode"}, nil
		}, "invalid permission mode", 1},
		{"unrecognized output", session, func([]string) (execx.Result, error) { return execx.Result{Stdout: "ok"}, nil }, "did not report", 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, _ := createdStore(t)
			before := snapshot(t, s.dir)
			f := &fakeClaude{reply: tc.reply}
			if _, err := s.Relay(context.Background(), f, "/repo", tc.holder, now); err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err = %v, want %q", err, tc.want)
			}
			if len(f.cmds) != tc.runs || !reflect.DeepEqual(before, snapshot(t, s.dir)) {
				t.Fatalf("launches %d (want %d) or the record changed", len(f.cmds), tc.runs)
			}
		})
	}
}
