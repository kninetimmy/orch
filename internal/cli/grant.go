package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/kninetimmy/orch/internal/config"
	"github.com/kninetimmy/orch/internal/grant"
)

const grantUsage = "usage: orch grant [revoke|relay] | orch grant preview|create (JSON document on stdin)"

// runGrant shows (no argument) or revokes the active autonomy grant for a
// human; preview and create are JSON stdin/stdout plumbing for the adapter
// that asks the user to approve a grant.
func runGrant(env Env, args []string) error {
	if len(args) > 1 {
		return usageError(grantUsage)
	}
	verb := ""
	if len(args) == 1 {
		verb = args[0]
	}
	now := time.Now()
	if verb == "preview" || verb == "create" {
		if err := checkGrantProtected(env.RepoRoot); err != nil {
			return err
		}
	}
	switch verb {
	case "preview":
		input, err := io.ReadAll(env.Stdin)
		if err != nil {
			return fmt.Errorf("read stdin: %w", err)
		}
		p, err := grant.DecodeProposal(input)
		if err != nil {
			return err
		}
		preview, err := grant.MakePreview(*p, os.Getenv(grant.SessionEnv), now)
		if err != nil {
			return err
		}
		return writeJSON(env.Stdout, preview)
	case "create":
		input, err := io.ReadAll(env.Stdin)
		if err != nil {
			return fmt.Errorf("read stdin: %w", err)
		}
		req, err := grant.DecodeCreate(input)
		if err != nil {
			return err
		}
		store, err := grant.Open(context.Background(), env.Runner, env.RepoRoot)
		if err != nil {
			return err
		}
		g, err := store.Create(req, os.Getenv(grant.SessionEnv), now)
		if err != nil {
			return err
		}
		return writeJSON(env.Stdout, g)
	case "relay":
		store, err := grant.Open(context.Background(), env.Runner, env.RepoRoot)
		if err != nil {
			return err
		}
		g, kind, err := store.Relay(context.Background(), env.Runner, env.RepoRoot, os.Getenv(grant.SessionEnv), now)
		if err != nil {
			return err
		}
		_, err = fmt.Fprintf(env.Stdout, "Relayed autonomy grant %s to session %s.\n%s\n", g.Terms.ID, g.SessionID, relayedHolderNote(kind))
		return err
	case "", "revoke":
		store, err := grant.Open(context.Background(), env.Runner, env.RepoRoot)
		if err != nil {
			return err
		}
		var g *grant.Grant
		if verb == "" {
			g, err = store.Active(now)
		} else {
			g, err = store.Revoke(now)
		}
		if err != nil {
			return err
		}
		if g == nil {
			_, err = fmt.Fprintln(env.Stdout, "No autonomy grant is active.")
			return err
		}
		if verb == "revoke" {
			_, err = fmt.Fprintf(env.Stdout, "Revoked autonomy grant %s at %s.\n", g.Terms.ID, g.RevokedAt.Format(time.RFC3339))
			return err
		}
		return writeGrant(env.Stdout, g, now)
	default:
		return usageError(fmt.Sprintf("orch grant: unsupported verb %q; %s", verb, grantUsage))
	}
}

// relayedHolderNote tells the relaying session who ends it: the successor
// stops a background holder when it starts, and only the user can end an
// interactive one.
func relayedHolderNote(kind grant.HolderKind) string {
	switch kind {
	case grant.KindBackground:
		return "This session is a background session: its successor stops it with `claude stop` when it starts, so the relay may be your last action."
	case grant.KindInteractive:
		return "This session is an interactive session: Orch cannot stop it. Your last message must tell the user to /exit it."
	}
	return "This session's kind could not be determined, so Orch may not stop it. Your last message must tell the user to check `claude agents` and stop or /exit this session."
}

// checkGrantProtected refuses a grant in a repository whose committed
// configuration has no approved protected list for this binary's survey
// methodology version.
func checkGrantProtected(repoRoot string) error {
	cfg, err := config.Load(repoRoot)
	if err != nil {
		return err
	}
	if err := cfg.CheckGrantProtected(); err != nil {
		return fmt.Errorf("autonomy grant refused: %w", err)
	}
	return nil
}

func writeJSON(w io.Writer, v any) error {
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	_, err = fmt.Fprintf(w, "%s\n", data)
	return err
}

func writeGrant(w io.Writer, g *grant.Grant, now time.Time) error {
	t := g.Terms
	var b strings.Builder
	fmt.Fprintf(&b, "Active autonomy grant %s\n", t.ID)
	fmt.Fprintf(&b, "  approved by:        %s at %s\n", g.ApprovedBy, g.ApprovedAt.Format(time.RFC3339))
	fmt.Fprintf(&b, "  expires:            %s (in %s)\n", t.ExpiresAt.Format(time.RFC3339), t.ExpiresAt.Sub(now).Round(time.Minute))
	fmt.Fprintf(&b, "  covered gates:      %s\n", strings.Join(t.Gates, ", "))
	fmt.Fprintf(&b, "  runs:               %d of %d used, %d remaining\n", len(g.Runs), t.RunLimit, t.RunLimit-len(g.Runs))
	fmt.Fprintf(&b, "  merges:             %d of %d used, %d remaining\n", len(g.Merges), t.MergeLimit, t.MergeLimit-len(g.Merges))
	fmt.Fprintf(&b, "  fix-cycle limit:    %d\n", t.FixCycleLimit)
	fmt.Fprintf(&b, "  context threshold:  %d tokens\n", t.ContextThreshold)
	fmt.Fprintf(&b, "  relay permission:   %s\n", t.RelayPermissionMode)
	fmt.Fprintf(&b, "  session holder:     %s\n", g.SessionID)
	b.WriteString("  relay chain:\n")
	fmt.Fprintf(&b, "    - %s (creator, %s)\n", t.SessionID, g.CreatedAt.Format(time.RFC3339))
	for _, r := range g.Relays {
		fmt.Fprintf(&b, "    - %s (took over %s)\n", r.To, r.At.Format(time.RFC3339))
	}
	b.WriteString("  scope:\n")
	for _, s := range t.Scope {
		fmt.Fprintf(&b, "    - %s: %s\n", s.Name, s.Description)
	}
	if len(g.Approvals) == 0 {
		b.WriteString("  approvals:          none recorded\n")
	} else {
		b.WriteString("  approvals:\n")
		for _, a := range g.Approvals {
			fmt.Fprintf(&b, "    - %s gate, run %s", a.Gate, a.RunID)
			// A plan approval covers a whole run, so it names no issue, PR or head.
			if a.Issue != 0 {
				fmt.Fprintf(&b, ", issue #%d, PR #%d, head %s", a.Issue, a.PR, a.Head)
			}
			fmt.Fprintf(&b, ", at %s\n", a.At.Format(time.RFC3339))
		}
	}
	_, err := io.WriteString(w, b.String())
	return err
}
