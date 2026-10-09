package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kninetimmy/orch/internal/execx"
	"github.com/kninetimmy/orch/internal/grant"
)

func TestHelpListsGrantSubcommands(t *testing.T) {
	env, stdout, _ := testEnv(t)
	if code := Run([]string{"help"}, env); code != ExitOK {
		t.Fatalf("exit = %d", code)
	}
	for _, sub := range []string{"grant ", "grant revoke", "grant preview", "grant create"} {
		if !strings.Contains(stdout.String(), sub) {
			t.Errorf("help missing %q", sub)
		}
	}
}

func TestGrantCommands(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skipf("git not on PATH: %v", err)
	}
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	res, err := (execx.Local{}).Run(context.Background(), execx.Cmd{Name: "git", Args: []string{"init", "-b", "main"}, Dir: root})
	if err != nil || res.ExitCode != 0 {
		t.Fatalf("git init: %v %s", err, res.Stderr)
	}
	t.Setenv(grant.SessionEnv, "session-cli")
	run := func(stdin string, args ...string) (int, string, string) {
		var out, errOut bytes.Buffer
		code := Run(args, Env{RepoRoot: root, Stdin: strings.NewReader(stdin), Stdout: &out, Stderr: &errOut, Runner: execx.Local{}})
		return code, out.String(), errOut.String()
	}
	for _, args := range [][]string{{"grant"}, {"grant", "revoke"}} {
		if code, out, _ := run("", args...); code != ExitOK || !strings.Contains(out, "No autonomy grant is active") {
			t.Fatalf("%v with none: %d %q", args, code, out)
		}
	}
	proposal := `{"schema_version":1,"scope":[{"name":"issue-351","description":"Add grants"}],"gates":["merge"],` +
		`"expires_at":"` + time.Now().Add(time.Hour).UTC().Format(time.RFC3339) + `","run_limit":1,"merge_limit":2,"relay_permission_mode":"auto"}`
	code, out, stderr := run(proposal, "grant", "preview")
	if code != ExitOK {
		t.Fatalf("preview: %d %s", code, stderr)
	}
	var pv grant.Preview
	if err := json.Unmarshal([]byte(out), &pv); err != nil {
		t.Fatal(err)
	}
	req, err := json.Marshal(grant.CreateRequest{SchemaVersion: 1, Terms: pv.Terms, Approval: grant.Approval{
		GrantDigest: pv.Digest, ApprovedBy: "kninetimmy", ApprovedAt: time.Now(), Statement: grant.ApprovalStatement,
	}})
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv(grant.SessionEnv, "")
	if code, _, stderr := run(string(req), "grant", "create"); code != ExitError || !strings.Contains(stderr, grant.SessionEnv) {
		t.Fatalf("create without session: %d %s", code, stderr)
	}
	t.Setenv(grant.SessionEnv, "session-cli")
	if code, _, stderr := run(string(req), "grant", "create"); code != ExitOK {
		t.Fatalf("create: %d %s", code, stderr)
	}
	code, out, _ = run("", "grant")
	for _, want := range []string{pv.Terms.ID, "issue-351: Add grants", "covered gates:      merge", "0 of 1 used, 1 remaining", "0 of 2 used, 2 remaining", "450000 tokens", "session-cli", "none recorded"} {
		if code != ExitOK || !strings.Contains(out, want) {
			t.Errorf("show missing %q: %s", want, out)
		}
	}
	store, err := grant.Open(context.Background(), execx.Local{}, root)
	if err != nil {
		t.Fatal(err)
	}
	for _, a := range []grant.RecordedApproval{
		{Gate: "plan", RunID: "run-1"},
		{Gate: "merge", RunID: "run-1", Issue: 7, PR: 9, Head: "abc123"},
	} {
		if _, err := store.RecordApproval(pv.Terms.ID, a, time.Now()); err != nil {
			t.Fatal(err)
		}
	}
	code, out, _ = run("", "grant")
	for _, want := range []string{"1 of 1 used, 0 remaining", "1 of 2 used, 1 remaining", "- plan gate, run run-1, at ", "- merge gate, run run-1, issue #7, PR #9, head abc123, at "} {
		if code != ExitOK || !strings.Contains(out, want) {
			t.Errorf("show missing %q: %s", want, out)
		}
	}
	if code, out, _ := run("", "grant", "revoke"); code != ExitOK || !strings.Contains(out, "Revoked autonomy grant "+pv.Terms.ID) {
		t.Fatalf("revoke: %d %s", code, out)
	}
	if code, out, _ := run("", "grant"); code != ExitOK || !strings.Contains(out, "No autonomy grant is active") {
		t.Fatalf("after revoke: %d %s", code, out)
	}
	if code, _, _ := run("", "grant", "extend"); code != ExitUsage {
		t.Fatalf("unknown verb exit = %d", code)
	}
}
