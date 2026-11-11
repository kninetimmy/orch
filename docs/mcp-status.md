# Read-only live status over MCP

`orch mcp` is an optional local stdio MCP server using the official Go SDK
v1.8.0. This is an unreleased source feature, not part of the v0.15.2 binary.
Build `./cmd/orch` locally with Go 1.26+ to try it; no global installation or
host configuration edits are performed automatically.

## Launch and repository binding

Point an MCP client at an absolute path to your built binary with these arguments:

```text
orch mcp --repo /absolute/path/to/main-checkout
```

Without `--repo`, Orch binds to the process's current working directory. The
selected directory must already exist and contain a real `.orchestrator/`
directory, not a metadata file or symlink. Orch canonicalizes the selected path
using its existing path checks and fails clearly if it cannot verify it. It
never searches ancestors or initializes a repository. Tool arguments cannot
change this binding; launch another server to inspect another checkout.

**Bind the main checkout explicitly to inspect its live run.** Delivery
worktrees contain committed metadata but do not share the main checkout's
machine-local state or Delivery lock. Launching there without `--repo` can
legitimately report Assist even while the main checkout has an active run.

The client owns the child process and its stdin/stdout pipes. Stdout contains
only MCP protocol messages; diagnostics go to stderr. Closing the client's
session closes stdin and lets the server exit cleanly. Running the launch
command alone in a terminal waits for MCP input; it does not print a status.

## Client configuration and tool

Minimal configuration for clients that use an `mcpServers` object (adapt the
paths and the configuration location for your client):

```json
{
  "mcpServers": {
    "orch": {
      "command": "/absolute/path/to/local/orch",
      "args": ["mcp", "--repo", "/absolute/path/to/main-checkout"]
    }
  }
}
```

On Windows, use `orch.exe` and JSON-escaped absolute paths such as
`C:\\work\\orch.exe` and `C:\\work\\project`. Review and apply configuration
yourself; the server does not edit client or host settings.

Tool discovery returns exactly one tool, **`orch_run_status`**, annotated
read-only. Call it with `{}`; its empty object schema rejects unexpected
arguments, including a repository selector. Each invocation reads fresh local
state through the same engine as `orch run status --json` and returns that
entire document as both `structuredContent` and equivalent JSON text content.
The status document currently has `schema_version: 4`. Mode, consistency
warnings, lock, active run, issue phases and block/resolution histories are
preserved whenever present. Inconsistent state/lock pairs remain inspectable
with warnings. Corrupt state is an explicit MCP tool error (`isError: true`),
not a fabricated Assist result.

Initialization, discovery, calls (including failures) and disconnect do not
write repository files, run lifecycle mutations or launch external commands.
This server does not replace the CLI, the thin host plugins or their guards,
and it does not change Orch's separate memhub CLI integration.

## Reproducible smoke test and trial checkpoint

From the source checkout, run:

```sh
go test ./internal/cli -run '^TestMCPStdio$' -v -count=1
```

This builds a temporary real `orch` binary and uses an SDK client over its
stdio pipes to initialize, discover and call the tool, then disconnect and
verify a successful process exit. No global install or client configuration
is needed. The client stays connected while test fixtures change from Assist
to blocked Delivery and inconsistent state/lock pairs. Output reports
`CLI = structured = JSON text` for each decoded status comparison and the
explicit corrupt-state tool error. It also checks strict arguments, repository
binding and unchanged repository files. These tests run in `go test ./...`.

For a manual trial, use the configuration above, connect your client, discover
`orch_run_status`, and call it with `{}`. Compare its decoded JSON with
`/absolute/path/to/local/orch run status --json` run **from the bound checkout**.
After a separately approved normal workflow changes that checkout's state,
call again in the same session and compare again. Disconnect the client and
confirm its server child exits. Do not edit live state files to create a trial;
the automated smoke test supplies isolated fixtures for that purpose.

This increment implements roadmap item 1 (live status) only. The trial
checkpoint is to **finish roadmap items 1 through 4, then stop and assess the
benefit against CLI usage before approving later roadmap items**. This status
server does not approve or implement those later increments.
