package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/kninetimmy/orch/internal/claudeusage"
	"github.com/kninetimmy/orch/internal/execx"
	"github.com/kninetimmy/orch/internal/grant"
	"github.com/kninetimmy/orch/internal/lockfile"
	"github.com/kninetimmy/orch/internal/metrics"
)

const grantHolder = "session-holder"

// grantRepo is a temp git repo with an Orch config, optionally holding an
// active grant whose holder session is grantHolder and whose context
// threshold is 100 tokens.
func grantRepo(t *testing.T, withGrant bool) Env {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skipf("git not on PATH: %v", err)
	}
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if res, err := (execx.Local{}).Run(context.Background(), execx.Cmd{Name: "git", Args: []string{"init", "-b", "main"}, Dir: root}); err != nil || res.ExitCode != 0 {
		t.Fatalf("git init: %v %s", err, res.Stderr)
	}
	writeConfig(t, root, validTOML)
	env := Env{RepoRoot: root, Runner: execx.Local{}}
	if !withGrant {
		return env
	}
	now := time.Now()
	threshold := 100
	pv, err := grant.MakePreview(grant.Proposal{
		SchemaVersion: grant.SchemaVersion, Scope: []grant.ScopeItem{{Name: "n", Description: "d"}}, Gates: []string{"merge"},
		ExpiresAt: now.Add(3 * time.Hour), RunLimit: 2, MergeLimit: 3, ContextThreshold: &threshold, RelayPermissionMode: "auto",
	}, grantHolder, now)
	if err != nil {
		t.Fatal(err)
	}
	store, err := grant.Open(context.Background(), env.Runner, root)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.Create(&grant.CreateRequest{SchemaVersion: grant.SchemaVersion, Terms: pv.Terms, Approval: grant.Approval{
		GrantDigest: pv.Digest, ApprovedBy: "tester", ApprovedAt: now, Statement: grant.ApprovalStatement,
	}}, grantHolder, now); err != nil {
		t.Fatal(err)
	}
	return env
}

func runHookVerb(env Env, stdin string, verb string) (int, string, string) {
	var out, errOut bytes.Buffer
	env.Stdout, env.Stderr, env.Stdin = &out, &errOut, strings.NewReader(stdin)
	code := Run([]string{"hook", "claude", verb}, env)
	return code, out.String(), errOut.String()
}

func hookDoc(fields map[string]string) string {
	data, _ := json.Marshal(fields)
	return string(data)
}

func TestHookSessionStartGrantContext(t *testing.T) {
	env := grantRepo(t, true)
	_, base, _ := runHookVerb(grantRepo(t, false), "", "session-start")

	_, out, _ := runHookVerb(env, hookDoc(map[string]string{"session_id": grantHolder}), "session-start")
	if !strings.HasPrefix(out, base) {
		t.Fatalf("grant output does not extend the grant-free output:\n%s", out)
	}
	for _, want := range []string{"grant-", "expiring", "2 of 2 runs", "3 of 3 merges", "is the current holder", "read the latest handoff session note", "100 tokens"} {
		if !strings.Contains(out, want) {
			t.Errorf("holder output missing %q:\n%s", want, out)
		}
	}

	_, out, _ = runHookVerb(env, hookDoc(map[string]string{"session_id": "someone-else"}), "session-start")
	if !strings.Contains(out, "is not the current holder") || strings.Contains(out, "handoff") {
		t.Errorf("non-holder output wrong:\n%s", out)
	}
	for _, stdin := range []string{"", "not json", "{}"} {
		_, out, _ = runHookVerb(env, stdin, "session-start")
		if !strings.Contains(out, "could not be determined") || strings.Contains(out, "handoff") {
			t.Errorf("stdin %q: undetermined holder output wrong:\n%s", stdin, out)
		}
	}
}

// claudeVersionRunner answers `claude --version` and runs everything else
// (the grant store's git) for real.
type claudeVersionRunner struct {
	execx.Runner
	stdout string
	err    error
}

func (r claudeVersionRunner) Run(ctx context.Context, c execx.Cmd) (execx.Result, error) {
	if c.Name == "claude" {
		return execx.Result{Stdout: r.stdout}, r.err
	}
	return r.Runner.Run(ctx, c)
}

func TestHookSessionStartReportsUnrecognizedClaudeVersionToHolder(t *testing.T) {
	env := grantRepo(t, true)
	real := env.Runner
	env.LookPath = func(name string) (string, error) { return "/fake/" + name, nil }
	holder := hookDoc(map[string]string{"session_id": grantHolder})
	other := hookDoc(map[string]string{"session_id": "someone-else"})
	const wontFire = "the context-relay instruction will not fire in this session"

	env.Runner = claudeVersionRunner{Runner: real, stdout: "2.1.301 (Claude Code)\n"}
	if _, out, _ := runHookVerb(env, holder, "session-start"); strings.Contains(out, "Warning") || strings.Contains(out, wontFire) {
		t.Errorf("recognized version added text:\n%s", out)
	}

	env.Runner = claudeVersionRunner{Runner: real, stdout: "9.9.9 (Claude Code)\n"}
	_, out, _ := runHookVerb(env, holder, "session-start")
	if !strings.Contains(out, "version 9.9.9 is not one the transcript reader recognizes, so "+wontFire) {
		t.Errorf("holder output missing unrecognized-version statement:\n%s", out)
	}
	if _, out, _ := runHookVerb(env, other, "session-start"); strings.Contains(out, wontFire) {
		t.Errorf("non-holder got the statement:\n%s", out)
	}
	if _, out, _ := runHookVerb(env, "", "session-start"); strings.Contains(out, wontFire) {
		t.Errorf("undetermined session got the statement:\n%s", out)
	}

	env.Runner = claudeVersionRunner{Runner: real, err: errors.New("claude not found on PATH")}
	if _, out, _ := runHookVerb(env, holder, "session-start"); !strings.Contains(out, "could not be determined (claude not found on PATH), so "+wontFire) {
		t.Errorf("holder output missing undetermined-version statement:\n%s", out)
	}

	// With no grant the session-start output is unchanged, version or not.
	noGrant := grantRepo(t, false)
	_, want, _ := runHookVerb(noGrant, holder, "session-start")
	noGrant.Runner = claudeVersionRunner{Runner: noGrant.Runner, stdout: "9.9.9 (Claude Code)\n"}
	if _, got, _ := runHookVerb(noGrant, holder, "session-start"); got != want {
		t.Errorf("no-grant output changed:\n%s\nvs\n%s", got, want)
	}
}

func TestHookSessionStartNoGrantIgnoresStdin(t *testing.T) {
	env := grantRepo(t, false)
	_, want, _ := runHookVerb(env, "", "session-start")
	_, got, _ := runHookVerb(env, hookDoc(map[string]string{"session_id": grantHolder}), "session-start")
	if got != want || strings.Contains(got, "grant") {
		t.Errorf("no-grant output changed:\n%s\nvs\n%s", got, want)
	}
}

func writeTranscript(t *testing.T, tokens int) string {
	t.Helper()
	line := `{"type":"assistant","isSidechain":false,"version":"2.1.295","message":{"usage":{"input_tokens":` + strconv.Itoa(tokens) +
		`,"cache_creation_input_tokens":0,"cache_read_input_tokens":0,"output_tokens":9999}}}` + "\n"
	path := filepath.Join(t.TempDir(), "t.jsonl")
	if err := os.WriteFile(path, []byte(line), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestHookContextCheck(t *testing.T) {
	env := grantRepo(t, true)
	over, under := writeTranscript(t, 101), writeTranscript(t, 100)
	doc := func(event, session, transcript string, extra ...string) string {
		f := map[string]string{"hook_event_name": event, "session_id": session, "transcript_path": transcript}
		for i := 0; i+1 < len(extra); i += 2 {
			f[extra[i]] = extra[i+1]
		}
		return hookDoc(f)
	}

	for _, event := range []string{"PostToolUse", "UserPromptSubmit"} {
		code, out, stderr := runHookVerb(env, doc(event, grantHolder, over), "context-check")
		if code != ExitOK || stderr != "" {
			t.Fatalf("%s: exit %d stderr %q", event, code, stderr)
		}
		var got struct {
			Specific struct {
				Event   string `json:"hookEventName"`
				Context string `json:"additionalContext"`
			} `json:"hookSpecificOutput"`
		}
		if err := json.Unmarshal([]byte(out), &got); err != nil {
			t.Fatalf("%s: output %q: %v", event, out, err)
		}
		if got.Specific.Event != event || !strings.Contains(got.Specific.Context, "Relay at the next stopping point") || !strings.Contains(got.Specific.Context, "101 tokens") {
			t.Errorf("%s: output %q", event, out)
		}
	}

	silent := map[string]string{
		"below threshold": doc("PostToolUse", grantHolder, under),
		"not the holder":  doc("PostToolUse", "other", over),
		"inside subagent": doc("PostToolUse", grantHolder, over, "agent_id", "a1"),
		"other event":     doc("Stop", grantHolder, over),
		"no transcript":   doc("PostToolUse", grantHolder, filepath.Join(t.TempDir(), "missing")),
		"not json":        "oops",
		"empty":           "",
	}
	for name, stdin := range silent {
		if code, out, _ := runHookVerb(env, stdin, "context-check"); code != ExitOK || out != "" {
			t.Errorf("%s: exit %d output %q, want silent", name, code, out)
		}
	}
	if code, out, _ := runHookVerb(grantRepo(t, false), doc("PostToolUse", grantHolder, over), "context-check"); code != ExitOK || out != "" {
		t.Errorf("no grant: exit %d output %q, want silent", code, out)
	}
}

func TestHookPreCompact(t *testing.T) {
	env := grantRepo(t, true)
	code, out, _ := runHookVerb(env, hookDoc(map[string]string{"session_id": grantHolder, "trigger": "auto"}), "pre-compact")
	var got map[string]string
	if err := json.Unmarshal([]byte(out), &got); code != ExitOK || err != nil || got["decision"] != "block" || got["reason"] == "" {
		t.Errorf("holder auto: exit %d output %q err %v", code, out, err)
	}
	silent := map[string]string{
		"manual":       hookDoc(map[string]string{"session_id": grantHolder, "trigger": "manual"}),
		"not holder":   hookDoc(map[string]string{"session_id": "other", "trigger": "auto"}),
		"no session":   hookDoc(map[string]string{"trigger": "auto"}),
		"unreadable":   "oops",
		"empty stdin":  "",
		"unknown mode": hookDoc(map[string]string{"session_id": grantHolder, "trigger": "x"}),
	}
	for name, stdin := range silent {
		if code, out, _ := runHookVerb(env, stdin, "pre-compact"); code != ExitOK || out != "" {
			t.Errorf("%s: exit %d output %q, want silent", name, code, out)
		}
	}
	if code, out, _ := runHookVerb(grantRepo(t, false), hookDoc(map[string]string{"session_id": grantHolder, "trigger": "auto"}), "pre-compact"); code != ExitOK || out != "" {
		t.Errorf("no grant: exit %d output %q, want silent", code, out)
	}
	// Outside any git repository the hook still fails open.
	if code, out, _ := runHookVerb(Env{RepoRoot: t.TempDir(), Runner: execx.Local{}}, hookDoc(map[string]string{"session_id": grantHolder, "trigger": "auto"}), "pre-compact"); code != ExitOK || out != "" {
		t.Errorf("no repo: exit %d output %q, want silent", code, out)
	}
}

func TestHookPreCompactFromSubdirectory(t *testing.T) {
	env := grantRepo(t, true)
	sub := filepath.Join(env.RepoRoot, "a", "b")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	env.RepoRoot = sub
	_, out, _ := runHookVerb(env, hookDoc(map[string]string{"session_id": grantHolder, "trigger": "auto"}), "pre-compact")
	if !strings.Contains(out, `"decision": "block"`) {
		t.Errorf("holder started in a subdirectory was not blocked: %q", out)
	}
	_, out, _ = runHookVerb(env, hookDoc(map[string]string{"session_id": grantHolder}), "session-start")
	if !strings.Contains(out, "is the current holder") {
		t.Errorf("session-start from a subdirectory lost the grant lines: %q", out)
	}
}

func TestHookSessionStartRecordsClaudeSessionOnlyWithMetrics(t *testing.T) {
	const session = "aaaaaaaa-0000-4000-8000-000000000001"
	transcript := filepath.Join(t.TempDir(), session+".jsonl")
	doc := hookDoc(map[string]string{"session_id": session, "transcript_path": transcript, "source": "startup"})
	sessions := func(root string) string { return filepath.Join(root, filepath.FromSlash(claudeusage.SessionsDir)) }

	disabled := grantRepo(t, false)
	code, want, _ := runHookVerb(disabled, "", "session-start")
	if c, got, _ := runHookVerb(disabled, doc, "session-start"); c != code || got != want {
		t.Errorf("disabled metrics: output changed:\n%s\nvs\n%s", got, want)
	}
	if _, err := os.Stat(filepath.Join(disabled.RepoRoot, filepath.FromSlash(metrics.Dir))); !os.IsNotExist(err) {
		t.Errorf("disabled metrics wrote storage (stat err %v)", err)
	}

	env := grantRepo(t, false)
	enableMetrics(t, env.RepoRoot)
	code, want, _ = runHookVerb(env, "", "session-start")
	// Bad documents and identities record nothing and print the same.
	for _, stdin := range []string{"not json", "{}", hookDoc(map[string]string{"session_id": "../" + session, "transcript_path": transcript}),
		hookDoc(map[string]string{"session_id": session, "transcript_path": "relative.jsonl"})} {
		if c, got, _ := runHookVerb(env, stdin, "session-start"); c != code || got != want {
			t.Errorf("stdin %q: output changed:\n%s\nvs\n%s", stdin, got, want)
		}
	}
	if _, err := os.Stat(sessions(env.RepoRoot)); !os.IsNotExist(err) {
		t.Errorf("an invalid document was recorded (stat err %v)", err)
	}

	// A run verb holding the Delivery mutation lock does not hold the hook up.
	lock, err := lockfile.AcquireMutation(env.RepoRoot)
	if err != nil {
		t.Fatal(err)
	}
	c, got, _ := runHookVerb(env, doc, "session-start")
	if err := lock.Release(); err != nil {
		t.Fatal(err)
	}
	if c != code || got != want {
		t.Errorf("recording changed the output:\n%s\nvs\n%s", got, want)
	}
	data, err := os.ReadFile(filepath.Join(sessions(env.RepoRoot), session+".json"))
	if err != nil || !strings.Contains(string(data), `"transcript_path":`+strconv.Quote(transcript)) {
		t.Errorf("record = %s (%v)", data, err)
	}
}

func TestHookSessionStartWithMetricsStillReadsTheGrantHolder(t *testing.T) {
	env := grantRepo(t, true)
	enableMetrics(t, env.RepoRoot)
	_, out, _ := runHookVerb(env, hookDoc(map[string]string{"session_id": grantHolder}), "session-start")
	if !strings.Contains(out, "is the current holder") {
		t.Errorf("the one stdin read did not reach the grant check:\n%s", out)
	}
}

// enableMetrics rewrites a repository's validTOML config with metrics enabled.
func enableMetrics(t *testing.T, root string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(root, ".orchestrator", "config.toml"), []byte(validTOML+"\n[metrics]\nenabled = true\n"), 0o644); err != nil {
		t.Fatal(err)
	}
}
