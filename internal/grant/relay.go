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

// Relay starts a successor session for the active grant on behalf of holder
// and records it as the current holder. The holder check, the launch and the
// write share one lock, so two relays cannot both succeed. If the launch
// fails the record is unchanged.
//
// `claude --bg` prints only a short id for the session it started, and
// Claude Code 2.1.301 ignores a requested --session-id. The successor's full
// id is therefore read from the one background entry `claude agents --json`
// lists under that short id; anything else fails the relay. A failure after
// the launch can leave a started session behind; the error names it.
func (s *Store) Relay(ctx context.Context, r execx.Runner, dir, holder string, now time.Time) (*Grant, error) {
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
		next, err := backgroundSession(ctx, r, dir, m[1])
		if err != nil {
			return fmt.Errorf("identify successor session %s: %w; it may be running, `%s stop %s` removes it; holder unchanged", m[1], err, ClaudeBinary, m[1])
		}
		g.Relays = append(g.Relays, Relay{From: g.SessionID, To: next, At: now})
		g.SessionID = next
		return nil
	})
	if err == nil && g == nil {
		return nil, ErrNoActiveGrant
	}
	return g, err
}

// runClaude runs one bounded `claude` call.
func runClaude(ctx context.Context, r execx.Runner, dir string, args []string) (execx.Result, error) {
	ctx, cancel := context.WithTimeout(ctx, relayTimeout)
	defer cancel()
	return r.Run(ctx, execx.Cmd{Name: ClaudeBinary, Args: args, Dir: dir})
}

// backgroundSession returns the full session id of the one background
// session `claude agents --json` lists under short.
func backgroundSession(ctx context.Context, r execx.Runner, dir, short string) (string, error) {
	res, err := runClaude(ctx, r, dir, agentsArgs)
	if err != nil {
		return "", fmt.Errorf("%s %s: %w", ClaudeBinary, strings.Join(agentsArgs, " "), err)
	}
	if res.ExitCode != 0 {
		return "", fmt.Errorf("%s %s exited %d: %s", ClaudeBinary, strings.Join(agentsArgs, " "), res.ExitCode, strings.TrimSpace(res.Stderr+" "+res.Stdout))
	}
	var agents []agent
	if err := json.Unmarshal([]byte(res.Stdout), &agents); err != nil {
		return "", fmt.Errorf("decode %s %s: %w", ClaudeBinary, strings.Join(agentsArgs, " "), err)
	}
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
