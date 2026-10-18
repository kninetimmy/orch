package grant

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/kninetimmy/orch/internal/execx"
)

// ClaudeBinary is the Claude Code executable a relay launches.
const ClaudeBinary = "claude"

// relayPromptFormat is the whole prompt a successor starts with; its only
// variable is the grant id, which comes from the record.
const relayPromptFormat = "You continue autonomy grant %s, relayed to you by the previous holder so this work " +
	"proceeds in a clean context. Follow the Orch grant guidance before acting."

// relayTimeout bounds each `claude` call a relay makes; `claude --bg` returns
// immediately and `claude agents --json` only reads.
var relayTimeout = time.Minute

// backgrounded matches the line `claude --bg` prints for the session it
// started, such as "backgrounded · 20b2927b".
var backgrounded = regexp.MustCompile(`(?m)^[ \t]*backgrounded[^\w\n]+([0-9a-f]+)\b`)

// Relay is one hand-off of a grant's session holder.
type Relay struct {
	From string    `json:"from"`
	To   string    `json:"to"`
	At   time.Time `json:"at"`
}

// RelayArgs returns the exact `claude` argument vector that starts g's
// successor as a background session with Remote Control on. Every element
// comes from this build or the grant record.
func RelayArgs(g *Grant) []string {
	return []string{
		"--bg",
		"--remote-control", g.Terms.ID,
		"--permission-mode", g.Terms.RelayPermissionMode,
		fmt.Sprintf(relayPromptFormat, g.Terms.ID),
	}
}

// agentsArgs lists Claude Code's sessions as JSON; the bare listing refuses
// to run without a terminal.
var agentsArgs = []string{"agents", "--json"}

// agent is the part of one `claude agents --json` entry a relay reads.
type agent struct {
	ID        string `json:"id"`
	Kind      string `json:"kind"`
	SessionID string `json:"sessionId"`
}

// HolderKind is what `claude agents --json` says a session is.
type HolderKind string

// Holder kinds. KindUnknown covers a session the listing does not include.
const (
	KindBackground  HolderKind = "background"
	KindInteractive HolderKind = "interactive"
	KindUnknown     HolderKind = "unknown"
)

// kindOf returns the kind the listing gives the session with the full id id.
func kindOf(agents []agent, id string) HolderKind {
	for _, a := range agents {
		if a.SessionID == id {
			switch HolderKind(a.Kind) {
			case KindBackground, KindInteractive:
				return HolderKind(a.Kind)
			}
		}
	}
	return KindUnknown
}

// Relay starts a successor session for the active grant on behalf of holder
// and records it as the current holder. It also reports holder's own kind,
// read from the same listing, so the caller can say who ends the holder. The holder check, the launch and the
// write share one lock, so two relays cannot both succeed. If the launch
// fails the record is unchanged.
//
// `claude --bg` prints only a short id for the session it started, and
// Claude Code 2.1.301 ignores a requested --session-id. The successor's full
// id is therefore read from the one background entry `claude agents --json`
// lists under that short id; anything else fails the relay. A failure after
// the launch can leave a started session behind; the error names it.
func (s *Store) Relay(ctx context.Context, r execx.Runner, dir, holder string, now time.Time) (*Grant, HolderKind, error) {
	kind := KindUnknown
	g, err := s.update(now, "", func(g *Grant) error {
		if holder == "" {
			return fmt.Errorf("%s is absent from the environment; only the session holding grant %s can relay it", SessionEnv, g.Terms.ID)
		}
		if holder != g.SessionID {
			return fmt.Errorf("this session (%s=%q) does not hold grant %s; its current holder is %q", SessionEnv, holder, g.Terms.ID, g.SessionID)
		}
		// The record is the only source of the launch mode, so it must still be
		// the record the user approved and a mode a relay may use.
		if digest, err := Digest(g.Terms); err != nil {
			return err
		} else if digest != g.Digest {
			return fmt.Errorf("grant %s does not match its approved digest; refusing to relay", g.Terms.ID)
		}
		if mode := g.Terms.RelayPermissionMode; mode == "bypassPermissions" || !slices.Contains(permissionModes, mode) {
			return fmt.Errorf("grant %s records relay permission mode %q, which a relay never uses; refusing to relay", g.Terms.ID, mode)
		}
		// The grant lock is held across every call, so a hung claude must not
		// wedge `orch grant revoke`.
		res, err := runClaude(ctx, r, dir, RelayArgs(g))
		if err != nil {
			return fmt.Errorf("start successor session: %w (a session may be running; check `%s agents`; holder unchanged)", err, ClaudeBinary)
		}
		if res.ExitCode != 0 {
			return fmt.Errorf("start successor session: %s exited %d: %s", ClaudeBinary, res.ExitCode, strings.TrimSpace(res.Stderr+" "+res.Stdout))
		}
		m := backgrounded.FindStringSubmatch(res.Stdout)
		if m == nil {
			return fmt.Errorf("start successor session: %s did not report a backgrounded session id; a session may be running, check `%s agents` (output: %q); holder unchanged", ClaudeBinary, ClaudeBinary, strings.TrimSpace(res.Stdout))
		}
		agents, err := listAgents(ctx, r, dir)
		var next string
		if err == nil {
			next, err = backgroundSession(agents, m[1])
		}
		if err != nil {
			return fmt.Errorf("identify successor session %s: %w; it may be running, `%s stop %s` removes it; holder unchanged", m[1], err, ClaudeBinary, m[1])
		}
		kind = kindOf(agents, holder)
		g.Relays = append(g.Relays, Relay{From: g.SessionID, To: next, At: now})
		g.SessionID = next
		return nil
	})
	if err == nil && g == nil {
		return nil, KindUnknown, ErrNoActiveGrant
	}
	return g, kind, err
}

// runClaude runs one bounded `claude` call.
func runClaude(ctx context.Context, r execx.Runner, dir string, args []string) (execx.Result, error) {
	ctx, cancel := context.WithTimeout(ctx, relayTimeout)
	defer cancel()
	return r.Run(ctx, execx.Cmd{Name: ClaudeBinary, Args: args, Dir: dir})
}

// listAgents runs `claude agents --json` and decodes the sessions it lists.
func listAgents(ctx context.Context, r execx.Runner, dir string) ([]agent, error) {
	res, err := runClaude(ctx, r, dir, agentsArgs)
	if err != nil {
		return nil, fmt.Errorf("%s %s: %w", ClaudeBinary, strings.Join(agentsArgs, " "), err)
	}
	if res.ExitCode != 0 {
		return nil, fmt.Errorf("%s %s exited %d: %s", ClaudeBinary, strings.Join(agentsArgs, " "), res.ExitCode, strings.TrimSpace(res.Stderr+" "+res.Stdout))
	}
	var agents []agent
	if err := json.Unmarshal([]byte(res.Stdout), &agents); err != nil {
		return nil, fmt.Errorf("decode %s %s: %w", ClaudeBinary, strings.Join(agentsArgs, " "), err)
	}
	return agents, nil
}

// backgroundSession returns the full session id of the one background
// session agents lists under short.
func backgroundSession(agents []agent, short string) (string, error) {
	var found []string
	for _, a := range agents {
		if a.ID == short && a.Kind == "background" {
			found = append(found, a.SessionID)
		}
	}
	if len(found) != 1 {
		return "", fmt.Errorf("%s %s lists %d background sessions with id %s, want exactly 1", ClaudeBinary, strings.Join(agentsArgs, " "), len(found), short)
	}
	// An empty holder could never relay or approve again, so refuse it.
	if found[0] == "" {
		return "", fmt.Errorf("%s %s lists background session %s without a sessionId", ClaudeBinary, strings.Join(agentsArgs, " "), short)
	}
	return found[0], nil
}

// stopTimeout bounds the whole of StopPredecessor, which runs inside a
// session-start hook that must not hang.
var stopTimeout = 30 * time.Second

// StopPredecessor ends the session a relay handed the grant over from, which
// cannot stop itself. It stops only a session `claude agents --json` lists as
// a background session, with `claude stop`, which keeps the conversation for
// audits; it never runs `claude rm`. It reports the kind it found and, for a
// background session, the short id it stopped (or failed to stop). An
// interactive or unlisted session is left alone: only the user can end an
// interactive one.
func StopPredecessor(ctx context.Context, r execx.Runner, dir, from string) (kind HolderKind, short string, err error) {
	ctx, cancel := context.WithTimeout(ctx, stopTimeout)
	defer cancel()
	agents, err := listAgents(ctx, r, dir)
	if err != nil {
		return KindUnknown, "", err
	}
	for _, a := range agents {
		if a.SessionID != from || a.Kind != string(KindBackground) {
			continue
		}
		if a.ID == "" {
			return KindBackground, "", fmt.Errorf("%s %s lists background session %s without an id", ClaudeBinary, strings.Join(agentsArgs, " "), from)
		}
		args := []string{"stop", a.ID}
		res, err := runClaude(ctx, r, dir, args)
		if err != nil {
			return KindBackground, a.ID, fmt.Errorf("%s %s: %w", ClaudeBinary, strings.Join(args, " "), err)
		}
		if res.ExitCode != 0 {
			return KindBackground, a.ID, fmt.Errorf("%s %s exited %d: %s", ClaudeBinary, strings.Join(args, " "), res.ExitCode, strings.TrimSpace(res.Stderr+" "+res.Stdout))
		}
		return KindBackground, a.ID, nil
	}
	return kindOf(agents, from), "", nil
}
