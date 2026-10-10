package ghops

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/kninetimmy/orch/internal/execx/execxtest"
)

func TestCreatePR(t *testing.T) {
	root := tempRoot(t)
	g, script := openScripted(t, root, execxtest.Call{
		Name:   "gh",
		Args:   []string{"pr", "create", "--head", "orch/7-add-widget", "--base", "main", "--title", "Add widget", "--body-file", "-"},
		Dir:    root,
		Env:    ghTestEnv,
		Stdin:  "manifest body",
		Stdout: "https://github.com/octo/orch/pull/43\n",
	})
	number, url, err := g.CreatePR(context.Background(), PRSpec{
		Head: "orch/7-add-widget", Base: "main", Title: "Add widget", Body: "manifest body",
	})
	if err != nil {
		t.Fatalf("CreatePR: %v", err)
	}
	script.AssertExhausted()
	if number != 43 {
		t.Errorf("number = %d, want 43", number)
	}
	if url != "https://github.com/octo/orch/pull/43" {
		t.Errorf("url = %q", url)
	}
}

func TestCreatePRMissingBranches(t *testing.T) {
	root := tempRoot(t)
	g, script := openScripted(t, root) // no gh call may happen
	_, _, err := g.CreatePR(context.Background(), PRSpec{Title: "t", Body: "b"})
	script.AssertExhausted()
	if err == nil || !strings.Contains(err.Error(), "requires head and base") {
		t.Fatalf("err = %v, want head/base validation error", err)
	}
}

func TestPRView(t *testing.T) {
	root := tempRoot(t)
	g, script := openScripted(t, root, execxtest.Call{
		Name: "gh",
		Args: []string{"pr", "view", "43", "--json", "number,state,title,url,headRefName,baseRefName,headRefOid,mergeStateStatus,mergedAt,body"},
		Dir:  root,
		Env:  ghTestEnv,
		Stdout: `{"number":43,"state":"OPEN","title":"Add widget","url":"https://github.com/o/r/pull/43",` +
			`"headRefName":"orch/7-add-widget","baseRefName":"main","headRefOid":"abc123",` +
			`"mergeStateStatus":"CLEAN","mergedAt":null,"body":"manifest body"}`,
	})
	pr, err := g.PR(context.Background(), 43)
	if err != nil {
		t.Fatalf("PR: %v", err)
	}
	script.AssertExhausted()
	if pr.Number != 43 || pr.State != "OPEN" || pr.HeadRefOid != "abc123" || pr.MergeStateStatus != "CLEAN" {
		t.Errorf("pr = %+v", pr)
	}
	if pr.MergedAt != "" {
		t.Errorf("MergedAt = %q, want empty while unmerged", pr.MergedAt)
	}
	if pr.Body != "manifest body" {
		t.Errorf("Body = %q, want the round-tripped audit record", pr.Body)
	}
}

func TestPRViewNumberMismatch(t *testing.T) {
	root := tempRoot(t)
	g, script := openScripted(t, root, execxtest.Call{
		Name:   "gh",
		Args:   []string{"pr", "view", "43", "--json", "number,state,title,url,headRefName,baseRefName,headRefOid,mergeStateStatus,mergedAt,body"},
		Dir:    root,
		Env:    ghTestEnv,
		Stdout: `{"number":9,"state":"OPEN"}`,
	})
	_, err := g.PR(context.Background(), 43)
	script.AssertExhausted()
	if err == nil || !strings.Contains(err.Error(), "returned PR 9") {
		t.Fatalf("err = %v, want number mismatch", err)
	}
}

func TestPRForBranch(t *testing.T) {
	root := tempRoot(t)
	g, script := openScripted(t, root, execxtest.Call{
		Name: "gh",
		Args: []string{"pr", "list", "--head", "orch/7-add-widget", "--state", "open", "--json", prFields},
		Dir:  root,
		Env:  ghTestEnv,
		Stdout: `[{"number":43,"state":"OPEN","title":"Add widget","url":"https://github.com/o/r/pull/43",` +
			`"headRefName":"orch/7-add-widget","baseRefName":"main","headRefOid":"abc123",` +
			`"mergeStateStatus":"CLEAN","mergedAt":null,"body":"b"}]`,
	})
	pr, err := g.PRForBranch(context.Background(), "orch/7-add-widget")
	if err != nil {
		t.Fatalf("PRForBranch: %v", err)
	}
	script.AssertExhausted()
	if pr == nil || pr.Number != 43 || pr.HeadRefOid != "abc123" {
		t.Errorf("pr = %+v, want #43 headRefOid abc123", pr)
	}
}

func TestPRForBranchNone(t *testing.T) {
	root := tempRoot(t)
	g, script := openScripted(t, root, execxtest.Call{
		Name:   "gh",
		Args:   []string{"pr", "list", "--head", "orch/gone", "--state", "open", "--json", prFields},
		Dir:    root,
		Env:    ghTestEnv,
		Stdout: "[]",
	})
	pr, err := g.PRForBranch(context.Background(), "orch/gone")
	if err != nil {
		t.Fatalf("PRForBranch: %v", err)
	}
	script.AssertExhausted()
	if pr != nil {
		t.Errorf("pr = %+v, want nil", pr)
	}
}

func TestSetPRBody(t *testing.T) {
	root := tempRoot(t)
	g, script := openScripted(t, root, execxtest.Call{
		Name:  "gh",
		Args:  []string{"pr", "edit", "43", "--body-file", "-"},
		Dir:   root,
		Env:   ghTestEnv,
		Stdin: "updated body",
	})
	if err := g.SetPRBody(context.Background(), 43, "updated body"); err != nil {
		t.Fatalf("SetPRBody: %v", err)
	}
	script.AssertExhausted()
}

func TestMergePR(t *testing.T) {
	tests := map[string]struct {
		strategy string
		flag     string
	}{
		"squash":       {"squash", "--squash"},
		"rebase":       {"rebase", "--rebase"},
		"merge-commit": {"merge-commit", "--merge"},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			root := tempRoot(t)
			g, script := openScripted(t, root, execxtest.Call{
				Name: "gh",
				Args: []string{"pr", "merge", "43", tt.flag, "--match-head-commit", "abc123"},
				Dir:  root,
				Env:  ghTestEnv,
			})
			if err := g.MergePR(context.Background(), 43, tt.strategy, "abc123", ExplicitConfirmation()); err != nil {
				t.Fatalf("MergePR: %v", err)
			}
			script.AssertExhausted()
		})
	}
}

func TestMergePRNotConfirmed(t *testing.T) {
	root := tempRoot(t)
	g, script := openScripted(t, root) // empty transcript proves the short circuit
	err := g.MergePR(context.Background(), 43, "squash", "abc123", Confirmation{})
	if !errors.Is(err, ErrNotConfirmed) {
		t.Fatalf("err = %v, want ErrNotConfirmed", err)
	}
	script.AssertExhausted()
}

func TestMergePRUnknownStrategy(t *testing.T) {
	root := tempRoot(t)
	g, script := openScripted(t, root)
	err := g.MergePR(context.Background(), 43, "octopus", "abc123", ExplicitConfirmation())
	script.AssertExhausted()
	if err == nil || !strings.Contains(err.Error(), `unknown merge strategy "octopus"`) {
		t.Fatalf("err = %v, want unknown strategy error", err)
	}
}

func TestMergePRRequiresHeadOID(t *testing.T) {
	root := tempRoot(t)
	g, script := openScripted(t, root)
	err := g.MergePR(context.Background(), 43, "squash", "", ExplicitConfirmation())
	script.AssertExhausted()
	if err == nil || !strings.Contains(err.Error(), "head commit SHA") {
		t.Fatalf("err = %v, want missing head SHA error", err)
	}
}

func TestClosePR(t *testing.T) {
	root := tempRoot(t)
	g, script := openScripted(t, root, execxtest.Call{
		Name: "gh",
		Args: []string{"pr", "close", "43"},
		Dir:  root,
		Env:  ghTestEnv,
	})
	if err := g.ClosePR(context.Background(), 43, ExplicitConfirmation()); err != nil {
		t.Fatalf("ClosePR: %v", err)
	}
	script.AssertExhausted()
}

func TestClosePRNotConfirmed(t *testing.T) {
	root := tempRoot(t)
	g, script := openScripted(t, root)
	err := g.ClosePR(context.Background(), 43, Confirmation{})
	if !errors.Is(err, ErrNotConfirmed) {
		t.Fatalf("err = %v, want ErrNotConfirmed", err)
	}
	script.AssertExhausted()
}

func TestPRFiles(t *testing.T) {
	view := func(head string, changed int) execxtest.Call {
		return execxtest.Call{
			Name: "gh", Args: []string{"pr", "view", "43", "--json", "headRefOid,changedFiles"}, Env: ghTestEnv,
			Stdout: fmt.Sprintf(`{"headRefOid":%q,"changedFiles":%d}`, head, changed),
		}
	}
	list := func(stdout string, exit int) execxtest.Call {
		return execxtest.Call{
			Name: "gh", Args: []string{"api", "--paginate", "repos/{owner}/{repo}/pulls/43/files?per_page=100", "--jq", `.[] | [.filename, (.previous_filename // "")]`},
			Env: ghTestEnv, Stdout: stdout, Exit: exit, Stderr: "boom",
		}
	}
	const two = "[\"README.md\",\"\"]\n[\"docs/new.md\",\"CLAUDE.md\"]\n"

	root := tempRoot(t)
	g, script := openScripted(t, root, view("abc", 2), list(two, 0))
	files, err := g.PRFiles(context.Background(), 43, "abc")
	if err != nil {
		t.Fatalf("PRFiles: %v", err)
	}
	script.AssertExhausted()
	if want := []string{"README.md", "docs/new.md", "CLAUDE.md"}; !slices.Equal(files, want) {
		t.Errorf("files = %q, want %q (a rename names its previous path too)", files, want)
	}

	for name, tc := range map[string]struct {
		calls []execxtest.Call
		want  string
	}{
		"head moved":           {[]execxtest.Call{view("def", 2)}, "not \"abc\""},
		"list truncated":       {[]execxtest.Call{view("abc", 3), list(two, 0)}, "incomplete"},
		"api error":            {[]execxtest.Call{view("abc", 2), list("", 1)}, "boom"},
		"unreadable entry":     {[]execxtest.Call{view("abc", 1), list("README.md\n", 0)}, "unreadable entry"},
		"entry without a path": {[]execxtest.Call{view("abc", 1), list(`["",""]`, 0)}, "unreadable entry"},
	} {
		t.Run(name, func(t *testing.T) {
			g, script := openScripted(t, tempRoot(t), tc.calls...)
			files, err := g.PRFiles(context.Background(), 43, "abc")
			if err == nil || !strings.Contains(err.Error(), tc.want) || files != nil {
				t.Fatalf("PRFiles = %q, %v; want no list and an error naming %q", files, err, tc.want)
			}
			script.AssertExhausted()
		})
	}
}
