import assert from "node:assert/strict"
import { cp, mkdtemp, mkdir, readFile, rm, writeFile } from "node:fs/promises"
import { spawn, spawnSync } from "node:child_process"
import { createServer } from "node:net"
import { tmpdir } from "node:os"
import path, { delimiter } from "node:path"
import { fileURLToPath } from "node:url"

import { Backend } from "@opencode-ai/protocol/simulation"
import { pluginID } from "./src/index.js"

const pinned = "opencode2 v0.0.0-beta-18314"
const opencode = "opencode2"
const serverPassword = "orch-smoke"
process.env.OPENCODE_SERVER_PASSWORD = serverPassword
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

async function apiEnvelope(baseURL, pathname, options = {}) {
  const authorization = `Basic ${Buffer.from(`opencode:${serverPassword}`).toString("base64")}`
  const response = await fetch(baseURL + pathname, {
    ...options,
    headers: { authorization, "content-type": "application/json", ...options.headers },
  })
  if (!response.ok) throw new Error(`${response.status} ${await response.text()}`)
  return response.status === 204 ? undefined : response.json()
}

async function api(baseURL, pathname, options = {}) {
  return (await apiEnvelope(baseURL, pathname, options))?.data
}

async function within(promise, label) {
  let timer
  try {
    return await Promise.race([
      promise,
      new Promise((_, reject) => (timer = setTimeout(() => reject(new Error(`timed out waiting for ${label}`)), 20_000))),
    ])
  } finally {
    clearTimeout(timer)
  }
}

async function simulation(endpoint) {
  let socket
  for (let attempt = 0; attempt < 40; attempt++) {
    try {
      socket = new WebSocket(endpoint)
      await new Promise((resolve, reject) => {
        socket.addEventListener("open", resolve, { once: true })
        socket.addEventListener("error", reject, { once: true })
      })
      break
    } catch {
      socket?.close()
      socket = undefined
      await new Promise((resolve) => setTimeout(resolve, 250))
    }
  }
  assert(socket, `simulation backend did not listen at ${endpoint}`)

  let nextID = 1
  const pending = new Map()
  const notifications = []
  const waiters = []
  socket.addEventListener("message", ({ data }) => {
    const message = JSON.parse(data)
    if (message.id !== undefined) {
      const callback = pending.get(message.id)
      pending.delete(message.id)
      callback(message)
      return
    }
    Backend.decodeNotification(message)
    const waiter = waiters.find(({ method }) => method === message.method)
    if (waiter) {
      waiters.splice(waiters.indexOf(waiter), 1)
      waiter.resolve(message.params)
    } else {
      notifications.push(message)
    }
  })

  const request = (method, params) => new Promise((resolve, reject) => {
    const id = nextID++
    pending.set(id, (message) => message.error ? reject(new Error(message.error.message)) : resolve(message.result))
    socket.send(JSON.stringify({ jsonrpc: "2.0", id, method, ...(params === undefined ? {} : { params }) }))
  })
  const next = (method) => {
    const index = notifications.findIndex((message) => message.method === method)
    if (index >= 0) return Promise.resolve(notifications.splice(index, 1)[0].params)
    return new Promise((resolve) => waiters.push({ method, resolve }))
  }
  await request("simulation.handshake", {
    client: { name: "orch-smoke", version: "1" },
    expectedRole: "backend",
    offeredVersions: [1],
    requiredCapabilities: ["llm.chunk", "llm.finish"],
    optionalCapabilities: [],
  })
  await request("llm.attach")
  return { socket, request, next }
}

assert.equal(command(opencode, ["--version"], repo).trim(), pinned, "OpenCode V2 beta drifted")

const temp = await mkdtemp(path.join(tmpdir(), "orch-opencode-smoke-"))
let server
let controller
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
  const backendPort = await availablePort()
  const uiPort = await availablePort()
  const state = path.join(temp, "state")
  await mkdir(path.join(temp, ".config"))
  await mkdir(path.join(state, "opencode-drive", "instances"), { recursive: true })
  await writeFile(path.join(state, "opencode-drive", "instances", "orch-smoke.json"), JSON.stringify({
    endpoints: { ui: `ws://127.0.0.1:${uiPort}`, backend: `ws://127.0.0.1:${backendPort}` },
  }))
  await writeFile(path.join(temp, "opencode.json"), JSON.stringify({
    model: "smoke/smoke",
    permissions: [{ action: "write", resource: "*", effect: "allow" }],
    plugins: [path.join(adapter, "src", "index.js")],
    snapshots: false,
    providers: {
      smoke: {
        package: "aisdk:@ai-sdk/openai-compatible",
        settings: { apiKey: "smoke", baseURL: "https://api.openai.com/v1" },
        models: {
          smoke: {
            name: "Smoke",
            capabilities: { tools: true, input: ["text"], output: ["text"] },
            variants: [{ id: "low" }, { id: "high" }],
          },
        },
      },
    },
  }))
  const port = await availablePort()
  const baseURL = `http://127.0.0.1:${port}`
  server = spawn(opencode, ["serve", "--hostname", "127.0.0.1", "--port", String(port)], {
    cwd: temp,
    env: {
      ...process.env,
      OPENCODE_CONFIG_DIR: path.join(temp, ".config"),
      OPENCODE_DB: ":memory:",
      OPENCODE_DRIVE: "orch-smoke",
      OPENCODE_SERVER_PASSWORD: serverPassword,
      OPENCODE_SIMULATE: "1",
      XDG_STATE_HOME: state,
    },
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
  const query = new URLSearchParams({ "location[directory]": temp })
  const endpoint = `/api/model?${query}`
  const catalog = JSON.parse(command(opencode, ["api", "get", endpoint, "--server", baseURL], temp))
  assert.equal(path.resolve(catalog.location.directory), path.resolve(temp), "catalog response location drifted")
  const smokeModel = catalog.data.find((model) => model.providerID === "smoke" && model.id === "smoke")
  assert(smokeModel?.enabled, "project model was not enabled in the live catalog")
  assert(smokeModel.capabilities.tools && smokeModel.capabilities.input.includes("text") && smokeModel.capabilities.output.includes("text"), "project model was not agent-capable")
  assert.deepEqual(smokeModel.variants.map((variant) => variant.id), ["low", "high"], "live catalog variants drifted")

  controller = await simulation(`ws://127.0.0.1:${backendPort}`)
  const tracked = path.join(temp, "tracked.txt")
  const before = await readFile(tracked, "utf8")
  const scratch = path.join(temp, ".scratch", "opencode-smoke.tmp")
  const session = await api(baseURL, "/api/session", {
    method: "POST",
    body: JSON.stringify({ location: { directory: temp }, model: { providerID: "smoke", id: "smoke" } }),
  })
  await api(baseURL, `/api/session/${session.id}/prompt`, {
    method: "POST",
    body: JSON.stringify({ text: "Run the requested writes.", files: [], agents: [], skills: [] }),
  })

  let trackedTurn = await within(controller.next("llm.request"), "the first model request")
  if (/title generator/.test(JSON.stringify(trackedTurn.body))) {
    await controller.request("llm.chunk", { id: trackedTurn.id, items: [{ type: "textDelta", text: "Smoke writes" }] })
    await controller.request("llm.finish", { id: trackedTurn.id, reason: "stop" })
    trackedTurn = await within(controller.next("llm.request"), "the main model request")
  }
  assert.match(JSON.stringify(trackedTurn.body), /repository is managed by Orch/, "live session context omitted Orch instructions")
  await controller.request("llm.chunk", {
    id: trackedTurn.id,
    items: [{ type: "toolCall", index: 0, id: "tracked", name: "write", input: { path: tracked, content: "after\n" } }],
  })
  await controller.request("llm.finish", { id: trackedTurn.id, reason: "tool-calls" })

  const scratchTurn = await within(controller.next("llm.request"), "the denied write result")
  assert.match(JSON.stringify(scratchTurn.body), /orch guard:.*assist is read-only/, "denial did not come from the Orch guard")
  assert.equal(await readFile(tracked, "utf8"), before, "denied mutation reached the filesystem")
  await controller.request("llm.chunk", {
    id: scratchTurn.id,
    items: [{ type: "toolCall", index: 0, id: "scratch", name: "write", input: { path: scratch, content: "allowed\n" } }],
  })
  await controller.request("llm.finish", { id: scratchTurn.id, reason: "tool-calls" })

  const finishTurn = await within(controller.next("llm.request"), "the allowed write result")
  await controller.request("llm.chunk", { id: finishTurn.id, items: [{ type: "textDelta", text: "done" }] })
  await controller.request("llm.finish", { id: finishTurn.id, reason: "stop" })
  await api(baseURL, `/api/session/${session.id}/wait`, { method: "POST" })
  assert.equal(await readFile(scratch, "utf8"), "allowed\n", "allowed mutation did not traverse the live write tool")
} finally {
  controller?.socket.close()
  if (server?.pid) {
    if (process.platform === "win32") spawnSync("taskkill", ["/pid", String(server.pid), "/t", "/f"], { stdio: "ignore", windowsHide: true })
    else server.kill("SIGTERM")
  }
  await rm(temp, { recursive: true, force: true, maxRetries: 20, retryDelay: 100 })
}

console.log("OpenCode V2 smoke passed")
