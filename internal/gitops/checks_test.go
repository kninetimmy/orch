package gitops

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/kninetimmy/orch/internal/execx/execxtest"
)

var statusArgs = []string{"status", "--porcelain=v1", "--untracked-files=all"}

func TestRequireClean(t *testing.T) {
	cases := map[string]struct {
		stdout  string
		wantErr error
		wantIn  string
	}{
		"clean":     {stdout: ""},
		"modified":  {stdout: " M internal/foo.go\n", wantErr: ErrNotClean, wantIn: "internal/foo.go"},
		"untracked": {stdout: "?? junk.txt\n", wantErr: ErrNotClean, wantIn: "junk.txt"},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			root := tempRoot(t)
			g, script := openScripted(t, root, execxtest.Call{
				Name: "git", Args: statusArgs, Dir: root, Stdout: tc.stdout,
			})
			err := g.RequireClean(context.Background(), "")
			script.AssertExhausted()
			if !errors.Is(err, tc.wantErr) {
				t.Fatalf("err = %v, want %v", err, tc.wantErr)
			}
			if tc.wantIn != "" && !strings.Contains(err.Error(), tc.wantIn) {
				t.Errorf("error %q does not name %q", err, tc.wantIn)
			}
		})
	}
}

func TestRequireCleanTruncatesLongLists(t *testing.T) {
	dirty := strings.Repeat("?? junk\n", 8)
	g, script := openScripted(t, tempRoot(t), execxtest.Call{Name: "git", Args: statusArgs, Stdout: dirty})
	err := g.RequireClean(context.Background(), "")
	script.AssertExhausted()
	if err == nil || !strings.Contains(err.Error(), "and 3 more") {
		t.Fatalf("err = %v, want truncated listing", err)
	}
}

func TestCurrentBranch(t *testing.T) {
	g, script := openScripted(t, tempRoot(t), execxtest.Call{
		Name: "git", Args: []string{"symbolic-ref", "--short", "HEAD"}, Stdout: "main\n",
	})
	branch, err := g.CurrentBranch(context.Background())
	script.AssertExhausted()
	if err != nil || branch != "main" {
		t.Fatalf("CurrentBranch = %q, %v; want main, nil", branch, err)
	}
}

func TestCurrentBranchDetached(t *testing.T) {
	g, script := openScripted(t, tempRoot(t), execxtest.Call{
		Name: "git", Args: []string{"symbolic-ref", "--short", "HEAD"},
		Stderr: "fatal: ref HEAD is not a symbolic ref", Exit: 1,
	})
	_, err := g.CurrentBranch(context.Background())
	script.AssertExhausted()
	if !errors.Is(err, ErrDetachedHead) {
		t.Fatalf("err = %v, want ErrDetachedHead", err)
	}
}

func TestRequireNotOn(t *testing.T) {
	cases := map[string]struct {
		branch    string
		protected []string
		wantErr   error
	}{
		"on protected":  {branch: "main", protected: []string{"main", "master"}, wantErr: ErrProtectedBranch},
		"on feature":    {branch: "orch/issue-4", protected: []string{"main", "master"}},
		"nothing named": {branch: "main", protected: nil},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			g, script := openScripted(t, tempRoot(t), execxtest.Call{
				Name: "git", Args: []string{"symbolic-ref", "--short", "HEAD"}, Stdout: tc.branch + "\n",
			})
			err := g.RequireNotOn(context.Background(), tc.protected...)
			script.AssertExhausted()
			if !errors.Is(err, tc.wantErr) {
				t.Fatalf("err = %v, want %v", err, tc.wantErr)
			}
		})
	}
}

func TestRevParse(t *testing.T) {
	const hash = "0123456789abcdef0123456789abcdef01234567"
	g, script := openScripted(t, tempRoot(t), execxtest.Call{
		Name: "git", Args: []string{"rev-parse", "--verify", "main^{commit}"}, Stdout: hash + "\n",
	})
	got, err := g.RevParse(context.Background(), "main")
	script.AssertExhausted()
	if err != nil || got != hash {
		t.Fatalf("RevParse = %q, %v; want %q, nil", got, err, hash)
	}
}
