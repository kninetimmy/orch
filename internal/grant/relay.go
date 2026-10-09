package grant

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
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

// Relay is one hand-off of a grant's session holder.
type Relay struct {
	From string    `json:"from"`
	To   string    `json:"to"`
	At   time.Time `json:"at"`
}

// RelayArgs returns the exact `claude` argument vector that starts g's
// successor as a background session with Remote Control on. Every element
// comes from this build, the grant record, or the pre-generated session id.
func RelayArgs(g *Grant, sessionID string) []string {
	return []string{
		"--bg",
		"--session-id", sessionID,
		"--remote-control", g.Terms.ID,
		"--permission-mode", g.Terms.RelayPermissionMode,
		fmt.Sprintf(relayPromptFormat, g.Terms.ID),
	}
}

// Relay starts a successor session for the active grant on behalf of holder
// and records it as the current holder. The holder check, the launch and the
// write share one lock, so two relays cannot both succeed. If the launch
// fails the record is unchanged.
//
// The successor's id is chosen here and passed as --session-id, because
// `claude --bg` prints only an 8-character prefix of the id. The launch is
// accepted only when that printed prefix matches, so a CLI that ignores the
// requested id fails the relay instead of recording an id nothing holds. Such
// a failure can leave a started session behind; the error names it.
func (s *Store) Relay(ctx context.Context, r execx.Runner, dir, holder string, now time.Time) (*Grant, error) {
	g, err := s.update(now, "", func(g *Grant) error {
		if holder == "" {
			return fmt.Errorf("%s is absent from the environment; only the session holding grant %s can relay it", SessionEnv, g.Terms.ID)
		}
		if holder != g.SessionID {
			return fmt.Errorf("this session (%s=%q) does not hold grant %s; its current holder is %q", SessionEnv, holder, g.Terms.ID, g.SessionID)
		}
		next, err := newSessionID()
		if err != nil {
			return err
		}
		res, err := r.Run(ctx, execx.Cmd{Name: ClaudeBinary, Args: RelayArgs(g, next), Dir: dir})
		if err != nil {
			return fmt.Errorf("start successor session: %w", err)
		}
		if res.ExitCode != 0 {
			return fmt.Errorf("start successor session: %s exited %d: %s", ClaudeBinary, res.ExitCode, strings.TrimSpace(res.Stderr+" "+res.Stdout))
		}
		if !strings.Contains(res.Stdout, next[:8]) {
			return fmt.Errorf("start successor session: %s did not report session %s; a session may be running, check `%s agents` (output: %q); holder unchanged", ClaudeBinary, next[:8], ClaudeBinary, strings.TrimSpace(res.Stdout))
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

// newSessionID returns a random version 4 UUID.
func newSessionID() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", fmt.Errorf("generate session id: %w", err)
	}
	b[6] = b[6]&0x0f | 0x40
	b[8] = b[8]&0x3f | 0x80
	h := hex.EncodeToString(b[:])
	return h[:8] + "-" + h[8:12] + "-" + h[12:16] + "-" + h[16:20] + "-" + h[20:], nil
}
