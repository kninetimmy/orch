import assert from "node:assert/strict"
import { existsSync } from "node:fs"
import { copyFile, cp, mkdir, mkdtemp, readFile, realpath, rm, symlink, writeFile } from "node:fs/promises"
import { spawn, spawnSync } from "node:child_process"
import { createServer as createSocket } from "node:net"
import { createServer } from "node:http"
import { hostname, tmpdir } from "node:os"
import path, { delimiter } from "node:path"
import { fileURLToPath } from "node:url"

import { Backend } from "@opencode/protocol/simulation"
import { pluginID } from "./src/index.js"

const adapter = path.dirname(fileURLToPath(import.meta.url))
const repo = path.resolve(adapter, "../..")
const baseline = "2.2.6"
const agentModels = {
  "orch-scout": { providerID: "smoke-alpha", id: "scout", variant: "fast" },
  "orch-implementer": { providerID: "smoke-beta", id: "implementer" },
  "orch-specialist": { providerID: "smoke-alpha", id: "team/specialist", variant: "deep" },
  "orch-reviewer": { providerID: "smoke-beta", id: "reviewer" },
  "orch-reviewer-safe": { providerID: "smoke-beta", id: "reviewer-safe", variant: "careful" },
}
const architect = { providerID: "smoke-alpha", id: "architect", variant: "steady" }
const patchModel = { providerID: "smoke-alpha", id: "gpt-5.6-smoke" }
const fixturePermissions = [
  { action: "*", resource: "*", effect: "allow" },
  { action: "external_directory", resource: "*", effect: "ask" },
]
const sleep = (ms) => new Promise((resolve) => setTimeout(resolve, ms))

function admittedVersion(output) {
  const match = /^opencode2? v(0\.0\.0-beta-(\d+)|(\d+)\.(\d+)\.(\d+))$/.exec(output.trim())
  if (!match) return undefined
  const parts = match[2] ? [match[2]] : match.slice(3)
  if (!match[2] && parts.some((part) => part.length > 1 && part[0] === "0")) return undefined
  if (parts.some((part) => part.length > 20 || BigInt(part) > 18446744073709551615n)) return undefined
  return (match[2] ? BigInt(match[2]) >= 18314n : BigInt(match[3]) >= 2n) ? match[1] : undefined
}

assert(admittedVersion("opencode v2.10.0"))
assert(admittedVersion("opencode v3.0.0"))
assert(admittedVersion("opencode2 v0.0.0-beta-18314"))
assert.equal(admittedVersion("opencode v1.18.34"), undefined)
assert.equal(admittedVersion("opencode v2.2.6 extra"), undefined)
assert.equal(admittedVersion("opencode v2.02.6"), undefined)

function findOpenCode() {
  for (const name of ["opencode2", "opencode"]) {
    const candidates = process.platform === "win32"
      ? (process.env.PATH ?? "").split(delimiter).filter(Boolean).flatMap((directory) => [
        path.join(directory, name + ".exe"),
        ...(existsSync(path.join(directory, name + ".cmd")) || existsSync(path.join(directory, name + ".ps1"))
          ? [path.join(directory, "node_modules", "@opencode", "cli", "bin", "opencode.exe")] : []),
      ]).filter(existsSync)
      : [name]
    for (const executable of candidates) {
      const result = spawnSync(executable, ["--version"], { encoding: "utf8", windowsHide: true })
      const version = result.status === 0 && admittedVersion(result.stdout)
      if (!version) continue
      if (version !== baseline) console.log("Advisory: OpenCode " + version + " is unverified; checking the live contracts.")
      return executable
    }
  }
  throw new Error("No supported OpenCode V2 executable found; minimum beta 18314 or stable 2.0.0 is required.")
}

function inside(root, target) {
  const relative = path.relative(root, target)
  return relative === "" || (!path.isAbsolute(relative) && relative !== ".." && !relative.startsWith(".." + path.sep))
}

function result(name, args, cwd, env = process.env) {
  const value = spawnSync(name, args, { cwd, env, encoding: "utf8", windowsHide: true })
  if (value.error) throw value.error
  return value
}

function command(name, args, cwd, env) {
  const value = result(name, args, cwd, env)
  if (value.status !== 0) throw new Error(name + " exited " + value.status + ": " + (value.stderr || value.stdout).trim())
  return value.stdout
}

async function availablePort() {
  const socket = createSocket()
  await new Promise((resolve, reject) => socket.listen(0, "127.0.0.1", resolve).once("error", reject))
  const port = socket.address().port
  await new Promise((resolve, reject) => socket.close((error) => error ? reject(error) : resolve()))
  return port
}

async function within(promise, label) {
  let timer
  try {
    return await Promise.race([
      promise,
      new Promise((_, reject) => (timer = setTimeout(() => reject(new Error("timed out waiting for " + label)), 30_000))),
    ])
  } finally {
    clearTimeout(timer)
  }
}

const opencode = findOpenCode()
const temp = await realpath(await mkdtemp(path.join(tmpdir(), "orch-opencode-smoke-")))
assert(!inside(await realpath(repo), temp), "smoke fixtures must be outside the source checkout")
const root = path.join(temp, "repo")
const bin = path.join(temp, "bin")
const state = path.join(temp, "state")
const configFile = path.join(root, "opencode.json")
const query = (directory) => new URLSearchParams({ "location[directory]": directory }).toString()
let server
let serverExit
let controller
let headers
let baseURL
let requests = 0
const provider = createServer((_, response) => {
  requests++
  response.writeHead(503)
  response.end("simulation must intercept every provider request")
})
provider.on("connect", (_, socket) => {
  requests++
  socket.end("HTTP/1.1 503 Service Unavailable\r\n\r\n")
})
let logs = ""
try {
  await new Promise((resolve) => provider.listen(0, "127.0.0.1", resolve))
  const trapURL = "http://127.0.0.1:" + provider.address().port
  const providerURL = "https://api.openai.com/v1"
  await mkdir(bin)
  await mkdir(path.join(temp, "tmp"))
  await mkdir(path.join(root, ".orchestrator"), { recursive: true })
  await cp(path.join(repo, "internal", "agents", "testdata", "opencode-mixed-provider.toml"), path.join(root, ".orchestrator", "config.toml"))
  await writeFile(path.join(root, ".gitignore"), ".opencode/agents/\n.orchestrator/\n.scratch/\nopencode.json\n")
  await writeFile(path.join(root, "tracked.txt"), "before\n")
  command("git", ["init", "-b", "main"], root)
  command("git", ["add", ".gitignore", "tracked.txt"], root)
  command("git", ["add", "-f", ".orchestrator/config.toml"], root)
  command("git", ["-c", "user.name=Orch Smoke", "-c", "user.email=orch-smoke@example.invalid", "commit", "-m", "Create synthetic smoke fixture"], root)
  const writable = path.join(root, ".orchestrator", "worktrees", "issue-1")
  const sibling = path.join(root, ".orchestrator", "worktrees", "sibling")
  command("git", ["worktree", "add", "-b", "smoke-issue", writable], root)
  command("git", ["worktree", "add", "-b", "smoke-sibling", sibling], root)
  const orchBinary = path.join(bin, process.platform === "win32" ? "orch.exe" : "orch")
  command("go", ["build", "-o", orchBinary, "./cmd/orch"], repo)
  const privateOpenCode = path.join(bin, process.platform === "win32" ? "opencode2.exe" : "opencode2")
  if (process.platform === "win32") await copyFile(opencode, privateOpenCode)
  else await symlink(command("which", [opencode], repo).trim(), privateOpenCode)
  const git = command(process.platform === "win32" ? "where.exe" : "which", ["git"], repo).trim().split(/\r?\n/)[0]
  const env = Object.fromEntries(Object.entries(process.env)
    .filter(([name]) => !/API_KEY|TOKEN|SECRET|CREDENTIAL|ACCESS_KEY|PASSWORD|COOKIE|^GIT_(DIR|WORK_TREE|INDEX_FILE)$|^(HTTP|HTTPS|ALL|NO)_PROXY$/i.test(name))
    .map(([name, value]) => [process.platform === "win32" ? name.toUpperCase() : name, value]))
  Object.assign(env, {
    PATH: [bin, path.dirname(git), ...(process.platform === "win32" ? [path.join(process.env.SystemRoot, "System32")] : ["/usr/bin", "/bin"])].join(delimiter),
    OPENCODE_TEST_HOME: temp,
    XDG_CONFIG_HOME: path.join(temp, "config"),
    XDG_STATE_HOME: state,
    XDG_DATA_HOME: path.join(temp, "data"),
    XDG_CACHE_HOME: path.join(temp, "cache"),
    TMP: path.join(temp, "tmp"),
    TEMP: path.join(temp, "tmp"),
    TMPDIR: path.join(temp, "tmp"),
    OPENCODE_DB: ":memory:",
    OPENCODE_DRIVE: "orch-smoke",
    OPENCODE_SIMULATE: "1",
    HTTP_PROXY: trapURL,
    HTTPS_PROXY: trapURL,
    ALL_PROXY: trapURL,
    NO_PROXY: "127.0.0.1,localhost",
  })
  delete env.OPENCODE_CONFIG
  delete env.OPENCODE_CONFIG_CONTENT
  delete env.OPENCODE_CONFIG_DIR
  delete env.OPENCODE_SERVER_PASSWORD
  delete env.OPENCODE_CLI_CONFIG_CONTENT
  for (const directory of [root, writable, sibling]) command(orchBinary, ["render-agents"], directory, env)
  const backendPort = await availablePort()
  const uiPort = await availablePort()
  await mkdir(path.join(state, "opencode-drive", "instances"), { recursive: true })
  await writeFile(path.join(state, "opencode-drive", "instances", "orch-smoke.json"), JSON.stringify({
    endpoints: { ui: "ws://127.0.0.1:" + uiPort, backend: "ws://127.0.0.1:" + backendPort },
  }))
  const configuration = {
    model: "smoke-alpha/architect#steady",
    permissions: fixturePermissions,
    plugins: [adapter],
    skills: [path.join(adapter, "skills")],
    snapshots: false,
    providers: {},
  }
  for (const [providerID, models] of Object.entries({
    "smoke-alpha": [["architect", "steady"], ["scout", "fast"], ["team/specialist", "deep"], [patchModel.id]],
    "smoke-beta": [["implementer"], ["reviewer"], ["reviewer-safe", "careful"]],
  })) {
    configuration.providers[providerID] = {
      package: "@opencode/ai/providers/openai-compatible",
      settings: { apiKey: "smoke", baseURL: providerURL },
      models: Object.fromEntries(models.map(([id, variant]) => [id, {
        name: id,
        capabilities: { tools: true, input: ["text"], output: ["text"] },
        variants: variant ? [{ id: variant }] : [],
      }])),
    }
  }
  await writeFile(configFile, JSON.stringify(configuration))
  const port = await availablePort()
  baseURL = "http://127.0.0.1:" + port
  server = spawn(privateOpenCode, ["serve", "--service", "--hostname", "127.0.0.1", "--port", String(port)], {
    cwd: root, env, stdio: ["ignore", "pipe", "pipe"], windowsHide: true,
  })
  server.stdout.on("data", (data) => (logs += data))
  server.stderr.on("data", (data) => (logs += data))
  serverExit = new Promise((resolve, reject) => {
    server.once("exit", resolve)
    server.once("error", reject)
  })

  async function eventually(read, label) {
    for (let attempt = 0; attempt < 80; attempt++) {
      if (!server.pid || server.exitCode !== null) throw new Error("private server exited: " + logs.slice(-4000))
      try {
        const value = await read()
        if (value) return value
      } catch (error) {
        if (!["ENOENT", "ECONNREFUSED"].includes(error.code ?? error.cause?.code)) throw error
      }
      await sleep(250)
    }
    throw new Error("timed out waiting for " + label + ": " + logs.slice(-4000))
  }

  const service = await eventually(async () => JSON.parse(await readFile(path.join(state, "opencode", "service.json"), "utf8")), "private service record")
  assert.equal(service.pid, server.pid, "service discovery must name our own process")
  assert.equal(service.url, baseURL)
  headers = { authorization: "Basic " + Buffer.from("opencode:" + service.password).toString("base64"), "content-type": "application/json" }
  async function api(endpoint, options = {}) {
    const response = await fetch(baseURL + endpoint, { ...options, headers, signal: AbortSignal.timeout(10_000) })
    if (!response.ok) throw new Error(endpoint + " returned HTTP " + response.status + ": " + (await response.text()).slice(0, 2000))
    return response.status === 204 ? undefined : response.json()
  }
  async function plugins(directory = root) {
    return (await api("/api/plugin?" + query(directory))).data.filter((entry) => entry.id === pluginID)
  }
  const info = await api("/api/info")
  assert.equal(info.pid, server.pid)
  assert(inside(temp, path.resolve(info.paths.tmp)), "runtime temporary files must stay in the isolated fixture")
  async function activePlugin(directory = root) {
    return eventually(async () => {
      const entries = await plugins(directory)
      assert(entries.length <= 1, "Orch must not be loaded twice")
      if (!entries.length) return undefined
      assert.notEqual(entries[0].state?.status, "failed", "the actual adapter package failed to load")
      if (entries[0].state?.status !== "active") return undefined
      assert.equal(entries[0].features?.server, true)
      return entries[0]
    }, "one active Orch server plugin")
  }
  await activePlugin()
  console.log("PASS real package-directory loading: one active orch.delivery server plugin")
  const agents = await eventually(async () => {
    const entries = (await api("/api/agent?" + query(root))).data
    return Object.keys(agentModels).every((id) => entries.some((entry) => entry.id === id)) && entries
  }, "generated agent discovery")
  for (const [id, model] of Object.entries(agentModels)) {
    const agent = agents.find((entry) => entry.id === id)
    assert.equal(agent.mode, "subagent")
    assert.deepEqual(agent.model, model)
  }
  const catalog = await api("/api/model?" + query(root))
  assert.equal(path.resolve(catalog.location.directory), root)
  for (const expected of [architect, ...Object.values(agentModels)]) {
    const model = catalog.data.find((entry) => entry.providerID === expected.providerID && entry.id === expected.id)
    assert(model?.enabled && model.capabilities.tools && model.capabilities.input.includes("text") && model.capabilities.output.includes("text"))
    if (expected.variant) assert(model.variants.some((variant) => variant.id === expected.variant))
  }
  const doctor = result(orchBinary, ["doctor", "--host", "opencode"], root, env)
  assert.match(doctor.stdout, /ok\s+opencode adapter/, doctor.stdout + doctor.stderr)
  assert.match(doctor.stdout, /ok\s+opencode agent files/, doctor.stdout + doctor.stderr)
  console.log("PASS source doctor OpenCode checks (isolated fixture; unrelated GitHub checks may fail)")

  controller = await simulation("ws://127.0.0.1:" + backendPort)
  console.log("PASS deterministic simulation handshake and attachment; no session has run yet")
  let callID = 0
  async function nextTurn() {
    const turn = await within(controller.next("llm.request"), "simulated model request")
    assert.equal(turn.url, "https://api.openai.com/v1/chat/completions", "model must use the registered simulation destination")
    return turn
  }
  function context(turn, mismatch = false) {
    const body = JSON.stringify(turn.body)
    assert.equal((body.match(/repository is managed by Orch/g) ?? []).length, 1, "context hook must run exactly once")
    assert.equal(/Warning: this main session selected/.test(body), mismatch, "Architect comparison must apply only to mismatched roots")
  }
  async function start(directory, model = architect, parentID) {
    const { data: session } = await api("/api/session", {
      method: "POST",
      body: JSON.stringify({ location: { directory }, model, parentID, title: "Orch deterministic smoke", permissions: fixturePermissions }),
    })
    assert(session.id, "session API must return a real session identity")
    await api("/api/session/" + session.id + "/prompt", { method: "POST", body: JSON.stringify({ text: "Execute the deterministic smoke tool calls." }) })
    return { session, turn: await nextTurn() }
  }
  async function tool(turn, name, input, denial) {
    const id = "smoke-" + ++callID
    await controller.request("llm.chunk", { id: turn.id, items: [{ type: "toolCall", index: 0, id, name, input }] })
    await controller.request("llm.finish", { id: turn.id, reason: "tool-calls" })
    const next = await nextTurn()
    const entry = next.body.messages.findLast((message) => message.tool_call_id === id)
    assert(entry, "real tool execution must return the requested call identity")
    const body = JSON.stringify(entry)
    if (denial) assert.match(body, denial, "denial must come from Orch before native mutation")
    else {
      assert.doesNotMatch(body, /orch guard:/, "permitted mutation was denied")
      assert.doesNotMatch(body, /Unknown tool|not found in the tool/, "tool must run through the real host")
      assert(!entry.content.includes('"error":'), "native tool failed: " + entry.content)
    }
    return next
  }
  async function finish(session, turn) {
    await controller.request("llm.chunk", { id: turn.id, items: [{ type: "textDelta", text: "done" }] })
    await controller.request("llm.finish", { id: turn.id, reason: "stop" })
    await eventually(async () => !(await api("/api/session/active")).data[session.id], "session idle")
  }

  const tracked = path.join(root, "tracked.txt")
  const scratch = path.join(root, ".scratch", "allowed.txt")
  const before = await readFile(tracked, "utf8")
  const homeRelative = path.relative(temp, tracked).split(path.sep).join("/")
  const homePaths = ["~/" + homeRelative, ...(process.platform === "win32" ? ["~\\" + homeRelative.replaceAll("/", "\\")] : [])]
  const windowsAliases = process.platform === "win32" ? (() => {
    const forward = tracked.replaceAll("\\", "/")
    const drive = forward[0].toLowerCase()
    const tail = forward.slice(3)
    return ["/" + drive + ":/" + tail, "/" + drive + "/" + tail, "/cygdrive/" + drive + "/" + tail, "/mnt/" + drive + "/" + tail]
  })() : []
  let { session, turn } = await start(root)
  context(turn)
  for (const [name, input] of [
    ["write", { path: tracked, content: "denied\n" }],
    ["edit", { path: tracked, oldString: "before", newString: "denied" }],
  ]) {
    turn = await tool(turn, name, input, /orch guard:.*assist is read-only/)
    assert.equal(await readFile(tracked, "utf8"), before)
  }
  for (const target of [...windowsAliases, ...homePaths]) {
    const denial = homePaths.includes(target) ? /orch: home-relative paths are unsupported/ : /orch guard:.*assist is read-only/
    for (const [name, input] of [
      ["write", { path: target, content: "denied\n" }],
      ["edit", { path: target, oldString: "before", newString: "denied" }],
    ]) {
      turn = await tool(turn, name, input, denial)
      assert.equal(await readFile(tracked, "utf8"), before)
    }
  }
  turn = await tool(turn, "write", { path: scratch, content: "allowed\n" })
  assert.equal(await readFile(scratch, "utf8"), "allowed\n")
  turn = await tool(turn, "edit", { path: scratch, oldString: "allowed", newString: "edited" })
  assert.equal(await readFile(scratch, "utf8"), "edited\n")
  await finish(session, turn)
  const editingSession = session
  ;({ session, turn } = await start(root, patchModel))
  context(turn, true)
  assert(turn.body.tools.some((entry) => entry.function?.name === "patch"), "GPT-shaped fixture must expose the real patch tool")
  turn = await tool(turn, "patch", { patchText: "*** Begin Patch\n*** Update File: " + tracked + "\n@@\n-before\n+denied\n*** End Patch" }, /orch guard:.*assist is read-only/)
  assert.equal(await readFile(tracked, "utf8"), before)
  const multi = path.join(root, ".scratch", "multi.txt")
  turn = await tool(turn, "patch", { patchText: "*** Begin Patch\n*** Add File: " + multi + "\n+must not exist\n*** Update File: " + tracked + "\n@@\n-before\n+denied\n*** End Patch" }, /orch guard:.*assist is read-only/)
  assert.equal(existsSync(multi), false)
  assert.equal(await readFile(tracked, "utf8"), before)
  turn = await tool(turn, "patch", { patchText: "*** Begin Patch\n*** Update File: " + scratch + "\n*** Move to: " + tracked + "\n@@\n-edited\n+denied\n*** End Patch" }, /orch guard:.*assist is read-only/)
  assert.equal(await readFile(scratch, "utf8"), "edited\n")
  assert.equal(await readFile(tracked, "utf8"), before)
  const moved = path.join(root, ".scratch", "moved.txt")
  turn = await tool(turn, "patch", { patchText: "*** Begin Patch\n*** Update File: " + scratch + "\n*** Move to: " + moved + "\n@@\n-edited\n+moved\n*** End Patch" })
  assert.equal(existsSync(scratch), false)
  assert.equal(await readFile(moved, "utf8"), "moved\n")
  const partial = path.join(root, ".scratch", "indented-partial.txt")
  for (const targetHeader of [
    "  *** Add File: tracked.txt\n+overwritten",
    "\t*** Update File: tracked.txt  \n@@\n-before\n+overwritten",
    "  *** Delete File: tracked.txt",
  ]) {
    turn = await tool(turn, "patch", { patchText: "*** Begin Patch\n*** Add File: .scratch/indented-partial.txt\n+safe\n" + targetHeader + "\n*** End Patch" }, /orch guard:.*assist is read-only/)
    assert.equal(existsSync(partial), false, "a later hidden target must deny the entire patch")
    assert.equal(await readFile(tracked, "utf8"), before)
  }
  const heredocSource = path.join(root, ".scratch", "header-source.txt")
  const heredocMoved = path.join(root, ".scratch", "header-moved.txt")
  const heredoc = (body) => "cat <<'PATCH'\r\n*** Begin Patch\r\n" + body.replaceAll("\n", "\r\n") + "\r\n*** End Patch\r\nPATCH"
  turn = await tool(turn, "patch", { patchText: heredoc("  *** Add File: " + heredocSource + "  \n+header") })
  const headerBytes = await readFile(heredocSource)
  assert.equal(headerBytes.toString().trimEnd(), "header")
  turn = await tool(turn, "patch", { patchText: heredoc("  *** Update File: " + heredocSource + "  \n*** Move to: tracked.txt  \n@@\n-header\n+overwritten") }, /orch guard:.*assist is read-only/)
  assert.deepEqual(await readFile(heredocSource), headerBytes)
  assert.equal(await readFile(tracked, "utf8"), before)
  turn = await tool(turn, "patch", { patchText: heredoc("  *** Update File: " + heredocSource + "  \n*** Move to: " + heredocMoved + "  \n@@\n-header\n+header moved") })
  assert.equal(existsSync(heredocSource), false)
  assert.equal((await readFile(heredocMoved, "utf8")).trimEnd(), "header moved")
  for (const target of [...windowsAliases, ...homePaths]) {
    const denial = homePaths.includes(target) ? /orch: home-relative paths are unsupported/ : /orch guard:.*assist is read-only/
    const attemptedMove = path.join(root, ".scratch", "alias-move.txt")
    for (const body of [
      "*** Add File: " + target + "\n+overwritten",
      "*** Update File: " + target + "\n@@\n-before\n+overwritten",
      "*** Delete File: " + target,
      "*** Update File: " + target + "\n*** Move to: " + attemptedMove + "\n@@\n-before\n+overwritten",
      "*** Update File: " + moved + "\n*** Move to: " + target + "\n@@\n-moved\n+overwritten",
    ]) {
      turn = await tool(turn, "patch", { patchText: "*** Begin Patch\n" + body + "\n*** End Patch" }, denial)
      assert.equal(await readFile(tracked, "utf8"), before)
      assert.equal(await readFile(moved, "utf8"), "moved\n")
      assert.equal(existsSync(attemptedMove), false)
    }
  }
  await finish(session, turn)
  console.log("PASS Assist edit/write/patch, ignored scratch, multi-file and both move targets")
  console.log("PASS native indented/heredoc parsing and alias/home path byte preservation")

  const mismatch = await start(root, agentModels["orch-implementer"])
  context(mismatch.turn, true)
  assert.equal(mismatch.session.model.id, agentModels["orch-implementer"].id)
  assert.equal(mismatch.session.model.providerID, agentModels["orch-implementer"].providerID)
  await finish(mismatch.session, mismatch.turn)
  assert.deepEqual((await api("/api/session/" + mismatch.session.id)).data.model, mismatch.session.model)
  const child = await start(root, agentModels["orch-implementer"], mismatch.session.id)
  context(child.turn)
  await finish(child.session, child.turn)
  console.log("PASS root warning without switching models; child context omits false Architect comparison")

  const fixtureState = {
    schema_version: 6, mode: "delivery",
    run: {
      id: "run-synthetic-smoke", host: "opencode", started_at: new Date().toISOString(),
      plan: { title: "Synthetic containment fixture; not a human-approved Delivery run" },
      issues: [{ plan_id: "synthetic", title: "Synthetic containment only", number: 1, phase: "dispatched", branch: "smoke-issue", worktree: ".orchestrator/worktrees/issue-1", decision: {} }],
    },
  }
  await writeFile(path.join(root, ".orchestrator", "delivery.lock"), JSON.stringify({ schema_version: 1, run_id: fixtureState.run.id, host: "opencode", hostname: hostname(), pid: process.pid, acquired_at: new Date().toISOString() }))
  const stateFile = path.join(root, ".orchestrator", "state.json")
  await writeFile(stateFile, JSON.stringify(fixtureState))
  session = editingSession
  await api("/api/session/" + session.id + "/move", { method: "POST", body: JSON.stringify({ directory: writable }) })
  await api("/api/session/" + session.id + "/prompt", { method: "POST", body: JSON.stringify({ text: "Execute the synthetic Delivery containment calls." }) })
  turn = await nextTurn()
  context(turn)
  turn = await tool(turn, "write", { path: "tracked.txt", content: "delivery allowed\n" })
  assert.equal(await readFile(path.join(writable, "tracked.txt"), "utf8"), "delivery allowed\n")
  assert.equal(await readFile(tracked, "utf8"), before)
  for (const literal of ["~notes.txt", ...(process.platform !== "win32" ? ["~\\notes.txt"] : [])]) {
    turn = await tool(turn, "write", { path: literal, content: "literal tilde name\n" })
    assert.equal(await readFile(path.join(writable, literal), "utf8"), "literal tilde name\n")
  }
  for (const [target, denial] of [
    [tracked, /orch guard:.*outside every registered worktree/],
    [path.join(sibling, "tracked.txt"), /orch guard:.*outside every registered worktree/],
    [path.join(writable, ".git"), /orch guard:.*git internals/],
    [stateFile, /orch guard:.*outside every registered worktree/],
  ]) {
    const bytes = await readFile(target)
    turn = await tool(turn, "write", { path: target, content: "denied\n" }, denial)
    assert.deepEqual(await readFile(target), bytes)
  }
  for (const phase of ["worktree-ready", "blocked"]) {
    fixtureState.run.issues[0].phase = phase
    fixtureState.run.issues[0].blocked_reason = "synthetic fixture hold"
    await writeFile(stateFile, JSON.stringify(fixtureState))
    turn = await tool(turn, "write", { path: "tracked.txt", content: "denied\n" }, new RegExp("orch guard:.*worktree not writable in phase " + phase))
    assert.equal(await readFile(path.join(writable, "tracked.txt"), "utf8"), "delivery allowed\n")
  }
  await finish(session, turn)
  console.log("PASS Delivery session directory, registered worktree, sibling/primary, phase and Git/control containment")

  fixtureState.run.issues[0].phase = "dispatched"
  await writeFile(stateFile, JSON.stringify(fixtureState))
  configuration.plugins = []
  await writeFile(configFile, JSON.stringify(configuration))
  await api("/api/location/reload?" + query(root), { method: "POST" })
  await eventually(async () => (await plugins()).length === 0, "plugin unload")
  const unloaded = await start(root)
  assert.doesNotMatch(JSON.stringify(unloaded.turn.body), /repository is managed by Orch/, "unload must remove the context hook")
  unloaded.turn = await tool(unloaded.turn, "write", { path: tracked, content: "unloaded fixture\n" })
  assert.equal(await readFile(tracked, "utf8"), "unloaded fixture\n")
  unloaded.turn = await tool(unloaded.turn, "write", { path: tracked, content: before })
  await finish(unloaded.session, unloaded.turn)
  configuration.plugins = [adapter]
  await writeFile(configFile, JSON.stringify(configuration))
  await api("/api/location/reload?" + query(root), { method: "POST" })
  await activePlugin()
  const reloaded = await start(root)
  context(reloaded.turn)
  reloaded.turn = await tool(reloaded.turn, "write", { path: tracked, content: "denied\n" }, /orch guard:.*outside every registered worktree/)
  assert.equal(await readFile(tracked, "utf8"), before)
  await finish(reloaded.session, reloaded.turn)
  assert.equal(requests, 0, "simulation must prevent every real provider request")
  console.log("PASS unload/reload: one active plugin, one context injection and retained enforcement")

  const disconnected = await start(root)
  context(disconnected.turn)
  const closed = new Promise((resolve) => controller.socket.addEventListener("close", resolve, { once: true }))
  controller.socket.close()
  await within(closed, "simulation backend disconnect")
  controller = await simulation("ws://127.0.0.1:" + backendPort)
  const suspended = await controller.request("llm.pending")
  assert(suspended.invocations.some((invocation) => invocation.id === disconnected.turn.id), "controller loss must preserve the local pending invocation instead of falling back to HTTP")
  await controller.request("llm.disconnect", { id: disconnected.turn.id })
  const disconnectError = await eventually(async () => {
    const messages = (await api("/api/session/" + disconnected.session.id + "/message")).data
    const message = messages.find((entry) => entry.type === "assistant")
    return message?.error?.message ?? message?.retry?.error?.message
  }, "disconnected-backend failure")
  assert.match(disconnectError, /disconnect|controller|simulation/i)
  await api("/api/session/" + disconnected.session.id + "/interrupt", { method: "POST" })
  await eventually(async () => !(await api("/api/session/active")).data[disconnected.session.id], "negative-test session idle")
  assert.equal(requests, 0, "disconnected simulation must not fall back to HTTP")
  const reclosed = new Promise((resolve) => controller.socket.addEventListener("close", resolve, { once: true }))
  controller.socket.close()
  await within(reclosed, "negative-test controller shutdown")
  controller = await simulation("ws://127.0.0.1:" + backendPort)
  assert.deepEqual((await controller.request("llm.pending")).invocations, [])
  console.log("PASS controller loss suspends locally; provider disconnect fails closed with no HTTP fallback")

  const junction = await start(writable)
  context(junction.turn)
  await mkdir(path.join(root, ".scratch"), { recursive: true })
  await mkdir(path.join(writable, ".scratch"), { recursive: true })
  await symlink(root, path.join(writable, ".scratch", "escape-root"), process.platform === "win32" ? "junction" : "dir")
  await symlink(sibling, path.join(writable, ".scratch", "escape-sibling"), process.platform === "win32" ? "junction" : "dir")
  for (const [alias, direct] of [
    [path.join(writable, ".scratch", "escape-root", "tracked.txt"), tracked],
    [path.join(writable, ".scratch", "escape-sibling", "new.txt"), path.join(sibling, "new.txt")],
  ]) {
    const existed = existsSync(direct)
    const bytes = existed ? await readFile(direct) : undefined
    junction.turn = await tool(junction.turn, "write", { path: alias, content: "denied\n" }, /orch guard:.*(outside every registered worktree|unresolved Windows redirecting reparse point)/)
    assert.equal(existsSync(direct), existed)
    if (existed) assert.deepEqual(await readFile(direct), bytes)
  }
  console.log("PASS junction escape denial for existing primary and missing sibling targets")
  await finish(junction.session, junction.turn)
} finally {
  controller?.socket.close()
  if (server?.pid && server.exitCode === null) {
    if (process.platform === "win32") {
      const stopped = spawnSync("taskkill", ["/pid", String(server.pid), "/t", "/f"], { encoding: "utf8", windowsHide: true })
      if (stopped.status !== 0 && server.exitCode === null) throw new Error("could not stop owned private server: " + stopped.stderr)
    } else server.kill("SIGTERM")
  }
  if (serverExit) await within(serverExit, "owned server shutdown")
  await new Promise((resolve, reject) => provider.close((error) => error ? reject(error) : resolve()))
  assert(inside(await realpath(tmpdir()), temp) && !inside(await realpath(repo), temp), "cleanup must stay in the owned OS-temp fixture")
  await rm(temp, { recursive: true, force: true, maxRetries: 20, retryDelay: 100 })
}

console.log("OpenCode V2 smoke passed; no real provider requests")

async function simulation(endpoint) {
  let socket
  for (let attempt = 0; attempt < 80; attempt++) {
    socket = new WebSocket(endpoint)
    try {
      await new Promise((resolve, reject) => {
        socket.addEventListener("open", resolve, { once: true })
        socket.addEventListener("error", reject, { once: true })
      })
      break
    } catch {
      socket.close()
      socket = undefined
      await sleep(250)
    }
  }
  assert(socket, "simulation backend did not listen at " + endpoint)
  let nextID = 1
  const pending = new Map()
  const notifications = []
  const waiters = []
  socket.addEventListener("message", ({ data }) => {
    const message = JSON.parse(data)
    if (message.id !== undefined) {
      const callback = pending.get(message.id)
      assert(callback, "unexpected simulation response")
      pending.delete(message.id)
      callback(message)
      return
    }
    Backend.decodeNotification(message)
    const index = waiters.findIndex(({ method }) => method === message.method)
    if (index >= 0) waiters.splice(index, 1)[0].resolve(message.params)
    else notifications.push(message)
  })
  const request = (method, params) => within(new Promise((resolve, reject) => {
    const id = nextID++
    pending.set(id, (message) => message.error ? reject(new Error(message.error.message)) : resolve(message.result))
    socket.send(JSON.stringify({ jsonrpc: "2.0", id, method, ...(params === undefined ? {} : { params }) }))
  }), method)
  const next = (method) => {
    const index = notifications.findIndex((message) => message.method === method)
    if (index >= 0) return Promise.resolve(notifications.splice(index, 1)[0].params)
    return new Promise((resolve) => waiters.push({ method, resolve }))
  }
  const handshake = await request("simulation.handshake", {
    client: { name: "orch-smoke", version: "1" }, expectedRole: "backend", offeredVersions: [1],
    requiredCapabilities: ["llm.attach", "llm.chunk", "llm.finish", "llm.request", "llm.pending", "llm.disconnect"], optionalCapabilities: [],
  })
  assert.equal(handshake.protocolVersion, 1)
  assert.equal(handshake.role, "backend")
  await request("llm.attach")
  return { socket, request, next }
}
