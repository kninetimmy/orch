package gitops

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// LocalBranchAtHead treats a missing exact branch as absent, but not a
// different tip, symbolic ref or failed lookup.
func (g *Git) LocalBranchAtHead(ctx context.Context, branch, head string) (bool, error) {
	ref := "refs/heads/" + branch
	res, err := g.run(ctx, g.root, "for-each-ref", "--format=%(refname)%09%(objectname)%09%(symref)", "--", ref)
	if err != nil {
		return false, err
	}
	if res.ExitCode != 0 || strings.TrimSpace(res.Stderr) != "" {
		return false, fmt.Errorf("could not verify the local branch ref; preserve it and inspect Git")
	}
	for _, line := range strings.Split(res.Stdout, "\n") {
		name, details, _ := strings.Cut(strings.TrimSuffix(line, "\r"), "\t")
		if name != ref {
			continue
		}
		if details != head+"\t" {
			return false, fmt.Errorf("local branch differs from the approved PR head or is symbolic; preserve it")
		}
		return true, nil
	}
	return false, nil
}

// RemoteBranchAtHead accepts only absence or the expected exact remote ref.
func (g *Git) RemoteBranchAtHead(ctx context.Context, remote, branch, head string) (bool, error) {
	// A fetch-side absence is not evidence about a different push destination.
	readURL, err := g.git(ctx, g.root, "remote", "get-url", "--", remote)
	if err != nil {
		return false, err
	}
	pushURL, err := g.git(ctx, g.root, "remote", "get-url", "--push", "--all", "--", remote)
	if err != nil {
		return false, err
	}
	if readURL == "" || pushURL != readURL || strings.ContainsAny(pushURL, "\r\n") {
		return false, fmt.Errorf("remote read and deletion destinations could not be matched; preserve the branch")
	}
	ref := "refs/heads/" + branch
	out, err := g.git(ctx, g.root, "ls-remote", "--heads", "--refs", "--", remote, ref)
	if err != nil || out == "" {
		return false, err
	}
	if out != head+"\t"+ref {
		return false, fmt.Errorf("remote branch differs from the approved PR head or could not be verified; preserve it")
	}
	return true, nil
}

// DeleteRemoteBranchAtHead compares the approved OID at the push, not the
// tracking ref or a lease inferred from the earlier remote observation.
func (g *Git) DeleteRemoteBranchAtHead(ctx context.Context, remote, branch, head string, c Confirmation) error {
	if !c.ok {
		return fmt.Errorf("%w: delete remote branch %s/%s", ErrNotConfirmed, remote, branch)
	}
	ref := "refs/heads/" + branch
	_, err := g.git(ctx, g.root, "push", "--force-with-lease="+ref+":"+head, "--", remote, ":"+ref)
	return err
}

// RequireBranchIdle permits attachment only in allowedWorktree, if provided.
// A detached worktree can still use a branch via rebase (including sequencer
// update-refs), git am or bisect. Conservatively refuse these operations rather
// than bypass git branch -D's protection when using a conditional ref update.
func (g *Git) RequireBranchIdle(ctx context.Context, branch, allowedWorktree string) error {
	worktrees, err := g.ListWorktrees(ctx)
	if err != nil {
		return err
	}
	common, err := g.CommonDir(ctx)
	if err != nil {
		return err
	}
	info, err := os.Stat(common)
	if err != nil || !info.IsDir() {
		return fmt.Errorf("could not verify the Git common directory; preserve the branch")
	}
	for _, wt := range worktrees {
		if wt.Branch == branch && wt.Path != allowedWorktree {
			return fmt.Errorf("branch %s is checked out in an unexpected worktree; preserve it", branch)
		}
		out, err := g.git(ctx, wt.Path, "rev-parse", "--path-format=absolute", "--git-common-dir",
			"--git-path", "rebase-merge", "--git-path", "rebase-apply", "--git-path", "BISECT_START")
		if err != nil {
			return err
		}
		fields := strings.Split(strings.ReplaceAll(out, "\r\n", "\n"), "\n")
		if len(fields) != 4 || !filepath.IsAbs(fields[0]) {
			return fmt.Errorf("could not verify Git operation state; preserve the branch")
		}
		linked, err := os.Stat(fields[0])
		if err != nil || !os.SameFile(info, linked) {
			return fmt.Errorf("could not verify worktree Git identity; preserve the branch")
		}
		for _, marker := range fields[1:] {
			rel, err := filepath.Rel(fields[0], marker)
			if err != nil || !filepath.IsLocal(rel) {
				return fmt.Errorf("could not verify Git operation state; preserve the branch")
			}
			if _, err := os.Lstat(marker); !os.IsNotExist(err) {
				return fmt.Errorf("rebase, am or bisect state is present or unverifiable; preserve the branch")
			}
		}
	}
	return nil
}

// ForceDeleteBranchAtHead checks worktree use and asks Git to delete the
// exact ref only if it still holds the approved OID. It leaves branch config
// alone, since removing the section separately could affect a recreated branch.
func (g *Git) ForceDeleteBranchAtHead(ctx context.Context, branch, head string, c Confirmation) error {
	if !c.ok {
		return fmt.Errorf("%w: force-delete branch %s", ErrNotConfirmed, branch)
	}
	exists, err := g.LocalBranchAtHead(ctx, branch, head)
	if err != nil || !exists {
		return err
	}
	if err := g.RequireBranchIdle(ctx, branch, ""); err != nil {
		return err
	}
	_, err = g.git(ctx, g.root, "update-ref", "--no-deref", "-d", "refs/heads/"+branch, head)
	return err
}
