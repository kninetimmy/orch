# OpenCode V2 adapter

This directory is Orch's npm-native OpenCode V2 adapter. Its package ID
is `@kninetimmy/orch-opencode` (version `0.13.0`) and its running plugin
ID is `orch.delivery`. It is not the Codex marketplace adapter: that is
`adapters/codex/`, installed as `orch@orch` from the shared marketplace.

## Compatibility

Package **0.13.0** is prepared for the **v0.16.0 engine target**, not a published
release or npm artifact. Latest published engine remains **v0.15.2**, whose
OpenCode package was 0.12.0. Claude/Codex plugins and marketplace stay 0.13.0:
their packaged executable guidance has not changed in this release range.
Use compatible engine/adapter **content**, not labels alone. Current source
uses state schema 6, approval/execution contract 2, Gate 3, Status 4, Activation 2,
Dispatch 4, Review 3, Escalate 2, Block 2, Resume 2, Resolve Block 1 and CLI
Repair 1; metrics documents remain schema 2. Repair does not migrate contract-1
runs: finish with original content or abort and obtain fresh approval.
Native child reads require lookup-context **schema 2** and a Memhub build with
explicit-root lookup-only serving, as detailed below.

The adapter targets the OpenCode V2 server plugin API. Before the project-scoped catalog
contract landed, the live smoke check exercised exactly `opencode2
v0.0.0-beta-17498` with `@opencode-ai/plugin`
`0.0.0-beta-17498` and did not check the model or returned-location
APIs. That old compatibility behavior no longer holds. After the
catalog change, the runtime floor became beta build 18314. Doctor then
accepted only numeric beta output, which rejected stable V2 releases.
Now version admission retains that beta floor and also accepts stable
`2.0.0` onward, including later major releases. It compares numeric
components, rejects V1/older/malformed versions, and never rejects a newer
version solely because it is outside a tested-version list. The verified
version baselines are beta 18314 and stable `2.2.6`; other admitted versions
produce an advisory warning that behavior may differ. Version admission
is separate from real compatibility: doctor still requires one active
`orch.delivery` server plugin and compatible project-scoped skill/model
catalogs. Missing contracts or failed enforcement can still require an
Orch update. Command selection prefers V2 `opencode2`, supports V2 installed
only as `opencode`, and never selects legacy V1 instead of available V2.
Previously the npm SDK and live smoke stayed pinned to
`@opencode-ai/plugin` `0.0.0-beta-18314` and that exact beta runtime.
The adapter now pins `@opencode/plugin` and its native parser/path utilities
`@opencode/util` at `2.2.6`, with the matching `@opencode/protocol`
development dependency for deterministic simulation.
These dependency pins do not impose an exact installed-runtime requirement:
the smoke accepts admitted newer runtimes with an advisory and checks their
actual contracts. Stable `2.2.6` remains the engine's verified version baseline;
Windows V2 `2.3.0` has also been observed in isolated smoke and installed
candidate checks with the existing unverified-version advisory. Other admitted
runtimes and platforms still need live qualification.
Source smoke evidence is separate from verification of an installed adapter.

## Catalog safety and blast radius

Orch queries `/api/model` with the repository in the URL-encoded
`location[directory]` parameter. The response must resolve to that same
filesystem directory identity. `ReadCatalog` exposes a safe runtime advisory,
`Models[].ID` as the
exact `providerID/id` and every `Models[].Variants[]` ID. Its safe-field
restriction applies to every returned provider and model, not only to a
named provider: settings, headers, bodies, credentials, account
identifiers, and all other raw response fields are discarded for all of
them and are never copied into diagnostics.

| Touched element | Does its previous behavior still hold? |
|---|---|
| OpenCode runtime admission and npm SDK/smoke pins | The beta 18314 and stable 2.0.0 admission floors remain, with advisory acceptance of unverified newer versions. Before, SDK and smoke were tied to beta 18314. Now the SDK/protocol dependencies are pinned to 2.2.6 and the real-host smoke checks contracts without an upper-version block. |
| `orch.delivery` plugin ID, setup, guard hooks, and session context | Before issue 495, this was only thin V2 guard/context wiring. Those hooks and the ID remain; native child lookup registration and executor checks are now additive (see below). The earlier SDK import migration and absolute-session-directory validation remain. The SDK scopes setup registrations to plugin unload. |
| File-tool target interpretation | Before, a line scanner could miss indented native patch headers and raw Windows shell aliases could identify a different destination to Go than to the host. Now native `Patch.parse` supplies every source/move target, `FSUtil.windowsPath` normalizes the host's drive aliases, and targets are resolved against the actual session directory before one all-or-nothing guard call. Invalid patches and native home-expanding forms fail before guard/tool actions. This applies to every `edit`, `write`, and parsed patch source/destination. |
| Configured local plugin path | Before, the guide and smoke configured `src/index.js`, which current V2 skips. Now both configure the package directory, whose existing package export supplies the same server entrypoint. Existing installations replace only their old Orch entry and retain unrelated plugins and skills. |
| Shared canonical paths and Windows junctions | Before, Go could leave a redirecting reparse ancestor unresolved and the missing-tail fallback trusted it lexically, allowing a junction to escape a Delivery worktree. Now shared `Canonical`, `Inside` and root discovery fail closed on unresolved redirecting ancestors. Nonredirecting tags remain unchanged; this is rejection, not general junction support. Native isolation retains its existing explicit alias resolver after its original syntax checks, before shared validation. No SUBST/8.3/UNC/hardlink admission rule is relaxed. |
| Catalog process invocation | Before, every read assumed `opencode2`. Now shared version probing selects V2 `opencode2` or V2 `opencode`, and all reads in a doctor invocation use that selection. Arguments and URL-encoded location remain separate from shell interpretation. |
| Response `location.directory` | New validation. The initial implementation inherited platform-wide case folding and could accept distinct case-different directories on a case-sensitive macOS or Windows volume; that behavior no longer holds. Directory identity is now compared by the filesystem, so every different directory is rejected while aliases of the same directory remain valid. |
| Response `data[].providerID` and `data[].id` | New projection. They form the exact `provider/id`; neither value is normalized or aliased. |
| Response `data[].enabled` and `data[].capabilities.{tools,input,output}` | New filtering. Only enabled, tool-capable, text-input/text-output models remain. |
| Response `data[].variants[].id` | New projection. Every advertised variant ID is preserved in server order, including an empty list. |
| Provider/model/variant settings, headers, bodies, credentials, account IDs, and raw payload | Previously Orch did not read the catalog; after this change these fields may arrive in command output but are never retained, returned, or included in an error. |
| Setup detection's existing CLI, git, GitHub, memhub, and instruction facts | CLI detection now verifies either V2 command name, rather than treating `opencode2` presence as proof. A failed read remains explicit data; git/GitHub/memhub/instruction detection and Detect's no-error contract remain. Runtime warnings are additive detection data. |
| Doctor's runtime check | Before, only beta 18314 or newer numeric beta output was accepted. Now stable 2.0.0 and later releases are admitted with warnings outside the verified version baselines. Unsupported versions still fail. |
| Doctor's plugin check | Before, it counted IDs without verifying active/server state or returned location. Now exactly one active server component is required in the verified requested project; failed, absent, duplicate, malformed, unready and wrong-directory catalogs fail safely. |
| OpenCode-host activation | Existing selection checks still run before activation artifacts. Shared V2 resolution now supports either command name, and successful activation adds an optional `warnings` array for unverified runtime versions. Other hosts' activation output is unchanged. |
| Live smoke's discovery, context and guarded tool execution | Before, the exact-beta smoke checked one denied write and one allowed ignored write. Now a private current-V2 server checks active loading, scoped doctor/catalogs, all supported file tools, multi-file/move targets, synthetic Delivery containment, root/child context, reload and disconnected simulation behavior. It preserves exact mixed-provider agent-discovery assertions and requires no paid generation. |
| Interview `profile`, `roleVariantID`, and `openCodeVariantQuestion` | Before, committed interviews represented every execution value as host-wide effort and suggested host-wide variant names. After, the shared profile carries a host-native execution value and each OpenCode variant question contains only no-variant plus the selected catalog model's advertised variants. This applies to every OpenCode role; Claude/Codex keep effort IDs and options. |
| Committed question writer `openCodeRoleDocSpecs` | Before, OpenCode shared the static `roleDocSpecs` model/effort path. After, all six roles ask a catalog-backed model question followed, only when applicable, by that model's dependent variant question. Claude/Codex retain their model+effort documents. |
| Committed config writer `materializeHost` | Before, every host answer was written to `RoleProfile.Effort`. After, a selected available/new OpenCode model writes `Variant` (empty for no variant). The preservation exception is an unchanged committed model absent from the live catalog: it keeps its prior `Effort`/`Variant` fields exactly so legacy configuration remains loadable. Every Claude/Codex role still writes `Effort`. |
| Existing-value reader `committedRoleDefaults` | Before, it read only `Effort`, so editing a native OpenCode profile could erase its variant or no-variant meaning. After, every OpenCode role reads `EffectiveOpenCodeVariant`, preserving native and legacy-effective selections; Claude/Codex still read `Effort`. |
| Local writer `localOpenCodeRoleDocSpecs` | Before, local OpenCode profiles used static model and variant suggestions. After, every role uses the live catalog, retains only its committed model as the non-catalog compatibility choice, and accepts only advertised variants for a selected live model. Selecting an unavailable committed model while replacing a different stale local model clears the local model/profile leaves and inherits the committed `Variant` or legacy `Effort` exactly. Any retained non-catalog model is labelled "committed, unavailable". |
| Native setup pagination (`question.Pagination` / `PaginateOptions`) | Before, schema v1 emitted no page contract and a one-answer domain could reach a setup skill as a host-invalid one-option select. Schema v2 still emits one shared deterministic page sequence and the complete answer domain: every answer appears exactly once, each page has two or three mutually exclusive answer/navigation options, and a one-answer domain gains cancel. Claude and Codex continue to present those pages. OpenCode now presents the complete parent `options[]` in one native picker, in emitted order, without page actions or free text. Schema-v1 AnswerSets remain rejected, and the answer domain — including configure and configure-local's sole unavailable committed-value exception — is unchanged. |
| `configure-local` catalog and environment reads | Before catalog-driven local setup, the interview needed only committed/local files. After, CLI step and apply calls rerun `initDetect` and pass its project catalog and environment snapshot to `NextConfigureLocalWithFacts`; the terminal local-write document keeps `Detection` nil because those facts are re-read, not serialized. |
| OpenCode model-reference grammar | Before, native profiles allowed exactly `provider/model` and rejected another slash. After, only the first slash separates the provider; the non-empty model ID remains opaque and may contain slashes, so catalog references such as `lmstudio/google/gemma-4-26b-a4b` round-trip exactly. This grammar applies to every native OpenCode role, with or without a variant; Claude/Codex grammar and legacy v0.8.0 loading are unchanged. |
| Plan `GateDoc` wire | Before, it remained schema v1 even after its selections could carry variant/no-variant fields, so a stale adapter could silently drop them. Schema v2 versioned all three selection shapes but bound approval only to the submitted plan. Now v3 retains those shapes and binds the effective contract, including engine criteria, all six host profiles, and execution settings. Adapters reject other result versions before reading a selection. |
| `Dispatch` wire | Before, request/result stayed at v3 while executor/reviewer gained the new Selection shape. After, v4 preserves all three shapes, and a v3 request is rejected before dispatch mutation. |
| `Escalate` wire | Before, request/result stayed at v1 while reroutes gained the new Selection shape. After, v2 preserves all three shapes, and a v1 request is rejected before escalation mutation. |
| `Review` wire | Before, request/result stayed at v2, allowing a stale adapter to submit an effort-only approximation that failed later as a reviewer mismatch. After, v3 accepts exact effort/variant/no-variant selections, and v2 is rejected before reviewer comparison. |
| `StatusDoc` wire | Before, it remained schema v1 while transitive state decisions gained the new Selection shape. Schema v2 versioned all three exact shapes. Schema v3 retained them and exposed state v5's approval-contract version and execution fingerprint. Now v4 retains those fields and exposes state v6 block causes and resolution history. Adapters reject other result versions before reading run state. State v4/v5 remain readable for inspection, but continuing them requires the original engine or aborting and obtaining fresh approval. |
| Adapter protocol pins | The Claude, Codex, and OpenCode Architect/Delivery skills now state the same closed versions. Shared `adaptertest.CheckSelectionWireVersions` derives every expected literal from the engine constants, so no host keeps an unpinned hand-synced Selection schema. |
| Legacy local `effort` validation | Before, raw `effort = ""` bypassed the named legacy-effort domain and silently cleared a committed variant into a bare-model selection. After, every OpenCode role rejects an explicitly empty legacy effort with `variant = ""` as the remediation. Named v0.8.0 efforts remain compatible; only native `variant = ""` selects no variant. Claude/Codex effort behavior is unchanged. |

## Role selection and sessions

`orch init`, `orch configure`, and `orch configure-local` discover models from
the current repository's live catalog on an admitted V2 runtime (beta 18314
or stable 2.0.0 and newer releases) that passes doctor's contract checks.
All six role selections are independent:
one profile can mix providers and models, and model IDs after the first slash are
opaque. After each model choice, setup offers only that model's advertised
variants; a model with no variants produces a bare `provider/model` selection.

Model and variant choices retain the shared deterministic setup pagination
contract. Claude and Codex present its two- or three-option pages, where every
answer appears exactly once, larger domains use explicit previous/next actions,
and a one-answer domain adds cancel. OpenCode instead presents every parent
option in one native picker, in catalog order, without page actions or free text.
A non-catalog model appears only when it is the committed value, labelled
"committed, unavailable".

Catalog discovery failure or an empty agent-capable catalog blocks initialization
and OpenCode role edits with an actionable error. Unrelated configuration edits
remain available and preserve existing OpenCode selections. Delivery activation
re-discovers the catalog and fails before mutation if any configured role names an
unavailable provider, model, or variant. It also fails if generated agent files do
not match the effective configuration, with `orch render-agents` as remediation.

The plugin passes the selected model of a root main session to Orch's context
hook. When it differs from the configured Architect model and optional variant,
Orch adds a warning but does not switch models; child sessions skip this Architect
comparison. `npm run smoke --prefix adapters/opencode` renders the shared
mixed-provider fixture and checks every discovered role's exact
provider/model/variant reference. Its synthetic sessions use the runtime's
simulation backend, not a provider connection. Current V2 exposes `patch`
for supported GPT-shaped models and `edit`/`write` for other models, so the
fixture exercises both real tool sets with simulated model identities.

The smoke is a required **local Windows** check. It starts its own localhost
server with isolated configuration/database paths and OS-temp repositories,
attaches simulation before any session action, and stops/joins that server.
Before issue 495 it deleted its own fixtures; now it retains synthetic fixtures
and `evidence.json` outside every source checkout for inspection. Its synthetic Delivery state does not
represent a real approved Orch run. Hosted CI runs Node and Go checks on
Windows, Linux and macOS; it **does not run this live-host smoke**. Other
platforms and installed manual Delivery operations require separate qualification.

## Bound child memory (issue 495)

**Before (policy):** OpenCode children were instructed to use Architect-mediated
memory and never invoke Memhub themselves; tool availability alone was not
write isolation. **After:** `orch-scout`, `orch-implementer`,
`orch-specialist`, `orch-reviewer` and `orch-reviewer-safe` may use only native
`orch_memhub` status, recall, get_row and locate. This is the approved policy
exception, not a claim that historical Architect mediation or the shell-bypass
warning disappeared. Claude/Codex direct access remains unclaimed and mediated.

**Before (issue 499 regression):** the globally loaded lookup integration applied
its child executor restrictions and Orch child-memory guidance even outside Orch
management. A plain Git checkout received no bound server, but an ordinary
`general` child still could not call its configured broad Memhub connection.
**After:** an authoritative unmanaged observation makes the entire lookup
integration inert: no server, permission transform, executor wrapper, reserved-name
collision handling or Orch child-memory context is added. Ordinary roots and
children keep their configured Memhub behavior in plain Git checkouts, nested
directories and non-Git directories without a managed ancestor. This is a scope
correction, not a relaxation for general children inside managed projects.

The plugin still invokes read-only `orch hook opencode lookup-context --json`
at its native location. Before issue 499, schema 1 returned only managed roots
and failed outside Orch. Schema **2** adds a required boolean `managed`:
`{"schema_version":2,"managed":false}` is the complete unmanaged result;
`managed: true` retains mode, memory/code/common roots and HEAD. Only shared
`FindOutermostRoot`'s `ErrNotFound` permits the unmanaged distinction. The engine
first verifies the location and its marker ancestry. With no Orch ancestor, it
skips Git only if no ancestor has any `.git` entry; otherwise Git's selected
checkout, primary registration/common directory and primary Orch ancestry must
still verify. In particular, an external registered worktree with a missing
copied Orch marker still uses its managed primary. Invalid/unreadable markers,
invalid configuration, failed Git discovery and unverifiable roots remain errors,
never unmanaged observations. Managed locations still load primary effective
configuration. The observation reads no memory and changes no state.

The new adapter requires schema 2. Missing commands, schema 1, malformed or
contradictory responses and missing host capabilities retain unavailable-lookup
diagnostics and child fences, not an unmanaged no-op. An old adapter rejects
schema 2 too; qualify and update the engine/adapter together through a separately
approved installation, not a full-server or CLI fallback. Reload the native
location after changing management scope, just as after mode/service changes.
The existing fail-open textual session-start and observation-only issue context
are unchanged. No second adapter mode resolver, memory copying, retrieval engine,
MCP relay or new dependency is introduced.

When enabled, native MCP launches
`memhub serve --memory-root <primary> --code-root <checkout> --profile lookup-only`
with explicit cwd, legacy protocol and `pool: false`. Connections are per native
location, **not automatically per child**. Native child creation can inherit the
parent's directory: move each child session to its assigned checkout and read its
location back before lookup. Supply expected roots/common directory and revision
in every dispatch; reviewers get a separate registered checkout pinned to the PR's
live head. Call status first, compare binding evidence with that assignment and
the engine context, and refuse mismatches rather than use primary code. Equivalent
Windows extended `\\?\` paths are compared by directory identity, not string
spelling. HEAD does not identify dirty tracked content; inspect locate's SHA-256
snippet/chunk/indexed-file/worktree-file evidence, independent index path and
freshness limitations. No atomic-tree-snapshot or untracked-code coverage is claimed.

In managed projects, native role permissions deny broad Memhub and allow just the four bound actions.
Executor checks also apply inside Code Mode and reject unsupported roles and
full-server calls from children, even if later session permission rules allow
them. Memhub itself supplies the lookup-only catalog and rejects excluded tools
before handlers. The restriction is **not only about a server named `memhub`**:
the plugin recognizes every configured Memhub-named server, local command with a
`memhub[.exe]` argument basename, and renamed/remote catalog exposing the four
Memhub lookup symbols. It preserves Architect access to existing broad servers
and does not edit user MCP configuration. A normalized `orch_memhub` collision
reports failure without overwriting it or dropping the write guard. Arbitrary
external CLI/proxy access is not a sandbox claim; shell-capable children must
still obey the no-CLI-fallback and Architect-only semantic/admin-write instructions.

Code Mode can discard unknown input fields before MCP invocation. The smoke
therefore distinguishes an ignored attempted root argument (returned binding
must still name the assigned checkout) from direct MCP argument rejection.
Neither is permission to select another root.

Lookup-only is **not physically read-only**: project opens may migrate/upsert
metadata, maintain registry/metrics, expire pending proposals and log queries;
startup may initialize bundled models and locate may refresh disposable indexes.
Task/fact/decision/doc/note/command writes, proposals, administrative actions,
sync, installs and repairs remain Architect-only. Off launches no child service.
Best-effort reports missing/old servers or host capabilities and retains mediated
fallback. Required fails the affected preflight/lookup. Neither mode falls back to
a full server, copies memory, installs software or repairs configuration. Reload
the native location after changing Orch mode or server availability; in-flight
calls still recheck engine context and Memhub revalidates its pinned roots.

### Qualification, not installation

Policy permission, available source interfaces, isolated runtime evidence and
installed-live qualification are separate claims. Before issue 497, the local
smoke always built Orch from its checkout, loaded that checkout's adapter and
required an explicitly selected source-built Memhub. Now that **source-build
mode** remains the default when both artifact selectors are unset:

```powershell
Remove-Item Env:ORCH_SMOKE_ORCH, Env:ORCH_SMOKE_ADAPTER -ErrorAction SilentlyContinue
$env:ORCH_SMOKE_MEMHUB = 'C:\path\to\compatible\memhub.exe'
npm run smoke --prefix adapters/opencode
```

In **explicit-artifact mode**, supply both selectors. Orch is not rebuilt:
every direct and native-plugin invocation resolves to a verified byte-identical
private copy of the selected executable. The private host loads the selected
package directory and its skills, including after unload/reload:

```powershell
$env:ORCH_SMOKE_ORCH = 'C:\path\to\prebuilt\orch.exe'
$env:ORCH_SMOKE_ADAPTER = 'C:\path\to\installed\adapters\opencode'
$env:ORCH_SMOKE_MEMHUB = 'C:\path\to\compatible\memhub.exe'
npm run smoke --prefix adapters/opencode
```

Verify selected binaries against trusted expected SHA-256 values before execution
(downloaded release binaries against that release's published `SHA256SUMS`).
`ORCH_SMOKE_MEMHUB` remains required in both modes and must support explicit-root,
lookup-only serving. Every selector must be a nonempty absolute path of the
correct type. An incomplete pair, empty/relative/missing/wrong-type path,
escaping package link/export, invalid manifest/lockfile, missing runtime/skills
or unimportable server module fails before selected binaries or the private host
launch. There is no checkout, PATH Orch, global Memhub or other-adapter fallback.
Use a compatible Orch ESM package with its manifest, lockfile, `src/`, `skills/`
and existing dependencies already installed; qualification never installs or
repairs them. Its server export must be a local string export, `.` or `./server`.

The fixture isolates homes, user configuration, caches, registry/sync discovery
and temporary storage, launches only owned processes, and uses synthetic memory
and simulated model responses. The localhost provider trap must receive zero
requests. Native MCP lifecycle owns service cancellation/shutdown. Evidence and
fixtures are retained externally after the private process tree is stopped.

`evidence.json` records the mode, harness Git revision/status and harness-file
hashes separately from the selected canonical adapter path and SHA-256 identities
of its manifest, lockfile, server entrypoint, `src/**` and `skills/**`. It records
selected and actually executed Orch/Memhub paths and hashes, labels verified
private copies as copies, checks native PATH resolution, and retains initial/reload
plugin/skill catalogs, successful checks, provider count and post-run payload/binary
identity verification. Memhub negative tests replace only the private fixture
copy and must restore its qualified bytes. Version labels and source revisions
alone are not installed-content proof. The lockfile identifies declared dependency
resolution, **not the installed dependency tree**; dependency bytes, untracked
package files outside these identity sets and an atomic filesystem snapshot are
not certified.

Both invocations are the same required **local-only** gate:
`npm run smoke --prefix adapters/opencode`. Hosted CI still runs only the
deterministic Node/Go checks, not this smoke. An explicit-artifact pass qualifies
those artifacts in an isolated host; it neither activates them in the user's
shared service nor constitutes a real model-driven Delivery trial. Those require
separate approval and evidence. No shared service, installation or user configuration
is modified by this gate.

Isolated qualification tuple used on 2026-11-14:

| Element | Exact identity |
|---|---|
| Host | Windows `win32/x64`, OpenCode `opencode v2.3.0` (admitted with the existing unverified-version advisory, not a changed version floor) |
| Test runtime | Node `v24.15.0`, PowerShell `7.6.10`, Go `go1.26.5 windows/amd64` |
| Native SDK/utilities/simulation | `@opencode/plugin`, `@opencode/util`, `@opencode/protocol` all `2.2.6`; dependency pins unchanged |
| Real Memhub source build | `eec177f5bad21e9b1a443b986d30164b710a0b4a`; supplied Architect build using stable Rust `1.95.0`, `cargo build --locked --offline --bin memhub` with isolated target (build scripts fetched declared model assets despite Cargo offline) |
| Memhub executable SHA-256 | `c1a3a66fd6b836c838eef431e3471bc1f5ce1aa93a91ee54d50961fcd0c5f4c3` |

The smoke uses real lookup-only processes for successful reads and direct profile
fences; legacy option rejection is a synthetic CLI fixture, not qualification of
an installed old release. It observes required-mode pre-model failure through
the native session outcome and parent failure report. Reviewer write attempts can be rejected by
Orch's containment hook before native permission evaluation; evidence records
the actual layer rather than calling every rejection a permission result.

### Installed candidate checkpoint (2026-11-14)

Separate approved cutover evidence records installed engine
**v0.16.0-prep.f57fc37**, adapter/skills from `f57fc37` still labelled **0.12.0**,
and the Memhub build above. Engine SHA-256:
`6f8de03a5f92916ee86dc99b487183537de89c685ea34c64096555d4ac44c922`.
Source and explicit-artifact smoke each recorded 25 checks, zero provider
requests and owned-host shutdown. Installed CLI/live-MCP doctor, the active
package path, MCP discovery of 17 tools/four resources/one template, an Assist
guard denial and an unmanaged project with no bound server were checked.
Only scoped locations were reloaded/reconnected; the shared OpenCode service
was not restarted and unrelated processes were retained.

Those installed bytes are **not this final 0.13.0 package** and are not a
published v0.16.0 artifact. Isolated smoke uses all five roles with simulation;
it is not a model-driven five-role trial. This normal Delivery cycle's executor
successfully used installed native bound status, `get_row` (decision 70) and
locate from its assigned implementation checkout at `f57fc37`, with matching
explicit roots/common directory and hash-verified locate hits. A fresh reviewer
must independently move to its assigned review checkout, validate status and
complete at least one bound recall/get_row/locate; exact returned binding,
revision, result and retained evidence belong in the PR audit, not a prospective
claim of reviewer success. Unavailable or mismatched native lookup stops
qualification for an Architect decision, never CLI/full-server/wrong-root fallback.

Qualification is limited to the observed Windows/OpenCode tuple and operations.
It does not qualify Claude/Codex direct access, complete host parity, guide
search, wrap-up integration or all tasks319/331 rollout work. Final merged
package, published binary/checksum and installed-release verification remain
separate gates after explicit publication/installation approval.

### Artifact-smoke blast radius (issue 497)

| Touched element | Does its previous behavior still hold? |
|---|---|
| `smoke.mjs` source roots and helper constants | Yes: harness fixtures and expectations still come from its checkout. Before, that also unconditionally selected the adapter and built Orch; now only default source-build mode does. Explicit mode never substitutes those source artifacts. |
| `selectArtifacts`, `adapterIdentity`, `checkAdapterModule` in `smoke-artifacts.mjs` | New harness-only preflight. Both optional selectors are paired, and every selector has the same nonempty/absolute/type restrictions. Containment applies to every identity file under the selected package, not just `src/index.js` or one named skill. Module import uses isolated homes/caches and does not call plugin setup. |
| `prepareOrch`, `copyBinary` and private PATH | Default source building remains. Explicit Orch never builds; both Memhub modes retain a private copy, now verified byte-for-byte before use. Native resolution and direct commands name the same private binaries. Only that Memhub copy is renamed/replaced in negative tests. |
| Fixture plugin/skills configuration, active-plugin and initial/reload catalog checks | Existing real package-directory loading and unload/reload enforcement remain. Both load stages now verify the selected local plugin path and every selected `SKILL.md` path/hash, not only `orch-architect`. Fixture configuration alone is edited. |
| `pass`, evidence creation and `verifyArtifacts` | Existing retained synthetic evidence remains; every successful check is now recorded, with separate harness/selected/executed identities and post-shutdown payload checks. Lockfile evidence does not certify installed dependencies. |
| Host/simulation lifecycle, synthetic memory, role/root/mode/collision/guard checks | Preserved: the same isolated server, provider trap, real tools and all five roles run both modes. Shutdown targets only owned processes; no permission, approval or shared-service behavior changes. |
| `test/smoke-artifacts.test.js`, README and existing test entrypoints | Adds deterministic selector, module, containment, source/copy and evidence checks plus both invocation contracts. Existing tests and local-only smoke entrypoint remain; CI configuration, dependency pins, runtime product code, other hosts and Delivery policy are untouched. |

### Child-memory blast radius

| Touched element | Does its previous behavior still hold? |
|---|---|
| `hook` CLI dispatch/usage | Existing host verbs and fail-open session-start remain; a new strict, argv-only versioned read observation is added. |
| `lookup-context` root/config projection | Introduced with schema 1 and managed-only output. Issue 499 replaces that wire with schema 2's explicit managed/unmanaged distinction; managed root/config verification still reuses the same primitives and rejects wrong, missing and unrelated roots. No Memhub/project-state read or mutation is added. |
| `orch.delivery` setup and native MCP lifecycle | Existing guard/context registrations remain. Adds a separate lookup-only, non-pooled connection per verified location; native lifecycle owns startup/cancellation/shutdown, without relay or fallback. |
| MCP names/configuration and existing Architect connections | User configuration is not overwritten. Normalized reserved-name collisions now refuse child lookup; known broad connections remain usable by the Architect, never child fallback. Other servers are unchanged. |
| Native `Agent.Info.permissions` transforms | Existing edit/shell/subagent rules remain for every role. Adds a bound deny for unsupported agents and four exceptions for all five Orch children, plus broad-server denials; model/variant selections are untouched. |
| Native `Tool.Info.execute` transforms and Code Mode snapshots | Before issue 495, Memhub executors had no Orch child restriction. Issue 495 added checks to every identified Memhub executor, erroneously including ordinary children outside Orch. After issue 499, those checks and matching bound-result evidence still apply inside managed projects (and failed/unverified discovery), but authoritative unmanaged locations receive no wrapper. The managed restriction covers all tools in those catalogs, not only `recall` or a server literally named `memhub`. Unrelated executors are unchanged. |
| Status-first assignment guidance and per-call evidence | New. Each child is instructed to validate status against its assignment before lookup; every executor independently verifies session/root context and each result's binding, without a transient authorization cache that could disappear on native reload. Prompt assignment/revision comparison remains instructional, not invented authorization or an atomic snapshot. |
| Five canonical OpenCode agent files and generated outputs | Before, no explicit four-tool allowance and blanket no-write prose. After, all five get the same lookup exception and maintenance distinction. Existing scope, review independence, native read-only roles, routed models, branch/ref guidance and no-delegation rules remain. `internal/agents.Render` still substitutes routing fields only. |
| OpenCode Architect/Delivery skills | Before, children never invoked Memhub and every Memhub process used main as cwd. After, explicit bound child reads use assigned code cwd; Architect full/CLI operations and semantic/admin writes remain primary-only. Approval gates, engine routing, issue-context observations, review independence and wrap-up obligations are unchanged. |
| Existing isolated smoke and provider simulation | Existing guard/catalog/reload/containment checks remain; explicit parent-report handling keeps native child notifications from being mistaken for another test's turn. Adds real four-tool processes/roles and dirty implementation/pinned-review bindings. Before fixtures were deleted; after they and evidence remain outside all checkouts after owned services stop. |
| Go/Node deterministic tests | Existing checks remain; new fixture tests exercise strict context, modes, collision preservation, permissions, per-call evidence and executor denials without project-memory access. |
| SDK, dependency graph, shared write guard, other hosts and Delivery engine | Unchanged; no dependency/pin, scheduler, lifecycle wire, approval, shell/edit grant or Claude/Codex access expansion. Source checks do not certify an installed adapter. |

### Lookup-scope blast radius (issue 499)

| Touched element | Does its previous behavior still hold? |
|---|---|
| `hookLookupContext`, `lookupContext` and `lookupHasGitAncestor` | Managed primary/config/common-directory/registered-checkout checks remain. Before, non-Git and plain Git locations failed discovery; now verified absence yields the two-field schema-2 unmanaged result. Git-marker inspection lives only in the engine and never interprets stderr as non-management. Shared canonical/root discovery is reused, not modified. |
| `readLookupContext` | Before, accepted schema 1 and validated mode/roots/revision. Now accepts only schema 2 with an explicit boolean scope; managed observations still require valid mode, directory roots and revision. Only the exact unmanaged shape skips those managed fields; old/invalid output remains a failure. |
| `installLookup` native MCP, agent and tool transforms, including Code Mode executors | Before, discovery failure still registered child fences even in unrelated projects. Now only authoritative unmanaged scope skips **all** these registrations. Inside managed projects and on discovery failure, every previously identified broad Memhub catalog remains fenced for every child, including `general`; the five supported roles retain only four bound actions. This covers every matching symbol, not just `memhub_recall`, and every recognized server alias, not just `memhub`. Architect broad access, collisions and existing non-Memhub permissions/executors remain unchanged. |
| `preflight`, result binding and returned child-context hook | Managed calls still recheck mode, assigned checkout/common-directory identity, exact four-tool catalog, service connection and each result's binding. A managed connection cannot follow a session into unmanaged scope; it fails explicitly and requires native-location reload. Authoritative unmanaged setup adds no child guidance. Off/best-effort/required and no full-server/CLI fallback remain. |
| Delivery skill's lookup-context pin | Before, dispatch instructions required schema 1. Now they require schema 2 with `managed: true`; root/revision comparison and mediated refusal remain unchanged. No other skill, role or approval instructions change. |
| `orch.delivery` setup, textual context and write guard | Unchanged: setup still calls lookup installation, and registers the same independent context/guard hooks. A lookup no-op is not a write-guard bypass. Native MCP still owns transport, cancellation and shutdown. |
| `hooklookup_test.go` and `test/lookup.test.js` | Existing all-role/mode/binding/collision checks remain; add unmanaged root/general-child no-op and engine/error distinctions, including markerless external worktrees. Directory-mode permission tests skip on hosts/elevated users that cannot enforce them; invalid-marker/config and other failure checks still run there. |
| `smoke.mjs` fixtures and retained evidence | Existing source/explicit selection, all five managed roles, roots, modes, reload, guard and provider-trap checks remain. Adds synthetic plain/nested Git and non-Git locations using the same loaded adapter and configured full Memhub: ordinary root and general-child reads and semantic calls must succeed, with no bound connection, permission changes or Orch memory context. Evidence additionally records those outcomes. Only external owned fixtures are mutated; shutdown still targets owned processes. |
| README and qualification boundary | Records the erroneous before-state rather than erasing it. This source correction is **not** installed qualification: the local smoke must build/load this changed source with both optional artifact selectors unset. Explicit-artifact results qualify only their selected bytes in an isolated host. Neither proves activation in the shared service or real model-driven child access. No installation, host configuration, version, dependency/pin or release change is included. |

Windows junctions that the Go runtime cannot resolve are refused by shared
path checks, including existing and not-yet-created targets. The smoke checks
aliases escaping to primary and sibling files. Native evaluation's separate
verified alias resolver and its stricter namespace/drive/link rules remain.

## Install order

The adapter is not published to npm and releases do not publish an npm
artifact. Install the checked-out `adapters/opencode/` package — the
same directory CI installs with `npm ci --prefix adapters/opencode` —
then load its package directory and canonical skills directory from absolute paths.

1. Install a compatible `orch` binary first, verifying downloaded release bytes
   against the trusted release's published `SHA256SUMS` before execution. Then
   clone the repository somewhere
   persistent (not into a project it will orchestrate):

   ```sh
   git clone https://github.com/kninetimmy/orch.git ~/src/orch
   cd ~/src/orch
   # Set ORCH_ADAPTER_COMMIT to the inspected commit matching the engine.
   git checkout --detach "$ORCH_ADAPTER_COMMIT"
   npm ci --prefix adapters/opencode
   ```

2. Append the absolute plugin package directory and canonical skill source to the
   existing `plugins` and `skills` arrays in OpenCode V2's `opencode.json`.
   Keep all unrelated configuration, plugin entries, and skill-source entries:

   ```json
   {
     "plugins": ["/absolute/path/to/orch/adapters/opencode"],
     "skills": ["/absolute/path/to/orch/adapters/opencode/skills"]
   }
   ```

   These are entries to add, not replacement arrays. The checkout-backed skill
   source makes updates available after a location reload without copying skill
   files into OpenCode's global skill directory.

   Configured local paths must be directories; current V2 skips a configured
   single-file path. The server module is supplied by the local
   `@kninetimmy/orch-opencode` package; `npm install
   @kninetimmy/orch-opencode` is not an installation path because that
   package is not published.

3. Reload the affected OpenCode V2 location, then verify both the supported runtime
   and the active plugin ID before relying on enforcement:

   ```sh
   opencode2 --version
   opencode2 api get /api/plugin
   orch doctor --host opencode
   ```

   Version admission accepts beta 18314 or newer numeric beta builds, and
   stable `2.0.0` or newer releases. Unverified versions warn without a
   version-only block. If only the V2 `opencode` command is installed, use
   it for the first two commands; do not substitute a legacy V1 binary.
   The old third command counted IDs only; `orch doctor --host opencode`
   now requires one active server plugin and verifies project location. It
   also queries that repository's live skill and model catalogs, verifies their
   returned locations, and requires compatible `orch-architect`,
   `orch-delivery`, and `orch-setup` wire contracts. It cannot check an
   OpenCode adapter version because the API does not expose one; compare the
   loaded package/skill paths, hashes and source commit separately.

4. In each repository, run `orch init`, review and merge its bootstrap
   PR, then run `orch render-agents`. It writes the five OpenCode role
   definitions under `.opencode/agents/`; run it again after changing
   `hosts.opencode.roles`, or when an upgrade changes definitions or doctor
   reports them stale.

   Each OpenCode role selects an exact `provider/model` and an optional
   model-specific `variant`. Generated frontmatter uses
   `provider/model#variant`, or bare `provider/model` when no variant is
   selected (omit it in committed configuration; write `variant = ""` to
   override a committed variant locally). Before optional variants,
   schema-v1 OpenCode roles used `effort` and always appended it; those
   committed and local v0.8.0 values remain
   compatible aliases with unchanged effective selections and revisions.
   Claude Code and Codex configuration are unaffected by this OpenCode-only
   compatibility rule.

## Upgrade

Do not reinstall as though this were a first install. Update only **between runs
in Assist**, with separate approval for installed changes. Preserve the old
engine, adapter/skills, Memhub and configuration plus their paths, source commits
and hashes. Select compatible engine/adapter content together, including
lookup-context schema 2 and explicit-root lookup-only Memhub; an old engine or
schema-1 adapter is not a fallback. Never replace these during an active run.
Verify downloaded release binaries against trusted published checksums before
execution; v0.16.0 has no published final artifact yet.

In the persistent checkout, set `ORCH_ADAPTER_COMMIT` to the inspected, approved
commit matching the selected engine, not an unreviewed moving branch:

```sh
git -C ~/src/orch fetch origin
git -C ~/src/orch checkout --detach "$ORCH_ADAPTER_COMMIT"
npm ci --prefix ~/src/orch/adapters/opencode
# Reload the affected native location before the checks below.
opencode2 api get /api/plugin
orch doctor --host opencode
```

If the existing Orch plugin entry ends in `adapters/opencode/src/index.js`,
replace only that entry with the absolute `adapters/opencode` package directory.
Keep unrelated plugin entries and the canonical `adapters/opencode/skills`
source. If the checkout moves, update those two absolute directory paths
before reloading. The old file entry is not a fallback loading mechanism.

Compare actual loaded package/skills and engine identities with the selected
commit, refresh generated agents if changed/stale, and exercise a fresh bounded
approved workflow. Source checks and explicit-artifact smoke do not prove shared
service activation or real model-driven child lookup. Record installed and source
evidence separately.

**Rollback:** only in Assist, restore the previously recorded compatible engine,
adapter/skills checkout and any changed Memhub/configuration content. Restore
their recorded plugin/skill paths, reload, compare hashes/provenance and repeat
doctor and bounded workflow checks. Downloaded rollback binaries require that
previous release's trusted `SHA256SUMS`. Never mix new lookup wiring with an old
engine, migrate a live run or weaken permissions to force compatibility.

**Publication:** 0.13.0 is the final content-bearing release-preparation bump.
Any later approved v0.16.0 tag must name its merge commit. Tag/release publication
and final published-artifact verification are separate gates; this package stays
checkout-backed and is not published to npm.

## Limitations

- The API exposes plugin ID, state and server presence, but not its adapter
  version. Doctor verifies those fields; package provenance and real guard
  behavior need separate installed acceptance.
- The plugin guards OpenCode's `edit`, `write`, and `patch` tools. Shell
  writes bypass Orch's guard and remain subject to OpenCode's own
  permissions.
- Native home-expanding paths (`~`, `~/...`, and `~\...` on Windows) are
  refused before mutation; use an absolute or ordinary relative path.
  `~notes.txt` remains a literal name, as do backslashes on Unix. The plugin
  does not guess the server's home or substitute the real user's home for
  `OPENCODE_TEST_HOME` in isolated tests.
- The plugin needs `orch` on `PATH`. If it is absent, OpenCode cannot
  receive an Orch guard verdict.
