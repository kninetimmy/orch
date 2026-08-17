import assert from "node:assert/strict"
import { cp, mkdtemp, mkdir, readFile, rm, writeFile } from "node:fs/promises"
import { spawn, spawnSync } from "node:child_process"
import { createServer } from "node:net"
import { tmpdir } from "node:os"
import path, { delimiter } from "node:path"
import { fileURLToPath } from "node:url"

import plugin, { pluginID } from "./src/index.js"

const pinned = "opencode2 v0.0.0-beta-17498"
const opencode = "opencode2"
const serverPassword = "orch-smoke"
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

async function availablePort() {
  const socket = createServer()
  await new Promise((resolve, reject) => socket.listen(0, "127.0.0.1", resolve).once("error", reject))
  const { port } = socket.address()
  await new Promise((resolve, reject) => socket.close((error) => error ? reject(error) : resolve()))
  return port
}

async function apiList(baseURL, pathname, ready) {
  let data = []
  for (let attempt = 0; attempt < 40; attempt++) {
    try {
      const authorization = `Basic ${Buffer.from(`opencode:${serverPassword}`).toString("base64")}`
      data = (await (await fetch(baseURL + pathname, { headers: { authorization } })).json()).data
      if (ready(data)) return data
    } catch {}
    await new Promise((resolve) => setTimeout(resolve, 250))
  }
  return data
}

assert.equal(command(opencode, ["--version"], repo).trim(), pinned, "OpenCode V2 beta drifted")

const temp = await mkdtemp(path.join(tmpdir(), "orch-opencode-smoke-"))
let server
try {
  const orchBinary = path.join(temp, process.platform === "win32" ? "orch.exe" : "orch")
  command("go", ["build", "-o", orchBinary, "./cmd/orch"], repo)
  process.env.PATH = `${temp}${delimiter}${process.env.PATH}`
  await mkdir(path.join(temp, ".opencode", "agents"), { recursive: true })
  await mkdir(path.join(temp, ".orchestrator"), { recursive: true })
  await cp(path.join(adapter, "agents"), path.join(temp, ".opencode", "agents"), { recursive: true })
  await cp(path.join(repo, ".orchestrator", "config.toml"), path.join(temp, ".orchestrator", "config.toml"))
  await writeFile(path.join(temp, ".gitignore"), ".scratch/\n")
  await writeFile(path.join(temp, "tracked.txt"), "before\n")
  command("git", ["init"], temp)
  command("git", ["add", ".gitignore", "tracked.txt", ".orchestrator/config.toml"], temp)
  command("git", ["-c", "user.name=Orch Smoke", "-c", "user.email=orch-smoke@example.invalid", "commit", "-m", "smoke fixture"], temp)
  await writeFile(
    path.join(temp, "opencode.json"),
    JSON.stringify({ plugins: [path.join(adapter, "src", "index.js")] }),
  )
  const port = await availablePort()
  const baseURL = `http://127.0.0.1:${port}`
  server = spawn(opencode, ["serve", "--hostname", "127.0.0.1", "--port", String(port)], {
    cwd: temp,
    env: { ...process.env, OPENCODE_SERVER_PASSWORD: serverPassword },
    shell: process.platform === "win32",
    stdio: "ignore",
    windowsHide: true,
  })
  const plugins = await apiList(baseURL, "/api/plugin", (entries) => entries.some((entry) => entry.id === pluginID))
  assert.equal(plugins.filter((entry) => entry.id === pluginID).length, 1, `native plugin was not discovered exactly once: ${JSON.stringify(plugins)}`)
  const agentIDs = ["orch-scout", "orch-implementer", "orch-specialist", "orch-reviewer", "orch-reviewer-safe"]
  const agents = await apiList(baseURL, "/api/agent", (entries) => agentIDs.every((id) => entries.some((entry) => entry.id === id)))
  for (const id of agentIDs) {
    assert.equal(agents.find((agent) => agent.id === id)?.mode, "subagent", `${id} was not discovered: ${JSON.stringify(agents)}`)
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
  await assert.rejects(hooks.tool({ sessionID: "smoke", agent: "build", tool: "write", input: { path: tracked, content: "after\n" } }))
  assert.equal(await readFile(tracked, "utf8"), before, "denied mutation reached the filesystem")

  const scratch = path.join(temp, ".scratch", "opencode-smoke.tmp")
  await mkdir(path.dirname(scratch), { recursive: true })
  await hooks.tool({ sessionID: "smoke", agent: "build", tool: "write", input: { path: scratch, content: "allowed\n" } })
  await writeFile(scratch, "allowed\n")
  await rm(scratch)
} finally {
  if (server?.pid) {
    if (process.platform === "win32") spawnSync("taskkill", ["/pid", String(server.pid), "/t", "/f"], { stdio: "ignore", windowsHide: true })
    else server.kill("SIGTERM")
  }
  await rm(temp, { recursive: true, force: true, maxRetries: 20, retryDelay: 100 })
}

console.log("OpenCode V2 smoke passed")
