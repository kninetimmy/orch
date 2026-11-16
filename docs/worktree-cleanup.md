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
or review-detached checks **before host disposal**. The release/removal boundary
also inspects every interior entry, including ignored scratch, without descending
through links. Symlinks, Windows junctions/name-surrogate reparse points, other
non-regular entries, unreadable/changing directory identities, cancellation and
inspection-bound exhaustion refuse removal. It does not unlink anything.
Cleanup validates **both** checkout trees before releasing either location, then
performs the handoff before its remote → executor → review → local Git deletions.
Trees are inspected again after the handoffs before returning to Git. The exact
`cleanup-issue` confirmation and existing lifecycle/phase/configuration gates
still apply. Other hosts and direct generic Git callers retain their existing
code paths; this does not imply unchanged practical risk at the new call sites.

**Before the repair:** the first #506 implementation checked root/container/
common-directory identity but not interiors. Its guidance called generic Git
behavior unchanged. In practice, releasing service locks made destructive Git
traversal more reachable: an ignored junction could pass executor cleanliness
and redirect removal into primary or sibling files. A pre-existing lock was
never a safety guarantee. **After the repair:** the new OpenCode boundary refuses
these observed interior redirections before *any* host release or Git deletion,
for executor cleanup, review cleanup and review replacement. The generic Git
implementation is still unchanged and is not independently link-safe.

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
running background-job owners **and actual subagent children**, ordinary
shells/PTYs, and persistent terminals. **Before the repair:** ordinary shells/
PTYs were queried only under the target, discarding their cwd. Both official V2
creation APIs permit cwd independent of location. **After:** both ordinary
inventories are read under **every loaded location**, with explicit encoded
parameters and verified response roots. A running process's owner location
**or cwd** in the target prevents removal, even when the target is unloaded.
Missing/relative/unverifiable running cwd is refusal, not unrelated activity.
No other location is evicted. Persistent inventory
walks all session pages, not only sessions currently in the target: a terminal
can retain target cwd after its owner moves elsewhere. A running terminal's
owner location or cwd in the target prevents removal. Retained exited PTYs
are not running activity. Unloaded targets receive global activity checks but
no reads scoped to that target or eviction, so preflight does not itself activate
them. A target loaded during that check is refused. The ordinary and persistent
terminal domains are separate; checking one does not cover the other.

Every host release attempt has a two-minute deadline (external runner cancellation
also retains its existing bounded pipe-wait allowance). Inventories are capped:
1,024 loaded locations; 1,000 active sessions, running jobs or terminals per
response; ten session pages of at most 100 entries. Malformed, repeated,
oversized, unavailable, wrong-root or unsupported evidence fails closed, as
does unsuccessful disposal or a failed post-disposal observation. A larger
session history needs a future complete host inventory API, not a partial
"idle" conclusion. Interior inspection is metadata-only, handle-rooted and capped
at 100,000 total entries and 64 directory levels per tree; it honors cancellation.
The shell/PTY 1,000-entry cap now applies to both ordinary arrays as well as
persistent arrays (the first #506 implementation omitted the ordinary cap).

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
| `run.ReviewWorktree` replacement call site | Full OID, phase/configuration gates, derived detached path, no state/metrics write and no run-state registration remain. Before it went straight to Git; now OpenCode tree inspection/activity/disposal precede removal. Removing locks increases Git's practical reachability; interior-link refusal is a new prerequisite, not a property of generic Git. |
| `run.Cleanup` | Schema/confirmation, merged/abandoned phase, Git deletion order, final save and metrics remain. Before no host handoff existed; now both ownership/interior preflights precede either handoff, and both handoffs precede Git deletions. A refusal leaves phase unchanged. Before repair, executor disposal could precede discovering a dangerous review interior; that ordering no longer holds. |
| New `verbCtx.releaseCheckouts` / `checkoutReleaseTarget` | Split all-target verification from host mutation. Preserve engine-derived naming, canonical placement plus filesystem identity, Git registration/common directory, non-primary and per-kind prerequisites. Add interior inspection before release and reinspection before Git. Never dispose unregistered residue; other hosts still bypass this boundary. |
| New `inspectCheckout` and its recursive walk | No interior production check existed before repair. Bounded metadata inspection now refuses links/non-regular entries and inspection errors without following observed redirections or deleting entries. Handle-rooted child opens and identity rechecks precede enumeration; no canonicalization of link destinations. This is not an atomic filesystem snapshot or a replacement Git remover. |
| New `opencode.ReleaseLocation`, `releaseLocation`, `releaseParameter`, `releaseCall` and its inventory/CLI closures | Add bounded authenticated scoped release, strict necessary response fields, safe projected errors, exact encoded target and post-disposal observations. No prior disposal API is replaced. Raw subprocess errors, host payloads, credentials and session identifiers are not copied into diagnostics. |
| New `releaseDirectory` / `releaseBelongs` | Add filesystem identity/absolute-path verification and segment-aware target/descendant activity attribution; equivalent Windows extended paths remain valid, distinct directories do not become aliases through spelling. Shared path algorithms are unchanged. |
| New `releaseActiveJobs` | Reads global active drains/jobs, verifies session identities and both owner/child locations. No session is interrupted, deleted or moved. Unknown activity states are refusal, not idle. |
| New `releaseActivity` | Adds bounded paginated persistent-terminal evidence, including moved owners and target cwd. It does not turn inactive foreground status into a claim about background resources. |
| New `releaseOrdinaryProcesses` and loaded-inventory path/identity evidence | Before repair, target-only shell/PTY reads discarded cwd and omitted the array cap. Now every loaded owner's two inventories are checked, filesystem-attributing both owner and running cwd, even for an unloaded target. Unrelated running processes do not themselves block; malformed/unavailable/wrong-root/oversized evidence does. No global eviction or unsupported process fields are introduced. |
| Reused runtime/command boundary (`Resolve`, `Runtime`, `execx.Runner`/`Local`, URL encoding) | Implementations and version floors/advisory policy remain. Either resolved V2 executable uses its existing authenticated discovery; no raw HTTP credentials, dependency or alternate host resolver is added. The package comment in `catalog.go` now names both responsibilities; catalog behavior is unchanged. |
| Generic `gitops` removal/absence checks, confirmation, guard/role restrictions, state/approval schemas | Implementations unchanged; direct callers do not acquire OpenCode policy. Practical deletion reachability at the new callers changes when locks are released; the new boundary refuses observed interior hazards instead of trusting those locks. `WithBaseWorktree` and bootstrap scratch teardown still remain separate and do not inherit this check. |
| New `internal/opencode/release_test.go` | Retains synthetic loaded/unloaded, exact-scoping, active/job/persistent, descendant, disposal/postcheck, pagination, diagnostic, runtime and cancellation checks. Adds cross-location ordinary owner/cwd, unloaded-target activity, unrelated success, malformed/unavailable cwd/inventory and both ordinary response caps. No real providers or live locations are touched. |
| New `internal/run/checkout_release_test.go` | Retains disposable real-Git/scripted-host replacement/cleanup, ownership/confirmation/cleanliness/branch, absent/unloaded and partial-removal retry checks. Adds both checkout kinds' ignored-link refusal before any host call/Git mutation, preserved primary/sibling sentinels/registration/state, and inspection bounds/cancellation/error cases. Existing lifecycle tests remain. |
| `smoke.mjs` `result`/`command`, `api`, terminal probe and lifecycle fixture (`runResult`/`runCommand`/`refuseRelease`/`nativeLookup`) | Existing default execution remains; adds optional stdin and asynchronous lifecycle subprocesses so HTTP/simulation stays responsive. Transport errors name their endpoint. Retains completed lookup → different-OID replacement → cleanup, fresh roots/revision/hashes and surviving other lookups. Adds owned primary-location ordinary PTY **and shell** with target cwd and production interior-link refusal cases. Its gate/state remain synthetic, not human approval or GitHub artifacts. |
| Smoke process/junction fixtures and shutdown | Guard denial alone formerly led to harness unlinking without product refusal evidence. Now product operations must first refuse the owned interior-link fixtures and preserve sentinels/registrations/state/loaded services. Only then are the harness's verified links unlinked for its independent success tests; unlinking is not the production mitigation. Commands still exit cooperatively. Normal shutdown uses native `service stop` after verifying the isolated owned record; startup-only fallback and retained shutdown errors remain. |
| This guide and OpenCode README | Keep the historical absence/no-handoff statements as before/after, add lifecycle use, bounded-evidence limits and explicit source/installed qualification distinctions. No role instructions or managed definitions are rewritten. |

The new handoff applies to **both classes** of registered engine-owned
disposable checkout, not just `RemoveDetachedWorktree` or one review symbol.
It applies specifically at `ReviewWorktree` and `Cleanup`, **not every caller**
of generic worktree-removal APIs. Non-primary/identity/refusal checks cover
both kinds, including every interior-link inspection at these entry points;
detached-only is review-specific, whereas executor removal
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

**Before repair, this guide said:** the Windows fixture exposed Git traversing
an ignored directory junction into primary data; the native-Git command was
unchanged, the smoke unlinked its own links, and production removal policy was
unchanged. **Correction/after:** unchanged source did not mean unchanged practical
risk after lock release. Production now adds the narrow interior refusal above
for both OpenCode checkout kinds. The harness also proves product refusal; its
own unlinking is only fixture preparation, never production protection.

Generic Git teardown remains outside this repair. Direct generic callers,
other hosts, filesystem mount aliases not identifiable by ordinary link/type
metadata, and concurrent filesystem/host re-entry are not newly made safe.
Even a successful bounded inspection is not atomic with later Git traversal.
Keep targets unused; an inability to establish the required safe observations
means refusal/human handling, never a process kill or recursive fallback.
Broader link-safe teardown needs separate approval.
