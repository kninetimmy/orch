package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"strconv"

	"github.com/kninetimmy/orch/internal/evalplan"
)

func runEval(env Env, args []string) error {
	if len(args) == 0 {
		return usageError("usage: orch eval preview|run|status|stop|report|grade (orch eval help for arguments)")
	}
	switch args[0] {
	case "preview":
		return runEvalPreview(env, args[1:])
	case "run", "status", "stop", "report", "grade":
		return runEvalController(env, args[0], args[1:])
	case "help":
		if len(args) != 1 {
			return usageError("usage: orch eval help")
		}
		_, err := fmt.Fprintln(env.Stdout, evalUsage)
		return err
	default:
		return usageError(fmt.Sprintf("orch eval: unsupported verb %q; orch eval help lists supported arguments", args[0]))
	}
}

const evalUsage = `usage:
  orch eval preview --plan FILE [--json]
  orch eval run --plan sha256:DIGEST --storage-root ROOT [--approval FILE] [--json]
  orch eval status --run ID --storage-root ROOT [--json]
  orch eval stop --run ID --storage-root ROOT [--json]
  orch eval report --run ID --storage-root ROOT --format text|markdown|json
  orch eval grade --run ID --storage-root ROOT --unit N --attempt N --submission FILE [--json]
Grade retains bounded evaluator assertions/evidence; it executes no supplied code or commands.
Corrections, disputes and rubric validation are append-only submissions, never execution approval.
Run returns the exact frozen scope before requiring a single-use human assertion:
schema_version=1, plan_digest, approved_by, approved_at (preceding 24h), statement=approve-evaluation.
JSON run output is a scope document followed by the retained snapshot when execution is reached.
Production execution remains refused; approval cannot establish native/worker isolation.
Exit 0: successful inspection/publication/request; 1: operational/refusal/approval error; 2: arguments.
Stop receipt is distinct from controller acknowledgement. No takeover, replay or durable resume.`

func runEvalPreview(env Env, args []string) error {
	const usage = "usage: orch eval preview --plan FILE [--json]"
	var file string
	jsonOutput := false
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--plan":
			if file != "" || i+1 == len(args) || args[i+1] == "" || args[i+1][0] == '-' {
				return usageError(usage)
			}
			i++
			file = args[i]
		case "--json":
			if jsonOutput {
				return usageError(usage)
			}
			jsonOutput = true
		default:
			return usageError(fmt.Sprintf("orch eval preview: unexpected argument %q; %s", args[i], usage))
		}
	}
	if file == "" {
		return usageError(usage)
	}
	record, err := evalplan.Preview(context.Background(), env.RepoRoot, file, env.Runner)
	if err != nil {
		return err
	}
	if jsonOutput {
		encoder := json.NewEncoder(env.Stdout)
		encoder.SetIndent("", "  ")
		return encoder.Encode(record)
	}
	return evalplan.WriteText(env.Stdout, record)
}

func runEvalController(env Env, verb string, args []string) error {
	flags := map[string]string{}
	jsonOutput := false
	allowed := map[string]bool{"--storage-root": true}
	if verb == "run" {
		allowed["--plan"], allowed["--approval"] = true, true
	} else {
		allowed["--run"] = true
	}
	if verb == "report" {
		allowed["--format"] = true
	}
	if verb == "grade" {
		allowed["--unit"], allowed["--attempt"], allowed["--submission"] = true, true, true
	}
	bad := func() error {
		return usageError("orch eval " + verb + ": require strict explicit arguments; " + evalUsage)
	}
	for i := 0; i < len(args); i++ {
		if args[i] == "--json" && verb != "report" {
			if jsonOutput {
				return bad()
			}
			jsonOutput = true
			continue
		}
		flag := args[i]
		if !allowed[flag] || flags[flag] != "" || i+1 == len(args) || args[i+1] == "" || args[i+1][0] == '-' {
			return bad()
		}
		i++
		flags[flag] = args[i]
	}
	root, id := flags["--storage-root"], flags["--run"]
	if root == "" || verb == "run" && flags["--plan"] == "" || verb != "run" && id == "" {
		return bad()
	}
	if verb == "grade" {
		unit, unitErr := strconv.Atoi(flags["--unit"])
		attempt, attemptErr := strconv.Atoi(flags["--attempt"])
		if unitErr != nil || attemptErr != nil || unit < 1 || attempt < 1 ||
			strconv.Itoa(unit) != flags["--unit"] || strconv.Itoa(attempt) != flags["--attempt"] || flags["--submission"] == "" {
			return bad()
		}
		if _, err := evalplan.SubmitGrade(env.RepoRoot, root, id, unit, attempt, flags["--submission"]); err != nil {
			return err
		}
	}
	format := "text"
	if jsonOutput {
		format = "json"
	}
	if verb == "report" {
		format = flags["--format"]
		if format != "text" && format != "markdown" && format != "json" {
			return bad()
		}
	}
	if verb == "run" {
		ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt)
		defer cancel()
		record, err := evalplan.Load(ctx, env.RepoRoot, root, flags["--plan"])
		if err != nil {
			return err
		}
		encoder := json.NewEncoder(env.Stdout)
		encoder.SetIndent("", "  ")
		if !jsonOutput {
			if _, err := fmt.Fprintln(env.Stdout, "Frozen evaluation approval scope (preview is not approval):"); err != nil {
				return err
			}
		}
		if err := encoder.Encode(evalplan.Scope(record)); err != nil {
			return err
		}
		if flags["--approval"] == "" {
			return fmt.Errorf("explicit human approval is required; retain the displayed scope and supply --approval FILE with statement %q", evalplan.ApprovalStatement)
		}
		a, err := evalplan.ReadApproval(env.RepoRoot, flags["--approval"])
		if err != nil {
			return err
		}
		e, err := evalplan.PrepareApproved(ctx, env.RepoRoot, root, record.PlanDigest, *a)
		if err != nil {
			return err
		}
		p, runErr := evalplan.Run(ctx, root, e.ID, Version)
		r, inspectErr := evalplan.Inspect(root, e.ID)
		if inspectErr != nil {
			return errors.Join(runErr, inspectErr)
		}
		if err := evalplan.RenderReport(env.Stdout, r, format); err != nil {
			return errors.Join(runErr, err)
		}
		if runErr != nil {
			return runErr
		}
		if p == nil || p.State != "completed" {
			return fmt.Errorf("evaluation %s: %s; retained blockers and evidence require separately approved prerequisites", e.ID, r.Snapshot.Progress.State)
		}
		return nil
	}
	if verb == "stop" {
		if err := evalplan.Stop(root, id); err != nil {
			return err
		}
	}
	var r *evalplan.Report
	var err error
	if verb == "report" {
		r, err = evalplan.RetainReport(root, id)
	} else {
		r, err = evalplan.Inspect(root, id)
	}
	if err != nil {
		return err
	}
	return evalplan.RenderReport(env.Stdout, r, format)
}
