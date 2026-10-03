# scout-held-git-gates task v1

Trace the selected internal/gitops code. Identify the confirmation boundary for DeleteBranch, ForceDeleteBranch, DeleteRemoteBranch and RemoveWorktree; explain ForceDeleteBranch's treatment of a checked-out branch; and explain the gates and order before FastForward merges. Explain whether the mechanical helpers themselves verify a GitHub squash merge. Cite source or an injected-runner reproduction. Do not execute destructive Git commands or modify source.

Permitted source paths: `internal/gitops/gitops.go`, `internal/gitops/checks.go`, `internal/gitops/branch.go`, `internal/gitops/worktree.go`, `internal/gitops/errors.go`, `internal/paths/paths.go`, `internal/execx/execx.go`.
Supplied instructions: ROLE.md and CONTEXT.md. All other history is excluded.
