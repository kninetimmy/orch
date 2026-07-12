package ghops

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/kninetimmy/orch/internal/execx/execxtest"
)

func TestCreateIssue(t *testing.T) {
	root := tempRoot(t)
	g, script := openScripted(t, root, execxtest.Call{
		Name: "gh",
		Args: []string{
			"issue", "create", "--title", "Add widget", "--body-file", "-",
			"--label", "ready", "--label", "feature", "--label", "implementer", "--label", "standard",
			"--label", "core",
		},
		Dir:    root,
		Env:    ghTestEnv,
		Stdin:  "audit record body",
		Stdout: "https://github.com/octo/orch/issues/42\n",
	})
	labels := validLabels()
	labels.Areas = []string{"core"}
	number, url, err := g.CreateIssue(context.Background(), "Add widget", "audit record body", labels)
	if err != nil {
		t.Fatalf("CreateIssue: %v", err)
	}
	script.AssertExhausted()
	if number != 42 {
		t.Errorf("number = %d, want 42", number)
	}
	if url != "https://github.com/octo/orch/issues/42" {
		t.Errorf("url = %q", url)
	}
}

func TestCreateIssueBadLabels(t *testing.T) {
	root := tempRoot(t)
	g, script := openScripted(t, root) // empty transcript: no gh call may happen
	_, _, err := g.CreateIssue(context.Background(), "t", "b", Labels{})
	if !errors.Is(err, ErrBadLabels) {
		t.Fatalf("err = %v, want ErrBadLabels", err)
	}
	script.AssertExhausted()
}

func TestCreateIssueForbiddenArea(t *testing.T) {
	root := tempRoot(t)
	g, script := openScripted(t, root)
	labels := validLabels()
	labels.Areas = []string{"opus-4-8"}
	_, _, err := g.CreateIssue(context.Background(), "t", "b", labels, "opus-4-8")
	if !errors.Is(err, ErrBadLabels) {
		t.Fatalf("err = %v, want ErrBadLabels", err)
	}
	script.AssertExhausted()
}

func TestCreateIssueUnparsableURL(t *testing.T) {
	root := tempRoot(t)
	g, script := openScripted(t, root, execxtest.Call{
		Name: "gh",
		Args: []string{
			"issue", "create", "--title", "t", "--body-file", "-",
			"--label", "ready", "--label", "feature", "--label", "implementer", "--label", "standard",
		},
		Dir:    root,
		Env:    ghTestEnv,
		Stdin:  "b",
		Stdout: "something unexpected",
	})
	_, _, err := g.CreateIssue(context.Background(), "t", "b", validLabels())
	script.AssertExhausted()
	if err == nil || !strings.Contains(err.Error(), "cannot parse issues number") {
		t.Fatalf("err = %v, want parse failure", err)
	}
}

func TestParseNumberFromURL(t *testing.T) {
	tests := map[string]struct {
		url, kind string
		want      int // 0 = expect error
	}{
		"issue url":            {"https://github.com/o/r/issues/7", "issues", 7},
		"pr url":               {"https://github.com/o/r/pull/123", "pull", 123},
		"trailing newline":     {"https://github.com/o/r/issues/7\n", "issues", 7},
		"trailing slash":       {"https://github.com/o/r/issues/7/", "issues", 7},
		"wrong kind":           {"https://github.com/o/r/issues/7", "pull", 0},
		"no number":            {"https://github.com/o/r/issues/abc", "issues", 0},
		"negative":             {"https://github.com/o/r/issues/-1", "issues", 0},
		"zero":                 {"https://github.com/o/r/issues/0", "issues", 0},
		"empty":                {"", "issues", 0},
		"not a url":            {"created!", "issues", 0},
		"kind not penultimate": {"https://github.com/o/r/issues/7/comments", "issues", 0},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			got, err := parseNumberFromURL(tt.url, tt.kind)
			if tt.want == 0 {
				if err == nil {
					t.Fatalf("parseNumberFromURL(%q) = %d, want error", tt.url, got)
				}
				return
			}
			if err != nil {
				t.Fatalf("parseNumberFromURL(%q): %v", tt.url, err)
			}
			if got != tt.want {
				t.Errorf("got %d, want %d", got, tt.want)
			}
		})
	}
}

func TestIssueView(t *testing.T) {
	root := tempRoot(t)
	g, script := openScripted(t, root, execxtest.Call{
		Name:   "gh",
		Args:   []string{"issue", "view", "42", "--json", "number,state,title,url,labels,body"},
		Dir:    root,
		Env:    ghTestEnv,
		Stdout: `{"number":42,"state":"CLOSED","title":"Add widget","url":"https://github.com/o/r/issues/42","labels":[{"name":"ready"},{"name":"feature"}],"body":"audit record body"}`,
	})
	issue, err := g.Issue(context.Background(), 42)
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}
	script.AssertExhausted()
	if issue.Number != 42 || issue.State != "CLOSED" || issue.Title != "Add widget" {
		t.Errorf("issue = %+v", issue)
	}
	if len(issue.Labels) != 2 || issue.Labels[0] != "ready" || issue.Labels[1] != "feature" {
		t.Errorf("labels = %v", issue.Labels)
	}
	if issue.Body != "audit record body" {
		t.Errorf("Body = %q, want the round-tripped audit record", issue.Body)
	}
}

func TestIssueViewNumberMismatch(t *testing.T) {
	root := tempRoot(t)
	g, script := openScripted(t, root, execxtest.Call{
		Name:   "gh",
		Args:   []string{"issue", "view", "42", "--json", "number,state,title,url,labels,body"},
		Dir:    root,
		Env:    ghTestEnv,
		Stdout: `{"number":7,"state":"OPEN","title":"","url":"","labels":[],"body":""}`,
	})
	_, err := g.Issue(context.Background(), 42)
	script.AssertExhausted()
	if err == nil || !strings.Contains(err.Error(), "returned issue 7") {
		t.Fatalf("err = %v, want number mismatch", err)
	}
}

func TestSetIssueBody(t *testing.T) {
	root := tempRoot(t)
	g, script := openScripted(t, root, execxtest.Call{
		Name:  "gh",
		Args:  []string{"issue", "edit", "42", "--body-file", "-"},
		Dir:   root,
		Env:   ghTestEnv,
		Stdin: "updated audit record",
	})
	if err := g.SetIssueBody(context.Background(), 42, "updated audit record"); err != nil {
		t.Fatalf("SetIssueBody: %v", err)
	}
	script.AssertExhausted()
}

func TestSetStatus(t *testing.T) {
	root := tempRoot(t)
	g, script := openScripted(t, root, execxtest.Call{
		Name: "gh",
		Args: []string{
			"issue", "edit", "42",
			"--remove-label", "ready", "--remove-label", "in-progress",
			"--remove-label", "needs-human", "--remove-label", "awaiting-review",
			"--remove-label", "delivered",
			"--add-label", "blocked",
		},
		Dir: root,
		Env: ghTestEnv,
	})
	if err := g.SetStatus(context.Background(), 42, StatusBlocked); err != nil {
		t.Fatalf("SetStatus: %v", err)
	}
	script.AssertExhausted()
}

func TestSetStatusBadValue(t *testing.T) {
	root := tempRoot(t)
	g, script := openScripted(t, root) // no gh call may happen
	err := g.SetStatus(context.Background(), 42, Status("done"))
	if !errors.Is(err, ErrBadLabels) {
		t.Fatalf("err = %v, want ErrBadLabels", err)
	}
	script.AssertExhausted()
}

func TestSetRole(t *testing.T) {
	root := tempRoot(t)
	g, script := openScripted(t, root, execxtest.Call{
		Name: "gh",
		Args: []string{
			"issue", "edit", "42",
			"--remove-label", "implementer",
			"--add-label", "specialist",
		},
		Dir: root,
		Env: ghTestEnv,
	})
	if err := g.SetRole(context.Background(), 42, RoleSpecialist); err != nil {
		t.Fatalf("SetRole: %v", err)
	}
	script.AssertExhausted()
}

func TestSetRoleBadValue(t *testing.T) {
	root := tempRoot(t)
	g, script := openScripted(t, root) // no gh call may happen
	err := g.SetRole(context.Background(), 42, Role("architect"))
	if !errors.Is(err, ErrBadLabels) {
		t.Fatalf("err = %v, want ErrBadLabels", err)
	}
	script.AssertExhausted()
}

func TestCloseIssue(t *testing.T) {
	root := tempRoot(t)
	g, script := openScripted(t, root, execxtest.Call{
		Name: "gh",
		Args: []string{"issue", "close", "42"},
		Dir:  root,
		Env:  ghTestEnv,
	})
	if err := g.CloseIssue(context.Background(), 42, ExplicitConfirmation()); err != nil {
		t.Fatalf("CloseIssue: %v", err)
	}
	script.AssertExhausted()
}

func TestCloseIssueNotConfirmed(t *testing.T) {
	root := tempRoot(t)
	g, script := openScripted(t, root) // empty transcript proves the short circuit
	err := g.CloseIssue(context.Background(), 42, Confirmation{})
	if !errors.Is(err, ErrNotConfirmed) {
		t.Fatalf("err = %v, want ErrNotConfirmed", err)
	}
	script.AssertExhausted()
}
