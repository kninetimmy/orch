package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

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
		g, err := store.Relay(context.Background(), env.Runner, env.RepoRoot, os.Getenv(grant.SessionEnv), now)
		if err != nil {
			return err
		}
		_, err = fmt.Fprintf(env.Stdout, "Relayed autonomy grant %s to session %s.\n", g.Terms.ID, g.SessionID)
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
