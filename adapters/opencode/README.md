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
   override a committed variant locally). Before optional variants, schema-v1
   OpenCode roles used `effort`
   and always appended it; those committed and local v0.8.0 values remain
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
