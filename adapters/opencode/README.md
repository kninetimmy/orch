# OpenCode V2 adapter

This directory is Orch's npm-native OpenCode V2 adapter. Its package ID
is `@kninetimmy/orch-opencode` (version `0.8.0`) and its running plugin
ID is `orch.delivery`. It is not the Codex marketplace adapter: that is
`adapters/codex/`, installed as `orch@orch` from the shared marketplace.

## Compatibility

The OpenCode V2 plugin API is beta. Before the project-scoped catalog
contract landed, the live smoke check exercised exactly `opencode2
v0.0.0-beta-17498` with `@opencode-ai/plugin`
`0.0.0-beta-17498` and did not check the model or returned-location
APIs. That old compatibility behavior no longer holds. After the
catalog change, the package, doctor, documentation, and live smoke pin
exactly `opencode2 v0.0.0-beta-18314` with `@opencode-ai/plugin`
`0.0.0-beta-18314`; that exact beta is the current compatibility floor,
not a claim of compatibility with other V2 builds.

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
| OpenCode runtime and npm plugin pins | No. The before-and-after is recorded above: beta 17498 is replaced by beta 18314. |
| `orch.delivery` plugin ID, setup, guard hooks, and session context | Yes. The same plugin contract remains; unit and live smoke checks still exercise it on the new beta. |
| Catalog process invocation | New. It is one argument-vector call in the repository directory, with the location URL-encoded rather than shell-interpolated. |
| Response `location.directory` | New validation. The initial implementation inherited platform-wide case folding and could accept distinct case-different directories on a case-sensitive macOS or Windows volume; that behavior no longer holds. Directory identity is now compared by the filesystem, so every different directory is rejected while aliases of the same directory remain valid. |
| Response `data[].providerID` and `data[].id` | New projection. They form the exact `provider/id`; neither value is normalized or aliased. |
| Response `data[].enabled` and `data[].capabilities.{tools,input,output}` | New filtering. Only enabled, tool-capable, text-input/text-output models remain. |
| Response `data[].variants[].id` | New projection. Every advertised variant ID is preserved in server order, including an empty list. |
| Provider/model/variant settings, headers, bodies, credentials, account IDs, and raw payload | Previously Orch did not read the catalog; after this change these fields may arrive in command output but are never retained, returned, or included in an error. |
| Setup detection's existing CLI, git, GitHub, memhub, and instruction facts | Yes. Catalog and catalog-error fields are additive, and a failed read remains explicit data rather than changing Detect's no-error contract. |
| Doctor's runtime and active-plugin checks | Yes. They still run and the catalog/location check now follows them. |
| Live smoke's agent discovery, context injection, denied tracked write, and allowed ignored write | Yes. The catalog/location assertions are additive. |
| Interview `profile`, `roleVariantID`, and `variantOptionValues` | Before, committed interviews represented every execution value as host-wide effort. After, the shared profile carries a host-native execution value, OpenCode has a variant question ID plus an explicit no-variant option, and the option builder retains every committed/effective value before adding suggestions. This applies to every OpenCode role; Claude/Codex keep effort IDs and options. |
| Committed question writer `roleDocSpecs` / `committedProfileQuestion` | Before, all six OpenCode role documents asked for effort. After, all six ask for model-specific variant or no variant. Claude/Codex role documents retain their prior model+effort shape. |
| Committed config writer `materializeHost` | Before, every host answer was written to `RoleProfile.Effort`. After, every OpenCode role writes `Variant` (empty for no variant), while every Claude/Codex role still writes `Effort`. |
| Existing-value reader `committedRoleDefaults` | Before, it read only `Effort`, so editing a native OpenCode profile could erase its variant or no-variant meaning. After, every OpenCode role reads `EffectiveOpenCodeVariant`, preserving native and legacy-effective selections; Claude/Codex still read `Effort`. |
| Local writer `localProfileQuestion` / `variantOptionsLocal` | Before, inserting a custom effective override could evict a committed variant such as `high` from the four arrow-key choices. After, both committed and effective values are retained and labelled before suggestions fill remaining slots. |
| OpenCode model-reference grammar | Before, native profiles allowed exactly `provider/model` and rejected another slash. After, only the first slash separates the provider; the non-empty model ID remains opaque and may contain slashes, so catalog references such as `lmstudio/google/gemma-4-26b-a4b` round-trip exactly. This grammar applies to every native OpenCode role, with or without a variant; Claude/Codex grammar and legacy v0.8.0 loading are unchanged. |
| Plan `GateDoc` wire | Before, it remained schema v1 even after its selections could carry variant/no-variant fields, so a stale adapter could silently drop them. After, schema v2 carries all three selection shapes and adapters reject any other result version before reading a selection. |
| `Dispatch` wire | Before, request/result stayed at v3 while executor/reviewer gained the new Selection shape. After, v4 preserves all three shapes, and a v3 request is rejected before dispatch mutation. |
| `Escalate` wire | Before, request/result stayed at v1 while reroutes gained the new Selection shape. After, v2 preserves all three shapes, and a v1 request is rejected before escalation mutation. |
| `Review` wire | Before, request/result stayed at v2, allowing a stale adapter to submit an effort-only approximation that failed later as a reviewer mismatch. After, v3 accepts exact effort/variant/no-variant selections, and v2 is rejected before reviewer comparison. |
| `StatusDoc` wire | Before, it remained schema v1 while transitive state decisions gained the new Selection shape. After, schema v2 exposes all three exact shapes and adapters reject any other result version before reading run state. |
| Adapter protocol pins | The Claude, Codex, and OpenCode Architect/Delivery skills now state the same closed versions. Shared `adaptertest.CheckSelectionWireVersions` derives every expected literal from the engine constants, so no host keeps an unpinned hand-synced Selection schema. |
| Legacy local `effort` validation | Before, raw `effort = ""` bypassed the named legacy-effort domain and silently cleared a committed variant into a bare-model selection. After, every OpenCode role rejects an explicitly empty legacy effort with `variant = ""` as the remediation. Named v0.8.0 efforts remain compatible; only native `variant = ""` selects no variant. Claude/Codex effort behavior is unchanged. |

## Install order

The adapter is not published to npm and releases do not publish an npm
artifact. Install the checked-out `adapters/opencode/` package — the
same directory CI installs with `npm ci --prefix adapters/opencode` —
then load its plugin module from an absolute path.

1. Install the `orch` binary first, then clone the repository somewhere
   persistent (not into a project it will orchestrate):

   ```sh
   git clone https://github.com/kninetimmy/orch.git ~/src/orch
   cd ~/src/orch
   npm ci --prefix adapters/opencode
   ```

2. Add the absolute plugin module path to OpenCode V2's `opencode.json`.
   Keep any existing configuration and plugins:

   ```json
   {
     "plugins": ["/absolute/path/to/orch/adapters/opencode/src/index.js"]
   }
   ```

   The module is supplied by the local
   `@kninetimmy/orch-opencode` package; `npm install
   @kninetimmy/orch-opencode` is not an installation path because that
   package is not published.

3. Restart the OpenCode V2 service, then verify both the pinned runtime
   and the active plugin ID before relying on enforcement:

   ```sh
   opencode2 --version
   opencode2 api get /api/plugin
   opencode2 api get /api/plugin | node -e 'let s=""; process.stdin.on("data", d => s += d).on("end", () => { if (JSON.parse(s).data.filter(p => p.id === "orch.delivery").length !== 1) throw new Error("orch.delivery is not active exactly once") })'
   ```

   The first command must print `opencode2 v0.0.0-beta-18314`. The JSON
   from the second must contain exactly one `data` entry whose `id` is
   `orch.delivery`; the third command fails otherwise. `orch doctor`
   performs the same runtime and plugin-ID checks for a repository with
   `hosts.opencode` enabled, then queries that repository's model catalog
   and verifies its returned location. It cannot check an OpenCode
   adapter version because the beta API does not expose one.

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

The configured absolute module path stays the same. If the checkout
path changes, update `opencode.json` to the new absolute
`adapters/opencode/src/index.js` path before restarting.

## Limitations

- The beta API only proves the active plugin ID, not its adapter version.
- The plugin guards OpenCode's `edit`, `write`, and `patch` tools. Shell
  writes bypass Orch's guard and remain subject to OpenCode's own
  permissions.
- The plugin needs `orch` on `PATH`. If it is absent, OpenCode cannot
  receive an Orch guard verdict.
