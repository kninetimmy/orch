import { statSync } from "node:fs"
import { isAbsolute, win32 } from "node:path"

export const lookupServer = "orch_memhub"
export const lookupTools = ["status", "recall", "get_row", "locate"]
export const childRoles = new Set(["orch-scout", "orch-implementer", "orch-specialist", "orch-reviewer", "orch-reviewer-safe"])
const normalize = (name) => name.replace(/[^a-zA-Z0-9_-]/g, "_")
const unavailable = (detail) => `orch: Memhub child lookup unavailable: ${detail}. Ask the Architect for mediated lookup; never use a full server, CLI, copied memory or another checkout as fallback.`

export function sameDirectory(left, right) {
  const a = statSync(left, { bigint: true })
  const b = statSync(right, { bigint: true })
  return a.isDirectory() && b.isDirectory() && a.dev === b.dev && a.ino === b.ino
}

export function readLookupContext(run, directory) {
  if (typeof directory !== "string" || !isAbsolute(directory)) throw new Error("lookup session directory is not an absolute path")
  const value = JSON.parse(run(["hook", "opencode", "lookup-context", "--json"], directory))
  if (value?.schema_version !== 2 || typeof value.managed !== "boolean") {
    throw new Error("unsupported Orch lookup-context schema or management scope; update Orch")
  }
  if (!value.managed) {
    if (Object.keys(value).length !== 2) throw new Error("invalid unmanaged lookup-context")
    return value
  }
  if (!["off", "best-effort", "required"].includes(value.memhub_mode)) {
    throw new Error("unsupported Orch lookup-context mode; update Orch")
  }
  for (const key of ["memory_root", "code_root", "git_common_dir"]) {
    if (typeof value[key] !== "string" || !isAbsolute(value[key])) throw new Error(`invalid lookup-context ${key}`)
    if (!statSync(value[key]).isDirectory()) throw new Error(`invalid lookup-context ${key} directory`)
  }
  if (!/^[a-f0-9]{40,64}$/.test(value.head)) throw new Error("invalid lookup-context revision")
  return value
}

export function checkBinding(binding, expected) {
  if (binding?.mode !== "explicit" || !["same_checkout", "registered_worktree"].includes(binding.repository_relationship)) {
    throw new Error("Memhub did not return explicit Git binding evidence")
  }
  for (const key of ["memory_root", "code_root", "git_common_dir"]) {
    if (typeof binding[key] !== "string" || !sameDirectory(binding[key], expected[key])) {
      throw new Error(`Memhub ${key} identity mismatch`)
    }
  }
}

function payload(result) {
  if (result.output?.binding) return result.output
  const text = typeof result.content === "string" ? result.content : result.content?.find((part) => part.type === "text")?.text
  return JSON.parse(text)
}

// Native MCP owns transport, startup, cancellation and shutdown. This module
// only adds configuration/permissions and validates calls at native executors,
// including executors captured by Code Mode; it is not an MCP relay.
export async function installLookup(ctx, run) {
  let expected
  let diagnostic
  try {
    expected = readLookupContext(run, ctx.location?.directory)
  } catch (error) {
    diagnostic = unavailable(error.message)
  }
  // Only the engine's successful, versioned unmanaged observation is a no-op.
  // Discovery/configuration/compatibility failures still install child fences.
  if (expected?.managed === false) return async () => {}
  const capable = typeof ctx.mcp?.transform === "function" && typeof ctx.mcp?.list === "function"
    && typeof ctx.agent?.transform === "function" && typeof ctx.tool?.transform === "function" && typeof ctx.tool?.list === "function"
  if (!capable) diagnostic = unavailable("host lacks native MCP, agent or tool transforms; update OpenCode V2")
  const broad = new Set(["memhub"])
  let ownsConnection = false
  if (capable) {
    await ctx.mcp.transform((editor) => {
      ownsConnection = false
      const entries = editor.list()
      const collision = entries.some(([name]) => normalize(name) === lookupServer)
      if (collision) diagnostic = unavailable(`MCP name collision for ${lookupServer}; user configuration was not overwritten`)
      for (const [name, config] of entries) {
        if (/memhub/i.test(name) || (config.type === "local" && config.command.some((arg) => /^memhub(?:\.exe)?$/i.test(win32.basename(arg))))) {
          broad.add(normalize(name))
        }
      }
      if (!collision && expected && expected.memhub_mode !== "off") {
        editor.set(lookupServer, {
          type: "local", command: ["memhub", "serve", "--memory-root", expected.memory_root, "--code-root", expected.code_root, "--profile", "lookup-only"],
          cwd: expected.code_root, protocol: "legacy", pool: false,
        })
        ownsConnection = true
      }
    })
    await ctx.agent.transform((editor) => {
      for (const agent of editor.list()) {
        editor.update(agent.id, (draft) => {
          if (ownsConnection || childRoles.has(agent.id)) draft.permissions.push({ action: `${lookupServer}_*`, resource: "*", effect: "deny" })
          if (!childRoles.has(agent.id)) return
          for (const name of broad) draft.permissions.push({ action: `${name}_*`, resource: "*", effect: "deny" })
          if (ownsConnection) {
            for (const tool of lookupTools) draft.permissions.push({ action: `${lookupServer}_${tool}`, resource: "*", effect: "allow" })
          }
        })
      }
    })
    await ctx.tool.transform((editor) => {
      const tools = editor.list()
      // Detect renamed/remote full Memhub catalogs too, without changing their
      // configuration or Architect executors. Names alone are not a sandbox.
      for (const entry of tools) {
        if (!entry.id.endsWith("_get_row")) continue
        const name = entry.id.slice(0, -"_get_row".length)
        if (name !== lookupServer && lookupTools.every((tool) => editor.get(`${name}_${tool}`))) broad.add(name)
      }
      for (const entry of tools) {
        const bound = ownsConnection && entry.id.startsWith(`${lookupServer}_`)
        if (!bound && ![...broad].some((name) => entry.id.startsWith(`${name}_`))) continue
        editor.update(entry.id, (draft) => {
          const execute = draft.execute
          draft.execute = async (input, context) => {
            const session = await ctx.session.get({ sessionID: context.sessionID })
            if (!bound) {
              if (session.parentID || childRoles.has(context.agent)) throw new Error("orch: child roles must not use a broad Memhub connection")
              return execute(input, context)
            }
            if (!session.parentID || !childRoles.has(context.agent)) throw new Error("orch: bound Memhub lookup requires a supported Orch child role")
            const current = await preflight(session.location?.directory)
            const name = entry.id.slice(lookupServer.length + 1)
            if (!lookupTools.includes(name)) throw new Error("orch: lookup-only Memhub tool denied")
            const result = await execute(input, context)
            checkBinding(payload(result).binding, current)
            return result
          }
        })
      }
    })
  }
  async function preflight(directory, current = readLookupContext(run, directory)) {
    if (diagnostic) throw new Error(diagnostic)
    if (!current.managed) throw new Error(unavailable("session is outside Orch management; reload its native location"))
    if (current.memhub_mode === "off") throw new Error(unavailable("mode is off; no child lookup is permitted"))
    if (!sameDirectory(current.memory_root, expected.memory_root) || !sameDirectory(current.code_root, expected.code_root)
      || !sameDirectory(current.git_common_dir, expected.git_common_dir)) {
      throw new Error(unavailable("session and native MCP location roots differ; move the session to its assigned checkout and reload that location"))
    }
    const server = (await ctx.mcp.list()).data.find((entry) => entry.name === lookupServer)
    if (server?.status.status !== "connected") {
      throw new Error(unavailable(`native lookup-only service is ${server?.status.status ?? "absent"}; require Memhub serve --memory-root/--code-root/--profile lookup-only support`))
    }
    const tools = (await ctx.tool.list()).filter((entry) => entry.id.startsWith(`${lookupServer}_`)).map((entry) => entry.id.slice(lookupServer.length + 1)).sort()
    if (JSON.stringify(tools) !== JSON.stringify([...lookupTools].sort())) throw new Error(unavailable("Memhub must advertise exactly status, recall, get_row and locate; old/full catalogs are refused"))
    return current
  }
  return async (event, session) => {
    if (!session.parentID && !childRoles.has(event.agent)) return
    if (!childRoles.has(event.agent)) {
      event.system.push({ type: "text", text: "Orch bound Memhub is denied to unsupported child roles; ask the Architect." })
      return
    }
    let mode = expected?.memhub_mode
    try {
      const current = readLookupContext(run, session.location?.directory)
      if (current.managed) mode = current.memhub_mode
      await preflight(session.location?.directory, current)
      event.system.push({ type: "text", text: `Orch child Memhub: native ${lookupServer}, lookup-only; call status first and compare binding identities with the dispatch prompt. Expected memory_root=${current.memory_root}; code_root=${current.code_root}; git_common_dir=${current.git_common_dir}; observed HEAD=${current.head} (not dirty-content identity). Service maintenance may write indexes/logs; semantic/admin writes remain Architect-only.` })
    } catch (error) {
      if (mode === "required") throw error
      event.system.push({ type: "text", text: unavailable(error.message) })
    }
  }
}
