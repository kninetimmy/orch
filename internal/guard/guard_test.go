package guard

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/kninetimmy/orch/internal/lockfile"
	"github.com/kninetimmy/orch/internal/manifest"
	"github.com/kninetimmy/orch/internal/state"
)

// deliveredFacts is a fully-passing Delivery facts value (row 15) that
// each case mutates to isolate one decision-table row.
func deliveredFacts() facts {
	return facts{
		path:            "/repo/.orchestrator/worktrees/issue-3/src/x.go",
		inRepo:          true,
		mode:            state.ModeDelivery,
		worktreeMatched: true,
		worktreeIssue:   3,
		worktreePhase:   state.PhaseDispatched,
		worktreeBranch:  "feature-3",
		headRef:         "ref: refs/heads/feature-3",
	}
}

func TestEvaluate(t *testing.T) {
	implementer := manifest.RoleImplementer

	cases := map[string]struct {
		req       Request
		facts     facts
		wantAllow bool
		reasonHas string
	}{
		// Row 3: outside any orch repo.
		"outside repo allows": {
			facts:     facts{path: "/etc/hosts", inRepo: false},
			wantAllow: true,
		},
		// Row 4: git internals.
		"git segment denies": {
			facts:     facts{path: "/repo/.git/config", inRepo: true, gitSeg: true},
			reasonHas: "git internals",
		},
		// Row 6: orchestrator internals in assist.
		"assist orchestrator internals deny": {
			facts:     facts{path: "/repo/.orchestrator/state.json", inRepo: true, mode: state.ModeAssist, underOrch: true},
			reasonHas: "orchestrator internals",
		},
		// Row 7: ignored path in assist.
		"assist ignored allows": {
			facts:     facts{path: "/repo/build/out", inRepo: true, mode: state.ModeAssist, ignored: true},
			wantAllow: true,
		},
		// Row 8: not ignored in assist.
		"assist tracked denies": {
			facts:     facts{path: "/repo/src/x.go", inRepo: true, mode: state.ModeAssist},
			reasonHas: "assist is read-only",
		},
		// Row 8: ignore probe failed in assist.
		"assist ignore probe error denies": {
			facts:     facts{path: "/repo/src/x.go", inRepo: true, mode: state.ModeAssist, ignoreErr: errors.New("git boom")},
			reasonHas: "cannot confirm ignore status",
		},
		// Row 9: stopped run.
		"delivery stopped denies": {
			req:       Request{Role: implementer},
			facts:     withStopped(deliveredFacts(), "secret in diff"),
			reasonHas: "run stopped",
		},
		// Row 10: read-only roles deny even inside a valid worktree.
		"delivery reviewer denies": {
			req:       Request{Role: manifest.RoleReviewer},
			facts:     deliveredFacts(),
			reasonHas: "mechanically read-only",
		},
		"delivery scout denies": {
			req:       Request{Role: manifest.RoleScout},
			facts:     deliveredFacts(),
			reasonHas: "mechanically read-only",
		},
		"delivery architect denies": {
			req:       Request{Role: manifest.RoleArchitect},
			facts:     deliveredFacts(),
			reasonHas: "mechanically read-only",
		},
		// Row 10: specialist and implementer are allowed executors.
		"delivery specialist allows": {
			req:       Request{Role: manifest.RoleSpecialist},
			facts:     deliveredFacts(),
			wantAllow: true,
		},
		"delivery implementer allows": {
			req:       Request{Role: implementer},
			facts:     deliveredFacts(),
			wantAllow: true,
		},
		// Empty role falls through to the pure containment rule.
		"delivery empty role allows": {
			facts:     deliveredFacts(),
			wantAllow: true,
		},
		// Row 11: outside every registered worktree.
		"delivery outside worktree denies": {
			facts:     withNoWorktree(deliveredFacts()),
			reasonHas: "outside every registered worktree",
		},
		// Row 12: issue assertion mismatch.
		"delivery issue mismatch denies": {
			req:       Request{Issue: 4},
			facts:     deliveredFacts(),
			reasonHas: "belongs to issue #3",
		},
		"delivery issue match allows": {
			req:       Request{Issue: 3},
			facts:     deliveredFacts(),
			wantAllow: true,
		},
		// Row 13: every non-writable phase denies.
		"delivery worktree-ready phase denies": {
			facts:     withPhase(deliveredFacts(), state.PhaseWorktreeReady),
			reasonHas: "not writable in phase worktree-ready",
		},
		"delivery awaiting-merge phase denies": {
			facts:     withPhase(deliveredFacts(), state.PhaseAwaitingMerge),
			reasonHas: "not writable in phase awaiting-merge",
		},
		"delivery blocked phase denies": {
			facts:     withPhase(deliveredFacts(), state.PhaseBlocked),
			reasonHas: "not writable in phase blocked",
		},
		"delivery abandoned phase denies": {
			facts:     withPhase(deliveredFacts(), state.PhaseAbandoned),
			reasonHas: "not writable in phase abandoned",
		},
		"delivery merged phase denies": {
			facts:     withPhase(deliveredFacts(), state.PhaseMerged),
			reasonHas: "not writable in phase merged",
		},
		// Row 13 writable phases pass.
		"delivery pr-open phase allows": {
			facts:     withPhase(deliveredFacts(), state.PhasePROpen),
			wantAllow: true,
		},
		"delivery in-review phase allows": {
			facts:     withPhase(deliveredFacts(), state.PhaseInReview),
			wantAllow: true,
		},
		// Row 14: HEAD mismatch and unreadable HEAD both deny.
		"delivery head mismatch denies": {
			facts:     withHead(deliveredFacts(), "ref: refs/heads/main", nil),
			reasonHas: "not on its registered branch",
		},
		"delivery detached head denies": {
			facts:     withHead(deliveredFacts(), "0123456789abcdef", nil),
			reasonHas: "not on its registered branch",
		},
		"delivery unreadable head denies": {
			facts:     withHead(deliveredFacts(), "", errors.New("no HEAD")),
			reasonHas: "not on its registered branch",
		},
	}

	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			v := evaluate(tc.req, tc.facts)
			if v.Allow != tc.wantAllow {
				t.Fatalf("Allow = %v, want %v (reason %q)", v.Allow, tc.wantAllow, v.Reason)
			}
			if tc.wantAllow {
				if v.Reason != "" {
					t.Errorf("allow carried a reason: %q", v.Reason)
				}
				return
			}
			if !strings.Contains(v.Reason, tc.reasonHas) {
				t.Errorf("reason = %q, want containing %q", v.Reason, tc.reasonHas)
			}
			if v.Path != tc.facts.path {
				t.Errorf("Path = %q, want %q", v.Path, tc.facts.path)
			}
		})
	}
}

func withStopped(f facts, reason string) facts { f.stopped = reason; return f }
func withNoWorktree(f facts) facts             { f.worktreeMatched = false; return f }
func withPhase(f facts, p state.Phase) facts   { f.worktreePhase = p; return f }
func withHead(f facts, ref string, err error) facts {
	f.headRef = ref
	f.headErr = err
	return f
}

// fakeChecker builds a Checker whose function fields are all stubbed, so
// Check can be exercised without a filesystem or git.
func fakeChecker() *Checker {
	return &Checker{
		canonical:       func(p string) (string, error) { return p, nil },
		inside:          func(root, path string) (bool, error) { return false, nil },
		findRoot:        func(startDir string) (string, error) { return "/repo", nil },
		loadState:       func(string) (*state.State, error) { return &state.State{Mode: state.ModeAssist}, nil },
		inspectLock:     func(string) (*lockfile.Owner, error) { return nil, nil },
		checkConsistent: func(*state.State, *lockfile.Owner) error { return nil },
		readHead:        func(string) (string, error) { return "", nil },
		ignored:         func(context.Context, string, string) (bool, error) { return false, nil },
	}
}

// TestCheckOperationalRowsError covers rows 1, 2, and 5: the "cannot
// verify" / "propagate message" rows surface as an error from Check,
// distinct from a policy denial.
func TestCheckOperationalRowsError(t *testing.T) {
	t.Run("row1 canonical failure", func(t *testing.T) {
		c := fakeChecker()
		c.canonical = func(string) (string, error) { return "", errors.New("bad path") }
		_, err := c.Check(context.Background(), Request{Paths: []string{"/repo/x"}})
		if err == nil || !strings.Contains(err.Error(), "cannot verify path") {
			t.Fatalf("err = %v, want cannot-verify-path", err)
		}
	})
	t.Run("row2 root walk failure", func(t *testing.T) {
		c := fakeChecker()
		c.findRoot = func(string) (string, error) { return "", errors.New("permission denied") }
		_, err := c.Check(context.Background(), Request{Paths: []string{"/repo/x"}})
		if err == nil || !strings.Contains(err.Error(), "cannot verify repository root") {
			t.Fatalf("err = %v, want cannot-verify-root", err)
		}
	})
	t.Run("row5 state failure", func(t *testing.T) {
		c := fakeChecker()
		c.loadState = func(string) (*state.State, error) { return nil, errors.New("corrupt state") }
		_, err := c.Check(context.Background(), Request{Paths: []string{"/repo/x"}})
		if err == nil || !strings.Contains(err.Error(), "corrupt state") {
			t.Fatalf("err = %v, want propagated state error", err)
		}
	})
}

// TestCheckMultiPathFirstDenyWins confirms the first denied path denies
// the whole invocation and names that path.
func TestCheckMultiPathFirstDenyWins(t *testing.T) {
	c := fakeChecker()
	// Assist mode, not under .orchestrator, not ignored → every in-repo
	// path denies (row 8).
	v, err := c.Check(context.Background(), Request{Paths: []string{"/repo/a.go", "/repo/b.go"}})
	if err != nil {
		t.Fatalf("Check err = %v", err)
	}
	if v.Allow {
		t.Fatal("Allow = true, want deny")
	}
	if v.Path != "/repo/a.go" {
		t.Errorf("Path = %q, want first path /repo/a.go", v.Path)
	}
}

func TestCheckAllPathsAllow(t *testing.T) {
	c := fakeChecker()
	c.ignored = func(context.Context, string, string) (bool, error) { return true, nil }
	v, err := c.Check(context.Background(), Request{Paths: []string{"/repo/a", "/repo/b"}})
	if err != nil {
		t.Fatalf("Check err = %v", err)
	}
	if !v.Allow {
		t.Fatalf("Allow = false, reason %q", v.Reason)
	}
}

func TestCheckNoPaths(t *testing.T) {
	if _, err := fakeChecker().Check(context.Background(), Request{}); err == nil {
		t.Fatal("Check with no paths returned nil error")
	}
}
