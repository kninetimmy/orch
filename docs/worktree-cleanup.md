# Truthful checkout removal

Issue #505 corrects the absence assumption introduced for executor cleanup
in PR #12 and extended to disposable review cleanup/replacement in PR #97.
It is a fail-closed safety change, not complete partial-removal recovery.
No historical residue is repaired or deleted automatically.

## Before and after

**Before:** cleanup tolerated `ErrUnknownWorktree` for both checkout paths
because missing Git registration was treated as an already-removed checkout.
An empty or nonempty unregistered directory could remain while the issue
advanced to `cleaned`. Both removal methods also trusted a successful
`git worktree remove` response without checking the filesystem.

**After:** missing registration is tolerable only when filesystem inspection
confirms absence. Existing unregistered paths are preserved and produce an
error naming the residue with preservation, inspection and safe-removal/retry
guidance. Successful Git removal must also leave the requested path absent
before pruning or reporting success. Inspection errors fail closed.
Neither recursive nor non-recursive filesystem deletion is a fallback.

**Before:** review replacement tolerated missing registration, then an
existing unregistered path failed the creation helper's clobber check.
**After:** it refuses that residue at removal, before creation, with the
same actionable diagnostic as cleanup. The no-clobber guarantee still holds.

An unfinished cleanup keeps its issue phase. After separately inspecting,
preserving needed data and safely removing residue outside that failed
attempt, retry can complete when both checkout paths are absent. Do not
interpret the diagnostic as permission to delete an arbitrary directory.
Confirmation remains required, and the remote branch may already have been
deleted before a later checkout-removal failure.

These are filesystem observations, not an atomic absence guarantee against
concurrent external recreation. Existing command serialization and path
identity checks remain; no new locking or host-location disposal is added.
OpenCode service/location release and broader orphan recovery are separate
work.

## Touched elements and retained boundaries

| Element | Before → after; does its earlier behavior still hold? |
| --- | --- |
| `gitops.findWorktree` | Canonical registered-path matching through `ListWorktrees`/`samePath` still holds. Before, every missing registration returned `ErrUnknownWorktree`; now it checks absence first and returns a non-tolerable error for residue or inspection failure. It never deletes a path. |
| `gitops.ErrUnknownWorktree` | Before, an existing unregistered target also matched this sentinel. After, the two removal APIs return it only for an absent unregistered target, preserving truly absent retries without letting callers swallow residue errors. |
| New `gitops.requireWorktreeAbsent` | No prior filesystem postcondition existed. `os.Lstat` now distinguishes absence, residue (including dangling symlinks) and inspection errors. It performs no deletion and does not follow a final symlink to infer absence. |
| `gitops.Git.RemoveWorktree` | Confirmation before Git access, canonical registration identity, primary refusal, cleanliness including untracked files, and non-forced Git removal all still hold. Only the successful-removal contract is strengthened with a filesystem postcondition. |
| `gitops.Git.RemoveDetachedWorktree` | Canonical registration identity, primary refusal even at detached HEAD, detached-only forced removal, and discarding disposable modified/untracked scratch without its own confirmation all still hold. Its successful-removal contract gains the same filesystem postcondition. |
| `gitops.Git.PruneWorktrees` call sites | Pruning still removes metadata, not working files. Both removal methods now call it only after verifying absence following Git success; a residue error stops before prune. The implementation is unchanged. |
| `run.Cleanup` | Its request/result schema, exact `cleanup-issue` statement, merged/abandoned phase gate, remote → executor → review → local deletion order, final phase save and metrics remain unchanged. Before, lost registration could permit `cleaned` with residue; after, either checkout residue/inspection failure prevents that transition. Comments retain this before/after. |
| `run.ReviewWorktree` | Full 40/64-hex commit OID validation, detached checkout, derived in-repository review path, `pr-open`/`in-review` gate, and no state/phase/metrics write still hold. The review path stays absent from run-state registration and therefore is not made writable through that registration. Before, residue reached creation's clobber refusal; after, removal refuses it first. |
| Creation and guard consumers | `AddWorktree`, `AddDetachedWorktree`, `prepareWorktreePath`, path derivation and the guard are unchanged. Existing-path/placement checks and tool-mediated containment remain; shell writes are not newly mediated. |
| `internal/gitops/worktree_test.go` | Existing confirmation, primary, unknown-path, dirty-executor and branch-held-review refusals remain tested. The two unknown-path fixtures now model actual absence; successful scripted removals also model absence because the script has no filesystem effects. New tests cover false-success Git responses for both methods and absence/residue/inspection-error distinctions. |
| `internal/run/lifecycle_test.go` | The existing real-Git lifecycle walk remains. New tests cover executor/review × empty/nonempty residue with byte-unchanged state and safe external-fixture-removal retries. Scripted tests cover both removal kinds returning failure or success with residue, then losing registration, then succeeding only after external fixture removal; abandoned and merged cleanup phases are exercised. |
| `internal/run/reviewworktree_test.go` | Existing full-OID, detached, run-state-unregistered, replacement and cleanup tests remain. New empty/nonempty unregistered-residue tests assert preservation, actionable refusal and unchanged state. |
| Documentation | This explanation and the README link are new. The affected removal, sentinel, cleanup and replacement source comments now retain the old assumption explicitly as before/after instead of silently deleting it. |

The primary-checkout and unregistered-directory refusals apply to **both**
registered-worktree removal methods, not just `RemoveDetachedWorktree`.
The detached-only restriction applies specifically to
`RemoveDetachedWorktree`; `RemoveWorktree` instead requires confirmation and
cleanliness and may remove a branch checkout. `WithBaseWorktree` is a separate,
unchanged direct-Git teardown of its own generated disposable checkout. It
does not inherit this new shared absence postcondition. Bootstrap scratch
teardown is also unchanged; this change is not a redesign of every teardown.

## Regression evidence

- `TestCleanupPreservesUnregisteredResidue`: all four real-Git residue
  combinations refuse without changing state or residue; a retry succeeds
  after the test separately removes only its inspected fixture.
- `TestCleanupPartialRemovalThenLostRegistration`: deterministic transcripts
  for executor/review removal exit 255 and false-success exit 0, refusal after
  lost registration, and genuinely absent retry success.
- `TestRemoveWorktreesRequireAbsentPathAfterGitSuccess`: neither removal
  method accepts a successful Git response with a remaining directory.
- `TestRequireWorktreeAbsent`: genuine absence, directory/file residue and
  a deterministic invalid-path inspection failure.
- `TestReviewWorktreePreservesUnregisteredResidue`: empty and nonempty review
  residue is not clobbered or registered by a refused replacement.
- Existing `TestReviewWorktreeCheckoutIsDetachedAndUnregistered`,
  `TestReviewWorktreeReplacesPreviousCycle`,
  `TestCleanupRemovesReviewWorktree`, `TestLifecycleWalk`, and removal-gate
  tests retain the successful paths and earlier restrictions.

Required verification is `go build ./...`, `go vet ./...`, `go test ./...`
and empty output from `gofmt -l .`. This list does not claim that CI runs
every check or qualify installed host lifecycle behavior.
