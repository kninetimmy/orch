package bootstrap

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/kninetimmy/orch/internal/config"
	"github.com/kninetimmy/orch/internal/ghops"
	"github.com/kninetimmy/orch/internal/gitops"
	"github.com/kninetimmy/orch/internal/paths"
	"github.com/kninetimmy/orch/internal/question"
)

// stage1 runs the ordered mutation sequence (PRD §18 steps 10-15):
// label taxonomy, issue, worktree, files, §18.13 validation, commit,
// push, PR, status, worktree cleanup. State advances only on success;
// every failure after the issue is created is reported through
// failClosed with the artifacts it left behind and their exact
// cleanup commands (contract call 3).
func stage1(ctx context.Context, deps Deps, git *gitops.Git, gh *ghops.GH, repo ghops.Repo, complete *question.Complete, cfg *config.Config) (Report, error) {
	if _, err := gh.EnsureLabelTaxonomy(ctx); err != nil {
		return Report{}, err
	}

	issueBody, err := renderRecord(complete, nil, 0)
	if err != nil {
		return Report{}, err
	}
	labels := ghops.Labels{Status: ghops.StatusInProgress, Type: ghops.TypeInfra, Role: ghops.RoleImplementer, Risk: ghops.RiskStandard}
	issueNumber, issueURL, err := gh.CreateIssue(ctx, bootstrapTitle, issueBody, labels, modelNames(cfg)...)
	if err != nil {
		return Report{}, err
	}

	return stage1AfterIssue(ctx, deps, git, gh, repo, complete, issueNumber, issueURL)
}

// stage1AfterIssue is stage1's tail, split out because every error
// return from here on must go through failClosed: the issue already
// exists.
func stage1AfterIssue(ctx context.Context, deps Deps, git *gitops.Git, gh *ghops.GH, repo ghops.Repo, complete *question.Complete, issueNumber int, issueURL string) (Report, error) {
	dir, err := newWorktreeDir()
	if err != nil {
		return Report{}, failClosed(ctx, git, gh, issueNumber, issueURL, "", false, fmt.Errorf("create bootstrap worktree directory: %w", err))
	}

	wt, err := git.AddWorktree(ctx, dir, BootstrapBranch, "HEAD")
	if err != nil {
		return Report{}, failClosed(ctx, git, gh, issueNumber, issueURL, "", false, err)
	}

	// From here, a branch and worktree exist but carry no commit yet:
	// force-deleting the branch loses nothing (it is identical to
	// HEAD), so every failure up through the commit itself cleans up
	// both.
	if err := writeFiles(wt.Path, complete); err != nil {
		return Report{}, failClosed(ctx, git, gh, issueNumber, issueURL, wt.Path, true, err)
	}

	validations, err := validateInstallation(wt.Path, complete, deps.now())
	if err != nil {
		return Report{}, failClosed(ctx, git, gh, issueNumber, issueURL, wt.Path, true, err)
	}

	commitMessage := fmt.Sprintf("%s\n\nCloses #%d\n", bootstrapTitle, issueNumber)
	if err := git.CommitAll(ctx, wt.Path, commitMessage); err != nil {
		return Report{}, failClosed(ctx, git, gh, issueNumber, issueURL, wt.Path, true, err)
	}

	// A commit now exists on the branch: it is preserved work, not a
	// leftover to auto-delete. Every failure from here removes only
	// the disposable worktree.
	if err := git.Push(ctx, wt.Path, "origin", BootstrapBranch); err != nil {
		return Report{}, failClosed(ctx, git, gh, issueNumber, issueURL, wt.Path, false, err)
	}

	prBody, err := renderRecord(complete, validations, issueNumber)
	if err != nil {
		return Report{}, failClosed(ctx, git, gh, issueNumber, issueURL, wt.Path, false, err)
	}
	prNumber, prURL, err := gh.CreatePR(ctx, ghops.PRSpec{Head: BootstrapBranch, Base: repo.DefaultBranch, Title: bootstrapTitle, Body: prBody})
	if err != nil {
		return Report{}, failClosed(ctx, git, gh, issueNumber, issueURL, wt.Path, false, err)
	}

	if err := gh.SetStatus(ctx, issueNumber, ghops.StatusAwaitingReview); err != nil {
		return Report{}, failClosed(ctx, git, gh, issueNumber, issueURL, wt.Path, false,
			fmt.Errorf("PR #%d (%s) opened but marking issue #%d awaiting-review failed: %w", prNumber, prURL, issueNumber, err))
	}

	// Everything a human cares about (issue, commit, PR, status) is
	// already correct; a failure to remove the disposable worktree
	// here is reported as a plain cleanup note, not routed through
	// failClosed — flipping the issue back to needs-human would
	// misrepresent a fully successful bootstrap over a leftover local
	// temp directory (a deliberate narrower reading of "any
	// post-mutation failure").
	if err := removeWorktree(ctx, git, wt.Path); err != nil {
		return Report{}, fmt.Errorf("bootstrap succeeded (issue #%d %s, PR #%d %s) but the disposable worktree %s could not be removed: %w; remove it by hand and run `git worktree prune`", issueNumber, issueURL, prNumber, prURL, wt.Path, err)
	}

	return Report{
		SchemaVersion: ReportSchemaVersion,
		Issue:         ReportRef{Number: issueNumber, URL: issueURL},
		PR:            ReportRef{Number: prNumber, URL: prURL},
		Branch:        BootstrapBranch,
		Validations:   validations,
		NextSteps: []string{
			fmt.Sprintf("review and merge %s", prURL),
			"git pull",
			fmt.Sprintf("optionally: git branch -d %s", BootstrapBranch),
			"orch status",
		},
	}, nil
}

// newWorktreeDir returns a fresh, canonical, not-yet-existing
// directory path for AddWorktree to create (WithBaseWorktree
// precedent, base.go): os.MkdirTemp both generates a unique name and
// creates it, but AddWorktree's own precondition refuses a path that
// already exists (never clobber), so the directory is removed again
// immediately before AddWorktree recreates it via `git worktree add`.
// Canonicalizing first (while the directory still exists) resolves
// any symlinked ancestor the same way WithBaseWorktree does.
func newWorktreeDir() (string, error) {
	tmp, err := os.MkdirTemp("", "orch-bootstrap-*")
	if err != nil {
		return "", err
	}
	dir, err := paths.Canonical(tmp)
	if err != nil {
		return "", errors.Join(err, os.RemoveAll(tmp))
	}
	if err := os.RemoveAll(dir); err != nil {
		return "", err
	}
	return dir, nil
}

// removeWorktree deletes dir outright and prunes the now-stale
// worktree registration (`git worktree prune`, no Confirmation
// needed: it deletes no working files). This — rather than
// gitops.RemoveWorktree, whose own RequireClean gate exists to
// preserve blocked human work — is bootstrap's forced-removal
// primitive: the worktree is disposable scratch by construction
// (regenerable from the same answers), so os.RemoveAll followed by a
// prune is the "forced removal is safe" cleanup the spec calls for,
// without adding a second gitops primitive beyond CommitAll.
func removeWorktree(ctx context.Context, git *gitops.Git, dir string) error {
	if err := os.RemoveAll(dir); err != nil {
		return fmt.Errorf("remove worktree directory %s: %w", dir, err)
	}
	return git.PruneWorktrees(ctx)
}

// failClosed finishes a Stage 1 failure (contract call 3): it
// best-effort marks the issue needs-human, attempts worktree removal
// whenever worktreeDir is set, force-deletes the local bootstrap
// branch when deleteBranch is true (only safe pre-commit, when the
// branch carries no unique work), and returns cause wrapped with the
// exact remediation commands for whatever it could not undo itself.
// Cleanup always runs under context.WithoutCancel (WithBaseWorktree
// precedent, base.go): a canceled ctx must not skip cleanup.
func failClosed(ctx context.Context, git *gitops.Git, gh *ghops.GH, issueNumber int, issueURL, worktreeDir string, deleteBranch bool, cause error) error {
	cleanupCtx := context.WithoutCancel(ctx)
	problems := []error{cause}
	remediation := []string{fmt.Sprintf("issue #%d (%s)", issueNumber, issueURL)}

	if worktreeDir != "" {
		if err := removeWorktree(cleanupCtx, git, worktreeDir); err != nil {
			problems = append(problems, fmt.Errorf("remove worktree %s: %w", worktreeDir, err))
			remediation = append(remediation, fmt.Sprintf("remove the leftover worktree by hand (`git worktree remove --force %s` or delete the directory, then `git worktree prune`)", worktreeDir))
		}
		if deleteBranch {
			if err := git.ForceDeleteBranch(cleanupCtx, BootstrapBranch, gitops.ExplicitConfirmation()); err != nil {
				problems = append(problems, fmt.Errorf("force-delete branch %s: %w", BootstrapBranch, err))
				remediation = append(remediation, fmt.Sprintf("delete the leftover local branch by hand: `git branch -D %s`", BootstrapBranch))
			}
		} else {
			// A commit exists on the branch: it is preserved work, not a
			// leftover to auto-delete (contract call 3: an orphan
			// artifact is tolerated and named, never destroyed).
			remediation = append(remediation, fmt.Sprintf("the local branch %s carries the bootstrap commit; delete it by hand once you no longer need it (`git branch -D %s`)", BootstrapBranch, BootstrapBranch))
		}
	}

	if err := gh.SetStatus(cleanupCtx, issueNumber, ghops.StatusNeedsHuman); err != nil {
		remediation = append(remediation, fmt.Sprintf("mark issue #%d needs-human by hand: %v", issueNumber, err))
	}

	return fmt.Errorf("%w; re-run `orch init --bootstrap` from the same answers once cleaned up; %s", errors.Join(problems...), strings.Join(remediation, "; "))
}

// modelNames returns cfg's distinct enabled-host model strings
// (run.modelDenylist precedent, duplicated in miniature here rather
// than exported from internal/run, which stays Delivery-only): the
// forbidden-areas denylist CreateIssue enforces so a model name can
// never land as a GitHub label (PRD §13).
func modelNames(cfg *config.Config) []string {
	seen := map[string]bool{}
	add := func(h *config.Host) {
		if h == nil {
			return
		}
		for _, rp := range []config.RoleProfile{
			h.Roles.Architect, h.Roles.Scout, h.Roles.Implementer,
			h.Roles.Specialist, h.Roles.Reviewer, h.Roles.ReviewDowngrade,
		} {
			if rp.Model != "" {
				seen[rp.Model] = true
			}
		}
	}
	add(cfg.Hosts.Claude)
	add(cfg.Hosts.Codex)
	names := make([]string, 0, len(seen))
	for m := range seen {
		names = append(names, m)
	}
	return names
}
