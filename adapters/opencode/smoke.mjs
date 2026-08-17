import assert from "node:assert/strict"
import { cp, mkdtemp, mkdir, readFile, rm, writeFile } from "node:fs/promises"
import { spawnSync } from "node:child_process"
import { tmpdir } from "node:os"
import path from "node:path"
import { fileURLToPath } from "node:url"

import plugin, { pluginID } from "./src/index.js"

const pinned = "opencode2 v0.0.0-next-17444"
const opencode = "opencode2"
const adapter = path.dirname(fileURLToPath(import.meta.url))
const repo = path.resolve(adapter, "../..")

function command(name, args, cwd) {
  const result = spawnSync(name, args, {
    cwd,
    encoding: "utf8",
    shell: process.platform === "win32" && name === opencode,
    windowsHide: true,
  })
  if (result.error) throw result.error
  if (result.status !== 0) throw new Error((result.stderr || result.stdout).trim())
  return result.stdout
}

assert.equal(command(opencode, ["--version"], repo).trim(), pinned, "OpenCode V2 beta drifted")

const temp = await mkdtemp(path.join(tmpdir(), "orch-opencode-smoke-"))
try {
  await mkdir(path.join(temp, ".opencode", "agents"), { recursive: true })
  await mkdir(path.join(temp, ".orchestrator"), { recursive: true })
  await cp(path.join(adapter, "agents"), path.join(temp, ".opencode", "agents"), { recursive: true })
  await cp(path.join(repo, ".orchestrator", "config.toml"), path.join(temp, ".orchestrator", "config.toml"))
  await writeFile(path.join(temp, ".gitignore"), ".scratch/\n")
  await writeFile(path.join(temp, "tracked.txt"), "before\n")
  command("git", ["init"], temp)
  command("git", ["add", ".gitignore", "tracked.txt", ".orchestrator/config.toml"], temp)
  await writeFile(
    path.join(temp, "opencode.json"),
    JSON.stringify({ plugins: [path.join(adapter, "src", "index.js")] }),
  )
  const plugins = JSON.parse(command(opencode, ["api", "get", "/api/plugin"], temp)).data
  assert.equal(plugins.filter((entry) => entry.id === pluginID).length, 1, "native plugin was not discovered exactly once")
  const agents = JSON.parse(command(opencode, ["api", "get", "/api/agent"], temp)).data
  for (const id of ["orch-scout", "orch-implementer", "orch-specialist", "orch-reviewer", "orch-reviewer-safe"]) {
    assert.equal(agents.find((agent) => agent.id === id)?.mode, "subagent", `${id} was not discovered`)
  }

  const hooks = {}
  await plugin.setup({
    tool: { hook: async (_, callback) => (hooks.tool = callback) },
    session: {
      get: async () => ({ location: { directory: temp } }),
      hook: async (_, callback) => (hooks.session = callback),
    },
  })
  const context = { sessionID: "smoke", system: [] }
  await hooks.session(context)
  assert.match(context.system.at(-1)?.text ?? "", /repository is managed by Orch/)

  const tracked = path.join(temp, "tracked.txt")
  const before = await readFile(tracked, "utf8")
  await assert.rejects(hooks.tool({ sessionID: "smoke", tool: "write", input: { filePath: tracked } }))
  assert.equal(await readFile(tracked, "utf8"), before, "denied mutation reached the filesystem")

  const scratch = path.join(temp, ".scratch", "opencode-smoke.tmp")
  await mkdir(path.dirname(scratch), { recursive: true })
  await hooks.tool({ sessionID: "smoke", tool: "write", input: { filePath: scratch } })
  await writeFile(scratch, "allowed\n")
  await rm(scratch)
} finally {
  await rm(temp, { recursive: true, force: true })
}

console.log("OpenCode V2 smoke passed")
