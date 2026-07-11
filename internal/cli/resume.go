package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/kninetimmy/orch/internal/run"
	"github.com/kninetimmy/orch/internal/state"
)

// resumeUsage is the one-line usage for the human resume command.
const resumeUsage = "orch resume: usage: orch resume [--json] [--dry-run] [--resume-stopped-run]"

// runResume reconciles an interrupted Delivery run against GitHub and
// continues it (PRD §23). Unlike the adapter-plumbing `run` verbs it is a
// human command, so it parses its own flags and renders a human report by
// default.
func runResume(env Env, args []string) error {
	var (
		asJSON  bool
		dryRun  bool
		stopped bool
	)
	for _, a := range args {
		switch a {
		case "--json":
			asJSON = true
		case "--dry-run":
			dryRun = true
		case "--resume-stopped-run":
			stopped = true
		default:
			return usageError(resumeUsage)
		}
	}

	req := run.ResumeRequest{DryRun: dryRun}
	if stopped {
		req.Statement = run.ResumeStoppedRunStatement
	}

	runEnv := run.Env{RepoRoot: env.RepoRoot, Runner: env.Runner, Now: time.Now}
	doc, err := run.Resume(context.Background(), runEnv, req)
	if err != nil {
		return err
	}

	if asJSON {
		data, err := json.MarshalIndent(doc, "", "  ")
		if err != nil {
			return fmt.Errorf("encode resume result: %w", err)
		}
		_, err = fmt.Fprintf(env.Stdout, "%s\n", data)
		return err
	}
	return renderResume(env, doc)
}

// renderResume prints the human report the resume example in PRD §23
// describes: a run header, one line per issue, the stopped-run outcome,
// and any run-level warnings.
func renderResume(env Env, doc *run.ResumeDoc) error {
	if doc.Mode != state.ModeDelivery {
		fmt.Fprintln(env.Stdout, "no delivery run to resume; already in assist")
		return nil
	}

	fmt.Fprintf(env.Stdout, "run:     %s\n", doc.RunID)
	for _, iss := range doc.Issues {
		transition := string(iss.PhaseBefore)
		if iss.PhaseAfter != iss.PhaseBefore {
			transition = fmt.Sprintf("%s → %s", iss.PhaseBefore, iss.PhaseAfter)
		}
		// A planned issue never got a GitHub number; label it by plan ID.
		label := iss.PlanID
		if iss.Number > 0 {
			label = fmt.Sprintf("#%d %s", iss.Number, iss.PlanID)
		}
		fmt.Fprintf(env.Stdout, "%-8s %-24s %s\n", label+":", transition, iss.Reason)
	}

	switch {
	case doc.StoppedReasonBefore == "":
		// Nothing to report about a run that was never stopped.
	case doc.Applied && doc.StoppedReasonAfter == "":
		fmt.Fprintf(env.Stdout, "stopped: cleared (was %q)\n", doc.StoppedReasonBefore)
	default:
		fmt.Fprintf(env.Stdout, "stopped: run is stopped (%s); nothing written — re-run with --resume-stopped-run\n", doc.StoppedReasonBefore)
	}

	if !doc.Applied && doc.StoppedReasonBefore == "" {
		fmt.Fprintln(env.Stdout, "note:    dry run; nothing written")
	}
	for _, w := range doc.Warnings {
		fmt.Fprintf(env.Stdout, "warning: %s\n", w)
	}
	return nil
}
