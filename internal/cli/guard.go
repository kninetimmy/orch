package cli

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"

	"github.com/kninetimmy/orch/internal/guard"
	"github.com/kninetimmy/orch/internal/manifest"
)

// guardUsage is the one-line usage for the adapter plumbing surface,
// mirroring runUsage.
const guardUsage = "orch guard: usage: orch guard check [--role <role>] [--issue <n>] [--] <path>... | orch guard claude [--role <role>] [--issue <n>] (PreToolUse JSON on stdin)"

// runGuard dispatches the pre-write enforcement verbs (PRD §23): `orch
// guard check` (host-neutral argv) and `orch guard claude` (Claude Code
// PreToolUse JSON on stdin). Host adapters call it from their own
// PreToolUse hooks before every agent write; it is never invoked by a
// human directly.
func runGuard(env Env, args []string) error {
	if len(args) == 0 {
		return usageError(guardUsage)
	}
	switch args[0] {
	case "check":
		return guardCheck(env, args[1:])
	case "claude":
		return guardClaude(env, args[1:])
	default:
		return usageError(guardUsage)
	}
}

// guardCheck answers a host-neutral argv invocation. It never reads
// stdin (the same console-hang concern as `run status --json`). Exit 0
// allows silently; a policy denial or an operational failure is returned
// as an error so Run exits 1 with a one-line reason on stderr; a usage
// mistake returns usageError for exit 2.
func guardCheck(env Env, args []string) error {
	role, issue, paths, err := parseGuardFlags(args)
	if err != nil {
		return err
	}
	if len(paths) == 0 {
		return usageError(guardUsage)
	}
	v, err := guard.NewChecker(env.Runner).Check(context.Background(), guard.Request{
		Paths: paths,
		Role:  role,
		Issue: issue,
	})
	if err != nil {
		return err // operational failure → exit 1, one-line reason
	}
	if v.Allow {
		return nil // exit 0, silent
	}
	return fmt.Errorf("%s: %s", v.Path, v.Reason) // exit 1, names path + reason
}

// guardClaude answers a Claude Code PreToolUse event read from stdin. It
// never emits a permissionDecision of "allow" — an allow is silence, so
// guard never bypasses the user's own permission prompts. A denial exits
// 0 with the hook's deny document on stdout. Any internal failure exits
// 2 (the hook protocol's blocking code); the verb never exits 1.
func guardClaude(env Env, args []string) error {
	role, issue, rest, err := parseGuardFlags(args)
	if err != nil {
		return err
	}
	if len(rest) != 0 {
		return usageError(guardUsage)
	}

	payload, err := io.ReadAll(env.Stdin)
	if err != nil {
		return exitCodeError{code: ExitUsage, err: fmt.Errorf("read stdin: %w", err)}
	}

	targets, err := guard.PathsFromClaudeEvent(payload)
	if err != nil {
		if errors.Is(err, guard.ErrUnknownTool) {
			return emitClaudeDeny(env.Stdout, err.Error())
		}
		return exitCodeError{code: ExitUsage, err: err}
	}

	v, err := guard.NewChecker(env.Runner).Check(context.Background(), guard.Request{
		Paths: targets,
		Role:  role,
		Issue: issue,
	})
	if err != nil {
		return exitCodeError{code: ExitUsage, err: err} // operational → blocking exit 2
	}
	if v.Allow {
		return nil // exit 0, no output, no opinion
	}
	return emitClaudeDeny(env.Stdout, v.Reason)
}

// hookOutput is the PreToolUse response guard writes on a denial. Its
// shape is fixed by the Claude Code hook protocol.
type hookOutput struct {
	HookSpecificOutput hookSpecificOutput `json:"hookSpecificOutput"`
}

type hookSpecificOutput struct {
	HookEventName            string `json:"hookEventName"`
	PermissionDecision       string `json:"permissionDecision"`
	PermissionDecisionReason string `json:"permissionDecisionReason"`
}

// emitClaudeDeny writes the deny document to stdout and returns nil so
// the verb exits 0 (the denial is carried by the document, not the exit
// code). A stdout write failure is an internal failure → blocking exit 2.
func emitClaudeDeny(w io.Writer, reason string) error {
	data, err := json.Marshal(hookOutput{HookSpecificOutput: hookSpecificOutput{
		HookEventName:            "PreToolUse",
		PermissionDecision:       "deny",
		PermissionDecisionReason: reason,
	}})
	if err != nil {
		return exitCodeError{code: ExitUsage, err: fmt.Errorf("encode deny document: %w", err)}
	}
	if _, err := fmt.Fprintf(w, "%s\n", data); err != nil {
		return exitCodeError{code: ExitUsage, err: fmt.Errorf("write deny document: %w", err)}
	}
	return nil
}

// parseGuardFlags parses the shared --role and --issue narrowing flags
// and returns the remaining positional arguments. Parse errors, an
// unknown role, or a negative issue map to a usage error. The FlagSet
// discards its own output so the dispatcher owns all user-facing text.
func parseGuardFlags(args []string) (manifest.Role, int, []string, error) {
	fs := flag.NewFlagSet("guard", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	var roleStr string
	var issue int
	fs.StringVar(&roleStr, "role", "", "narrowing role assertion")
	fs.IntVar(&issue, "issue", 0, "narrowing issue-number assertion")
	if err := fs.Parse(args); err != nil {
		return "", 0, nil, usageError(guardUsage)
	}
	if issue < 0 {
		return "", 0, nil, usageError(guardUsage)
	}
	var role manifest.Role
	if roleStr != "" {
		role = manifest.Role(roleStr)
		if !validGuardRole(role) {
			return "", 0, nil, usageError(guardUsage)
		}
	}
	return role, issue, fs.Args(), nil
}

// validGuardRole reports whether r is one of the five routed roles guard
// accepts as a narrowing assertion (PRD §13 role set).
func validGuardRole(r manifest.Role) bool {
	switch r {
	case manifest.RoleArchitect, manifest.RoleScout, manifest.RoleImplementer,
		manifest.RoleSpecialist, manifest.RoleReviewer:
		return true
	default:
		return false
	}
}
