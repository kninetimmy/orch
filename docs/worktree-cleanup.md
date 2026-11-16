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
identity checks remain. **Before #506:** no new locking or host-location disposal is added.
OpenCode service/location release and broader orphan recovery are separate
work. **After #506:** the scoped host handoff below adds location release;
broader orphan recovery and atomic host exclusion remain separate work.

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

## Explicit OpenCode checkout release (issue #506)

**Before:** `ReviewWorktree` replacement and `Cleanup` called Git removal
without releasing OpenCode's checkout-scoped MCP connections and cached
watchers. A completed child could leave its native location loaded; Windows
removal could then fail after Git deregistration. `pool: false` never meant
shutdown on child completion.

**After:** only those explicit engine operations add a host handoff. For an
OpenCode run, a registered disposable target must pass filesystem ownership,
container/common-directory identity, non-primary and executor-branch/cleanliness
or review-detached checks **before host disposal**. Cleanup performs the handoff
before its remote → executor → review → local Git deletions. The exact
`cleanup-issue` confirmation and existing lifecycle/phase/configuration gates
still apply. Other hosts and generic Git removal retain their behavior.

The handoff reuses OpenCode V2 runtime resolution and authenticated `api`
discovery. It requires the advertised `debug.location.list` and
`debug.location.evict` deep-object contract, filesystem-verifies the loaded
inventory, and refuses unexpected loaded descendants. A loaded target's
`/api/location` roots and scoped shell/PTY response roots must match the
verified checkout/primary. The only disposal request is
`DELETE /api/debug/location?location%5Bdirectory%5D=<encoded-exact-directory>`.
The directory is never omitted. Afterward, inventory must show no target or
descendant and must retain every previously loaded other location; target
filesystem identity is checked again before returning to Git.

Activity observations cover foreground drains and the session locations of
running background-job owners **and actual subagent children**, running
location-scoped shells/PTYs, and persistent terminals. Persistent inventory
walks all session pages, not only sessions currently in the target: a terminal
can retain target cwd after its owner moves elsewhere. A running terminal's
owner location or cwd in the target prevents removal. Retained exited PTYs
are not running activity. Unloaded targets receive global activity checks but
no location-scoped reads or eviction, so preflight does not itself activate
them. A target loaded during that check is refused.

Every release attempt has a two-minute deadline (external runner cancellation
also retains its existing bounded pipe-wait allowance). Inventories are capped:
1,024 loaded locations; 1,000 active sessions, running jobs or terminals per
response; ten session pages of at most 100 entries. Malformed, repeated,
oversized, unavailable, wrong-root or unsupported evidence fails closed, as
does unsuccessful disposal or a failed post-disposal observation. A larger
session history needs a future complete host inventory API, not a partial
"idle" conclusion.

**These are bounded observations, not atomic exclusion of concurrent host
re-entry.** The engine serializer does not lock OpenCode. A prompt, shell,
terminal, session move or external recreation can race the observation/disposal/
Git sequence. No host-wide quiescence, atomic tree snapshot or concurrent-entry
guarantee is claimed. Keep the disposable target unused during the explicit
operation; do not use ordinary idle/completion as a teardown signal.

Failure never advances the issue to `cleaned` or invokes Git removal for a
target whose release failed. An earlier successful handoff may already have
unloaded an idle cache before a later check fails. Unregistered residue is
**never evicted or deleted** by this handoff: #505's preservation/absence rules
still decide the Git outcome, including retries after partial deregistration.
This does not recover historical residue or authorize manual removal.
There is no shared-service restart, process-kill fallback, session deletion,
Memhub relay/full-profile fallback, idle timer, installation or global-config
change.

### Touched elements and before/after scope

| Element | Does its earlier behavior still hold? |
| --- | --- |
| `run.ReviewWorktree` replacement call site | Yes: full OID, phase/configuration gates, derived detached path, no state/metrics write and no run-state registration remain. Before it went straight to Git; now OpenCode disposal precedes that same Git removal. |
| `run.Cleanup` | Yes: schema/confirmation, merged/abandoned phase, Git deletion order, final save and metrics remain. Before no host handoff existed; now both checkout handoffs precede Git deletions. A host refusal leaves phase unchanged. |
| New `verbCtx.releaseCheckout` | Verifies engine-derived executor/review naming, canonical placement plus filesystem identity, Git registration/common directory, non-primary and per-kind removal prerequisites. It never disposes unregistered residue. |
| New `opencode.ReleaseLocation`, `releaseLocation`, `releaseParameter`, `releaseCall` and its inventory/CLI closures | Add bounded authenticated scoped release, strict necessary response fields, safe projected errors, exact encoded target and post-disposal observations. No prior disposal API is replaced. Raw subprocess errors, host payloads, credentials and session identifiers are not copied into diagnostics. |
| New `releaseDirectory` / `releaseBelongs` | Add filesystem identity/absolute-path verification and segment-aware target/descendant activity attribution; equivalent Windows extended paths remain valid, distinct directories do not become aliases through spelling. Shared path algorithms are unchanged. |
| New `releaseActiveJobs` | Reads global active drains/jobs, verifies session identities and both owner/child locations. No session is interrupted, deleted or moved. Unknown activity states are refusal, not idle. |
| New `releaseActivity` | Adds bounded paginated persistent-terminal evidence, including moved owners and target cwd. It does not turn inactive foreground status into a claim about background resources. |
| Reused runtime/command boundary (`Resolve`, `Runtime`, `execx.Runner`/`Local`, URL encoding) | Implementations and version floors/advisory policy remain. Either resolved V2 executable uses its existing authenticated discovery; no raw HTTP credentials, dependency or alternate host resolver is added. The package comment in `catalog.go` now names both responsibilities; catalog behavior is unchanged. |
| Generic `gitops` removal/absence checks, confirmation, guard/role restrictions, state/approval schemas | Unchanged. Direct callers do not acquire OpenCode policy. `WithBaseWorktree` and bootstrap scratch teardown still remain separate. |
| New `internal/opencode/release_test.go` | Synthetic host responses test loaded/unloaded targets, exact scoping, all activity types, owner/child distinction, malformed/unavailable/unsupported evidence, descendants, disposal/postcheck failures, pagination, safe diagnostics, runtime fallback and cancellation bounds. No real providers or live locations are touched. |
| New `internal/run/checkout_release_test.go` | Real disposable Git fixtures with a scripted host exercise replacement, cleanup, pre-host ownership/confirmation/dirty/branch refusals, absent/unloaded paths and retries after partial Git removal/lost registration. Existing lifecycle tests remain. |
| `smoke.mjs` `result`/`command`, `api`, terminal probe and lifecycle fixture (`runResult`/`runCommand`/`refuseRelease`/`nativeLookup`) | Existing default execution remains; adds optional stdin and asynchronous lifecycle subprocesses so the HTTP/simulation loop stays responsive during multi-request preflight. Transport errors now name their endpoint. The same private server/simulation exercises native activity refusal, completed lookups → different-OID review replacement → executor/review cleanup, fresh root/revision/hash evidence and surviving primary/unrelated lookups. Its gate/state are explicitly synthetic, not human approval or GitHub artifacts. |
| Smoke process/junction fixtures and shutdown | Existing guard/junction denial checks remain. Only the test's verified adversarial junctions are unlinked after their denial test, before real Git teardown. Activity commands exit cooperatively via fixture markers; no killing makes checkout removal pass. Normal shutdown now uses native `service stop` after verifying the isolated record still names the owned server; only pre-discovery startup failure retains the old owned-tree fallback. Shutdown errors are retained instead of masking the body error or skipping evidence. |
| This guide and OpenCode README | Keep the historical absence/no-handoff statements as before/after, add lifecycle use, bounded-evidence limits and explicit source/installed qualification distinctions. No role instructions or managed definitions are rewritten. |

The new handoff applies to **both classes** of registered engine-owned
disposable checkout, not just `RemoveDetachedWorktree` or one review symbol.
It applies specifically at `ReviewWorktree` and `Cleanup`, **not every caller**
of generic worktree-removal APIs. Non-primary/identity/refusal checks cover
both kinds; detached-only is review-specific, whereas executor removal
requires its registered branch and clean tracked/untracked work. Native
lookup/child-role fences still cover every supported role and matching
Memhub executor, not just a service literally named `memhub`.

### Qualification and remaining work

Run all four Go gates above, plus `npm test --prefix adapters/opencode` and
the required **local Windows** `npm run smoke --prefix adapters/opencode`.
Hosted CI does **not** run the latter. Its retained `evidence.json` records
source/selected artifact identities, native lookup bindings/hash evidence,
refusals, lifecycle outcomes, provider count and owned-host shutdown.
Source/isolated success is not installation, current installed cleanup,
real-model Delivery qualification, full partial-cleanup recovery or proof of
atomic host quiescence. Unverified runtime versions keep their advisory.

The extended Windows fixture also exposed Git traversing an ignored directory
junction during `git worktree remove`, potentially reaching its primary
fixture. An isolated native-Git reproduction uses the same unchanged removal
command as base. This issue does not repair that separate generic-Git hazard;
the smoke unlinks only its own adversarial links and leaves production
removal policy unchanged. Broader link-safe teardown needs separate approval.
