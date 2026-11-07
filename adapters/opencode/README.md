# OpenCode V2 adapter

This directory is Orch's npm-native OpenCode V2 adapter. Its package ID
is `@kninetimmy/orch-opencode` (version `0.12.0`) and its running plugin
ID is `orch.delivery`. It is not the Codex marketplace adapter: that is
`adapters/codex/`, installed as `orch@orch` from the shared marketplace.

## Compatibility

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
The adapter now pins `@opencode/plugin` `2.2.6`, with the matching
`@opencode/protocol` development dependency for deterministic simulation.
These dependency pins do not impose an exact installed-runtime requirement:
the smoke accepts admitted newer runtimes with an advisory and checks their
actual contracts. Windows V2 `2.2.6` is the current live test baseline;
older admitted runtimes and other platforms still need live qualification.
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
| `orch.delivery` plugin ID, setup, guard hooks, and session context | The ID and thin V2 hook behavior remain. The import changes from the obsolete SDK to `@opencode/plugin`; missing/non-absolute session directories now fail before invoking Orch. The SDK scopes setup registrations to plugin unload. |
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
attaches simulation before any session action, and stops/joins that server
before deleting its own fixtures. Its synthetic Delivery state does not
represent a real approved Orch run. Hosted CI runs Node and Go checks on
Windows, Linux and macOS; it **does not run this live-host smoke**. Other
platforms and the installed manual Delivery workflow remain separately qualified.

Windows junctions that the Go runtime cannot resolve are refused by shared
path checks, including existing and not-yet-created targets. The smoke checks
aliases escaping to primary and sibling files. Native evaluation's separate
verified alias resolver and its stricter namespace/drive/link rules remain.

## Install order

The adapter is not published to npm and releases do not publish an npm
artifact. Install the checked-out `adapters/opencode/` package — the
same directory CI installs with `npm ci --prefix adapters/opencode` —
then load its package directory and canonical skills directory from absolute paths.

1. Install the `orch` binary first, then clone the repository somewhere
   persistent (not into a project it will orchestrate):

   ```sh
   git clone https://github.com/kninetimmy/orch.git ~/src/orch
   cd ~/src/orch
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
   source makes updates available after a service restart without copying skill
   files into OpenCode's global skill directory.

   Configured local paths must be directories; current V2 skips a configured
   single-file path. The server module is supplied by the local
   `@kninetimmy/orch-opencode` package; `npm install
   @kninetimmy/orch-opencode` is not an installation path because that
   package is not published.

3. Restart the OpenCode V2 service, then verify both the supported runtime
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
   OpenCode adapter version because the beta API does not expose one.

4. In each repository, run `orch init`, review and merge its bootstrap
   PR, then run `orch render-agents`. It writes the five OpenCode role
   definitions under `.opencode/agents/`; run it again after changing
   `hosts.opencode.roles` or upgrading Orch.

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

Do not reinstall as though this were a first install. In the existing
checkout, update the same local adapter package CI exercises, then
restart OpenCode V2 and repeat the project-scoped health verification:

```sh
git -C ~/src/orch pull --ff-only
npm ci --prefix ~/src/orch/adapters/opencode
opencode2 api get /api/plugin
orch doctor --host opencode
```

If the existing Orch plugin entry ends in `adapters/opencode/src/index.js`,
replace only that entry with the absolute `adapters/opencode` package directory.
Keep unrelated plugin entries and the canonical `adapters/opencode/skills`
source. If the checkout moves, update those two absolute directory paths
before restarting. The old file entry is not a fallback loading mechanism.

## Limitations

- The API exposes plugin ID, state and server presence, but not its adapter
  version. Doctor verifies those fields; package provenance and real guard
  behavior need separate installed acceptance.
- The plugin guards OpenCode's `edit`, `write`, and `patch` tools. Shell
  writes bypass Orch's guard and remain subject to OpenCode's own
  permissions.
- The plugin needs `orch` on `PATH`. If it is absent, OpenCode cannot
  receive an Orch guard verdict.
