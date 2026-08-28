# OpenCode V2 adapter

This directory is Orch's npm-native OpenCode V2 adapter. Its package ID
is `@kninetimmy/orch-opencode` (version `0.9.0`) and its running plugin
ID is `orch.delivery`. It is not the Codex marketplace adapter: that is
`adapters/codex/`, installed as `orch@orch` from the shared marketplace.

## Compatibility

The OpenCode V2 plugin API is beta. Before the project-scoped catalog
contract landed, the live smoke check exercised exactly `opencode2
v0.0.0-beta-17498` with `@opencode-ai/plugin`
`0.0.0-beta-17498` and did not check the model or returned-location
APIs. That old compatibility behavior no longer holds. After the
catalog change, `opencode2 v0.0.0-beta-18314` is the minimum supported
runtime. Doctor accepts newer numeric V2 beta builds only when exactly
one `orch.delivery` plugin is active and the repository-scoped model
catalog preserves the returned-location contract. Upstream drift in a
newer build can therefore still require an Orch update. The npm SDK and
live smoke remain exactly pinned to `@opencode-ai/plugin`
`0.0.0-beta-18314` and `opencode2 v0.0.0-beta-18314`, respectively, to
prove the oldest supported contract; those test pins do not cap runtime
support.

## Catalog safety and blast radius

Orch queries `/api/model` with the repository in the URL-encoded
`location[directory]` parameter. The response must resolve to that same
filesystem directory identity. `ReadCatalog` exposes only `Models[].ID` as the
exact `providerID/id` and every `Models[].Variants[]` ID. Its safe-field
restriction applies to every returned provider and model, not only to a
named provider: settings, headers, bodies, credentials, account
identifiers, and all other raw response fields are discarded for all of
them and are never copied into diagnostics.

| Touched element | Does its previous behavior still hold? |
|---|---|
| OpenCode runtime floor and npm SDK/smoke pins | No. The before-and-after is recorded above: beta 17498 is replaced by a beta 18314 runtime floor, while the SDK and smoke stay exactly pinned to beta 18314. |
| `orch.delivery` plugin ID, setup, guard hooks, and session context | Yes. The same plugin contract remains; unit and live smoke checks still exercise it on the new beta. |
| Catalog process invocation | New. It is one argument-vector call in the repository directory, with the location URL-encoded rather than shell-interpolated. |
| Response `location.directory` | New validation. The initial implementation inherited platform-wide case folding and could accept distinct case-different directories on a case-sensitive macOS or Windows volume; that behavior no longer holds. Directory identity is now compared by the filesystem, so every different directory is rejected while aliases of the same directory remain valid. |
| Response `data[].providerID` and `data[].id` | New projection. They form the exact `provider/id`; neither value is normalized or aliased. |
| Response `data[].enabled` and `data[].capabilities.{tools,input,output}` | New filtering. Only enabled, tool-capable, text-input/text-output models remain. |
| Response `data[].variants[].id` | New projection. Every advertised variant ID is preserved in server order, including an empty list. |
| Provider/model/variant settings, headers, bodies, credentials, account IDs, and raw payload | Previously Orch did not read the catalog; after this change these fields may arrive in command output but are never retained, returned, or included in an error. |
| Setup detection's existing CLI, git, GitHub, memhub, and instruction facts | Yes. Catalog and catalog-error fields are additive, and a failed read remains explicit data rather than changing Detect's no-error contract. |
| Doctor's runtime and active-plugin checks | Yes. The runtime check now accepts beta 18314 or a newer numeric beta, then still requires exactly one active plugin before the catalog/location check follows. |
| Live smoke's agent discovery, context injection, denied tracked write, and allowed ignored write | Yes. The catalog/location assertions are additive, and the smoke now renders a mixed-provider fixture and checks every discovered dispatched agent's exact model reference. |
| Interview `profile`, `roleVariantID`, and `openCodeVariantQuestion` | Before, committed interviews represented every execution value as host-wide effort and suggested host-wide variant names. After, the shared profile carries a host-native execution value and each OpenCode variant question contains only no-variant plus the selected catalog model's advertised variants. This applies to every OpenCode role; Claude/Codex keep effort IDs and options. |
| Committed question writer `openCodeRoleDocSpecs` | Before, OpenCode shared the static `roleDocSpecs` model/effort path. After, all six roles ask a catalog-backed model question followed, only when applicable, by that model's dependent variant question. Claude/Codex retain their model+effort documents. |
| Committed config writer `materializeHost` | Before, every host answer was written to `RoleProfile.Effort`. After, a selected available/new OpenCode model writes `Variant` (empty for no variant). The preservation exception is an unchanged committed model absent from the live catalog: it keeps its prior `Effort`/`Variant` fields exactly so legacy configuration remains loadable. Every Claude/Codex role still writes `Effort`. |
| Existing-value reader `committedRoleDefaults` | Before, it read only `Effort`, so editing a native OpenCode profile could erase its variant or no-variant meaning. After, every OpenCode role reads `EffectiveOpenCodeVariant`, preserving native and legacy-effective selections; Claude/Codex still read `Effort`. |
| Local writer `localOpenCodeRoleDocSpecs` | Before, local OpenCode profiles used static model and variant suggestions. After, every role uses the live catalog, retains only its committed model as the non-catalog compatibility choice, and accepts only advertised variants for a selected live model. Selecting an unavailable committed model while replacing a different stale local model clears the local model/profile leaves and inherits the committed `Variant` or legacy `Effort` exactly. Any retained non-catalog model is labelled "committed, unavailable". |
| Native setup pagination (`question.Pagination` / `PaginateOptions`) | Before, schema v1 emitted no page contract and a one-answer domain could reach a setup skill as a host-invalid one-option select. Schema v2 still emits one shared deterministic page sequence and the complete answer domain: every answer appears exactly once, each page has two or three mutually exclusive answer/navigation options, and a one-answer domain gains cancel. Claude and Codex continue to present those pages. OpenCode now presents the complete parent `options[]` in one native picker, in emitted order, without page actions or free text. Schema-v1 AnswerSets remain rejected, and the answer domain — including configure and configure-local's sole unavailable committed-value exception — is unchanged. |
| `configure-local` catalog and environment reads | Before catalog-driven local setup, the interview needed only committed/local files. After, CLI step and apply calls rerun `initDetect` and pass its project catalog and environment snapshot to `NextConfigureLocalWithFacts`; the terminal local-write document keeps `Detection` nil because those facts are re-read, not serialized. |
| OpenCode model-reference grammar | Before, native profiles allowed exactly `provider/model` and rejected another slash. After, only the first slash separates the provider; the non-empty model ID remains opaque and may contain slashes, so catalog references such as `lmstudio/google/gemma-4-26b-a4b` round-trip exactly. This grammar applies to every native OpenCode role, with or without a variant; Claude/Codex grammar and legacy v0.8.0 loading are unchanged. |
| Plan `GateDoc` wire | Before, it remained schema v1 even after its selections could carry variant/no-variant fields, so a stale adapter could silently drop them. After, schema v2 carries all three selection shapes and adapters reject any other result version before reading a selection. |
| `Dispatch` wire | Before, request/result stayed at v3 while executor/reviewer gained the new Selection shape. After, v4 preserves all three shapes, and a v3 request is rejected before dispatch mutation. |
| `Escalate` wire | Before, request/result stayed at v1 while reroutes gained the new Selection shape. After, v2 preserves all three shapes, and a v1 request is rejected before escalation mutation. |
| `Review` wire | Before, request/result stayed at v2, allowing a stale adapter to submit an effort-only approximation that failed later as a reviewer mismatch. After, v3 accepts exact effort/variant/no-variant selections, and v2 is rejected before reviewer comparison. |
| `StatusDoc` wire | Before, it remained schema v1 while transitive state decisions gained the new Selection shape. After, schema v2 exposes all three exact shapes and adapters reject any other result version before reading run state. |
| Adapter protocol pins | The Claude, Codex, and OpenCode Architect/Delivery skills now state the same closed versions. Shared `adaptertest.CheckSelectionWireVersions` derives every expected literal from the engine constants, so no host keeps an unpinned hand-synced Selection schema. |
| Legacy local `effort` validation | Before, raw `effort = ""` bypassed the named legacy-effort domain and silently cleared a committed variant into a bare-model selection. After, every OpenCode role rejects an explicitly empty legacy effort with `variant = ""` as the remediation. Named v0.8.0 efforts remain compatible; only native `variant = ""` selects no variant. Claude/Codex effort behavior is unchanged. |

## Role selection and sessions

`orch init`, `orch configure`, and `orch configure-local` discover models from
the current repository's live catalog on the minimum
`opencode2 v0.0.0-beta-18314` runtime or a newer runtime that passes doctor's
contract checks. All six role selections are independent:
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
comparison. `npm run smoke` renders the shared mixed-provider fixture, starts the
pinned runtime, and checks `/api/agent` for every dispatched role's exact
provider/model/variant reference. Its later tool-path generation uses the runtime's
simulation backend, so the smoke never requires a paid model request.

## Install order

The adapter is not published to npm and releases do not publish an npm
artifact. Install the checked-out `adapters/opencode/` package — the
same directory CI installs with `npm ci --prefix adapters/opencode` —
then load its plugin module and canonical skills directory from absolute paths.

1. Install the `orch` binary first, then clone the repository somewhere
   persistent (not into a project it will orchestrate):

   ```sh
   git clone https://github.com/kninetimmy/orch.git ~/src/orch
   cd ~/src/orch
   npm ci --prefix adapters/opencode
   ```

2. Append the absolute plugin module path and canonical skill source to the
   existing `plugins` and `skills` arrays in OpenCode V2's `opencode.json`.
   Keep all unrelated configuration, plugin entries, and skill-source entries:

   ```json
   {
     "plugins": ["/absolute/path/to/orch/adapters/opencode/src/index.js"],
     "skills": ["/absolute/path/to/orch/adapters/opencode/skills"]
   }
   ```

   These are entries to add, not replacement arrays. The checkout-backed skill
   source makes updates available after a service restart without copying skill
   files into OpenCode's global skill directory.

   The module is supplied by the local
   `@kninetimmy/orch-opencode` package; `npm install
   @kninetimmy/orch-opencode` is not an installation path because that
   package is not published.

3. Restart the OpenCode V2 service, then verify both the supported runtime
   and the active plugin ID before relying on enforcement:

   ```sh
   opencode2 --version
   opencode2 api get /api/plugin
   opencode2 api get /api/plugin | node -e 'let s=""; process.stdin.on("data", d => s += d).on("end", () => { if (JSON.parse(s).data.filter(p => p.id === "orch.delivery").length !== 1) throw new Error("orch.delivery is not active exactly once") })'
   ```

   The first command must print `opencode2 v0.0.0-beta-18314` or a newer
   numeric beta build. The JSON from the second must contain exactly one
   `data` entry whose `id` is `orch.delivery`; the third command fails
   otherwise. `orch doctor` enforces that runtime floor and the same
   plugin-ID requirement for a repository with `hosts.opencode` enabled. It
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
restart OpenCode V2 and repeat the plugin-ID verification:

```sh
git -C ~/src/orch pull --ff-only
npm ci --prefix ~/src/orch/adapters/opencode
opencode2 api get /api/plugin
opencode2 api get /api/plugin | node -e 'let s=""; process.stdin.on("data", d => s += d).on("end", () => { if (JSON.parse(s).data.filter(p => p.id === "orch.delivery").length !== 1) throw new Error("orch.delivery is not active exactly once") })'
```

The configured absolute module and skill-source paths stay the same. If the
checkout path changes, update `opencode.json` to the new absolute
`adapters/opencode/src/index.js` and `adapters/opencode/skills` paths before
restarting.

## Limitations

- The beta API only proves the active plugin ID, not its adapter version.
- The plugin guards OpenCode's `edit`, `write`, and `patch` tools. Shell
  writes bypass Orch's guard and remain subject to OpenCode's own
  permissions.
- The plugin needs `orch` on `PATH`. If it is absent, OpenCode cannot
  receive an Orch guard verdict.
