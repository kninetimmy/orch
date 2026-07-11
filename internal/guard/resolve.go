package guard

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/kninetimmy/orch/internal/execx"
	"github.com/kninetimmy/orch/internal/lockfile"
	"github.com/kninetimmy/orch/internal/paths"
	"github.com/kninetimmy/orch/internal/state"
)

// gitDirName is the git metadata directory/file name whose presence in a
// path segment marks git internals (row 4).
const gitDirName = ".git"

// gitEnv keeps every git invocation fail-closed and locale-stable, the
// same environment gitops applies: GIT_TERMINAL_PROMPT=0 makes git error
// instead of prompting (never hang), LC_ALL=C stabilizes output.
var gitEnv = []string{"GIT_TERMINAL_PROMPT=0", "LC_ALL=C"}

// Checker resolves the facts behind a guard Verdict. Its function fields
// are injected so tests drive the decision without a real filesystem or
// git; NewChecker wires the production implementations.
type Checker struct {
	canonical       func(string) (string, error)
	inside          func(root, path string) (bool, error)
	findRoot        func(startDir string) (string, error)
	loadState       func(repoRoot string) (*state.State, error)
	inspectLock     func(repoRoot string) (*lockfile.Owner, error)
	checkConsistent func(*state.State, *lockfile.Owner) error
	readHead        func(worktreeAbs string) (string, error)
	ignored         func(ctx context.Context, repoRoot, canonPath string) (bool, error)
}

// NewChecker returns a Checker wired to the production path, state, lock,
// and git implementations. The ignore probe runs through runner, the
// only place guard shells out (and only on the rare Assist in-repo
// write); every other row is resolved from state, the lock, and a small
// number of file reads and lstats.
func NewChecker(runner execx.Runner) *Checker {
	return &Checker{
		canonical:       paths.Canonical,
		inside:          paths.Inside,
		findRoot:        paths.FindOutermostRoot,
		loadState:       state.Load,
		inspectLock:     lockfile.Inspect,
		checkConsistent: state.CheckConsistent,
		readHead:        readWorktreeHead,
		ignored:         gitCheckIgnore(runner),
	}
}

// Check evaluates req and returns the verdict for the first path that is
// not allowed, or an allow when every path passes. Multiple paths are
// all-or-nothing: the first denial denies the whole invocation. A
// non-nil error reports an operational failure the caller must treat as
// "cannot decide" (decision-table rows 1, 2, and 5): an unverifiable
// path, an unwalkable root, or unreadable/inconsistent state. Those are
// distinct from a policy denial, which is a Verdict with Allow false.
func (c *Checker) Check(ctx context.Context, req Request) (Verdict, error) {
	if len(req.Paths) == 0 {
		return Verdict{}, errors.New("no paths to check")
	}
	for _, p := range req.Paths {
		f, err := c.resolve(ctx, p)
		if err != nil {
			return Verdict{}, err
		}
		if v := evaluate(req, f); !v.Allow {
			return v, nil
		}
	}
	return Verdict{Allow: true}, nil
}

// resolve gathers the facts for one path, short-circuiting its own I/O in
// the decision table's order so an early terminal condition never pays
// for later work. It returns an error for the "cannot verify" /
// "propagate message" rows (1, 2, 5, and any containment check that
// cannot complete); every other outcome is a fully-populated facts.
func (c *Checker) resolve(ctx context.Context, path string) (facts, error) {
	f := facts{path: path}

	// Row 1: canonicalize the target (works for a not-yet-existing file).
	canon, err := c.canonical(path)
	if err != nil {
		return f, fmt.Errorf("cannot verify path %s: %w", path, err)
	}
	f.path = canon

	// Rows 2/3: the governing root is the outermost .orchestrator
	// ancestor. No such ancestor means the path is outside any orch repo.
	// The walk starts at the directory the write lands in, never the
	// target itself: for an existing file, probing <file>/.orchestrator
	// returns ENOTDIR on Unix, which is not fs.ErrNotExist and would turn
	// every edit of an existing file into a spurious operational deny.
	root, err := c.findRoot(filepath.Dir(canon))
	if err != nil {
		if errors.Is(err, paths.ErrNotFound) {
			return f, nil // row 3: inRepo stays false → allow
		}
		return f, fmt.Errorf("cannot verify repository root for %s: %w", canon, err)
	}
	f.inRepo = true

	// Row 4: a .git segment anywhere between the root and the target.
	rel, err := filepath.Rel(root, canon)
	if err != nil {
		return f, fmt.Errorf("cannot place %s under %s: %w", canon, root, err)
	}
	for _, seg := range strings.Split(filepath.ToSlash(rel), "/") {
		if strings.EqualFold(seg, gitDirName) {
			f.gitSeg = true
			return f, nil // evaluate denies; no state read needed
		}
	}

	// Row 5: load state, inspect the lock, and verify they agree.
	st, err := c.loadState(root)
	if err != nil {
		return f, err
	}
	owner, err := c.inspectLock(root)
	if err != nil {
		return f, err
	}
	if err := c.checkConsistent(st, owner); err != nil {
		return f, err
	}
	f.mode = st.Mode

	switch st.Mode {
	case state.ModeAssist:
		orchDir := filepath.Join(root, paths.OrchestratorDir)
		under, err := c.inside(orchDir, canon)
		if err != nil {
			return f, fmt.Errorf("cannot verify %s against %s: %w", canon, orchDir, err)
		}
		if under {
			f.underOrch = true
			return f, nil // row 6 denies; skip the ignore probe
		}
		// Row 7/8: the sole subprocess on any path, and only here.
		ignored, ierr := c.ignored(ctx, root, canon)
		f.ignored = ignored
		f.ignoreErr = ierr
		return f, nil

	case state.ModeDelivery:
		f.stopped = st.Run.StoppedReason
		for i := range st.Run.Issues {
			iss := &st.Run.Issues[i]
			if iss.Worktree == "" {
				continue
			}
			wtAbs := filepath.Join(root, filepath.FromSlash(iss.Worktree))
			in, err := c.inside(wtAbs, canon)
			if err != nil {
				return f, fmt.Errorf("cannot verify %s against worktree %s: %w", canon, wtAbs, err)
			}
			if !in {
				continue
			}
			f.worktreeMatched = true
			f.worktreeIssue = iss.Number
			f.worktreePhase = iss.Phase
			f.worktreeBranch = iss.Branch
			// Row 14 is only reached for a writable phase (row 13 denies
			// first otherwise), so the HEAD read is skipped elsewhere.
			if writablePhase(iss.Phase) {
				head, herr := c.readHead(wtAbs)
				f.headRef = head
				f.headErr = herr
			}
			break
		}
		return f, nil

	default:
		// state.validate accepts only assist and delivery; fail closed.
		return f, fmt.Errorf("state records unknown mode %q", st.Mode)
	}
}

// readWorktreeHead reads a worktree's checked-out branch ref with zero
// subprocesses: it resolves <worktree>/.git (a gitdir pointer file in a
// linked worktree, or the directory itself in a primary checkout) and
// returns the trimmed contents of that git directory's HEAD. A detached
// HEAD returns a bare object id (no "ref:" prefix), which evaluate treats
// as a branch mismatch.
func readWorktreeHead(worktreeAbs string) (string, error) {
	dotGit := filepath.Join(worktreeAbs, gitDirName)
	info, err := os.Lstat(dotGit)
	if err != nil {
		return "", fmt.Errorf("read %s: %w", dotGit, err)
	}

	gitDir := dotGit
	if !info.IsDir() {
		data, err := os.ReadFile(dotGit)
		if err != nil {
			return "", fmt.Errorf("read %s: %w", dotGit, err)
		}
		const prefix = "gitdir:"
		line := strings.TrimSpace(string(data))
		if !strings.HasPrefix(line, prefix) {
			return "", fmt.Errorf("%s: not a gitdir pointer", dotGit)
		}
		gitDir = strings.TrimSpace(strings.TrimPrefix(line, prefix))
		if !filepath.IsAbs(gitDir) {
			gitDir = filepath.Join(worktreeAbs, gitDir)
		}
	}

	headPath := filepath.Join(gitDir, "HEAD")
	head, err := os.ReadFile(headPath)
	if err != nil {
		return "", fmt.Errorf("read %s: %w", headPath, err)
	}
	return strings.TrimSpace(string(head)), nil
}

// gitCheckIgnore builds the production ignore probe: `git check-ignore
// -q` on the path relative to the repo root, mirroring
// gitops.RequireIgnored's exit-code handling but file-oriented (no
// trailing slash, since a guard target is a concrete file). Exit 0 means
// ignored, exit 1 means not ignored, anything else is an error.
func gitCheckIgnore(runner execx.Runner) func(ctx context.Context, repoRoot, canonPath string) (bool, error) {
	return func(ctx context.Context, repoRoot, canonPath string) (bool, error) {
		rel, err := filepath.Rel(repoRoot, canonPath)
		if err != nil {
			return false, fmt.Errorf("compute path for %s relative to %s: %w", canonPath, repoRoot, err)
		}
		query := filepath.ToSlash(rel)
		res, err := runner.Run(ctx, execx.Cmd{
			Name: "git",
			Args: []string{"check-ignore", "-q", "--", query},
			Dir:  repoRoot,
			Env:  gitEnv,
		})
		if err != nil {
			return false, err
		}
		switch res.ExitCode {
		case 0:
			return true, nil
		case 1:
			return false, nil
		default:
			return false, fmt.Errorf("git check-ignore in %s exited %d: %s", repoRoot, res.ExitCode, strings.TrimSpace(res.Stderr))
		}
	}
}
