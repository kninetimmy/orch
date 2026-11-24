# Truthful checkout removal

For confirmed OpenCode issue cleanup, see the
[direct tool](#direct-opencode-cleanup-issue-547). For legacy unbound cleanup,
see the [standalone recovery steps](#resume-normal-cleanup-from-a-standalone-terminal).

Issue #505 corrects the absence assumption introduced for executor cleanup
in PR #12 and extended to disposable review cleanup/replacement in PR #97.
It is a fail-closed safety change, not complete partial-removal recovery.
No historical residue is repaired or deleted automatically.

The #505 and #506 sections below retain their historical contracts. They are
the **before #510** record where they say generic Git, base reproduction,
bootstrap or other hosts are unchanged/outside the repair. The explicit
**after #510** contract and per-element inventory follow at the end; neither
historical wording nor source qualification implies installed protection.

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
The directory is never omitted. **Before #523:** afterward, inventory had to
show no target or descendant and retain every previously loaded other location;
even one unrelated cache disappearance during preflight refused Git removal.
**After #523:** one missing non-target exact key permits at most one full
read-only preflight recheck, only if its physical directory is still the same
and no other inventory change accompanied that loss. The original target
identity and two-minute deadline are retained. Fresh target/project roots,
foreground/background activity, persistent terminals and both ordinary process
inventories for every currently loaded exact key must pass; the missing key
receives no scoped read or disposal. Its filesystem identity is checked again
before disposal. Repeated churn (including a new/reappearing key), target
re-entry/change, changed/unverifiable identities or incomplete evidence refuses
before DELETE. Afterward, inventory must still show no target or descendant
and retain the exact key and physical identity of every other location in the
final pre-disposal inventory; the original target filesystem identity is checked
again before returning to Git. A failed final observation never retries.

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
response; at most 100 entries per session page. **Before #537:** ten session
pages and sequential terminal-list requests imposed a ceiling of 1,000 saved
sessions. A larger history was refused pending a future complete host
inventory API, rather than treating a partial scan as idle. **After #537:**
Windows follows the validated cursor chain to its end under the original
deadline, with at most eight terminal-list reads in flight. Non-Windows
retains the ten-page ceiling and sequential reads.
Malformed, repeated, oversized, unavailable, wrong-root, unsupported or
incomplete evidence still fails closed, as does unsuccessful disposal or a
failed post-disposal observation. Interior inspection is metadata-only,
handle-rooted and capped at 100,000 total entries and 64 directory levels per
tree; it honors cancellation.
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
Hosted CI does **not** run the latter. Before #539 its retained `evidence.json`
recorded source/selected artifact identities, native lookup bindings/hash
evidence, refusals, lifecycle outcomes, provider count and owned-host shutdown.
The same checks still run. After #539 a pass retains a verified receipt of
identities, checks, provider count and shutdown; a verified failure archive
retains the full `evidence.json`. See
[smoke fixture retention and recovery](../adapters/opencode/README.md#smoke-fixture-retention-and-recovery-issue-539).
Both smoke entrypoints now verify private local journal storage before reading
recovery state. The macOS Node tests exercise the normal OS temp aliases without
running a host. **Before the storage-lock repair:** the smoke-only mutex was
a localhost socket, which did not exclude another network namespace using
the same storage. **After:** the Node mutator holds a nonblocking OS lock on
the permanent private `smoke.lock` throughout journal and scratch operations;
release on exit allows inspection, not automatic reclamation. The same
retention boundary refuses incomplete activity observations or macOS resource
forks it cannot preserve in its archive. These are smoke/recovery safeguards,
not changes to the explicit checkout-release or Git-removal contracts here.
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

### Issue #523 touched elements and retained behavior

| Element | Before → after; does earlier behavior still hold? |
| --- | --- |
| `ReleaseLocation` deadline and `call` closure | One shared two-minute context, existing runtime/authenticated command binding, response validation and safe errors remain. Explicit before/after-command cancellation checks now also reject expired evidence; recheck never resets the deadline. |
| `releaseLocation`, original `targetInfo` and `inventory` closure | Exact keys remain distinct even for one physical directory. Absolute/physical identity, duplicate/ambiguous-target and loaded-descendant refusals remain; no inventory read activates a location. The original target identity is never replaced by a fresh baseline. |
| `preserve` closure | Before, missing and rebound keys both refused at both calls. Now only the first pre-disposal comparison can classify one missing non-target key for recheck; a present-but-rebound key still refuses. Final preservation remains strict for every non-target key in the final pre-disposal inventory; an alternate alias never substitutes for an exact key. |
| Preflight loop and new `unchanged` closure | Before, any unrelated preflight disappearance refused. Now one verified loss repeats the existing activity/root preflight without mutation or querying the missing key. Target and missing-directory identities are verified before proceeding, including after ordinary process reads. A second observed set/identity change refuses. `releaseActivity`, `releaseActiveJobs`, `releaseOrdinaryProcesses`, `releaseDirectory`, `releaseBelongs` and their caps/owner/cwd rules are unchanged and reused, not replaced by missing-cache idle inference. |
| Scoped DELETE and final observations | Exact encoded target-only disposal, no DELETE for unloaded targets, no disposal retry, unloaded target/descendant postcondition and original target identity remain. A failed final check still stops before Git teardown, even if an earlier handoff already unloaded a cache. |
| `run.releaseCheckouts`, `Cleanup`, `ReviewWorktree` and generic Git boundaries | Production callers are unchanged. The single-recheck rule covers **every** shared release invocation for executor cleanup, review cleanup and review replacement, not every generic Git remover or other host. Ownership, confirmation, cleanliness/detached, interior-hazard, absence/residue, partial-removal and state safeguards remain; detached-only is review-specific. |
| `releaseFixture.run` / `beforeCall`, alias and new eviction/identity tests | The old pre-disposal alias-refusal regression now proves stable success; post-disposal alias loss still refuses. Existing synthetic cases remain. Added scripted activity-time eviction, original-deadline equality, fresh safety evidence, repeated churn, missing/replaced identities, exact-key preservation and zero/one DELETE checks use no sleeps, live locations or providers. |
| `checkoutHost.Run` / `beforeAPI`, inventory counter and lifecycle tests | Existing disposable real-Git/scripted-host fixtures remain. Added stable success and fresh-active/repeated-change/final-check refusal at all three lifecycle paths prove preserved branches, checkouts/registration and state on refusal, with no Git mutation or disposal retry. Earlier successful handoffs are still allowed to have unloaded their cache. |
| This guide and adapter README preservation row | Retain the old blanket preflight-refusal statement explicitly as before/after; document the sole bounded exception and unchanged strict final check. No smoke, adapter runtime, host contract, runtime resolution, configuration, permission, installation or release behavior changes. |

## Windows teardown refusal (issue #510 / task 348)

### Before and after, by teardown class

**Before #510:** the generic Git implementation was unchanged by #506 and
was not independently link-safe. The earlier task 348 note demonstrated
direct native Git traversal only. The Architect subsequently reproduced the
deletion **through baseline Gitops**: an ignored executor junction passed
`RequireClean`, then confirmed, non-forced `RemoveWorktree` deleted a synthetic
primary sentinel and `.git` before prune failed. All affected bytes belonged
to isolated OS-temp fixtures, not the real primary checkout or user data.
**After #510:** product regression tests require refusal before Git removal,
not merely test-harness unlinking of a junction.

| Teardown class | Before → after; retained behavior |
| --- | --- |
| Registered executor (`Git.RemoveWorktree`) | Before, confirmation + registration/non-primary + cleanliness permitted ignored Windows junction traversal. After, Windows inspects the requested root/parents/interior before canonicalization and again after Git preconditions, before removal. Confirmation still precedes access; registration, primary refusal, tracked/untracked cleanliness, non-forced Git removal, verified absence and prune remain. |
| Registered detached (`Git.RemoveDetachedWorktree`) | Before, registration/non-primary/detached allowed forced Git traversal without interior evidence. After, it uses the same two Windows checks. Detached-only, refusal of branch checkouts, disposable modified/untracked scratch, no extra confirmation, absence and prune remain. These restrictions cover **both** registered removers except the deliberately per-kind confirmation/cleanliness and detached gates. |
| Base reproduction (`Git.WithBaseWorktree`) | Before, this separate direct forced-Git teardown did not inherit registered-remover checks; generated detached ownership was treated as sufficient. After, Windows checks the fresh path before canonicalization and the original path before teardown; successful Git removal must leave it absent. It still creates its own unique detached path, performs direct forced Git cleanup without a branch-removal API, and does not prune here. Callback and cleanup errors are joined. Cleanup still attempts inspection/removal under `context.WithoutCancel`; cancellation does not bypass the safety check. Refused/failed cleanup names the preserved callback directory for human inspection. Initial-add/canonicalization failure cleanup remains non-recursive `os.Remove` of the generated empty path, now guarded too. |
| Bootstrap/configure scratch (`bootstrap.removeWorktree`) | Before, its separate primitive was `os.RemoveAll` then prune, not either registered Git remover. The owned Windows characterization fixture shows Go's old primitive preserving the junction destination; the baseline native-Git exploit is **not** attributed to Go `RemoveAll`. After, bootstrap nevertheless refuses observed Windows root/parent/interior hazards before its existing primitive. No recursive fallback is added. Only stage 1's freshly generated scratch paths reach this unexported helper. Init-bootstrap and configure share it; neither arbitrary-directory deletion nor a new public remover is introduced. |
| Bootstrap fresh empty path (`newWorktreeDir`) | Before, `MkdirTemp` → canonicalization → `RemoveAll` freed the generated name for `AddWorktree`. After, Windows inspects the requested path before canonicalization/removal, and only non-recursive `os.Remove` removes the fresh empty directory. Nonempty/replaced/unverifiable paths refuse. Existing unique-name, no-clobber/placement and generated-path boundaries remain. Unix canonicalization through legitimate symlinked ancestors remains. |
| Lifecycle cleanup (`run.Cleanup` / `releaseCheckouts`) | Before, only OpenCode inspected both interiors before any host mutation; other hosts bypassed the boundary and could delete the remote branch first. After, **every Windows host path** checks both executor and review hazards before remote/local branch deletion or any OpenCode release. The same remote → executor → review → local order follows successful preflight. Confirmation, phase/schema/configuration gates, final state save and metrics remain. A preflight refusal preserves branches, registrations, protected bytes, host locations and lifecycle state. A later failure can still follow earlier successful mutations. |
| Review replacement (`run.ReviewWorktree`) | Before, only OpenCode had the stronger pre-disposal interior boundary. After, every Windows host also refuses hazardous existing review paths before removal or reprovisioning. Full OID, phase/configuration gates, derived detached path, no-clobber, no run-state registration and no state/metrics write remain. |

The statement above that the remote branch **may already have been deleted**
is retained for late failures. **Before #510**, a non-OpenCode interior hazard
could be such a late failure; **after**, an observed Windows hazard in either
checkout is refused before that deletion. This is not a transactional rollback
promise for later filesystem or host changes.

### Shared inspection and exact scope

The old `run.inspectCheckout` implementation is moved into
`gitops.InspectWorktree`/`inspectWorktree`, not replaced by another remover.
OpenCode's stronger check still applies on **every platform** before disposal
and after handoffs. Windows generic Git, base and bootstrap use
`gitops.CheckWorktreeRemoval`; elsewhere that wrapper is a no-op, so legitimate
generic Unix symlink removal is **not** newly prohibited. A successful check
does not establish ownership, registration, cleanliness or authority to delete.
An absent path is allowed by the Windows wrapper solely so existing absence
and retry gates can decide its meaning; strict `InspectWorktree` requires a tree.

Inspection remains metadata-only, handle-rooted, cancellation-aware, inclusive
of ignored entries and bounded at 100,000 entries / 64 interior levels per
tree. Windows requested-parent inspection is bounded at 64 components. It
checks the original spelling before canonicalization can hide a redirected
root/parent; Windows `..` components refuse instead of being silently cleaned.
All observed links, junctions, non-regular entries, **all unqualified Windows
reparse attributes** (including tags that look ordinary to Go's type bits),
unavailable metadata and changed directory identities refuse. No reparse tag
is qualified for deletion here. Directory handle/path identities are checked
before traversal and again afterward; root and parent identities are rechecked
too. Observed redirections are not enumerated. Errors/bounds/cancellation are
not partial evidence of safety, and production never unlinks the hazard.

This is a refusal policy, **not atomic exclusion** of concurrent changes.
Ordinary type/identity metadata cannot establish the absence of every mount
alias on every filesystem. A tree can change after a successful observation,
between canonicalization/Git preconditions/reinspection/removal, or during a
host handoff. No new filesystem/host lock, safe recursive Git implementation
or host-wide quiescence is claimed. Keep targets unused during teardown.
Preserve refused paths and inspect ownership/links before any manual handling;
do not blindly run a force-removal command on the reported hazard.

### Complete touched-element inventory

| Element | Does its pre-#510 behavior still hold? |
| --- | --- |
| `gitops.CheckWorktreeRemoval` | New Windows-only refusal wrapper, including absent-path handling; no deletion/ownership authority. Non-Windows generic callers retain prior symlink behavior. |
| `gitops.InspectWorktree`, `inspectWorktree`, recursive walk and parent/root/child identity closures | Reuse the former `run.inspectCheckout` bounds, cancellation, ignored-entry metadata walk and handle-rooted opens. Add raw Windows parent evidence, before/after identity rechecks and actionable paths; incomplete inspection still refuses. No content reads or remover are added. |
| Windows and non-Windows `ordinaryWorktreeEntry` | New factored entry check. Type-bit rejection remains everywhere for strict inspection; Windows also refuses missing attributes and every unqualified reparse attribute. This is not just a junction-specific or review-only rule. |
| `Git.RemoveWorktree` / `Git.RemoveDetachedWorktree` | Earlier per-kind authority gates and #505 absence/residue behavior remain; both acquire Windows pre-canonicalization and pre-deletion inspection. Their deletion argv and prune sequencing remain. |
| `Git.WithBaseWorktree` / generated-empty-path cleanup closure | Unique detached ownership, direct forced removal and joined callback/cleanup errors remain; add Windows checks and post-removal absence. Cancellation still cannot skip cleanup, but a safety refusal now intentionally preserves and names the generated path. |
| `gitops` package/removal/base comments | Retain and correct the old assumption that detached/generated ownership prevented filesystem reachability into other work; executable Git setup/registration/path-comparison implementations remain unchanged. |
| `bootstrap.stage1AfterIssue` | Mutation order and successful-run versus failure reporting remain. Pass context to the fresh-path helper; successful-PR cleanup errors now say preserve/inspect rather than blindly remove. No real issue/PR policy changes. |
| `bootstrap.newWorktreeDir` | Fresh generated name and canonical placement remain. Add cancellation/Windows checks; replace fresh-empty `RemoveAll` with `Remove`, never recursive deletion of unexpected contents. |
| `bootstrap.removeWorktree` | Existing scratch `RemoveAll` → prune remains after the new Windows refusal. It is still private and reached only with stage 1's generated disposable path, not arbitrary user input. |
| `bootstrap.failClosed` | Still uses `WithoutCancel`, retains original/cleanup errors, attempts needs-human status, deletes only the pre-commit branch when Git permits and preserves committed branches. Leftover-path guidance now requires preservation/inspection before manual removal rather than recommending unguarded `git worktree remove --force`. |
| `run.releaseCheckouts` / `checkoutReleaseTarget` | Existing all-target-before-host ordering, OpenCode ownership/common-dir/container/branch/clean-or-detached checks, scoped release and post-release inspection remain. Add raw Windows preflight for all hosts; strict inspection calls the shared implementation. Non-Windows non-OpenCode hosts retain their Git-only behavior. |
| `run.Cleanup` / `run.ReviewWorktree` | Existing verb schemas/approval, phases, ownership, Git ordering and state semantics remain as detailed above; comments identify the expanded Windows boundary. No guard, role, run-state or approval schema changes. |
| `internal/gitops/inspect*_test.go`, `base_test.go` | Real-Git ignored-junction refusal and safe retry for both registered kinds; root/parent refusal; reinspection after read-only Git probes; base cancellation with joined errors and named residue; unsupported/missing attribute, absence/invalid-path and cancellation evidence. Deterministic child replacement refuses; Unix also exercises root replacement, while Windows proves Go's open root handle prevents that rename during inspection, not later exclusion. Unix executor/detached/base symlink compatibility is covered. Existing removal/absence/confirmation/dirty/branch tests remain. `recordingRunner.Run` now honors cancellation; its refused empty fixture is cleaned. New fixture-only `ownedJunction`, `removalRecorder`, `reparseInfo`, `inspectionStepContext` and `fmtRemovalKind` create/observe only synthetic cases, never a production fallback. |
| `internal/bootstrap/teardown*_test.go` / `bootstrapJunction` | New owned real registered scratch hazard refusal before deletion, canceled pre-commit/post-commit failure evidence, preserved branch/registration/primary bytes, safe retry, redirected temp-parent refusal, separately characterized baseline Go `RemoveAll`, and Unix scratch symlink compatibility. Existing stage 1 success/failure coverage remains. |
| `internal/run/checkout_release_test.go` / `openCodeCheckoutFixture` / `checkoutFixture` | Reuse real-Git/scripted-host assertions for replacement, executor cleanup and review cleanup; extend Windows cases to Claude and Codex as well as OpenCode, including link-free lifecycle retries. Existing no-host/no-Git-mutation, sentinel, metadata, registration and state assertions remain. Bounds/cancellation checks exercise the moved inspector. Fixture unlinking is explicit after refusal, not a product operation. |
| This guide | Prior #505/#506 statements remain explicitly historical before/after; add each teardown class, the shared rule's scope, evidence and remaining limits. |

`ListWorktrees`, `findWorktree`, `requireWorktreeAbsent`, `PruneWorktrees`,
branch-deletion primitives, creation/guard consumers, path canonicalization,
configuration, permissions and state/approval schemas are not redesigned.
Primary/unregistered-residue protection still applies to **both registered
removal APIs**; `WithBaseWorktree` and bootstrap instead retain their own
generated-path ownership boundary. The Windows hazard rule covers all these
known teardown classes, not just the symbol `RemoveDetachedWorktree`. It does
not make every filesystem deletion elsewhere in Orch a newly guarded operation.

### Verification and limits

Run the four Go gates and both adapter commands listed above; require empty
`gofmt -l .` output and format only changed files. The unchanged smoke is a
**required local Windows** gate, not a hosted-CI command. Its private simulated
host must report zero provider requests and normal owned-service shutdown.
Fixture unlinking occurs only after product refusal, for separate success tests;
it is not production mitigation. The characterization test's synthetic
`RemoveAll` is evidence about the old bootstrap primitive, not a new fallback.

The OpenCode release implementation/tests, smoke harness, adapter README,
configuration and host/role permissions are outside #510's edits. Source and
isolated fixture results do not qualify the installed engine, manual/live
Delivery, alias handling owned by task 346, or a runtime-version advisory change.
No installation, service restart, process killing, historical-residue recovery
or deletion, recursive fallback, or Memhub semantic/admin change is introduced.

## Windows OpenCode command binding (issue #513 / task 349)

**Before #513:** the #506 table above correctly recorded reuse of the unchanged
runtime/runner implementations at that time. Shared `Resolve` nevertheless kept
only a bare V2 command name after version qualification, so later API calls
resolved PATH again. On Windows the default npm `opencode2.cmd` native launcher
forwarded `%*` through batch interpretation. A fixed benign read-only
`GET /api/session?limit=1&order=desc` failed with exit 1/non-JSON through the
installed shim, while its native executable returned exit 0/valid JSON.
Cleanup therefore refused safely with
`OpenCode checkout release: host get request unavailable or unsupported; preserve the checkout and inspect the host before retrying`.
That refusal did not delete a checkout or branch. It demonstrated transport
corruption, not exploitation of arbitrary command injection.

**After #513:** Windows production resolution uses `Local`'s existing injected
lookup to verify a native executable before `--version`, then retains its
absolute canonical path for **all** subsequent model, plugin, skill and
checkout-release requests. A qualifying version no longer means a batch
launcher is admitted for API transport. Non-Windows command behavior and
unrelated `Local` commands are unchanged. The old blanket argv-implies-no-shell
claim is retained and corrected in the `execx` package comment and adapter guide.

Supported Windows candidates are a direct native `opencode2.exe`/`opencode.exe`,
or npm's known native `.cmd` template associated with the selected prefix's
`node_modules/@opencode/cli/bin/opencode.exe`. The latter requires matching
`@opencode/cli` package identity, the selected name's native `bin` mapping,
canonical containment and filesystem identity evidence. Both names obey the
same rule; preferred `opencode2`, V2-only `opencode`, version floors and newer
unverified-version advisories remain. Legacy V1 is never silently admitted.

Other/custom/PowerShell-only launchers, malformed or missing package/bin
metadata, escaping links and unverifiable/nonabsolute native paths refuse
without executing the launcher. The diagnostic begins
`cannot verify a native Windows OpenCode executable` and recommends a direct
native executable or intact supported npm layout on PATH. Preserve artifacts
and inspect/correct the installation; do not run arbitrary wrappers, add a
shell-quoting workaround, relay credentials or infer permission for manual
deletion. An unsupported older package layout may require explicitly selecting
its verified direct native executable, not relaxing this association check.

### Touched boundary and retained teardown behavior

| Element | Before → after; does prior behavior still hold? |
| --- | --- |
| `execx.Local.ResolvePath` / `Local.Run`, `Cmd` and package comments | Run's existing lookup is factored into a callable method; execution, cwd/env/stdin, exit/error and cancellation semantics remain. Comments now correct the historical shell-safety claim. No universal native-only policy is imposed on git, gh or other CLIs; `Runner` is unchanged. |
| `opencode.Runtime` / `Resolve` | The bare-name binding is removed on Windows production Local; qualification and every later API call now share one verified absolute native path. Version parsing/floors/advisories and safe projected errors remain; scripted command namespaces and non-Windows selection remain. |
| `windowsExecutable` / `containedFile` | New narrow direct/native-npm verification, canonical containment and ancestor identity checks; no general wrapper parser, sibling-binary guess or shell execution. This covers **both command names and all shared API consumers**, not just `ReleaseLocation`. |
| Catalog/plugin/skill, doctor, setup detection and activation consumers | Caller implementations and their project/capability/plugin/skill/routing checks remain. They inherit the corrected shared binding; no URL caller or raw HTTP authentication path is redesigned. |
| `ReleaseLocation`, `releaseActivity`, other release helpers and engine handoff/removers | Implementations remain unchanged. Exact returned cache keys and independent alias preservation, target/project attribution, active/job/shell/PTY/persistent-terminal checks, valid bounded pagination, ownership/confirmation, interior-hazard and absence/residue refusals all remain. No broader eviction, host restart, session deletion or process termination. |
| Windows native-process/npm fixtures and regression cases | New production-runner evidence distinguishes the fixed benign old failure from native success, verifies both names/direct/npm layouts and later-PATH-change pinning, refuses unassociated/escaping/missing natives without execution, and exercises valid multi-page release plus malformed/unavailable/repeated/nonadvancing refusal. Existing safety tests remain. |
| Isolated smoke layout, source probe and graph preparation/test | The old top-level private native executable masked the shim defect. Windows now exposes only npm launchers at that name and keeps the native binary inside its verified package; actual Orch doctor/lifecycle use that PATH. A source-only Resolve/Local consumer checks real two-page transport separately from release's deterministic `limit=100` pagination. It builds offline/read-only from the existing pinned source graph/checksums in an external fixture; no tracked module change or dependency download. |
| Adapter README and this guide | Preserve historical statements and record this correction, supported/refused layouts, full per-element inventory and qualification limits. Dependencies/baselines, roles/configuration/permissions, state/approval schemas, other hosts and unrelated runtime-warning channels are unchanged. |

The complete symbol/fixture/evidence inventory is in the
[adapter binding blast radius](../adapters/opencode/README.md#binding-evidence-and-complete-blast-radius).
The native-only restriction is specific to Windows OpenCode resolution through
the production Local boundary, **not every `execx.Local` invocation**. It
applies to every shared OpenCode API consumer, not only the release symbol.
The existing Windows teardown hazard rule still covers every teardown class
enumerated under #510; this change does not expand or weaken those classes.

### Verification and qualification limits

Run the four Go gates and both adapter commands already listed, including the
exact required **local Windows** `npm run smoke --prefix adapters/opencode`.
Hosted CI does **not** run that smoke. Retained evidence must identify the
committed source/adapter, selected shim/manifest/native executable, transport
observations, zero real-provider requests and normal verified owned-host
shutdown. Source/isolated evidence is not installed-default-PATH or manual/live
Delivery qualification; no installation or shared-host mutation is included.

Executable path pinning is not an atomic filesystem anti-replacement guarantee.
The existing bounded filesystem/activity/cache observations likewise cannot
exclude concurrent replacement or host re-entry before Git removal. Keep
targets unused during explicit operations. Broader orphan recovery and
unrelated advisory-channel work remain separate; no historical residue is
recovered or deleted here.

## Keep both OpenCode checkouts released (issue #525)

### Before and after

**Before #525:** the #506/#523 handoff above validated both checkout trees,
then called `ReleaseLocation` independently for executor and review. Each call
walked all session histories and persistent-terminal inventories, including
completed children still assigned to the other checkout. The later preflight
could therefore load an already released executor. Its return was not followed
by a combined observation that **both** targets were still unloaded, or a
Windows directory delete-access check before remote-branch deletion. Successful
scoped DELETEs alone did not establish a usable Git handoff.

**After #525:** `releaseCheckouts` submits all verified targets to one
`ReleaseLocations` call. Global foreground/jobs, paginated persistent terminals,
each loaded target's project roots and every loaded exact key's ordinary
shell/PTY inventories are checked against **all** targets before either
eviction. **Before the #547 review fix:** no cleanup-owned session, terminal,
location-root or ordinary-process probe ran after the first eviction; only
exact scoped DELETEs and a final, non-loading `GET /api/debug/location`
remained. **After:** the handoff still ends that way, but issue cleanup adds
[late checks of surviving cache keys](#direct-opencode-cleanup-issue-547).
The final handoff inventory must show neither
target nor descendants and preserve every unrelated exact cache key and its
physical identity. Both original target identities must still match.

On Windows, each target must additionally open with the `DELETE` access right
and read/write/delete sharing, `OPEN_EXISTING`, `BACKUP_SEMANTICS` and
`OPEN_REPARSE_POINT`. The opened handle's identity must match the original
directory, and the handle is closed immediately. This only observes access:
it never sets delete disposition, renames, removes or retains a teardown lock.
A directory held without delete sharing, denied permissions, identity failure
or unavailable evidence refuses before remote-branch deletion or either Git
deregistration. The safe diagnostic names **Windows directory delete access
unavailable**, separately from **target reactivated**; neither includes raw host
output, session IDs, credentials or native error payloads.

One shared two-minute deadline and at most **one** unrelated-cache disappearance
recheck now cover the entire batch, rather than a new budget/recheck per target.
The #523 exception otherwise remains: unchanged missing-directory identity,
fresh full read-only activity/root checks, no scoped read of that missing key,
no second churn, no disposal retry and strict final unrelated-key preservation.
Zero verified targets still require no host calls. Single-review replacement
uses the same algorithm with one target; `ReleaseLocation` remains its
single-target API wrapper.

Completed children stay assigned to their worktrees. Neither their histories
nor assignments are moved or deleted. Unregistered residue is still never
evicted or repaired. All ownership, confirmation, phase/configuration,
clean-executor/detached-review, interior-hazard and absence checks remain;
post-handoff interior inspection still precedes Git. The Git deletion order
remains remote → executor → review → local, with state saved only on success.
An eviction or later failure can still leave an idle target unloaded; no
rollback, shared-host restart, process kill or force-removal fallback is added.

### Complete touched-element inventory

| Element | Does its earlier behavior still hold? |
| --- | --- |
| `run.releaseCheckouts` | All-target ownership/interior verification before host mutation, Windows raw-path inspection for all hosts, post-handoff interior reinspection and absent/unregistered handling remain. **Before:** independent sequential host handoffs. **After:** one batch, with no loading host preflight between evictions. |
| `checkoutReleaseTarget`, `Cleanup`, `ReviewWorktree` and generic Git consumers | Implementations unchanged. Engine-derived placement, Git registration/common-directory/branch authority, executor cleanliness versus review detached-only, confirmation, lifecycle gates, Git order, residue/absence, final state and metrics semantics remain. Only the shared OpenCode handoff changes; generic Git, base/bootstrap and other hosts do not acquire this access probe. |
| `ReleaseLocation` / new `ReleaseLocations` | Single-target calls still work through the wrapper. The new batch performs all activity/root probes before disposal and refuses if either target remains/reappears afterward. Empty batches do no host work. The deadline and one-recheck allowance are now shared across targets, not renewed per checkout. |
| Runtime resolution, `releaseCall`, `releaseParameter`, OpenAPI validation and `call` closure | Existing authenticated, pinned native executable, explicit deep-object contract, response validation, cancellation and safe projected errors remain. Resolution/contract discovery happen once per batch. No new host endpoint, credential transport or response relaxation. |
| `releaseLocation`, `targetInfos` / `targetIndex`, `inventory` | Exact keys remain independent from filesystem identity. Original identities are retained for every target; duplicate requested physical targets refuse. Each inventory checks both targets for ambiguous aliases and unexpected descendants, retaining all prior caps/malformed/unverifiable refusals. Debug inventory never loads a location. |
| `preserve`, `unchanged`, preflight/recheck loop and target-root validation | Original identity, exact-key preservation and the #523 bounded exception remain. Both intended targets, rather than one, may disappear through scoped release; unrelated aliases are never substitutes. Root checks use each target's corresponding exact key. A busy review now refuses before executor eviction, not during a later independent handoff. |
| Scoped DELETE loop and final observations | Exact encoded keys, no DELETE for unloaded targets, no retry and safe failure remain. **Before:** final observation applied only to the current target. **After:** after all evictions, one non-loading observation requires every target absent and every final unrelated key preserved; Windows access and original identity checks also gate return to Git. |
| `releaseBelongs` / unchanged `releaseDirectory` | Absolute/canonical/filesystem evidence and segment-aware owner/cwd attribution remain. Attribution now tests every target, so one shared response cannot overlook activity in the other checkout. No new path normalization or idle inference. |
| `releaseActiveJobs`, `releaseActivity`, `releaseOrdinaryProcesses` | Foreground drains; background owners and actual subagent children; bounded, validated session pagination; persistent owner/cwd; every exact loaded shell/PTY owner/cwd; exited-PTY handling and all caps/refusals remain. The existing twice-per-pass active/job checks and once-per-pass history/ordinary scans are shared by all targets, rather than repeated after releasing a sibling. |
| New Windows `releaseDeleteAccess` and non-Windows counterpart | New non-mutating Windows delete-access/handle-identity observation, with immediate close and safe refusal. The non-Windows implementation is a no-op; it does not impose Windows sharing semantics elsewhere. Neither implementation establishes deletion authority or excludes later clients. |
| `checkoutHost.Run`, `sessions` / `reactivated` fixture state | Existing real-Git/scripted-host behavior remains. Sorted completed-session inventories and terminal reads deterministically model reloading an assigned but evicted checkout; no live host/provider or user session is involved. |
| `TestOpenCodeCleanupDoesNotReactivateReleasedCheckouts`, `TestOpenCodeCleanupFinalReactivationRefusesBeforeGit` | New old-sequential characterization plus batched no-loading-probes/history-assignment assertions, and external reactivation of either target at the final observation. Refusals preserve both registrations, branches and phase with zero Git mutation. |
| `TestOpenCodeCleanupReleaseAndPartialRetry`, `TestOpenCodeCheckoutReleaseEviction` | Existing success/unloaded/partial-retry, #523 fresh-activity/churn/final-refusal checks remain. Add busy/descendant/ambiguous-alias cases for both cleanup targets. Eviction expectations now reflect all-target preflight before any disposal and final batch checks after both evictions; no refusal is weakened. |
| New `TestOpenCodeFinalWindowsDeleteAccessBeforeGit` | Real no-delete-share directory handles are introduced only at final preflight for executor cleanup, review cleanup and single-review replacement. Each refuses before all Git mutation; closing only the owned fixture handle permits normal retry. No process or session is moved/killed. |
| Smoke `nativeLookup`, release source identities, `completed_children` and `cleanup_artifacts` | Existing isolated source/explicit selection, all role/lookup/guard/activity/alias/link refusals, single-review replacement, unrelated live PTYs/lookups, provider trap and verified owned-host shutdown remain. Add completed implementer/reviewer children in both targets, connected bound Memhub before release, unchanged assignment/history hashes afterward, Git-registration absence and explicit local/remote ref observations (`show-ref --verify --quiet` distinguishes absent refs from errors). No shared-host query/mutation or runtime adapter change. |
| This guide | Earlier contracts remain as historical before/after evidence, including late-failure/race limitations. Adds the changed ordering, Windows access observation, full per-element inventory and isolated qualification; no role, permission, configuration, dependency or state schema changes. |

The unloaded-target, ambiguous-target-alias, descendant, activity, exact-key
preservation and Windows delete-access rules cover **every target in both
entry points**, not only a symbol called `ReleaseLocation` or
`RemoveDetachedWorktree`. Detached-only remains **review-specific**;
confirmation/cleanliness/owned branch remain executor-specific. This is not a
new restriction on every generic worktree remover or every Windows filesystem
deletion in Orch.

### Regression, Windows evidence and remaining race

`TestOpenCodeCleanupDoesNotReactivateReleasedCheckouts` first characterizes the
old two-call sequence: the second persistent-terminal scan reopens the released
executor while both completed sessions retain their assignments. Against the
old production sequence its cleanup subtest fails; the batched implementation
passes with no loading host requests after the first DELETE. This is a
deterministic interleaving, not a claim that every observed live review reload
has that cause. The earlier mediated shared-host evidence also admitted
asynchronous warming/location-scoped effects; its exact review reload cause
remains unproven and was not replayed on user worktrees.

The final-reactivation and real Windows handle regressions distinguish the two
safe refusals and assert no branch deletion, Git-registration change or phase
write. Existing single-target alias/activity/pagination/deadline regressions and
the all-target #523 lifecycle recheck tests remain required.

On 2026-11-19, the new regression against baseline `b74fb7e` failed on
cleanup-owned executor reactivation and on accepting final executor re-entry.
After the fix, the fresh focused regression passed, including both targets'
busy/descendant/alias refusals and all three real Windows held-directory/retry
cases. `go build ./...`, `go vet ./...`, `go test -timeout=20m ./...` (728.4s),
empty `gofmt -l .` and all 43 adapter unit tests passed. A Linux/amd64
cross-build also passed, and changed-package `golangci-lint` reported zero issues.

Run all six required commands listed above, including the local Windows smoke
that hosted CI does not run. The smoke's `checkout_release.completed_children`
records bound status/locate evidence, connected Memhub before release, completed
session assignments and before/after history hashes. Its cleanup evidence must
show both checkout directories/registrations and local/remote branch refs
absent, with primary/unrelated locations, exact alias keys and live unrelated
PTYs still usable. Before #539 the full isolated `evidence.json` was retained
on pass. Now the pass receipt keeps source-file/binary/OpenAPI hashes, check
outcomes, zero provider requests and shutdown proof, not the raw cleanup
payloads; the full file remains part of a verified failure archive. Neither
format is an installed qualification claim.

The final **2026-11-19 Windows source-build smoke passed all 35 checks** on
OpenCode `2.3.2` (the unverified-version advisory remains), Node `v24.15.0`,
Go `go1.26.5 windows/amd64` and the existing SDK/simulation `2.2.6` dependencies.
Retained fixture:
`%LOCALAPPDATA%\Temp\opencode\orch-opencode-smoke-n8TUw3\`.
`evidence.json`, `checkout-release-openapi.json` and `server.log` identify the
source/harness/binary bytes and host contract; `issue525-verification/` retains
the required-command logs, baseline failure and fresh passing regressions.
The final smoke's recorded source and harness hashes were independently
compared with the issue worktree afterward and matched.

Both actual `orch-implementer` and `orch-reviewer` children had outcome
`succeeded`, remained assigned to their respective checkouts and retained
identical before/after history hashes. Both checkout-bound Memhub services
were connected immediately before release. Both checkout directories and Git
registrations disappeared; remote heads were empty and the quiet local-ref
probe returned absence (exit 1). Only the two intended cache keys disappeared
(11 → 9); primary/unrelated native lookups, independent alias keys/identities
and live alias PTYs survived. Provider requests were **zero**, artifact
verification reported unchanged, and verified owned-service shutdown exited 0.
An intermediate smoke reached successful cleanup but failed a harness-only
non-quiet `show-ref` exit-code assertion (128 versus 1); the corrected quiet
probe and complete final smoke passed. None of these runs touched the shared
host, installed artifacts or active user worktrees.

**Remaining race:** the final non-loading host inventory, Windows access probe,
post-release interior reinspection and subsequent Git operations are bounded
observations, not atomic exclusion. External clients or asynchronous host work
can reactivate a target or acquire a handle after the checks; an access probe
does not certify every descendant handle or make Git transactional. Keep both
targets unused throughout cleanup. Late failures still preserve residue under
#505; no session relocation, manual residue recovery, service restart or
process-kill fallback is authorized by this change.

## Complete Windows persistent-terminal scans (issue #537)

Windows checkout release uses the same authenticated native OpenCode `api`
command for unfiltered session pages and per-session terminal lists. A page
and its continuation must validate; a repeated session ID, repeated cursor
(even across nonadjacent pages), or empty continuing page refuses. The engine
validates every terminal list before treating the scan as complete. A
successful scan skips no session based on age, owner activity, or location.
A running terminal's owner location or independent cwd in any target still
refuses.

**Before the #537 review fix:** ordinary JSON decoding could merge repeated
object members or let a later value replace an earlier one, hiding a continuation
cursor or a running terminal. **After:** every object in a Windows session page
or persistent-terminal list must have unique decoded member names, including
case equivalents the Go decoder would match. Other host responses and the
non-Windows path keep their existing decoding.

At most eight terminal-list reads can run at once **in each shared Windows
release**, across all requested checkouts. Pages are fetched one at a time;
workers finish or are canceled and joined before the next page, a refusal,
or any eviction. A deadline expiring during a host command is reported as a
timeout, not an unsupported request. The one allowed preflight recheck still
uses the remaining time on the original two-minute deadline. **Before the
#547 review fix:** only exact scoped evictions and non-loading debug inventory
could run after eviction started. **After:** issue cleanup can additionally
read ordinary shell/PTY inventories under verified surviving keys; it does not
repeat the persistent-terminal scan or probe released targets.

### Touched elements and retained behavior

| Element | Before → after; does the prior behavior still hold? |
| --- | --- |
| `ReleaseLocations`, `request`/`call` and `releaseContextError` | Requests still use the batch context; Windows terminal reads use a cancellable child that inherits its deadline. Windows reports actual cancellation/timeout without disclosing subprocess errors or endpoint data. Runtime resolution, authenticated argv/cwd, OpenAPI validation, unloaded-target handling, exact cache keys, detected drift, one-recheck gate, scoped DELETEs, final inventory and delete-access checks remain. Non-Windows keeps its previous request/error behavior. |
| `releaseActivity` and `releaseSession` | Before, the helper ran the first active/job check and then up to ten history pages. Now the caller runs the same active/job check before the extracted history scan. Windows replaces the page ceiling with the shared deadline and rejects any reused continuation cursor, not just the current cursor. The existing session-row shape, 100-entry limit, unique IDs, opaque cursor encoding and absolute owner paths remain. Only Windows applies the member-name guard to every page before using any row or cursor; non-Windows retains its prior pagination, decoding and order. |
| `releaseTerminalPage` and `windowsTerminalReads` | New bounded workers for one validated page at a time. A failed read or blocking activity cancels the other workers, but the engine waits for every issued read to finish or exit on cancellation before returning. The cap applies to every persistent-terminal list read in a Windows release batch, not to ordinary PTY/shell, foreground/job or generic `execx.Runner` calls. |
| `releaseSessionTerminals`, `releaseBelongs` and `releaseDirectory` | Session-scoped authenticated GETs, 1,000-entry list cap, expected owner IDs, supported running/exited states, absolute cwd and physical/descendant attribution remain. Windows also refuses duplicate terminal IDs within each session response and applies the member-name guard before using any terminal row. Exited terminals still do not count as running. No broader abort, process termination, session deletion or changed path-identity semantics. |
| `releaseTerminalEvidence`, `windowsReleaseEvidence.UnmarshalJSON` and `checkReleaseEvidenceNames` | Before, repeated or case-equivalent decoded member names could merge or overwrite values. Now the guard rejects such names per object, including nested objects, before the same typed decode, without exposing response content. This applies to all Windows release session pages and persistent-terminal lists, not to other JSON reads or non-Windows. |
| `releaseActiveJobs`, `releaseOrdinaryProcesses`, target/root/inventory checks, and `releaseDeleteAccess` | Unchanged checks for foreground drains, job owner/child, every exact loaded shell/PTY key and its owner/cwd, target and unrelated location identities, and Windows delete-access. Existing bounds remain. Only the history scan overlaps; no check can reload a sibling after eviction begins. |
| `ReleaseLocation`, `releaseCheckouts`, `checkoutReleaseTarget`, `Cleanup`, `ReviewWorktree` and Git removers | Both Windows lifecycle entry points inherit the shared scan. Refused preflight still performs no checkout or branch removal or issue phase change. Ownership, confirmation, clean executor vs. detached review, interior checks, verified absence, residue preservation and Git order remain. Generic removers and other hosts do not acquire this host scan. |
| `releaseFixture.run`, recheck diagnostic case, and `TestWindowsReleaseTerminalWorkers` / `TestWindowsReleaseRejectsIncompleteTerminalEvidence` / `TestWindowsReleaseRejectsAmbiguousTerminalEvidence` | Fixture call/deadline recording is concurrency-safe; earlier refusals remain. Barriers prove eight reads overlap, no release while blocked, and cancellation is joined on error and mid-call deadline. The simulated recheck consumes the remaining 30 seconds of the original deadline. New pagination and terminal validation cases, including repeated, escaped, ASCII and Unicode case-equivalent member names, refuse before DELETE on fixture responses alone. |
| `checkoutHost.Run` / `sessions` / `terminalCwds` and `TestWindowsOpenCodeCheckoutReleasePastFirstThousand` | Existing real-Git/scripted-host fixtures and reactivation model remain. Synchronized fixture state, multi-page results and a late terminal exercise both success paths and owner/cwd refusals at session 1,001. Refusals preserve registrations, branch tips and run state. Unexpected worker commands still fail the test but now report and return an error rather than calling `Fatal`/`Fatalf` from the worker. |
| `smoke.mjs` `checkout_release.source_files`, this guide and adapter README | The existing Windows smoke behavior stays the same; its source evidence also hashes the extracted scan and the new Windows tests. The guides retain the old bound as before/after. No adapter runtime, credentials, dependencies, configuration, permissions, state or installation changes. |

### Isolated Windows qualification

On 2026-11-21, one private OpenCode V2.3.2 service on Windows x64
(build 26300) was seeded through the native CLI with synthetic idle sessions.
The following historical timings used production source matching `f85aa22`,
before the member-name guard. The evidence records the pre-commit base HEAD
and the production file hashes, not a run at the review-fix revision.
No prompts or provider requests were made. For each cleanup pair, the
same host and session IDs were used, with both target checkouts reprovisioned
and loaded before timing. The sequential control was the same source built
with a temporary one-worker overlay, not the previous ten-page implementation.
The timings cover the complete Orch command, including Git, not just the
terminal reads.

| Operation | Saved sessions | One worker | Eight workers |
| --- | ---: | --- | --- |
| Cleanup (both checkouts) | 128 | 15.514 s, succeeded | 5.848 s, succeeded |
| Cleanup (both checkouts) | 1,001 | 99.662 s, succeeded | 22.779 s, succeeded |
| Review replacement | 1,001 | Not measured | 22.885 s, succeeded |

The 1,001-session inventory returned ten 100-row pages, one 1-row page
and the terminating empty page. Cleanup verified both caches, directories,
Git registrations and branch refs were gone. Replacement verified the old
review cache was gone and Git checked out the new detached OID without writing
run state. Inventory IDs and primary cache were preserved. The earlier
13.464 s vs. 2.737 s sample measured only 128 CLI reads, not a full release.
The isolated qualification script, overlay, source/binary hashes and outcomes
are retained at `%LOCALAPPDATA%\Temp\opencode\issue537-qualification-OWWNk3\`.

The Windows regression with the old ten-page implementation refused both
idle releases before reaching session 1,001. With the new implementation the
same fixture released both idle checkouts and refused a running terminal on the
last page in both lifecycle paths. The required source-build Windows smoke also
ran against the prior source: all 35 checks passed with zero provider requests
and verified owned-service shutdown. Its `evidence.json` and OpenAPI observations
are retained at
`%LOCALAPPDATA%\Temp\opencode\orch-opencode-smoke-OXQorh\`. These results do
not qualify an installed or shared user host.

The review-fix source was measured separately with the same script and
one-worker control on Windows x64 build 26300, with a fresh private OpenCode
V2.3.2 service and synthetic inventory. Each comparison used the same session
IDs and reprovisioned targets within the pair. All five commands released
their targets successfully; the provider-request count was zero and the owned
service stopped. The 1,001-session run again reached the terminating empty page.

| Operation | Saved sessions | One worker | Eight workers |
| --- | ---: | --- | --- |
| Cleanup (both checkouts) | 128 | 15.427 s, succeeded | 5.639 s, succeeded |
| Cleanup (both checkouts) | 1,001 | 101.617 s, succeeded | 23.644 s, succeeded |
| Review replacement | 1,001 | Not measured | 22.294 s, succeeded |

The review-fix artifact is
`%LOCALAPPDATA%\Temp\opencode\issue537-qualification-zsF5ow\evidence.json`
(SHA-256 `4da77dc670389924e015cadd98d366343ae0292c8a2ae9990ec63d681b1229f7`).
It records the pre-commit HEAD `f85aa22` plus modified source status. Its
`release_terminals.go` hash `77d040011fa1185e13d30807e82949b3fec4c7491f1f8f9c2a62c352b3a488ba`
identifies the hardened production file, not the old committed version.
Ambiguous responses were exercised only in deterministic fixtures.

## Code Mode holds during checkout release (issue #543)

**Before #543:** the running-job inventory recognized `shell`, `image` and
`subagent`. Code Mode, unknown job kinds, and rows with a missing ID, missing
kind or unexpected status all produced `unsupported background-job evidence`.
They prevented removal, but the diagnostic did not identify a Code Mode hold.

**After #543:** a running `codemode` job with a verified owner in a target
checkout prevents removal because the owning session is in the target.
A job with a verified owner outside all targets also prevents removal, with
a distinct explanation: its working directories cannot be verified. The
observed OpenCode 2.3.2 job record lists `kind`, `id`, `sessionID`, `status`,
`title` and `time`; it does not show where nested tools work. An owner in
primary, a caller ID matching the owner, or a job title is not proof that the
job is unrelated. Unknown kinds, malformed inventories or rows, and
unverifiable owners still refuse with evidence errors, not Code Mode
diagnostics. No diagnostic repeats the job's title, IDs or raw payload.

### Resume normal cleanup from a standalone terminal

In **v0.17.0** this was the recovery path when cleanup inside OpenCode
blocked itself. It remains available for explicitly legacy or unbound
cleanup. Earlier direct-tool guidance also suggested this path for bound
holds. A bound caller now waits, re-confirms the run and issue, and retries
[the same direct tool](#direct-opencode-cleanup-issue-547) with its bound checks.

1. Let the job finish and wait for OpenCode to be idle. Keep both issue
   checkouts unused.
2. Open a standalone foreground terminal outside OpenCode, not its shell tool
   or Code Mode. Change to the intended repository's primary checkout. Confirm
   that `git rev-parse --show-toplevel` matches the first `worktree` entry in
   `git worktree list --porcelain`, and use `orch run status --json` to check
   the run, issue number and merged or abandoned phase.
3. For confirmed cleanup, send the normal request to `orch run cleanup` on
   stdin. In this PowerShell example, replace `123` with the verified issue number:

   ```powershell
   '{"schema_version":1,"issue_number":123,"statement":"cleanup-issue"}' | orch run cleanup
   ```

This starts a separate **unbound** invocation. It retains the legacy host, Git
and filesystem checks but not the direct tool's run/checkout binding, recorded
PR destination and approved-ref checks, or late global job gate. With a verified
OpenCode target, a still-running Code Mode job again stops legacy cleanup
before eviction or Git deletion. If you were replacing a review checkout
rather than cleaning up an issue, rerun that authorized operation instead. Orch does
not retry a Code Mode hold, end the job, kill processes, delete paths manually,
install anything or bypass a check. These remain bounded observations, not a
guarantee against concurrent host re-entry.

### Touched elements and retained behavior

| Element | Before → after; does its earlier behavior still hold? |
| --- | --- |
| `releaseActiveJobs` session lookup (`sessionInTarget`, `checkSession`) and `releaseBelongs` / `releaseDirectory` | Before, the lookup combined identity/location verification and the foreground or legacy job hold. The same session-ID check, authenticated read, exact response ID match and physical/descendant attribution remain. The lookup now returns the location decision separately for Code Mode; foreground and legacy holds keep their old diagnostic and order. |
| `releaseActiveJobs` running-job inventory and kind dispatch | The same endpoint, running filter, 1,000-row cap and missing/invalid inventory refusals remain. Unknown kinds still refuse as unsupported; malformed rows get a separate evidence error. Shell and image still check the owner. Subagent jobs still check the parent owner and actual child; either can block. Code Mode still blocks, but is now identified after owner verification. The outside-owner restriction applies to **every** running `codemode` record, not just the invoking session or a job with a particular title. |
| `ReleaseLocations` and `ReleaseLocation` | Both global active/job checks still run before any eviction on a successful preflight, for every requested target (including unloaded ones). The single-target wrapper uses the same gate. Persistent terminals, ordinary shells/PTYs, root and inventory checks, the bounded unrelated-key recheck, exact scoped disposal and final checks are unchanged. No check or retry runs after a Code Mode refusal in the same invocation. |
| `run.releaseCheckouts`, `checkoutReleaseTarget`, `Cleanup`, `ReviewWorktree` and generic Git teardown | Both confirmed issue cleanup (executor and review) and review replacement inherit the shared Code Mode gate when there is a verified OpenCode target. Ownership, confirmation, eligible phases, clean executor vs. detached review, interior checks, absence/residue handling, Git order, run state and metrics semantics remain. A hold reaches neither eviction nor Git deletion. No new rule applies to other hosts or direct Git, base or bootstrap teardown. |
| `TestReleaseLocation` / `releaseFixture` | Existing scripted coverage remains. New cases distinguish target and primary Code Mode owners, unsupported and malformed evidence, and invalid or unverifiable owner identity without leaking fixture content. Shell, image and subagent controls confirm their existing policies. No live host is used. |
| `checkoutHost.Run` / `jobInventory` and `TestOpenCodeCleanupCodeModeHoldThenRetry` | Existing fixture defaults to an empty job inventory; it can now supply running-job responses and look up saved owners. The new test calls ordinary cleanup twice with the same primary-owned job and matching caller ID, preserving both checkouts, registrations, branch refs and state without eviction or an internal retry. A later call with a clean inventory evicts only the fixture targets and removes them normally. The host is scripted and Git uses only disposable repositories. |
| This guide | The old generic Code Mode refusal is preserved above as before/after. The foreground recovery is for this hold, not new permission to remove residue or to skip lifecycle checks. Previous qualification is historical; these regressions do not qualify new installed or live behavior. |

## Direct OpenCode cleanup (issue #547)

**Before (through v0.17.0):** an Architect who ran `orch run cleanup`
inside Code Mode could be blocked by that very job. Its owner in primary
did not prove where its nested tools ran. The safe recovery was to let it
finish and run the confirmed command in a standalone terminal.

**After:** on the matching engine and adapter, the Architect invokes
`orch_cleanup` as a direct OpenCode tool, **not** inside `execute`, from
the verified primary session. Confirm the exact run ID and merged or
abandoned issue from fresh `orch run status --json`, then pass
`{"schema_version":1,"run_id":"<confirmed run>","issue_number":123,"statement":"cleanup-issue"}`.
The tool runs the same engine verb without creating a Code Mode job. It waits
for the engine result; only the matching `cleaned` result is success. It
does not queue, retry a refusal, or resume work after another job finishes.
An interrupted or missing result is not evidence of success; inspect run
status before issuing another confirmed request. Do not replace a running
Delivery engine to make the direct tool available.

The protection applies to **every** running Code Mode job, not only
`execute` from a particular caller, job title or session. A separate
Code Mode job with unverifiable nested directories is still a hold.
Shells and terminals retain their existing owner/cwd checks; ordinary
session and background-job attribution still checks the appropriate owners
and subagent children. Keep both disposable checkouts unused. For a genuine
hold, let the job finish, re-confirm the current run and issue, then retry
`orch_cleanup` directly from the verified primary session, outside Code Mode.
The [standalone procedure](#resume-normal-cleanup-from-a-standalone-terminal)
is for legacy or explicitly unbound requests, not a fallback for a held or
unavailable bound invocation.
If identity or ownership is uncertain, preserve the paths and inspect the
diagnostic; waiting alone does not clear that refusal.

The direct path adds a final ownership, ordinary shell/PTY cwd, job and cache
observation before disposal. It also rechecks checkout identity before Git
deletions. Before #547, a clean executor on the recorded branch could still
contain commits newer than the approved PR. The direct path now compares a
merged executor's initial HEAD to the issue's recorded delivered PR head
before accepting the checkout as a removal target. It checks the exact local
and remote branch refs, even on a retry with an absent executor. A present ref
is removed only if Git still sees the approved OID. An already-absent branch
is fine, but a different tip or unverifiable ref is a refusal, not a reason
to rerun the standalone command. This head requirement applies to bound
**merged** cleanup, not abandoned issues, standalone cleanup or review
replacement.

**Before the #547 review fix:** the last host activity checks preceded
eviction. A primary-owned shell or PTY could start in a target cwd during the
subsequent Git lookup or push without loading that target. **After:** confirmed
OpenCode cleanup (direct or standalone) checks ordinary shells and PTYs under
every verified surviving cache key after the remote lookup, after a successful
push, and again before removing a remaining review checkout. Each scan is
bracketed by non-loading inventories: a reactivated target, changed inventory,
running work in a remaining target, or unverifiable cwd is a refusal. The
same release deadline, original filesystem identities and Windows delete-access
checks apply. There is no second session-history scan, no target-scoped probe
and no new eviction in this late check. All original pre-eviction foreground,
job, persistent-terminal and shell/PTY holds still apply.

**Before #549:** those late checks did not read running jobs. **After #549:**
bound merged cleanup also reads the global running-job inventory at those
boundaries, inside the same cache-inventory bracket and original deadline.
Every observed running Code Mode job blocks remaining removal, even if its
owner is in primary; a job record does not establish its nested working
directories. Malformed, unknown or unavailable job evidence refuses. The late
probe does not look up individual sessions, repeat the persistent-terminal walk
or attribute other known job kinds to targets; their existing pre-eviction
checks still apply. Before #549 an empty release batch did no host work.
Bound merged cleanup now still reads global jobs at the Git boundaries on
a ref-only retry, with no target or cache scan. Unbound cleanup, bound
abandoned cleanup and review replacement retain their previous checks,
including no host work for an empty batch.

**Before the #547 review fix:** bound merged cleanup compared the fetch and
push URLs, but the leased push still resolved `origin` again. Matching branch
OIDs in two repositories did not make that safe. **After:** both remote ref
reads and the leased push use the same verified effective URL. Observed
destination drift refuses before eviction or the next Git mutation; a
configuration change racing the push cannot redirect it to a different
`origin`. The Git commands use a private invocation-scoped URL mapping, not a
repository config write; they do not expose credentials in their diagnostics
or push other refs.

**Before #549:** the effective destinations could agree but already point to a
different repository, or a single push URL could hide multiple read URLs.
**After #549:** bound merged cleanup requires exactly one effective read URL
and one matching effective push URL, and checks the host/owner/repository
against the recorded HTTPS PR URL and number before a ref transfer or cache
eviction. It accepts direct HTTPS and Git SSH forms (`ssh://git@host/owner/repo`
and `git@host:owner/repo`), with or without the usual `.git` suffix and with
default ports only. Local paths, unsupported or ambiguous endpoints and
missing or inconsistent PR records refuse without printing the raw URLs.
This compares the configured destination, not a new GitHub node-ID lookup.
Before, HTTPS could follow a redirect or disable certificate verification after
matching the URL. Bound reads and pushes now refuse redirects and require TLS
certificate and hostname verification, even if Git config disables it. The
presence of `GIT_SSL_NO_VERIFY` holds cleanup; unset it rather than assigning
it a false value. Normal trust stores, configured CAs and authenticated HTTPS
proxies are still supported; their trust is not redefined here.

**HTTPS extra headers before and after:** matching the URL and validating TLS
previously still allowed arbitrary Git `http.extraHeader` values to affect the
request. Bound HTTPS now checks the effective ordered list before the first
transfer, including URL scopes, includes, command settings and empty resets.
It accepts only well-formed `Authorization` and `Proxy-Authorization` fields
with an auth scheme and credentials. The transfer resets Git's header list
at the exact URL and reapplies the checked values for both lookup and push;
later config edits do not add headers to that attempt. This is not a lock or
a snapshot of all Git configuration. Normal Git credential helpers, URL
credentials and proxy authentication still work. If the effective list has
other or malformed headers, or the config cannot be verified, preserve the
work and inspect the settings in their original scopes. Unset `GIT_CONFIG`
if it is set, since it changes which files the Git config query reads. Then
re-confirm the run and issue before retrying the direct tool. Bound transport
commands (HTTPS and SSH) also suppress inherited `GIT_TRACE_PERFORMANCE`,
which previously could log endpoint data. Refusals do not print header
values, raw URLs or transport errors.

**SSH transport before and after:** a matching SSH URL previously still used
Git's selected SSH command and the user's and system's SSH client configuration.
A hostname override, proxy or reused connection could therefore reach a
different server. Bound reads and pushes now use the platform OpenSSH client
(Windows System32 OpenSSH or `/usr/bin/ssh`) with no client config, hostname
canonicalization, proxy/jump or multiplexing. `GIT_SSH_COMMAND`, `GIT_SSH`,
`core.sshCommand` and SSH variant overrides no longer select the bound client.
The destination must already have a trusted key in the default known-hosts
stores; no first-use acceptance, DNS host-key trust or key updates. Default
identity files and the agent still work noninteractively. The approved SSH
URI and scp forms still work with that policy. If the system client, trusted
key or noninteractive authentication is unavailable, preserve the work and
inspect those prerequisites before retrying directly. SSH aliases, tunnels
and identities that depend on SSH config are not used by bound cleanup.

**Named-remote transport before and after:** before the pinned transport, Git
received the remote name. For SSH it could select `remote.<name>.uploadpack`
for the ref lookup and `remote.<name>.receivepack` for the push; for HTTPS it
could select `remote.<name>.proxy` and `remote.<name>.proxyAuthMethod` for
either. `remote.<name>.vcs` could select a named transport helper. Bound
merged reads and pushes now instead use the pinned URL via an invocation-scoped
token. SSH uses Git's default pack programs; HTTPS uses normal HTTP and
URL-scoped Git settings and proxy environment, not the named remote's proxy
settings. Neither operation inherits the named transport helper.

Before, `remote.<name>.mirror` could request mirror mode for a named push,
which is incompatible with an explicit deletion refspec. Bound deletion now
ignores that setting and pushes only the approved ref deletion. A successful
named push could also update or delete local remote-tracking refs via the
named remote's fetch refspec. Bound deletion does not do that: a named
remote-tracking ref can remain stale. Cleanup does not prune it.

These changes apply to **every** remote name passed to the bound helper, not
just `origin`. Effective read/push URLs and URL rewrites still take part in
preflight. Standalone and abandoned cleanup retain their named-remote Git
operations and transport behavior. Other Git paths do not gain the bound
transport restrictions.

The local branch guard still checks **every registered
worktree**, including unrelated worktrees: unexpected attachments and
observed or unverifiable rebase, am or bisect state refuse branch deletion.

The deletion order is unchanged: remote → executor → review → local. A hold
found after the remote lookup prevents the push and both checkout removals.
If it is first observed after a successful push, either cache may already have
been evicted and the remote ref is already gone, but both checkouts and the
local ref are preserved. A hold found before review removal after executor
removal preserves the review checkout and local ref. None of these refusals
reports or saves `cleaned`. There is no automatic rollback; do not recreate a
successfully deleted remote ref. These are bounded observations, not atomic
exclusion of new host work or external Git/filesystem changes. A job can
still start between a check and Git mutation. In particular, there is no new
job read between successful review removal and local-ref deletion. Keep both
checkouts unused throughout cleanup.

### Windows source evidence

The final source smoke uses the real isolated Windows OpenCode **2.4.0**
server with simulated model responses and a synthetic confirmed merge in a
private Git repository. Before #549 the fixture pointed `origin` directly at a
local bare path and the PR URL did not identify that remote. Now the fixture
uses a matching synthetic PR URL and Git destination. The first #549 smoke
used a fixture-only SSH shim. With the pinned OpenSSH policy that shim no
longer routes bound Git, so the final smoke uses authenticated HTTPS through
a loopback-only tunnel with a disposable TLS trust anchor. Before the bound
extra-header check, the HTTPS fixture used a custom header; it now uses a
disposable bearer credential in `Authorization`, without bypassing that check.
Both transfer directions use the real Git protocol against the private bare
repository.
The same engine identity gate runs; no production bypass or GitHub request is
involved. The earlier Windows **2.3.2**
[observation](#code-mode-holds-during-checkout-release-issue-543) identified
the Code Mode hold; it was not direct-tool qualification. The Windows fixture
enables Full Code Mode only when the host advertises the Code Mode job
contract; otherwise the historical control is skipped, not the direct-tool
check. With that contract present, the smoke observes
a real detached script job while its nested fixture tool invokes the old CLI
path. The engine refuses and preserves the paths, caches, registrations,
both issue refs and phase. The
smoke observes the job leave the running inventory and receives the native
completion notification. It then calls the top-level `orch_cleanup` tool
from primary, allowing up to 180 seconds for that simulated tool invocation;
other simulation waits are unchanged. It verifies no new Code Mode job and
proves both checkout paths and registrations, plus both issue-branch refs,
are gone with phase `cleaned`.
The smoke receipt retains these outcomes, zero provider requests and verified
shutdown of the owned host and HTTPS fixture. SSH policy and authentication
failure are covered independently without contacting an SSH server. The
engine's verified host baselines and advisory do not change. This is not
installed, shared-host, other-platform or real-model qualification.

### What #549 changes in the direct path

| Element | Before → after; does earlier behavior still hold? |
| --- | --- |
| `ghops.MatchesPullRequestRepository` and repository-name validation | New offline comparison of the engine-recorded PR number and repository with the effective Git URL. It reuses the host/name rules; `Repo.Target` still requires the same consistent repository URL, slug and node ID for plan approval. No approval or run-state schema change. |
| `releaseCheckouts`, `BindRemoteBranchAtHead`, `branchDestination`, `RemoteBranchBinding` and `branch_ssh*.go` | Bound merged preflight used to trust agreeing URLs without checking the recorded PR or authenticated transport. It now requires a unique matching destination and verified transport before eviction. The existing destination-drift recheck, approved OID, lease, opaque pin and redaction remain. Both SSH forms and HTTPS retain support under the policies above. `go.mod` now lists the already pinned Windows system API module as direct, without changing its version or the dependency graph. |
| `Cleanup`, `ReleaseLocations`, `ReleaseCheck`, `recheckReleasedLocations`, `releaseActiveJobs` / `releaseRunningJobs` | The late global job read is requested only for bound merged cleanup. It adds no target-scoped requests, history walk, eviction, retry or deadline extension. Foreground and owner/child attribution remain before eviction. Before the review fix, the pre-eviction hold advised the legacy standalone path even for bound callers. It now advises a direct retry at every bound hold, including abandoned preflight. Legacy and review replacement advice, late ordinary checks, confirmation, Git order and final state save remain. |
| `pr_destination_test.go`, `branch_head_test.go`, `branch_https_test.go`, `checkout_release_test.go`, `cleanup_binding_test.go` | Prior refusal, pinning and cleanup cases remain. Added canonical transport, strict OpenSSH configuration, authenticated HTTPS and redirect cases; same-OID wrong remote, missing/ambiguous origin, refusal before transfer or eviction for unverifiable HTTPS headers, and no eviction if SSH cannot authenticate. Loopback HTTPS covers ordered headers, a reset and a config edit before transfer; the tracing regression verifies the child environment and redaction. Named transport controls, bound vs. legacy retry advice, and primary-owned Code Mode jobs starting at lookup and push with both checkouts or on a ref-only retry are covered. Late tests assert that no owner or history read reloads a target. |
| `smoke.mjs`, `testdata/cleanup-https/main.go` and `testdata/cleanup-ssh.sh` | The smoke switches from the SSH shim to a verified TLS fixture as described above. The shim remains a pack-service substitute only in the Go test runner. The old Code Mode hold and direct success still check both refs and registrations; receipts add HTTPS artifact and shutdown evidence. Startup retry, separate unmanaged Memhub executable, provider trap and verified owned OpenCode shutdown remain. |
| README, Delivery skill and this guide | Released unbound recovery remains documented, not as a workaround for bound holds. The Windows qualification is source evidence only; no install, release or runtime baseline change. |

### Smoke startup

**Before:** the private service record was read and parsed inside the polling
helper. A missing file was retried; malformed JSON already failed on the
first read. The PID and loopback URL were checked after parsing, and checked
again before owned shutdown. Readiness made one `/api/info` request and did
not reread the record around that request.

**After:** the harness still retries a missing record and refuses malformed
JSON immediately, now with a sanitized error. It keeps the original bytes,
requires a nonempty password, and rechecks the child and exact record before
each readiness request and after a valid reply. Only startup
`GET /api/info` retries HTTP 503, at 250 ms intervals within one 20-second
deadline that also covers the in-flight request and body. Redirects, other
HTTP or transport failures, mismatched PID and an outside temporary path
refuse. Other API calls and owned shutdown checks keep their prior behavior.

### Touched elements and retained behavior

| Element | Before → after; does earlier behavior still hold? |
| --- | --- |
| OpenCode plugin setup (`src/index.js`, `installCleanup`) | Guard and Memhub lookup setup are unchanged. A verified managed primary location can register one new direct tool if native tools and permissions are available. Unmanaged/non-primary or unverifiable locations get no new mutator. The existing engine lookup-context read supplies checkout scope only; cleanup does not access a Memhub server. |
| Direct `orch_cleanup` executor and `runCleanup` | Before, the OpenCode skill called the CLI through its ordinary execution path. Now this one named tool is outside Code Mode; it checks the actual primary, non-child session and native permission, passes only the confirmed JSON by stdin, and waits asynchronously for the existing engine. The primary/child restriction applies to **every** caller of the tool, including a child in primary. No other tool becomes direct, no shell is spawned and no background cleanup is scheduled. |
| `run.CleanupRequest` / `Cleanup` | The exact `cleanup-issue` confirmation, consistent current run/configuration and merged/abandoned phase gates, deletion order, final save, metrics and schema-1 result remain. All requests now reject nonpositive issues. The optional `run_id` binds the new path to the matching OpenCode run and Git-verified primary; omitting it retains the standalone request. The direct tool always sends it. |
| `run.releaseCheckouts`, `checkoutReleaseTarget`, `cleanupBinding` / `cleanupCheckout`, `verifyCleanupCheckouts` and `verifyCheckoutCommon` | Both disposables still require their derived paths and normal ownership, common-directory and interior checks. The bound path records original physical identity and Git registration/HEAD or verified absence for **both** checkouts; unknown residue refuses before eviction/Git, and revalidation cannot adopt a replacement. A bound merged executor must also match the delivered head before the snapshot. Executor cleanliness applies to the branch worktree; detached-only and disposable scratch apply to review. Rechecks also precede Git removal. |
| `opencode.ReleaseLocations`, `ReleaseLocation`, `releaseOrdinaryProcesses` and `releaseActiveJobs` | The bound path adds an ownership recheck, repeats ordinary shell/PTY attribution across the verified loaded keys, rechecks sessions/jobs, and compares the cache inventory before the first eviction (no extra retry). Owner and independent cwd still both count for **every** running ordinary shell and PTY, including primary-owned processes. Existing activity rules, deadline and bounds, one unrelated-cache recheck, scoped eviction and handoff postchecks remain. There is no caller/job exemption. The extra pre-eviction gate is not used by the standalone request or the single-target review wrapper. |
| `ReleaseCheck`, `recheckReleasedLocations`, `checkReleaseIdentity` and `checkReleaseDeleteAccess` | No later activity gate existed before this review fix. It now reuses the same runtime, deadline, original target identities, scoped ordinary attribution, debug inventory and Windows access checks. OpenCode issue cleanup uses it at the Git boundaries above; review replacement does not. No late request intentionally loads a released key. |
| `gitops.LocalBranchAtHead`, `RequireBranchIdle`, `ForceDeleteBranchAtHead` and `RemoteBranchBinding` | Before, branch deletion used unconditional push deletion and `git branch -D` after preflight. The first bound merged path verified exact refs and used conditional Git deletion but `RemoteBranchAtHead` and `DeleteRemoteBranchAtHead` still used the mutable remote name. Now `BindRemoteBranchAtHead` checks and retains the destination for `Exists` and the single leased `Delete`; `VerifyDestination` refuses observed changes. Both ref deletions still require the approved OID. Branch-operation guards apply across all registered worktrees, with only the owned executor allowed to hold the branch during preflight. Conditional local deletion still leaves branch config alone. Ordinary Git branch deletion is unchanged. |
| `ReviewWorktree`, generic worktree removers and other hosts | Review replacement still uses its existing release path and Code Mode hold. Generic removers, base/bootstrap worktrees, and other hosts do not receive a new entry point or activity policy. No services are restarted and no sessions or processes are terminated. |
| `checkoutHost`, `TestBoundOpenCodeCleanupRefusesStaleOrActiveTargets`, `cleanup_binding_test.go`, `branch_head_test.go` and `test/cleanup.test.js` | Retain the scripted hold-then-foreground-retry regression. Add an independent second job, a drain and ordinary shell/PTY starting late (including unverifiable cwd during lookup or push), changed review commit and directory identity, stale run, ineligible phase and residue checks. Disposable real Git covers advanced refs, origin rename or retargeting and URL rewrites, unrelated refs and repositories, branch-operation guards and an absent remote ref. Test failures with synthetic credential-bearing URLs confirm sanitized diagnostics. Tool tests verify scope, permissions and refusal/waiting results. |
| `smoke.mjs` readiness and `test/smoke-startup.test.js` | The startup before/after above is harness-only. The test replays the production harness block with a controlled clock and checks 503, the shared deadline, record/child drift, and response/identity errors without launching a host. |
| `smoke.mjs` cleanup scenario and `smoke-retention.mjs` | Before, success invoked the CLI out of band. Now the fixture runs the real Code Mode refusal control only on Windows hosts advertising its job contract, requests background execution only when advertised, and still uses the actual direct tool for success. Only that direct invocation opts into a longer bounded wait. The source receipt also hashes the branch binding and late release check code and retains the cleanup outcomes; existing isolation, archive/recovery, cache survival, artifact identity, zero-provider and owned-shutdown checks remain. |
| README, Delivery skill and this guide | Record the prior manual recovery and direct path side by side. Existing standalone CLI, release approvals and installed-qualification boundaries remain. Adapter changes precede the separate final version bump; no dependency, runtime baseline, install or publication changes here. |
