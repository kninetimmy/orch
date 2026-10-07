// Package claudenative runs one approved evaluation task through Claude Code in
// non-interactive print mode with streaming JSON input and output. It launches
// only under the fixed containment flags below, verifies the session's reported
// startup state before accepting work, and records session, profile, usage and
// cleanup evidence. It grants no approval and has no Delivery entry point.
package claudenative

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/kninetimmy/orch/internal/manifest"
	"github.com/kninetimmy/orch/internal/metrics"
	"github.com/kninetimmy/orch/internal/nativehost"
)

var (
	// ErrUnavailable refuses an attempt before any output is accepted: a missing
	// flag, a workspace instruction file, or a startup state other than requested.
	ErrUnavailable      = errors.New("claude containment unavailable")
	ErrProfileMismatch  = errors.New("claude reported execution profile mismatch")
	ErrTaskBoundary     = errors.New("claude approved task boundary changed")
	ErrMalformedMessage = errors.New("malformed Claude Code stream message")
	ErrProcessExit      = errors.New("claude code exited before its result")
)

// Task is the shared caller-approved dispatch; see nativehost.Task.
type Task = nativehost.Task

type (
	EvaluationBinding   = nativehost.EvaluationBinding
	InstructionEvidence = nativehost.InstructionEvidence
	SessionCleanup      = nativehost.SessionCleanup
)

// Options identifies the installed host only. It cannot add flags, settings,
// credentials, directories or environment.
type Options struct {
	Executable string // empty resolves claude on PATH
}

const (
	maxLineBytes = 8 << 20
	helpTimeout  = 15 * time.Second
	maxTextBytes = 1 << 20
)

// Vars, not consts, only so the scripted stand-in tests stay fast.
var (
	shutdownTimeout = 2 * time.Second
	initWait        = 3 * time.Second
)

// requiredFlags are checked by capability in the installed --help text, never
// by version number. A missing flag refuses the attempt before launch.
var requiredFlags = []string{"--print", "--verbose", "--output-format", "--input-format", "--session-id", "--resume",
	"--model", "--effort", "--tools", "--allowedTools", "--add-dir", "--permission-mode", "--permission-prompts",
	"--restricted", "--safe-mode", "--strict-mcp-config", "--include-hook-events"}

// forbiddenArgs are never passed; launchArgs output is checked against them.
var forbiddenArgs = []string{"--bare", "--dangerously-skip-permissions", "--allow-dangerously-skip-permissions", "--fallback-model",
	"bypassPermissions", "--no-session-persistence", "--fork-session", "--continue", "--mcp-config", "--settings", "--plugin-dir", "--agents"}

// goCommands is the whole Bash allowlist. Every role gets it.
var goCommands = []string{"Bash(go build)", "Bash(go build *)", "Bash(go test)", "Bash(go test *)", "Bash(go vet)", "Bash(go vet *)", "Bash(gofmt -l *)"}

var (
	modelPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._\[\]-]{0,127}$`)
	efforts      = []string{"low", "medium", "high", "xhigh", "max"}
	// Characters cmd.exe interprets even inside quotes, so an npm .cmd shim cannot
	// receive them safely. Free text never enters argv; it goes over stdin.
	unsafeArg = "\"%^&|<>!\r\n\x00"
)

// roleTools is the exact --tools set the session must report at startup.
func roleTools(role string) []string {
	tools := []string{"Read", "Glob", "Grep", "Bash"}
	if role == "implementer" {
		tools = append(tools, "Edit", "Write")
	}
	return tools
}

func textSHA256(text string) string { return fmt.Sprintf("%x", sha256.Sum256([]byte(text))) }

// claudeHomes are Claude Code's own configuration and credential locations.
// nativehost adds no host home, so this bridge protects its own.
func claudeHomes() []string {
	var homes []string
	for _, name := range []string{"HOME", "USERPROFILE"} {
		if base := os.Getenv(name); base != "" {
			homes = append(homes, filepath.Join(base, ".claude"), filepath.Join(base, ".claude.json"))
		}
	}
	if dir := os.Getenv("CLAUDE_CONFIG_DIR"); dir != "" {
		homes = append(homes, dir)
	}
	return homes
}

// bindTask checks an evaluation task against its binding and the canonical
// layout. Claude sessions exist only for evaluation; plans approve no
// instruction files, so any declared source refuses.
func bindTask(task Task) (Task, nativehost.Boundary, error) {
	b := task.Evaluation
	if b == nil {
		return task, nativehost.Boundary{}, fmt.Errorf("%w: claude sessions run only approved evaluation tasks", ErrTaskBoundary)
	}
	if err := b.Identity.Validate(); err != nil {
		return task, nativehost.Boundary{}, fmt.Errorf("%w: %w", ErrTaskBoundary, err)
	}
	if task.ID != b.TaskID() || task.RunID != "" || task.IssueNumber != 0 || task.Attempt != "" || task.ReviewCycle != 0 ||
		task.Role != b.Identity.Role || task.Selection != b.Selection || textSHA256(task.Prompt) != b.PromptSHA256 ||
		textSHA256(task.Instructions) != b.InstructionsSHA256 || b.InstructionSources == nil || len(b.InstructionSources) != 0 ||
		!utf8.ValidString(task.Prompt) || !utf8.ValidString(task.Instructions) || strings.TrimSpace(task.Prompt) == "" ||
		strings.TrimSpace(task.Instructions) == "" || len(task.Prompt)+len(task.Instructions) > maxTextBytes {
		return task, nativehost.Boundary{}, fmt.Errorf("%w: evaluation identity/profile/public text differs", ErrTaskBoundary)
	}
	if !slices.Contains([]string{"scout", "implementer", "reviewer"}, task.Role) {
		return task, nativehost.Boundary{}, fmt.Errorf("%w: unsupported evaluation role", ErrTaskBoundary)
	}
	s := task.Selection
	if !modelPattern.MatchString(s.Model) || !slices.Contains(efforts, s.Effort) || s.Variant != "" || s.NoVariant {
		return task, nativehost.Boundary{}, fmt.Errorf("%w: claude sessions require an exact model and a supported effort", ErrTaskBoundary)
	}
	boundary, err := nativehost.ValidateLayout(task.Layout, claudeHomes())
	if err != nil {
		return task, boundary, fmt.Errorf("%w: %w", ErrUnavailable, err)
	}
	if b.Workspace != boundary.Workspace || b.Scratch != boundary.Scratch {
		return task, boundary, fmt.Errorf("%w: evaluation packet/scratch differs", ErrTaskBoundary)
	}
	task.Layout.Workspace, task.Layout.Scratch = boundary.Workspace, boundary.Scratch
	return task, boundary, nil
}

// checkWorkspace refuses before launch when the workspace root holds a file
// Claude Code or its built-in plugins may load as instructions or settings.
// Names compare case-insensitively so case-sensitive filesystems cannot alias.
func checkWorkspace(workspace string) error {
	entries, err := os.ReadDir(workspace)
	if err != nil {
		return fmt.Errorf("%w: workspace unreadable", ErrUnavailable)
	}
	for _, entry := range entries {
		for _, name := range []string{"CLAUDE.md", "CLAUDE.local.md", "AGENTS.md", ".claude"} {
			if strings.EqualFold(entry.Name(), name) {
				return fmt.Errorf("%w: workspace root contains %s", ErrUnavailable, entry.Name())
			}
		}
	}
	return nil
}

// launchArgs builds the complete argument vector. It carries no free text:
// prompt and role instructions are sent as the stream-json user message.
func launchArgs(task Task, sessionID string, resume bool) ([]string, error) {
	tools := roleTools(task.Role)
	args := []string{"--print", "--verbose", "--output-format", "stream-json", "--input-format", "stream-json"}
	if resume {
		args = append(args, "--resume", sessionID)
	} else {
		args = append(args, "--session-id", sessionID)
	}
	args = append(args, "--model", task.Selection.Model, "--effort", task.Selection.Effort,
		"--permission-mode", "dontAsk", "--permission-prompts", "none", "--restricted", "--safe-mode", "--strict-mcp-config",
		"--include-hook-events", "--tools", strings.Join(tools, ","), "--add-dir", task.Layout.Scratch, "--allowedTools")
	args = append(args, tools[:3]...) // Read, Glob, Grep; Bash only through the Go allowlist
	args = append(args, tools[4:]...) // Edit, Write for the implementer
	args = append(args, goCommands...)
	for _, arg := range args {
		if strings.ContainsAny(arg, unsafeArg) || slices.Contains(forbiddenArgs, arg) {
			return nil, fmt.Errorf("%w: launch argument cannot be passed safely", ErrUnavailable)
		}
	}
	return args, nil
}

// environment passes only what Claude Code needs to find its own subscription
// login and run Go, dropping API keys, provider overrides and the parent
// session's CLAUDECODE marker. Temporary files go to the attempt scratch.
func environment(environ []string, scratch string, resume bool) []string {
	keep := []string{"SYSTEMROOT", "SYSTEMDRIVE", "WINDIR", "COMSPEC", "PATH", "PATHEXT", "PROGRAMDATA", "PROGRAMFILES",
		"USERPROFILE", "HOME", "LOCALAPPDATA", "APPDATA", "USERNAME", "USER", "LOGNAME", "LANG",
		"XDG_CONFIG_HOME", "XDG_DATA_HOME", "CLAUDE_CONFIG_DIR", "CLAUDE_CODE_GIT_BASH_PATH"}
	var env []string
	for _, entry := range environ {
		name, _, ok := strings.Cut(entry, "=")
		if ok && slices.Contains(keep, strings.ToUpper(name)) {
			env = append(env, entry)
		}
	}
	env = append(env, "TEMP="+scratch, "TMP="+scratch, "TMPDIR="+scratch)
	if resume {
		env = append(env, "CLAUDE_CODE_RESUME_INTERRUPTED_TURN=1")
	}
	return env
}

// checkCapabilities reads the installed --help text and requires every flag
// and the dontAsk permission mode. It sends no prompt and uses no model.
func checkCapabilities(ctx context.Context, executable, scratch string) error {
	ctx, cancel := context.WithTimeout(ctx, helpTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, executable, "--help")
	cmd.Dir, cmd.Env = scratch, environment(os.Environ(), scratch, false)
	var out bytes.Buffer
	cmd.Stdout, cmd.Stderr = &limitedWriter{w: &out, n: 1 << 20}, io.Discard
	cmd.WaitDelay = shutdownTimeout
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("%w: installed Claude Code help unavailable: %w", ErrUnavailable, err)
	}
	help := out.String()
	for _, flag := range append(slices.Clone(requiredFlags), "dontAsk") {
		if !regexp.MustCompile(`(^|[\s,"])` + regexp.QuoteMeta(flag) + `([\s,="]|$)`).MatchString(help) {
			return fmt.Errorf("%w: installed Claude Code lacks required capability %s", ErrUnavailable, flag)
		}
	}
	return nil
}

type limitedWriter struct {
	w io.Writer
	n int
}

func (l *limitedWriter) Write(p []byte) (int, error) {
	if len(p) > l.n {
		return 0, errors.New("output bound exceeded")
	}
	l.n -= len(p)
	return l.w.Write(p)
}

// newSessionID returns a random version-4 UUID for --session-id.
func newSessionID() string {
	var b [16]byte
	_, _ = rand.Read(b[:]) // crypto/rand.Read never fails on supported platforms
	b[6], b[8] = b[6]&0x0f|0x40, b[8]&0x3f|0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}

func metricProfile(s manifest.Selection) metrics.Profile {
	return metrics.Profile{Model: s.Model, Effort: s.Effort}
}
