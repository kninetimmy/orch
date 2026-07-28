package run

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kninetimmy/orch/internal/execx"
	"github.com/kninetimmy/orch/internal/execx/execxtest"
	"github.com/kninetimmy/orch/internal/paths"
	"github.com/kninetimmy/orch/internal/state"
)

// seedReviewRun adds a second commit to the git sandbox at root and
// enters Delivery with one issue (#1) in phase, returning the two
// commit OIDs on main (oldest first) a review can be pinned to. The
// issue's own worktree is named in state but never created on disk: no
// code path under test needs it, and leaving it out keeps the review
// checkout the only worktree these tests can be observing.
func seedReviewRun(t *testing.T, root string, phase state.Phase) (string, string) {
	t.Helper()
	first := strings.TrimSpace(rawGit(t, root, "rev-parse", "HEAD"))
	if err := os.WriteFile(filepath.Join(root, "second.txt"), []byte("second\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	rawGit(t, root, "add", "-A")
	rawGit(t, root, "commit", "-m", "second")
	second := strings.TrimSpace(rawGit(t, root, "rev-parse", "HEAD"))

	enterDeliveryAt(t, root, "r1", []state.Issue{fixtureIssue("a", 1, phase)})
	return first, second
}

// reviewEnv is an Env whose git calls run for real and whose gh script
// is empty: any GitHub call the verb under test makes fails the test,
// which is itself an assertion — provisioning a review checkout is a
// purely local act.
func reviewEnv(t *testing.T, root string) (Env, *execxtest.Script) {
	t.Helper()
	script := &execxtest.Script{T: t}
	return Env{RepoRoot: root, Runner: muxRunner{git: execx.Local{}, gh: script}, Now: fixedNow}, script
}

func reviewWorktreeJSON(number int, headOID string) string {
	return fmt.Sprintf(`{"schema_version":1,"issue_number":%d,"head_oid":%q}`, number, headOID)
}

// detachedHeadAt fails the test unless dir is a checkout with a detached
// HEAD sitting on oid.
func detachedHeadAt(t *testing.T, dir, oid string) {
	t.Helper()
	if head := strings.TrimSpace(rawGit(t, dir, "rev-parse", "HEAD")); head != oid {
		t.Errorf("%s HEAD = %s, want %s", dir, head, oid)
	}
	if ref := strings.TrimSpace(rawGit(t, dir, "rev-parse", "--abbrev-ref", "HEAD")); ref != "HEAD" {
		t.Errorf("%s is on branch %s, want a detached HEAD (a branch is a name a concurrent verb can move)", dir, ref)
	}
}

// TestReviewWorktreeCheckoutIsDetachedAndUnregistered covers the shape
// of the created checkout and the invariant the whole feature rests on:
// persisted state must not refer to it. A change that registers the
// review worktree makes it a worktree the guard matches, and therefore
// writable by the guarded tools — the exact inversion of the point — so
// this test asserts the absence directly rather than through any
// downstream behavior.
func TestReviewWorktreeCheckoutIsDetachedAndUnregistered(t *testing.T) {
	root := newActivateRepo(t)
	_, second := seedReviewRun(t, root, state.PhaseInReview)
	before := stateBytes(t, root)

	res := runVerb(t, root, ReviewWorktree, reviewWorktreeJSON(1, second))

	if want := WorktreeContainer + "/review-issue-1"; res.Worktree != want {
		t.Errorf("worktree = %q, want %q", res.Worktree, want)
	}
	if res.Worktree == worktreeRel(1) {
		t.Fatalf("review worktree %q collides with issue #1's executor worktree", res.Worktree)
	}
	if res.HeadOID != second {
		t.Errorf("head_oid = %q, want the requested %q", res.HeadOID, second)
	}
	abs := reviewWorktreeAbs(root, 1)
	detachedHeadAt(t, abs, second)

	// Criterion: no issue's Worktree field equals the review path.
	st := loadRun(t, root)
	for i := range st.Run.Issues {
		if st.Run.Issues[i].Worktree == res.Worktree {
			t.Fatalf("issue #%d registers the review worktree %q in state; leaving it unregistered is what makes it read-only to the guarded tools", st.Run.Issues[i].Number, res.Worktree)
		}
	}
	// The same invariant over the whole document, so a future field that
	// records the path under another name fails here too.
	after := stateBytes(t, root)
	if bytes.Contains(after, []byte("review-issue-1")) {
		t.Fatalf("persisted state names the review worktree:\n%s", after)
	}
	// Stronger still, and true today: the verb persists nothing at all.
	if !bytes.Equal(before, after) {
		t.Errorf("state changed; the verb advances no phase and records nothing\nbefore %s\nafter  %s", before, after)
	}

	// The containment fact the guard's Delivery rule turns into a denial:
	// the review checkout lies inside no registered worktree, so a write
	// targeting it matches none of them.
	for i := range st.Run.Issues {
		registered := filepath.Join(root, filepath.FromSlash(st.Run.Issues[i].Worktree))
		inside, err := paths.Inside(registered, abs)
		if err != nil {
			t.Fatal(err)
		}
		if inside {
			t.Errorf("review checkout %s lies inside registered worktree %s", abs, registered)
		}
	}
}

// TestReviewWorktreeReplacesPreviousCycle covers a request-changes
// cycle: the second call re-provisions at the new head instead of
// failing on the leftover checkout or accumulating a second one, and it
// does so even though the reviewer left untracked files behind.
func TestReviewWorktreeReplacesPreviousCycle(t *testing.T) {
	root := newActivateRepo(t)
	first, second := seedReviewRun(t, root, state.PhaseInReview)
	abs := reviewWorktreeAbs(root, 1)

	if res := runVerb(t, root, ReviewWorktree, reviewWorktreeJSON(1, first)); res.HeadOID != first {
		t.Fatalf("first cycle head = %q, want %q", res.HeadOID, first)
	}
	detachedHeadAt(t, abs, first)
	scratch := filepath.Join(abs, "reviewer-notes.txt")
	if err := os.WriteFile(scratch, []byte("test output\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	if res := runVerb(t, root, ReviewWorktree, reviewWorktreeJSON(1, second)); res.HeadOID != second {
		t.Fatalf("second cycle head = %q, want the new head %q", res.HeadOID, second)
	}
	detachedHeadAt(t, abs, second)
	if _, err := os.Stat(scratch); err == nil {
		t.Error("the replaced checkout kept the previous cycle's untracked file")
	}
	if n := strings.Count(rawGit(t, root, "worktree", "list", "--porcelain"), "review-issue-1"); n != 1 {
		t.Errorf("git registers %d review worktrees for issue #1, want exactly 1", n)
	}
}

// TestReviewWorktreeBadRequestBeforeMutation pins the pre-mutation
// rejections. The head_oid cases matter beyond schema hygiene: a ref
// name resolves when git reads it, so accepting one would reintroduce
// the race the pinned checkout exists to remove.
func TestReviewWorktreeBadRequestBeforeMutation(t *testing.T) {
	cases := map[string]string{
		"unsupported schema":  `{"schema_version":2,"issue_number":1,"head_oid":"0123456789abcdef0123456789abcdef01234567"}`,
		"unknown field":       `{"schema_version":1,"issue_number":1,"head_oid":"0123456789abcdef0123456789abcdef01234567","ref":"main"}`,
		"empty head_oid":      reviewWorktreeJSON(1, ""),
		"fetch head":          reviewWorktreeJSON(1, "FETCH_HEAD"),
		"branch name":         reviewWorktreeJSON(1, "main"),
		"revision expression": reviewWorktreeJSON(1, "HEAD~1"),
		// An abbreviation is rejected too: git resolves a name as a ref
		// before it resolves it as a short object, so a short all-hex
		// string is a possible branch name, not a pinned commit.
		"abbreviated oid": reviewWorktreeJSON(1, "0123456"),
	}
	for name, doc := range cases {
		t.Run(name, func(t *testing.T) {
			root := newActivateRepo(t)
			seedReviewRun(t, root, state.PhaseInReview)
			before := stateBytes(t, root)

			env, script := reviewEnv(t, root)
			_, err := ReviewWorktree(context.Background(), env, []byte(doc))
			if !errors.Is(err, ErrBadRequest) {
				t.Fatalf("err = %v, want ErrBadRequest", err)
			}
			script.AssertExhausted()
			if _, err := os.Stat(reviewWorktreeAbs(root, 1)); err == nil {
				t.Error("a rejected request still created a checkout")
			}
			if !bytes.Equal(stateBytes(t, root), before) {
				t.Error("a rejected request changed state")
			}
		})
	}
}

// TestReviewWorktreeUnknownOIDFailsAndRetries covers the OID that is
// well-formed but absent from the repository — a head never fetched, or
// one that has since gone away. git's failure propagates rather than
// being swallowed into some fallback commit, it leaves no half-created
// checkout, and the next request with a real OID therefore succeeds
// instead of colliding with wreckage.
func TestReviewWorktreeUnknownOIDFailsAndRetries(t *testing.T) {
	root := newActivateRepo(t)
	_, second := seedReviewRun(t, root, state.PhaseInReview)

	env, script := reviewEnv(t, root)
	_, err := ReviewWorktree(context.Background(), env, []byte(reviewWorktreeJSON(1, "deadbeefdeadbeefdeadbeefdeadbeefdeadbeef")))
	if err == nil {
		t.Fatal("an OID the repository does not have was accepted")
	}
	script.AssertExhausted()
	if _, statErr := os.Stat(reviewWorktreeAbs(root, 1)); !os.IsNotExist(statErr) {
		t.Errorf("a failed provision left a checkout behind (stat err = %v)", statErr)
	}

	if res := runVerb(t, root, ReviewWorktree, reviewWorktreeJSON(1, second)); res.HeadOID != second {
		t.Fatalf("retry head = %q, want %q", res.HeadOID, second)
	}
}

// TestReviewWorktreePreconditions covers the two refusals the verb
// inherits from loadVerb rather than implementing itself: a phase
// outside {pr-open, in-review}, and no active Delivery run at all.
func TestReviewWorktreePreconditions(t *testing.T) {
	oidOf := func(t *testing.T, root string) string {
		t.Helper()
		return strings.TrimSpace(rawGit(t, root, "rev-parse", "HEAD"))
	}

	for _, phase := range []state.Phase{state.PhaseWorktreeReady, state.PhaseDispatched, state.PhaseAwaitingMerge, state.PhaseMerged} {
		t.Run(string(phase), func(t *testing.T) {
			root := newActivateRepo(t)
			_, second := seedReviewRun(t, root, phase)

			env, script := reviewEnv(t, root)
			_, err := ReviewWorktree(context.Background(), env, []byte(reviewWorktreeJSON(1, second)))
			if !errors.Is(err, ErrWrongPhase) {
				t.Fatalf("err = %v, want ErrWrongPhase", err)
			}
			script.AssertExhausted()
			if _, err := os.Stat(reviewWorktreeAbs(root, 1)); err == nil {
				t.Error("a refused request still created a checkout")
			}
		})
	}

	t.Run("assist", func(t *testing.T) {
		root := newActivateRepo(t) // no Delivery run entered
		env, script := reviewEnv(t, root)
		_, err := ReviewWorktree(context.Background(), env, []byte(reviewWorktreeJSON(1, oidOf(t, root))))
		if !errors.Is(err, ErrNoDeliveryRun) {
			t.Fatalf("err = %v, want ErrNoDeliveryRun", err)
		}
		script.AssertExhausted()
	})
}

// TestCleanupRemovesReviewWorktree covers cleanup's teardown from both
// sides: it removes a checkout a review cycle left behind — locating it
// by the same derivation, since state never held the path — and it stays
// the idempotent success it already was when there is none.
func TestCleanupRemovesReviewWorktree(t *testing.T) {
	for _, provision := range []bool{true, false} {
		name := "with a review worktree"
		if !provision {
			name = "without one"
		}
		t.Run(name, func(t *testing.T) {
			root := newLifecycleRepo(t)
			_, second := seedReviewRun(t, root, state.PhaseInReview)
			abs := reviewWorktreeAbs(root, 1)

			if provision {
				runVerb(t, root, ReviewWorktree, reviewWorktreeJSON(1, second))
				if _, err := os.Stat(abs); err != nil {
					t.Fatalf("review checkout was not created: %v", err)
				}
			}

			// The issue reaches cleanup's phase the way a real run does.
			st := loadRun(t, root)
			st.Run.Issues[0].Phase = state.PhaseMerged
			st.Run.Issues[0].ApprovedHeadOID = second
			if err := state.Save(root, st); err != nil {
				t.Fatal(err)
			}

			runVerb(t, root, Cleanup, `{"schema_version":1,"issue_number":1,"statement":"cleanup-issue"}`)
			wantPhase(t, root, 1, state.PhaseCleaned)
			if _, err := os.Stat(abs); !os.IsNotExist(err) {
				t.Errorf("review checkout still present after cleanup (stat err = %v)", err)
			}
			if strings.Contains(rawGit(t, root, "worktree", "list", "--porcelain"), "review-issue-1") {
				t.Error("git still registers the review worktree after cleanup")
			}
		})
	}
}
