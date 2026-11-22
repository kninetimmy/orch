import assert from "node:assert/strict"
import { createHash } from "node:crypto"
import { existsSync } from "node:fs"
import { copyFile, cp, lstat, mkdir, readFile, readlink, realpath, rename, rm, symlink, unlink, writeFile } from "node:fs/promises"
import { spawn, spawnSync } from "node:child_process"
import { createServer as createSocket } from "node:net"
import { createServer } from "node:http"
import { hostname } from "node:os"
import path, { delimiter } from "node:path"
import { fileURLToPath } from "node:url"
import { createInterface } from "node:readline"

import { lookupServer, lookupTools, sameDirectory } from "./src/lookup.js"
import { checkAdapterModule, copyBinary, fileIdentity, inside, prepareOrch, prepareSourceProbe, selectArtifacts, verifyArtifacts } from "./smoke-artifacts.mjs"
import { smokeLockEvidenceFiles } from "./smoke-lock.mjs"
import { openSmokeStorage } from "./smoke-retention.mjs"
import { present } from "./smoke-storage.mjs"
import { joined, verifyStopped } from "./smoke-shutdown.mjs"

const harnessAdapter = path.dirname(fileURLToPath(import.meta.url))
const repo = path.resolve(harnessAdapter, "../..")
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

function findOpenCode(cwd, env) {
  for (const name of ["opencode2", "opencode"]) {
    const candidates = process.platform === "win32"
      ? (process.env.PATH ?? "").split(delimiter).filter(Boolean).flatMap((directory) => [
        path.join(directory, name + ".exe"),
        ...(existsSync(path.join(directory, name + ".cmd")) || existsSync(path.join(directory, name + ".ps1"))
          ? [path.join(directory, "node_modules", "@opencode", "cli", "bin", "opencode.exe")] : []),
      ]).filter(existsSync)
      : result("which", [name], repo).stdout.trim().split(/\r?\n/).filter(Boolean)
    for (const executable of candidates) {
      const result = spawnSync(executable, ["--version"], { cwd, env, encoding: "utf8", windowsHide: true })
      const version = result.status === 0 && admittedVersion(result.stdout)
      if (!version) continue
      if (version !== baseline) console.log("Advisory: OpenCode " + version + " is unverified; checking the live contracts.")
      return executable
    }
  }
  throw new Error("No supported OpenCode V2 executable found; minimum beta 18314 or stable 2.0.0 is required.")
}

function result(name, args, cwd, env = process.env, input) {
  const value = spawnSync(name, args, { cwd, env, input, encoding: "utf8", windowsHide: true })
  if (value.error) throw value.error
  return value
}

function command(name, args, cwd, env, input) {
  const value = result(name, args, cwd, env, input)
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

await using storage = await openSmokeStorage()
await storage.requireIdle()
const selection = await selectArtifacts(process.env, harnessAdapter)
assert(!inside(await realpath(repo), storage.root), "smoke fixtures must be outside the source checkout")
const ancestry = result("git", ["rev-parse", "--show-toplevel"], storage.root)
assert(ancestry.status !== 0 && /not a git repository/i.test(ancestry.stderr), "smoke fixtures must be outside any Git checkout, not just Orch worktrees")
for (const entry of command("git", ["worktree", "list", "--porcelain"], repo).split(/\r?\n/).filter((line) => line.startsWith("worktree "))) {
  assert(!inside(await realpath(entry.slice(9)), storage.root), "smoke fixtures must be outside every source checkout")
}
const { Backend } = await import("@opencode/protocol/simulation")
const { pluginID } = await import("./src/index.js")
const adapter = selection.adapter.root
const evidence = {
  mode: selection.mode, node: process.version, sdk: "2.2.6",
  invocation: {
    command: "npm run smoke --prefix adapters/opencode",
    selectors: Object.fromEntries(["ORCH_SMOKE_ORCH", "ORCH_SMOKE_ADAPTER", "ORCH_SMOKE_MEMHUB"]
      .filter((name) => Object.hasOwn(process.env, name)).map((name) => [name, process.env[name]])),
  },
  harness: {
    repository: await realpath(repo),
    revision: command("git", ["rev-parse", "HEAD"], repo).trim(),
    status: command("git", ["status", "--porcelain", "--", "adapters/opencode"], repo).trim(),
    files: await Promise.all([
      ...["smoke.mjs", "smoke-artifacts.mjs", "smoke-retention.mjs", "smoke-storage.mjs", "smoke-shutdown.mjs",
        "smoke-lock.mjs", "smoke-native.mjs"].map((file) => path.join(harnessAdapter, file)),
      ...await smokeLockEvidenceFiles(),
    ].map(fileIdentity)),
  },
  adapter: selection.adapter, lookups: {}, checks: [],
}
function pass(message) {
  console.log("PASS " + message)
  evidence.checks.push(message)
}
const temp = evidence.fixture_root = await storage.begin()
const root = path.join(temp, process.platform === "win32" ? "repo & (native) % ! ^" : "repo")
const bin = path.join(temp, "bin")
const state = path.join(temp, "state")
const configFile = path.join(root, "opencode.json")
const query = (directory) => new URLSearchParams({ "location[directory]": directory }).toString()
let server
let serverExit
let controller
let headers
let baseURL
let stopHost
let failure
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
await storage.markRunning()
try {
  await new Promise((resolve, reject) => provider.listen(0, "127.0.0.1", resolve).once("error", reject))
  const trapURL = "http://127.0.0.1:" + provider.address().port
  const providerURL = "https://api.openai.com/v1"
  await mkdir(bin)
  await mkdir(path.join(temp, "tmp"))
  await mkdir(path.join(root, ".orchestrator"), { recursive: true })
  await cp(path.join(repo, "internal", "agents", "testdata", "opencode-mixed-provider.toml"), path.join(root, ".orchestrator", "config.toml"))
  await writeFile(path.join(root, ".gitignore"), ".opencode/agents/\n.orchestrator/\n.memhub/\n.scratch/\nopencode.json\n")
  await writeFile(path.join(root, "tracked.txt"), "before\n")
  await writeFile(path.join(root, "route.go"), 'package fixture\nfunc FixtureRoute() string { return "primary" }\n')
  const writable = path.join(root, ".orchestrator", "worktrees", "issue-1")
  const sibling = path.join(root, ".orchestrator", "worktrees", "sibling")
  const orchBinary = path.join(bin, process.platform === "win32" ? "orch.exe" : "orch")
  const git = command(process.platform === "win32" ? "where.exe" : "which", ["git"], repo).trim().split(/\r?\n/)[0]
  const env = Object.fromEntries(Object.entries(process.env)
    .filter(([name]) => !/API_KEY|TOKEN|SECRET|CREDENTIAL|ACCESS_KEY|PASSWORD|COOKIE|^GIT_|^MEMHUB_|^HF_|^FASTEMBED_|^NODE_COMPILE_CACHE$|^(HTTP|HTTPS|ALL|NO)_PROXY$/i.test(name))
    .map(([name, value]) => [process.platform === "win32" ? name.toUpperCase() : name, value]))
  Object.assign(env, {
    PATH: [bin, path.dirname(git), ...(process.platform === "win32" ? [path.join(process.env.SystemRoot, "System32")] : ["/usr/bin", "/bin"])].join(delimiter),
    OPENCODE_TEST_HOME: temp,
    HOME: path.join(temp, "home"),
    USERPROFILE: path.join(temp, "home"),
    APPDATA: path.join(temp, "home", "AppData", "Roaming"),
    LOCALAPPDATA: path.join(temp, "home", "AppData", "Local"),
    HF_HOME: path.join(temp, "models"),
    HF_HUB_CACHE: path.join(temp, "models", "hub"),
    HF_HUB_OFFLINE: "1",
    FASTEMBED_CACHE_PATH: path.join(temp, "models", "fastembed"),
    GIT_CONFIG_GLOBAL: process.platform === "win32" ? "NUL" : "/dev/null",
    GIT_CONFIG_NOSYSTEM: "1",
    XDG_CONFIG_HOME: path.join(temp, "config"),
    XDG_STATE_HOME: state,
    XDG_DATA_HOME: path.join(temp, "data"),
    XDG_CACHE_HOME: path.join(temp, "cache"),
    TMP: path.join(temp, "tmp"),
    TEMP: path.join(temp, "tmp"),
    TMPDIR: path.join(temp, "tmp"),
    NODE_DISABLE_COMPILE_CACHE: "1",
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
  for (const key of ["HOME", "APPDATA", "LOCALAPPDATA", "HF_HOME", "HF_HUB_CACHE", "FASTEMBED_CACHE_PATH"]) await mkdir(env[key], { recursive: true })
  checkAdapterModule(selection.adapter, temp, env)
  const opencode = findOpenCode(temp, env)
  evidence.runtime = command(opencode, ["--version"], temp, env).trim()
  evidence.selected_runtime_binary = await fileIdentity(opencode)
  const privateOpenCode = process.platform === "win32"
    ? path.join(bin, "node_modules", "@opencode", "cli", "bin", "opencode.exe")
    : path.join(bin, "opencode2")
  if (process.platform === "win32") {
    await mkdir(path.dirname(privateOpenCode), { recursive: true })
    await copyFile(opencode, privateOpenCode)
    const manifest = path.join(bin, "node_modules", "@opencode", "cli", "package.json")
    await writeFile(manifest, JSON.stringify({
      name: "@opencode/cli", bin: { opencode2: "./bin/opencode.exe", opencode: "./bin/opencode.exe" },
    }))
    const shim = "@ECHO off\r\nGOTO start\r\n:find_dp0\r\nSET dp0=%~dp0\r\nEXIT /b\r\n:start\r\nSETLOCAL\r\nCALL :find_dp0\r\n\"%dp0%\\node_modules\\@opencode\\cli\\bin\\opencode.exe\"   %*\r\n"
    for (const name of ["opencode2", "opencode"]) await writeFile(path.join(bin, name + ".cmd"), shim)
    const discoverable = command("where.exe", ["opencode2"], root, env).trim().split(/\r?\n/)
    assert.deepEqual(discoverable, [path.join(bin, "opencode2.cmd")], "Orch must discover only the npm shim, never a masking top-level native executable")
    assert(!existsSync(path.join(bin, "opencode2.exe")) && !existsSync(path.join(bin, "opencode.exe")))
    evidence.command_binding = {
      layout: "isolated npm native cmd-shim; no same-name top-level .exe",
      discoverable, manifest: await fileIdentity(manifest), shim: await fileIdentity(discoverable[0]),
      selected_native: await fileIdentity(privateOpenCode),
      qualification: "source/isolated fixture only; not the installed default PATH or manual Delivery",
    }
  } else await copyFile(evidence.selected_runtime_binary.path, privateOpenCode)
  evidence.runtime_binary = await fileIdentity(privateOpenCode)
  assert.equal(evidence.runtime_binary.sha256, evidence.selected_runtime_binary.sha256)
  evidence.orch = await prepareOrch(selection, orchBinary, (destination) => command("go", ["build", "-o", destination, "./cmd/orch"], repo))
  const memhubBinary = path.join(bin, process.platform === "win32" ? "memhub.exe" : "memhub")
  evidence.memhub = await copyBinary(selection.memhub, memhubBinary)
  for (const [name, binary] of [["orch", evidence.orch], ["memhub", evidence.memhub]]) {
    const resolved = command(process.platform === "win32" ? "where.exe" : "which", [name], root, env).trim().split(/\r?\n/)[0]
    binary.native_path_resolution = await fileIdentity(resolved)
    assert.deepEqual(binary.native_path_resolution, binary.executed, `native ${name} must resolve to the qualified private binary`)
  }
  pass(selection.mode + " artifact preflight, private binary identities and native PATH resolution")
  command("git", ["init", "-b", "main"], root, env)
  command("git", ["add", ".gitignore", "tracked.txt", "route.go"], root, env)
  command("git", ["add", "-f", ".orchestrator/config.toml"], root, env)
  command("git", ["-c", "user.name=Orch Smoke", "-c", "user.email=orch-smoke@example.invalid", "commit", "-m", "Create synthetic smoke fixture"], root, env)
  command("git", ["worktree", "add", "-b", "smoke-issue", writable], root, env)
  command("git", ["worktree", "add", "--detach", sibling, "HEAD"], root, env)
  command(memhubBinary, ["init"], root, env)
  const seed = JSON.parse(command(memhubBinary, ["task", "add", "Shared synthetic smoke memory", "--notes", "Only isolated fixture data.", "--json"], root, env))
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
    skills: [selection.adapter.skills_root],
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
  const unmanagedGit = path.join(temp, "unmanaged-git")
  const unmanagedNested = path.join(unmanagedGit, "nested")
  const unmanagedNonGit = path.join(temp, "unmanaged-non-git")
  await mkdir(unmanagedNested, { recursive: true })
  await mkdir(unmanagedNonGit)
  command("git", ["init", "-b", "main"], unmanagedGit, env)
  command(memhubBinary, ["init"], unmanagedGit, env)
  // The same loaded adapter, but only the user's ordinary full connection.
  // This synthetic memory is not an Orch-managed project or a child fallback.
  const unmanagedConfiguration = {
    ...configuration,
    mcp: { servers: { memhub: { type: "local", command: [memhubBinary, "serve"], cwd: unmanagedGit } } },
  }
  for (const directory of [unmanagedGit, unmanagedNonGit]) {
    await writeFile(path.join(directory, "opencode.json"), JSON.stringify(unmanagedConfiguration))
  }
  const port = await availablePort()
  baseURL = "http://127.0.0.1:" + port
  server = spawn(privateOpenCode, ["serve", "--service", "--hostname", "127.0.0.1", "--port", String(port)], {
    cwd: root, env, stdio: ["ignore", "pipe", "pipe"], windowsHide: true,
  })
  server.stdout.on("data", (data) => (logs += data))
  server.stderr.on("data", (data) => (logs += data))
  serverExit = joined(server)

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
    let response
    try {
      response = await fetch(baseURL + endpoint, { ...options, headers, signal: AbortSignal.timeout(10_000) })
    } catch (error) {
      throw new Error(endpoint + " transport failed: " + error.message, { cause: error })
    }
    if (!response.ok) throw new Error(endpoint + " returned HTTP " + response.status + ": " + (await response.text()).slice(0, 2000))
    return response.status === 204 ? undefined : response.json()
  }
  async function plugins(directory = root) {
    return (await api("/api/plugin?" + query(directory))).data.filter((entry) => entry.id === pluginID)
  }
  const info = await api("/api/info")
  assert.equal(info.pid, server.pid)
  assert(inside(temp, path.resolve(info.paths.tmp)), "runtime temporary files must stay in the isolated fixture")
  stopHost = async () => {
    const current = JSON.parse(await readFile(path.join(state, "opencode", "service.json"), "utf8"))
    assert.equal(current.pid, server.pid, "shutdown must still name our owned server")
    assert.equal(current.url, baseURL)
    return result(privateOpenCode, ["service", "stop"], root, env)
  }
  async function activePlugin(directory = root) {
    return eventually(async () => {
      const entries = await plugins(directory)
      assert(entries.length <= 1, "Orch must not be loaded twice")
      if (!entries.length) return undefined
      assert.notEqual(entries[0].state?.status, "failed", "the actual adapter package failed to load")
      if (entries[0].state?.status !== "active") return undefined
      assert.equal(entries[0].features?.server, true)
      assert.equal(entries[0].source?.type, "local", "native plugin must come from the selected package, not a registry/builtin fallback")
      assert(sameDirectory(entries[0].source.path, adapter), "native plugin must load the selected package directory")
      return entries[0]
    }, "one active Orch server plugin")
  }
  async function loadedArtifacts(stage) {
    const plugin = await activePlugin()
    const catalog = await api("/api/skill?" + query(root))
    assert(sameDirectory(catalog.location.directory, root))
    const skills = []
    for (const expected of selection.adapter.skill_files.filter((file) => path.basename(file.relative) === "SKILL.md")) {
      const entries = catalog.data.filter((entry) => entry.path && path.resolve(entry.path) === path.resolve(expected.path))
      assert.equal(entries.length, 1, `native skill catalog must load selected ${expected.relative} exactly once`)
      const actual = await fileIdentity(await realpath(entries[0].path))
      assert.deepEqual(actual, { path: expected.path, sha256: expected.sha256 })
      assert(entries[0].content.trim(), "selected skill must have native content")
      skills.push({ id: entries[0].id, ...actual })
    }
    ;(evidence.native_loads ??= []).push({ stage, plugin, skills })
  }
  await loadedArtifacts("initial")
  pass("real selected package-directory and skill loading: one active orch.delivery server plugin")
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
  pass("qualified Orch doctor OpenCode checks (isolated fixture; unrelated GitHub checks may fail)")

  controller = await simulation("ws://127.0.0.1:" + backendPort)
  pass("deterministic simulation handshake and attachment; no session has run yet")
  let callID = 0
  async function nextTurn() {
    const turn = await within(controller.next("llm.request"), "simulated model request")
    evidence.last_request = turn.body
    assert.equal(turn.url, "https://api.openai.com/v1/chat/completions", "model must use the registered simulation destination")
    return turn
  }
  function context(turn, mismatch = false) {
    const body = JSON.stringify(turn.body)
    assert.equal((body.match(/repository is managed by Orch/g) ?? []).length, 1, "context hook must run exactly once")
    assert.equal(/Warning: this main session selected/.test(body), mismatch, "Architect comparison must apply only to mismatched roots: " + body.match(/Warning: this main session selected[^\\]*/)?.[0])
  }
  async function start(directory, model = architect, parentID, agent, permissions, submit = true) {
    let { data: session } = await api("/api/session", {
      method: "POST",
      body: JSON.stringify({ location: { directory }, model, parentID, agent, title: "Orch deterministic smoke", permissions: permissions ?? (agent ? [] : fixturePermissions) }),
    })
    assert(session.id, "session API must return a real session identity")
    if (parentID) {
      // Native child creation inherits the parent location even when create
      // receives a directory. The approved contract requires an explicit move.
      await api("/api/session/" + session.id + "/move", { method: "POST", body: JSON.stringify({ directory }) })
      session = await eventually(async () => {
        const current = (await api("/api/session/" + session.id)).data
        return sameDirectory(current.location.directory, directory) && current
      }, "child session move to assigned checkout")
    }
    assert(sameDirectory(session.location.directory, directory), "native session must use its assigned checkout")
    if (!submit) return { session }
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
    evidence.last_tool = entry
    if (denial) assert.match(body, denial, "requested operation must fail before returning data or mutating files")
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

  // Scoped eviction cannot infer terminal inactivity from foreground drains.
  // Qualify persistent-terminal evidence before implementing any removal probe.
  const terminalProbe = await start(root, architect, undefined, undefined, undefined, false)
  evidence.lifecycle_terminal_probe = await api("/api/experimental/session/" + terminalProbe.session.id + "/terminal")
  assert.deepEqual(evidence.lifecycle_terminal_probe.data, [], "fresh fixture session must have no persistent terminals")
  pass("persistent-terminal activity inventory is available in the owned isolated host")

  if (process.platform === "win32" && selection.mode === "source-build") {
    // Build a tiny consumer of the actual source Resolve/Local boundary, not
    // another resolver or an HTTP/credential relay. Orch doctor and all lifecycle
    // commands independently run the qualified engine against this same shim PATH.
    const older = await start(root, architect, undefined, undefined, undefined, false)
    const newest = await start(root, architect, undefined, undefined, undefined, false)
    const probeRoot = path.join(temp, "native-boundary-probe")
    const graph = await prepareSourceProbe(repo, probeRoot)
    const source = path.join(probeRoot, "main.go")
    await writeFile(source, `package main
import ("context"; "encoding/json"; "fmt"; "net/url"; "os"
  "github.com/kninetimmy/orch/internal/execx"
  "github.com/kninetimmy/orch/internal/opencode")
func main() {
  ctx := context.Background(); local := execx.Local{}; directory := os.Args[1]
  runtime, err := opencode.Resolve(ctx, local, directory)
  check(err == nil)
  type page struct { Data []struct { ID string \`json:"id"\` } \`json:"data"\`; Cursor *struct { Next string \`json:"next"\` } \`json:"cursor"\` }
  pages := []page{}; requests := []string{}; endpoint := "/api/session?limit=1&order=desc"
  for i := 0; i < 2; i++ {
    result, err := local.Run(ctx, execx.Cmd{Name: runtime.Executable, Args: []string{"api", "get", endpoint}, Dir: directory})
    check(err == nil && result.ExitCode == 0)
    var response page
    check(json.Unmarshal([]byte(result.Stdout), &response) == nil && len(response.Data) == 1 && response.Cursor != nil)
    pages = append(pages, response); requests = append(requests, endpoint)
    if i == 0 { check(response.Cursor.Next != ""); endpoint = "/api/session?" + url.Values{"limit":{"1"}, "cursor":{response.Cursor.Next}}.Encode() }
  }
  check(pages[0].Data[0].ID != pages[1].Data[0].ID)
  check(json.NewEncoder(os.Stdout).Encode(map[string]any{"executable":runtime.Executable, "requests":requests, "pages":pages}) == nil)
}
func check(ok bool) { if !ok { fmt.Fprintln(os.Stderr, "native command-boundary probe failed (host payload omitted)"); os.Exit(1) } }
`)
    const probe = path.join(probeRoot, "native-boundary.exe")
    command("go", ["build", "-mod=readonly", "-o", probe, "."], probeRoot, {
      ...process.env, GOPROXY: "off", GONOPROXY: "none", GOWORK: "off", GOTOOLCHAIN: "local",
    })
    for (const file of Object.values(graph)) assert.deepEqual(await fileIdentity(file.path), file, "offline probe must not rewrite source or helper module files")
    const transport = JSON.parse(command(probe, [root], root, env))
    assert.deepEqual(await fileIdentity(transport.executable), evidence.command_binding.selected_native)
    assert.equal(transport.pages[0].data[0].id, newest.session.id)
    assert.equal(transport.pages[1].data[0].id, older.session.id)
    for (const [index, endpoint] of transport.requests.entries()) {
      const direct = await api(endpoint)
      assert.deepEqual(transport.pages[index].data.map((entry) => entry.id), direct.data.map((entry) => entry.id))
      assert.equal(transport.pages[index].cursor.next, direct.cursor.next)
    }
    evidence.command_binding.transport = {
      ...transport, source: await fileIdentity(source), probe: await fileIdentity(probe), graph,
      source_files: await Promise.all(["internal/opencode/runtime.go", "internal/opencode/executable.go", "internal/execx/execx.go"].map((file) => fileIdentity(path.join(repo, file)))),
      scope: "actual source Resolve/Local against owned real V2; selected engine doctor/lifecycle checked separately; no raw host payload retained by probe",
    }
    pass("npm-shim-only PATH: same native qualification/API identity, encoded metacharacter project, multi-query order/limit and opaque cursor pages through source production boundary")
  }

  let unmanagedTasks = 0
  evidence.unmanaged = []
  const noOrchMemoryContext = (turn) => assert.doesNotMatch(JSON.stringify(turn.body),
    /repository is managed by Orch|Orch (?:bound|child) Memhub|Memhub child lookup unavailable|Expected memory_root=/)
  for (const [label, directory] of [["plain Git", unmanagedGit], ["nested Git", unmanagedNested], ["non-Git", unmanagedNonGit]]) {
    const discovery = JSON.parse(command(orchBinary, ["hook", "opencode", "lookup-context", "--json"], directory, env))
    assert.deepEqual(discovery, { schema_version: 2, managed: false })
    await activePlugin(directory)
    const servers = await eventually(async () => {
      const entries = (await api("/api/mcp?" + query(directory))).data
      assert.deepEqual(entries.map((entry) => entry.name), ["memhub"], "unmanaged scope must not add a bound connection")
      return entries[0].status.status === "connected" && entries
    }, "configured unmanaged Memhub")
    const agents = (await api("/api/agent?" + query(directory))).data
    assert(agents.every((agent) => !agent.permissions.some((rule) => /^(orch_memhub|memhub)_/.test(rule.action))),
      "unmanaged scope must not add Memhub permission rules to any agent")
    const parent = await start(directory)
    noOrchMemoryContext(parent.turn)
    parent.turn = await tool(parent.turn, "execute", { code: "return await tools.memhub.status({})" })
    assert.equal(JSON.parse(evidence.last_tool.content).tasks_total, unmanagedTasks)
    parent.turn = await tool(parent.turn, "execute", { code: `return await tools.memhub.task_add({title: ${JSON.stringify("Ordinary root in " + label)}})` })
    const rootWrite = evidence.last_tool
    await finish(parent.session, parent.turn)
    const ordinary = await start(directory, architect, parent.session.id, "general", fixturePermissions)
    noOrchMemoryContext(ordinary.turn)
    ordinary.turn = await tool(ordinary.turn, "execute", { code: "return await tools.memhub.status({})" })
    assert.equal(JSON.parse(evidence.last_tool.content).tasks_total, ++unmanagedTasks)
    ordinary.turn = await tool(ordinary.turn, "execute", { code: `return await tools.memhub.task_add({title: ${JSON.stringify("Ordinary child in " + label)}})` })
    const childWrite = evidence.last_tool
    ordinary.turn = await tool(ordinary.turn, "execute", { code: "return await tools.memhub.status({})" })
    const status = JSON.parse(evidence.last_tool.content)
    assert.equal(status.tasks_total, ++unmanagedTasks, "ordinary-child semantic call must reach the configured full server")
    evidence.unmanaged.push({ label, directory, discovery, servers, agents, root_write: rootWrite, child_write: childWrite, status })
    await finish(ordinary.session, ordinary.turn)
    const report = await nextTurn()
    noOrchMemoryContext(report)
    await finish(parent.session, report)
    pass("unmanaged " + label + ": original root/general-child full Memhub calls, no bound server, permission changes or Orch memory context")
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
  pass("Assist edit/write/patch, ignored scratch, multi-file and both move targets")
  pass("native indented/heredoc parsing and alias/home path byte preservation")

  const mismatch = await start(root, agentModels["orch-implementer"])
  context(mismatch.turn, true)
  assert.equal(mismatch.session.model.id, agentModels["orch-implementer"].id)
  assert.equal(mismatch.session.model.providerID, agentModels["orch-implementer"].providerID)
  await finish(mismatch.session, mismatch.turn)
  assert.deepEqual((await api("/api/session/" + mismatch.session.id)).data.model, mismatch.session.model)
  const child = await start(root, agentModels["orch-implementer"], mismatch.session.id)
  context(child.turn)
  await finish(child.session, child.turn)
  const childReport = await nextTurn()
  context(childReport, true)
  await finish(mismatch.session, childReport)
  pass("root warning without switching models; child context omits false Architect comparison")

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
  pass("Delivery session directory, registered worktree, sibling/primary, phase and Git/control containment")

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
  await loadedArtifacts("reload")
  const reloaded = await start(root)
  context(reloaded.turn)
  reloaded.turn = await tool(reloaded.turn, "write", { path: tracked, content: "denied\n" }, /orch guard:.*outside every registered worktree/)
  assert.equal(await readFile(tracked, "utf8"), before)
  await finish(reloaded.session, reloaded.turn)
  assert.equal(requests, 0, "simulation must prevent every real provider request")
  pass("unload/reload: selected package/skills, one active plugin, one context injection and retained enforcement")

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
  pass("controller loss suspends locally; provider disconnect fails closed with no HTTP fallback")

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
  pass("junction escape denial for existing primary and missing sibling targets")
  await finish(junction.session, junction.turn)
  // These adversarial links were created above solely to test tool denial.
  // Windows Git removal can traverse an ignored directory junction; do not
  // carry those artificial escape routes into the later real-Git lifecycle.
  for (const [alias, destination] of [["escape-root", root], ["escape-sibling", sibling]]) {
    const link = path.join(writable, ".scratch", alias)
    assert(sameDirectory(link, destination), "only unlink the verified junction fixture created by this test")
    await unlink(link)
  }
  // Memhub qualification uses this same real plugin/server/simulation, never a
  // parallel fake host or real model provider.
  const orchConfig = path.join(root, ".orchestrator", "config.toml")
  const baseConfig = await readFile(orchConfig, "utf8")
  async function mode(value, directories = [root, writable, sibling]) {
    await writeFile(orchConfig, baseConfig.replace('mode = "off"', `mode = "${value}"`))
    for (const directory of directories) {
      await api("/api/location/reload?" + query(directory), { method: "POST" })
      await activePlugin(directory)
    }
  }
  async function connected(directory) {
    return eventually(async () => {
      const entries = (await api("/api/mcp?" + query(directory))).data
      const entry = entries.find((item) => item.name === lookupServer)
      if (entry?.status.status === "failed") throw new Error("required native Memhub startup failed: " + JSON.stringify(entry))
      return entry?.status.status === "connected" && entry
    }, "native lookup-only Memhub")
  }
  assert(!(await api("/api/mcp?" + query(root))).data.some((item) => item.name === lookupServer), "off must launch no child service")
  await writeFile(path.join(writable, "route.go"), 'package fixture\nfunc FixtureRoute() string { return "implementation_dirty" }\n')
  await writeFile(path.join(sibling, "route.go"), 'package fixture\nfunc FixtureRoute() string { return "review_dirty" }\n')
  configuration.mcp = { servers: {
    memhub: { type: "local", command: [memhubBinary, "serve"], cwd: root },
    docs: { type: "remote", url: "https://example.invalid", disabled: true },
  } }
  await writeFile(configFile, JSON.stringify(configuration))
  await mode("best-effort")
  await Promise.all([connected(writable), connected(sibling)])
  const lookupParent = await start(root)
  context(lookupParent.turn)
  await finish(lookupParent.session, lookupParent.turn)
  for (const [role, model] of Object.entries(agentModels)) {
    const directory = role.includes("reviewer") ? sibling : writable
    const child = await start(directory, model, lookupParent.session.id, role)
    context(child.turn)
    assert.equal(child.session.agent, role)
    assert(child.session.parentID)
    assert.match(JSON.stringify(child.turn.body), /Expected memory_root=/)
    const outputs = {}
    for (const [name, input] of [
      ["status", {}],
      ["recall", { query: `What is task ${seed.id}?`, mode: "fts", max_results: 1 }],
      ["get_row", { source_type: "task", source_id: seed.id }],
      ["locate", { query: "FixtureRoute", limit: 3 }],
    ]) {
      child.turn = await tool(child.turn, "execute", { code: `return await tools.${lookupServer}.${name}(${JSON.stringify(input)})` })
      const data = JSON.parse(evidence.last_tool.content)
      assert.equal(data.binding?.mode, "explicit", "native tool must return real binding evidence, not an empty/error result")
      assert(sameDirectory(data.binding.memory_root, root))
      assert(sameDirectory(data.binding.code_root, directory), "never return primary code for an assigned child checkout")
      assert(sameDirectory(data.binding.git_common_dir, path.join(root, ".git")))
      assert.equal(data.binding.repository_relationship, "registered_worktree")
      outputs[name] = data
    }
    assert.equal(outputs.recall.results[0].source_id, seed.id)
    assert.match(outputs.get_row.body, /Only isolated fixture data/)
    const hit = outputs.locate.results.find((item) => item.path === "route.go")
    assert(hit, "locate must index the selected tracked file")
    const hash = (value) => createHash("sha256").update(value).digest("hex")
    assert.match(hit.snippet, role.includes("reviewer") ? /review_dirty/ : /implementation_dirty/)
    assert.equal(hit.content.algorithm, "sha256")
    assert.equal(hit.content.snippet_sha256, hash(hit.snippet))
    assert.equal(hit.content.worktree_file_sha256, hash(await readFile(path.join(directory, "route.go"))))
    assert.equal(hit.content.indexed_file_sha256, hit.content.worktree_file_sha256)
    assert.equal(hit.content.verification, "worktree_file_hash")
    assert.match(hit.content.indexed_chunk_sha256, /^[a-f0-9]{64}$/)
    assert.equal(outputs.locate.index.freshness.atomic_tree_snapshot, false)
    assert(outputs.locate.index.freshness.limitation)
    assert.equal(existsSync(path.join(directory, ".memhub", "project.sqlite")), false, "no copied child project database")
    child.turn = await tool(child.turn, "execute", { code: `return search({namespace: "${lookupServer}", limit: 100})` })
    const catalog = JSON.parse(evidence.last_tool.content)
    assert.deepEqual(catalog.items.map((item) => item.path.split(".").at(-1)).sort(), [...lookupTools].sort())
    outputs.catalog = catalog
    for (const code of [
      `return await tools.${lookupServer}.task_add({title: "must never be written"})`,
      'return await tools.memhub.task_add({title: "must never be written"})',
    ]) {
      child.turn = await tool(child.turn, "execute", { code }, /Unknown tool|not found|not a function|denied|unavailable/i)
      ;(outputs.denials ??= []).push(evidence.last_tool)
    }
    // V2 can discard unknown fields before the MCP executor. That is not a
    // root override: require the returned binding/content to remain selected.
    child.turn = await tool(child.turn, "execute", { code: `return await tools.${lookupServer}.locate({query: "FixtureRoute", code_root: ${JSON.stringify(root)}})` })
    outputs.root_override = JSON.parse(evidence.last_tool.content)
    assert(sameDirectory(outputs.root_override.binding.code_root, directory))
    if (role === "orch-scout" || role.includes("reviewer")) {
      child.turn = await tool(child.turn, "write", { path: "must-not-exist.txt", content: "denied" }, /Unknown tool|No tool named|not found|denied|not allowed|orch guard:.*outside every registered worktree/i)
      assert.equal(existsSync(path.join(directory, "must-not-exist.txt")), false)
      outputs.write_denial = evidence.last_tool
      child.turn = await tool(child.turn, "shell", { command: "echo must-not-run" }, /Unknown tool|No tool named|not found|denied|not allowed/i)
      outputs.shell_denial = evidence.last_tool
    }
    evidence.lookups[role] = outputs
    pass("native four-tool reads, binding/hash/freshness evidence and denials: " + role)
    await finish(child.session, child.turn)
    const reportTurn = await nextTurn()
    context(reportTurn)
    await finish(lookupParent.session, reportTurn)
  }
  const implementation = evidence.lookups["orch-implementer"]
  const review = evidence.lookups["orch-reviewer"]
  assert.equal(implementation.locate.head, review.locate.head)
  assert.equal(implementation.get_row.body, review.get_row.body)
  assert.notEqual(implementation.locate.index.db_path, review.locate.index.db_path)
  assert.notEqual(implementation.locate.results.find((item) => item.path === "route.go").content.worktree_file_sha256,
    review.locate.results.find((item) => item.path === "route.go").content.worktree_file_sha256)
  evidence.concurrent_services = await Promise.all([connected(writable), connected(sibling)])
  assert.equal(result("git", ["symbolic-ref", "-q", "HEAD"], sibling, env).status, 1, "review fixture must be pinned/detached")
  async function finishChild(child) {
    await finish(child.session, child.turn)
    const reportTurn = await nextTurn()
    context(reportTurn)
    await finish(lookupParent.session, reportTurn)
  }
  const adversarial = await start(writable, agentModels["orch-scout"], lookupParent.session.id, "orch-scout", fixturePermissions)
  adversarial.turn = await tool(adversarial.turn, "execute", { code: 'return await tools.memhub.task_add({title: "must never be written"})' }, /child roles must not use a broad Memhub/)
  evidence.runtime_full_denial = evidence.last_tool
  const gitFile = path.join(writable, ".git")
  const gitBytes = await readFile(gitFile)
  await rm(gitFile)
  try {
    adversarial.turn = await tool(adversarial.turn, "execute", { code: `return await tools.${lookupServer}.status({})` }, /roots differ|unavailable|not a registered|lookup context/i)
    evidence.unverifiable_root = evidence.last_tool
  } finally {
    await writeFile(gitFile, gitBytes)
  }
  await finishChild(adversarial)
  const wrongRole = await start(writable, agentModels["orch-scout"], lookupParent.session.id, "general", fixturePermissions)
  wrongRole.turn = await tool(wrongRole.turn, "execute", { code: `return await tools.${lookupServer}.status({})` }, /supported Orch child role/)
  evidence.wrong_role = evidence.last_tool
  await finishChild(wrongRole)
  const primary = await start(root)
  primary.turn = await tool(primary.turn, "execute", { code: "return await tools.memhub.status({})" })
  const fullStatus = JSON.parse(evidence.last_tool.content)
  assert.equal(fullStatus.tasks_total, 1, "denied semantic writes must not reach handlers")
  evidence.architect_full_preserved = fullStatus
  await finish(primary.session, primary.turn)
  pass("Code Mode executor rejects full-server/unsupported-role/unverifiable-root paths despite permissive session rules; Architect full access preserved")
  // Exercise excluded direct MCP requests too: native Code Mode can strip
  // unknown input fields before sending them, so host evidence alone cannot
  // establish the server's strict argument/profile contract.
  const negativeRoot = path.join(temp, "negative-checkout")
  command("git", ["worktree", "add", "--detach", negativeRoot, "HEAD"], root, env)
  const probe = spawn(memhubBinary, ["serve", "--memory-root", root, "--code-root", negativeRoot, "--profile", "lookup-only"], {
    cwd: temp, env, stdio: ["pipe", "pipe", "pipe"], windowsHide: true,
  })
  let probeLog = ""
  probe.stderr.on("data", (data) => (probeLog += data))
  const probeExit = joined(probe)
  evidence.direct_memhub_shutdown = { pid: probe.pid, verified: false }
  const reader = createInterface({ input: probe.stdout })
  const pending = new Map()
  reader.on("line", (line) => {
    const response = JSON.parse(line)
    if (response.id !== undefined) {
      pending.get(response.id)?.(response)
      pending.delete(response.id)
    }
  })
  let probeID = 0
  const rpc = (method, params) => within(new Promise((resolve) => {
    const id = ++probeID
    pending.set(id, resolve)
    probe.stdin.write(JSON.stringify({ jsonrpc: "2.0", id, method, params }) + "\n")
  }), "isolated direct MCP " + method)
  const rejected = (response) => assert(response.error || response.result?.isError, "direct request must be rejected: " + JSON.stringify(response))
  try {
    await rpc("initialize", { protocolVersion: "2024-11-05", capabilities: {}, clientInfo: { name: "orch-isolated-smoke", version: "1" } })
    probe.stdin.write('{"jsonrpc":"2.0","method":"notifications/initialized"}\n')
    const catalog = await rpc("tools/list", {})
    assert.deepEqual(catalog.result.tools.map((entry) => entry.name).sort(), [...lookupTools].sort())
    const write = await rpc("tools/call", { name: "task_add", arguments: { title: "must never be written" } })
    const override = await rpc("tools/call", { name: "locate", arguments: { query: "FixtureRoute", code_root: root } })
    rejected(write)
    rejected(override)
    await rename(negativeRoot, negativeRoot + "-removed")
    const removed = await rpc("tools/call", { name: "status", arguments: {} })
    rejected(removed)
    evidence.direct_profile = { catalog, write, override, removed }
  } finally {
    probe.stdin.end()
    try {
      const exit = await within(probeExit, "owned direct Memhub shutdown")
      Object.assign(evidence.direct_memhub_shutdown, exit)
      assert(exit.code === 0 && !exit.signal && !exit.error, "direct Memhub did not shut down normally")
      evidence.direct_memhub_shutdown.verified = true
    } catch (error) {
      if (probe.exitCode === null && probe.signalCode === null) probe.kill()
      await within(probeExit, "owned direct Memhub exit after shutdown failure")
      throw error
    } finally {
      reader.close()
      await writeFile(path.join(temp, "direct-memhub.log"), probeLog)
    }
  }
  const unrelatedRoot = path.join(temp, "unrelated")
  await mkdir(unrelatedRoot)
  command("git", ["init", "-b", "main"], unrelatedRoot, env)
  for (const codeRoot of [unrelatedRoot, negativeRoot]) {
    const refused = spawnSync(memhubBinary, ["serve", "--memory-root", root, "--code-root", codeRoot, "--profile", "lookup-only"], {
      cwd: temp, env, input: "", encoding: "utf8", windowsHide: true, timeout: 30_000,
    })
    assert.notEqual(refused.status, 0)
    assert.match(refused.stderr, /unrelated|cannot be resolved|not exist/i)
    ;(evidence.refused_roots ??= []).push({ code_root: codeRoot, exit: refused.status, error: refused.stderr })
  }
  pass("real Memhub exact catalog, direct semantic/root-argument rejection and missing/unrelated-root refusal")
  configuration.mcp = { servers: { docs: configuration.mcp.servers.docs } }
  await writeFile(configFile, JSON.stringify(configuration))
  await mode("off")
  for (const directory of [root, writable, sibling]) {
    assert(!(await api("/api/mcp?" + query(directory))).data.some((item) => item.name === lookupServer || item.name === "memhub"))
  }
  const offChild = await start(writable, agentModels["orch-scout"], lookupParent.session.id, "orch-scout")
  assert.match(JSON.stringify(offChild.turn.body), /mode is off/)
  await finishChild(offChild)
  const savedBinary = path.join(bin, "qualified-memhub" + (process.platform === "win32" ? ".exe" : ""))
  await rename(memhubBinary, savedBinary)
  await mode("best-effort")
  async function failedService(directory) {
    return eventually(async () => (await api("/api/mcp?" + query(directory))).data.find((item) => item.name === lookupServer && item.status.status === "failed"), "explicit unavailable service")
  }
  evidence.missing_service = await failedService(writable)
  const missing = await start(writable, agentModels["orch-scout"], lookupParent.session.id, "orch-scout")
  assert.match(JSON.stringify(missing.turn.body), /Memhub child lookup unavailable/)
  assert.match(JSON.stringify(missing.turn.body), /Ask the Architect/)
  await finishChild(missing)
  await mode("required")
  await failedService(writable)
  const required = await start(writable, agentModels["orch-scout"], lookupParent.session.id, "orch-scout", undefined, false)
  await api("/api/session/" + required.session.id + "/prompt", { method: "POST", body: JSON.stringify({ text: "Required lookup preflight must fail without model generation." }) })
  evidence.required_failure = await eventually(async () => {
    // Context preflight fails before an assistant message exists. Observe the
    // native session outcome and parent report, not a fabricated tool error.
    const current = (await api("/api/session/" + required.session.id)).data
    evidence.required_state = current
    return current.outcome === "failed" && current
  }, "required lookup preflight failure")
  await eventually(async () => !(await api("/api/session/active")).data[required.session.id], "required failure idle")
  const requiredReport = await nextTurn()
  context(requiredReport)
  assert.match(JSON.stringify(requiredReport.body), /Memhub child lookup unavailable/)
  evidence.required_report = requiredReport.body
  await finish(lookupParent.session, requiredReport)
  await mode("off")
  const oldSource = path.join(temp, "legacy-memhub.go")
  await writeFile(oldSource, 'package main\nimport ("fmt"; "os")\nfunc main() { fmt.Fprintln(os.Stderr, "legacy Memhub: unexpected argument --memory-root; lookup-only is unsupported"); os.Exit(2) }\n')
  // A synthetic legacy CLI rejects binding flags before touching any store.
  // No old/global executable, install or automatic configuration repair.
  command("go", ["build", "-o", memhubBinary, oldSource], temp, {
    ...env, PATH: process.env.PATH, GOENV: "off", GOCACHE: path.join(temp, "go-cache"), GOMODCACHE: path.join(temp, "go-mod-cache"),
  })
  await mode("best-effort")
  evidence.old_service = await failedService(writable)
  const old = await start(writable, agentModels["orch-scout"], lookupParent.session.id, "orch-scout")
  assert.match(JSON.stringify(old.turn.body), /lookup-only support/)
  await finishChild(old)
  await mode("off")
  await rm(memhubBinary)
  await rename(savedBinary, memhubBinary)
  await mode("required")
  await connected(writable)
  const availableRequired = await start(writable, agentModels["orch-scout"], lookupParent.session.id, "orch-scout")
  availableRequired.turn = await tool(availableRequired.turn, "execute", { code: `return await tools.${lookupServer}.status({})` })
  assert(sameDirectory(JSON.parse(evidence.last_tool.content).binding.code_root, writable))
  await finishChild(availableRequired)
  pass("off/best-effort/required, missing executable and simulated legacy flag rejection; no full-profile fallback")
  configuration.mcp.servers["orch.memhub"] = { type: "remote", url: "https://example.invalid", disabled: true }
  await writeFile(configFile, JSON.stringify(configuration))
  await mode("best-effort")
  const collided = await start(writable, agentModels["orch-scout"], lookupParent.session.id, "orch-scout")
  assert.match(JSON.stringify(collided.turn.body), /MCP name collision/)
  evidence.collision = (await api("/api/mcp?" + query(writable))).data
  assert(evidence.collision.some((entry) => entry.name === "orch.memhub" && entry.status.status === "disabled"))
  assert(!evidence.collision.some((entry) => entry.name === lookupServer), "collision must not overwrite or launch a child service")
  await finishChild(collided)
  const guardedCollision = await start(root)
  guardedCollision.turn = await tool(guardedCollision.turn, "write", { path: tracked, content: "denied" }, /orch guard:.*outside every registered worktree/)
  assert.equal(await readFile(tracked, "utf8"), before)
  await finish(guardedCollision.session, guardedCollision.turn)
  pass("normalized MCP collision preserves user server and existing write guard")
  // Lifecycle probes use only this server and owned synthetic checkouts.
  // Never manually evict a location or kill a process to make Git removal pass.
  delete configuration.mcp.servers["orch.memhub"]
  await writeFile(configFile, JSON.stringify(configuration))
  await mode("best-effort")
  await Promise.all([connected(root), connected(writable), connected(sibling)])
  const releaseEvidence = evidence.checkout_release = { refusals: [], qualification: "source/selected-artifact isolated simulation only; not installed manual Delivery or atomic host quiescence" }
  if (selection.mode === "source-build") {
    releaseEvidence.source_files = await Promise.all([
      "internal/opencode/release.go", "internal/opencode/release_terminals.go", "internal/opencode/release_test.go", "internal/opencode/release_terminals_windows_test.go",
      "internal/opencode/release_access_windows.go", "internal/opencode/release_access_other.go",
      "internal/run/checkout_release.go", "internal/run/checkout_release_test.go", "internal/run/checkout_release_windows_test.go",
      "internal/run/checkout_pagination_windows_test.go",
    ].map((file) => fileIdentity(path.join(repo, file))))
  }
  const releaseSpec = await api("/openapi.json")
  const locationContract = releaseSpec.paths["/api/debug/location"]
  assert.equal(locationContract.get.operationId, "debug.location.list")
  assert.equal(locationContract.delete.operationId, "debug.location.evict")
  assert(locationContract.delete.parameters.some((entry) => entry.name === "location" && entry.in === "query" && entry.style === "deepObject" && entry.explode))
  const specFile = path.join(temp, "checkout-release-openapi.json")
  await writeFile(specFile, JSON.stringify(releaseSpec))
  releaseEvidence.contract = { artifact: await fileIdentity(specFile), location: locationContract,
    interpretation: "host-returned directory is an exact cache key; filesystem equivalence does not merge service owners" }
  const fileID = async (file) => {
    const info = await lstat(file, { bigint: true })
    return { dev: String(info.dev), ino: String(info.ino) }
  }
  const cacheSnapshot = async () => Promise.all((await api("/api/debug/location")).map(async (entry) => ({
    key: entry.directory, identity: await fileID(entry.directory),
  })))
  const targetKeys = (snapshot, directories) => snapshot.filter((entry) => directories.some((directory) => sameDirectory(entry.key, directory))).map((entry) => entry.key)
  function preservedCaches(before, after, targets) {
    for (const previous of before) {
      const current = after.find((entry) => entry.key === previous.key)
      if (targets.includes(previous.key)) assert(!current, "only the proven target keys may disappear")
      else assert.deepEqual(current, previous, "each non-target cache key and its physical identity must survive independently")
    }
  }
  const hash = (value) => createHash("sha256").update(value).digest("hex")
  const lockPath = path.join(root, ".orchestrator", "delivery.lock")
  // The existing containment fixture is synthetic. Obtain execution/config
  // fields from the real read-only gate, never weaken the engine's checks or
  // call activation or live GitHub to turn a fixture into a human approval.
  // Gate 4 requires a concrete target; only the two scripted gh reads are
  // available for this gate invocation.
  const gateProbe = path.join(temp, "gate-probe")
  await mkdir(gateProbe)
  command("go", ["build", "-o", path.join(gateProbe, process.platform === "win32" ? "gh.exe" : "gh"),
    "./internal/cli/testdata/doctor-probe"], repo)
  const gateProbeFile = path.join(gateProbe, "commands.json")
  await writeFile(gateProbeFile, JSON.stringify({
    Root: root, Log: path.join(gateProbe, "commands.log"), Probes: {
      "gh auth status": {},
      "gh repo view --json nameWithOwner,defaultBranchRef,url,id": {
        Stdout: JSON.stringify({ nameWithOwner: "fixture/orch-smoke", defaultBranchRef: { name: "main" },
          url: "https://github.example.invalid/fixture/orch-smoke", id: "R_kgDOAAAAAQ" }),
      },
    },
  }))
  const gateEnv = { ...env, PATH: gateProbe + delimiter + env.PATH, ORCH_TEST_DOCTOR_FIXTURE: gateProbeFile }
  await rm(stateFile)
  await rm(lockPath)
  const gate = JSON.parse(command(orchBinary, ["run", "plan"], root, gateEnv, JSON.stringify({
    schema_version: 1, host: "opencode", title: "Synthetic lifecycle smoke; no real approval",
    issues: [{ id: "synthetic", title: "Synthetic lifecycle", objective: "Exercise only owned fixtures",
      acceptance_criteria: ["Fixture paths and host activity are verified"], type: "bug", facts: {},
      wave: 1, required_tests: ["synthetic smoke"], usage_class: "heavy" }],
  })))
  assert.equal(gate.schema_version, 4)
  assert(sameDirectory(gate.target.checkout, root))
  assert.match(gate.target.checkout_id, /^sha256:[0-9a-f]{64}$/)
  assert.deepEqual(gate.target.github, { host: "github.example.invalid", owner: "fixture", repository: "orch-smoke", id: "R_kgDOAAAAAQ" })
  const executionBytes = JSON.stringify(gate.execution).replace(/[<>&\u2028\u2029]/g, (value) => "\\u" + value.charCodeAt(0).toString(16).padStart(4, "0"))
  fixtureState.run.plan = {
    title: gate.plan_title, digest: gate.plan_digest, approved_by: "synthetic fixture, not a human",
    approved_at: new Date().toISOString(), config_revision: gate.config_revision,
    contract_version: gate.execution.version, execution_digest: "sha256:" + hash(executionBytes),
  }
  Object.assign(fixtureState.run.issues[0], { phase: "pr-open", pr_number: 1, pr_url: "https://example.invalid/synthetic/1" })
  await writeFile(lockPath, JSON.stringify({ schema_version: 1, run_id: fixtureState.run.id, host: "opencode", hostname: hostname(), pid: process.pid, acquired_at: new Date().toISOString() }))
  await writeFile(stateFile, JSON.stringify(fixtureState))
  releaseEvidence.synthetic_gate = gate
  const oldOID = command("git", ["rev-parse", "HEAD"], root, env).trim()
  await writeFile(path.join(writable, "route.go"), 'package fixture\nfunc FixtureRoute() string { return "lifecycle_new_revision" }\n')
  command("git", ["add", "-A"], writable, env)
  command("git", ["-c", "user.name=Orch Smoke", "-c", "user.email=orch-smoke@example.invalid", "commit", "-m", "Create synthetic lifecycle revision"], writable, env)
  const newOID = command("git", ["rev-parse", "HEAD"], writable, env).trim()
  assert.notEqual(oldOID, newOID)
  const origin = path.join(temp, "origin.git")
  command("git", ["init", "--bare", origin], temp, env)
  command("git", ["remote", "add", "origin", origin], root, env)
  command("git", ["push", "origin", "main", "smoke-issue"], root, env)
  const reviewPath = path.join(root, ".orchestrator", "worktrees", "review-issue-1")
  const reviewRequest = (head, number = 1) => ({ schema_version: 1, issue_number: number, head_oid: head })
  // Keep the host/simulation HTTP event loop responsive through the engine's
  // multi-request preflight; spawnSync can leave a closed keep-alive socket
  // unprocessed until the first post-cleanup fetch attempts to reuse it.
  const runResult = (verb, request) => new Promise((resolve, reject) => {
    const child = spawn(orchBinary, ["run", verb], { cwd: root, env, stdio: ["pipe", "pipe", "pipe"], windowsHide: true })
    let stdout = "", stderr = ""
    child.stdout.on("data", (data) => (stdout += data))
    child.stderr.on("data", (data) => (stderr += data))
    child.once("error", reject)
    child.once("close", (status) => resolve({ status, stdout, stderr }))
    child.stdin.end(JSON.stringify(request))
  })
  const runCommand = async (verb, request) => {
    const value = await runResult(verb, request)
    assert.equal(value.status, 0, value.stderr)
    return JSON.parse(value.stdout)
  }
  const refuseRelease = async (verb, request, reason) => {
    const stateBefore = await readFile(stateFile)
    const refusal = await runResult(verb, request)
    assert.notEqual(refusal.status, 0, "observed target activity must prevent eviction/removal")
    assert.match(refusal.stderr, reason)
    assert.deepEqual(await readFile(stateFile), stateBefore, "refusal must not advance phase")
    releaseEvidence.refusals.push({ verb, request, exit: refusal.status, error: refusal.stderr })
  }
  const hasLocation = (locations, directory) => locations.some((entry) => sameDirectory(entry.directory, directory))
  await runCommand("review-worktree", reviewRequest(oldOID))
  async function nativeLookup(directory, expectedOID, role = "orch-scout") {
    const child = await start(directory, agentModels[role], lookupParent.session.id, role)
    const outputs = {}
    for (const [name, input] of [["status", {}], ["locate", { query: "FixtureRoute", limit: 3 }]]) {
      child.turn = await tool(child.turn, "execute", { code: `return await tools.${lookupServer}.${name}(${JSON.stringify(input)})` })
      const data = outputs[name] = JSON.parse(evidence.last_tool.content)
      assert(sameDirectory(data.binding.memory_root, root))
      assert(sameDirectory(data.binding.code_root, directory))
      assert(sameDirectory(data.binding.git_common_dir, path.join(root, ".git")))
    }
    assert.equal(outputs.locate.head, expectedOID)
    const hit = outputs.locate.results.find((entry) => entry.path === "route.go")
    assert.equal(hit.content.worktree_file_sha256, hash(await readFile(path.join(directory, "route.go"))))
    assert.equal(hit.content.indexed_file_sha256, hit.content.worktree_file_sha256)
    assert.equal(hit.content.verification, "worktree_file_hash")
    assert.equal(outputs.locate.index.freshness.atomic_tree_snapshot, false)
    await finishChild(child)
    outputs.session = (await api("/api/session/" + child.session.id)).data
    assert(sameDirectory(outputs.session.location.directory, directory), "completed child must remain assigned, not be moved to release its checkout")
    outputs.history_sha256 = hash(JSON.stringify((await api("/api/session/" + child.session.id + "/message")).data))
    return outputs
  }
  const aliasKeys = []
  if (process.platform === "win32") {
    // Only owned fixture paths, never the shared service or a real repository.
    // Let the host normalize transport spelling; retain the returned keys.
    const spelling = path.join(path.dirname(sibling), path.basename(sibling).toUpperCase())
    assert(sameDirectory(spelling, sibling))
    for (const directory of [sibling, spelling]) {
      const location = await api("/api/location?" + query(directory))
      aliasKeys.push(location.directory)
      assert(sameDirectory(location.directory, sibling))
      await activePlugin(location.directory)
      await connected(location.directory)
    }
    assert.notEqual(aliasKeys[0], aliasKeys[1], "V2 must really load two casing cache keys, not deduplicate our fixture")
    const before = await cacheSnapshot()
    const identities = aliasKeys.map((key) => before.find((entry) => entry.key === key))
    assert(identities.every(Boolean))
    assert.deepEqual(identities[0].identity, identities[1].identity)
    releaseEvidence.cache_aliases = { spelling, keys: aliasKeys, identities, before,
      scope: "owned isolated Windows host only; no shared-host location creation, eviction or restart" }
    releaseEvidence.cache_aliases.lookup_before = await nativeLookup(aliasKeys[1], oldOID)
    pass("real Windows casing-alias caches: distinct host keys, equal filesystem identities and native alias lookup")
  }
  releaseEvidence.old_review = await nativeLookup(reviewPath, oldOID)
  const activeReview = await start(reviewPath, agentModels["orch-reviewer"], lookupParent.session.id, "orch-reviewer")
  await refuseRelease("review-worktree", reviewRequest(newOID), /active session|unfinished background job/)
  assert.equal(command("git", ["rev-parse", "HEAD"], reviewPath, env).trim(), oldOID)
  await finishChild(activeReview)
  assert(hasLocation(await api("/api/debug/location"), reviewPath), "ordinary idle must not unload the review location")

  // Unlike the earlier guard-only junction check, exercise the production
  // release/removal boundary while the ignored redirections are still present.
  // Both cleanup trees must pass before any services are released.
  releaseEvidence.interior_links = []
  const sentinels = [root, sibling].map((directory) => path.join(directory, ".scratch", "release-sentinel"))
  for (const sentinel of sentinels) {
    await mkdir(path.dirname(sentinel), { recursive: true })
    await writeFile(sentinel, "preserve primary and sibling\n")
  }
  for (const directory of [writable, reviewPath]) {
    const links = []
    await mkdir(path.join(directory, ".scratch"), { recursive: true })
    for (const [name, destination] of [["escape-root", root], ["escape-sibling", sibling]]) {
      const link = path.join(directory, ".scratch", name)
      await symlink(destination, link, process.platform === "win32" ? "junction" : "dir")
      links.push({ path: link, id: await fileID(link), destination: await readlink(link) })
    }
    try {
      for (const verb of directory === reviewPath ? ["review-worktree", "cleanup"] : ["cleanup"]) {
        fixtureState.run.issues[0].phase = verb === "cleanup" ? "abandoned" : "pr-open"
        await writeFile(stateFile, JSON.stringify(fixtureState))
        const registrations = command("git", ["worktree", "list", "--porcelain"], root, env)
        const sentinelsBefore = await Promise.all(sentinels.map(fileIdentity))
        const gitBefore = await Promise.all([root, sibling].map((item) => fileID(path.join(item, ".git"))))
        const loadedBefore = (await api("/api/debug/location")).map((entry) => entry.directory).sort()
        const request = verb === "cleanup" ? { schema_version: 1, issue_number: 1, statement: "cleanup-issue" } : reviewRequest(newOID)
        await refuseRelease(verb, request, /linked or unverifiable checkout entry/)
        assert.equal(command("git", ["worktree", "list", "--porcelain"], root, env), registrations)
        assert.deepEqual(await Promise.all(sentinels.map(fileIdentity)), sentinelsBefore)
        assert.deepEqual(await Promise.all([root, sibling].map((item) => fileID(path.join(item, ".git")))), gitBefore)
        assert.deepEqual((await api("/api/debug/location")).map((entry) => entry.directory).sort(), loadedBefore)
        assert(command("git", ["ls-remote", "--heads", "origin", "smoke-issue"], root, env).trim())
        assert.equal(command("git", ["rev-parse", "HEAD"], reviewPath, env).trim(), oldOID)
        assert.equal(command("git", ["status", "--porcelain"], writable, env).trim(), "", "ignored links must pass executor cleanliness")
        releaseEvidence.interior_links.push({ directory, verb, links, sentinels: sentinelsBefore,
          git_identities: gitBefore, registrations_preserved: true, state_preserved: true,
          loaded_locations_unchanged: loadedBefore, remote_branch_preserved: true,
          boundary: "production refusal; deterministic runner test additionally asserts zero host calls or Git mutations" })
      }
    } finally {
      // Fixture preparation only, after product refusal. Never unlink a changed
      // entry, follow the link, or send an adversarial fixture to generic Git.
      for (const link of links) {
        assert.deepEqual(await fileID(link.path), link.id)
        assert.equal(await readlink(link.path), link.destination)
        assert((await lstat(link.path)).isSymbolicLink())
        await unlink(link.path)
      }
      fixtureState.run.issues[0].phase = "pr-open"
      await writeFile(stateFile, JSON.stringify(fixtureState))
    }
  }
  pass("production ignored interior-link refusal for executor cleanup, review cleanup and replacement; sentinels/state/registration/loaded services preserved")

  // Finite fixture commands exit cooperatively when a marker is written.
  // Natural exit, not process killing or shell/session deletion, ends each test.
  const nodeBinary = path.join(bin, process.platform === "win32" ? "node.exe" : "node")
  await copyFile(process.execPath, nodeBinary)
  await mkdir(path.join(reviewPath, ".scratch"), { recursive: true })
  const waitForMarker = (marker) => `const fs=require('fs'); const timer=setInterval(()=>{if(fs.existsSync('.scratch/${marker}'))clearInterval(timer)},50)`
  const shell = await api("/api/shell?" + query(reviewPath), { method: "POST", body: JSON.stringify({
    command: `node -e "${waitForMarker("finish-shell")}"`,
  }) })
  assert.equal(shell.data.status, "running")
  await refuseRelease("review-worktree", reviewRequest(newOID), /running shell or terminal/)
  await writeFile(path.join(reviewPath, ".scratch", "finish-shell"), "finish")
  await eventually(async () => (await api("/api/shell?" + query(reviewPath))).data.length === 0, "owned shell natural exit")

  const pty = await api("/api/pty?" + query(reviewPath), { method: "POST", body: JSON.stringify({
    command: nodeBinary, args: ["-e", waitForMarker("finish-pty")], cwd: reviewPath, title: "synthetic lifecycle PTY",
  }) })
  assert.equal(pty.data.status, "running")
  await refuseRelease("review-worktree", reviewRequest(newOID), /running shell or terminal/)
  await writeFile(path.join(reviewPath, ".scratch", "finish-pty"), "finish")
  await eventually(async () => (await api("/api/pty?" + query(reviewPath))).data.find((entry) => entry.id === pty.data.id)?.status === "exited", "owned PTY natural exit")

  // Persistent terminal belongs to a primary session but has target cwd:
  // checking just sessions currently located in the checkout would miss it.
  const persistent = await api("/api/experimental/session/" + terminalProbe.session.id + "/terminal", { method: "POST", body: JSON.stringify({
    command: nodeBinary, args: ["-e", waitForMarker("finish-persistent")], cwd: reviewPath, title: "synthetic lifecycle persistent PTY", env: {},
  }) })
  assert.equal(persistent.data.status, "running")
  await refuseRelease("review-worktree", reviewRequest(newOID), /running persistent terminal/)
  await writeFile(path.join(reviewPath, ".scratch", "finish-persistent"), "finish")
  await eventually(async () => (await api("/api/experimental/session/" + terminalProbe.session.id + "/terminal")).data.find((entry) => entry.id === persistent.data.id)?.status === "exited", "owned persistent PTY natural exit")
  releaseEvidence.process_probes = { shell, pty, persistent, exit: "cooperative natural exit; no process termination" }

  // Both ordinary APIs allow a cwd independent of their owning location.
  // Separate disposable targets retain useful evidence even on a regressed
  // engine that reaches Git. Markers live outside those targets so every
  // process can still exit cooperatively after a partial removal.
  releaseEvidence.cross_location = []
  for (const [index, kind] of ["pty", "shell"].entries()) {
    const number = index + 3
    const directory = path.join(root, ".orchestrator", "worktrees", "review-issue-" + number)
    fixtureState.run.issues.push({ ...fixtureState.run.issues[0], number, plan_id: "synthetic-cross-" + kind,
      branch: "unused-cross-" + kind, worktree: ".orchestrator/worktrees/issue-" + number, pr_number: number })
    await writeFile(stateFile, JSON.stringify(fixtureState))
    await runCommand("review-worktree", reviewRequest(oldOID, number))
    await activePlugin(directory)
    await mkdir(path.join(directory, ".scratch"), { recursive: true })
    const marker = path.join(temp, "finish-cross-" + kind)
    const relativeMarker = path.relative(path.join(directory, ".scratch"), marker).split(path.sep).join("/")
    assert.match(relativeMarker, /^[.a-z/-]+$/)
    const script = waitForMarker(relativeMarker)
    const processProbe = await api("/api/" + kind + "?" + query(root), { method: "POST", body: JSON.stringify(kind === "pty"
      ? { command: nodeBinary, args: ["-e", script], cwd: directory, title: "cross-location fixture" }
      : { command: `node -e "${script}"`, cwd: directory }) })
    assert(sameDirectory(processProbe.location.directory, root), "ordinary process owner must remain primary")
    assert(sameDirectory(processProbe.data.cwd, directory), "host must accept an independent target cwd")
    assert.equal(processProbe.data.status, "running")
    const stateBefore = await readFile(stateFile)
    const registrationBefore = command("git", ["worktree", "list", "--porcelain"], root, env)
    const observed = { kind, directory, process: processProbe }
    releaseEvidence.cross_location.push(observed)
    try {
      observed.result = await runResult("review-worktree", reviewRequest(newOID, number))
      observed.refused_before_removal = observed.result.status !== 0 && /running shell or terminal/.test(observed.result.stderr)
      observed.registration_preserved = command("git", ["worktree", "list", "--porcelain"], root, env) === registrationBefore
      observed.state_preserved = (await readFile(stateFile)).equals(stateBefore)
      observed.target_still_loaded = hasLocation(await api("/api/debug/location"), directory)
      observed.primary_still_loaded = hasLocation(await api("/api/debug/location"), root)
      observed.target_listing = result("git", ["rev-parse", "HEAD"], directory, env).stdout.trim()
    } finally {
      await writeFile(marker, "finish")
      await eventually(async () => {
        const entries = (await api("/api/" + kind + "?" + query(root))).data
        return kind === "shell" ? !entries.some((entry) => entry.id === processProbe.data.id)
          : entries.find((entry) => entry.id === processProbe.data.id)?.status === "exited"
      }, "cross-location " + kind + " cooperative exit")
    }
  }
  for (const observed of releaseEvidence.cross_location) {
    assert(observed.refused_before_removal && observed.registration_preserved && observed.state_preserved &&
      observed.target_still_loaded && observed.primary_still_loaded && observed.target_listing === oldOID,
    "cross-location " + observed.kind + " must refuse before disposal/Git: " + JSON.stringify(observed))
  }
  pass("primary-owned ordinary PTY and shell with independent target cwd refuse before disposal/Git; cooperative exits")

  // Keep a different running PTY in each unrelated alias cache across target
  // disposal. Matching IDs only within their queried owner proves that the
  // aliases are independent services, not just equal filesystem identities.
  const aliasProcesses = []
  for (const [index, key] of aliasKeys.entries()) {
    const marker = "finish-alias-" + index
    const created = await api("/api/pty?" + query(key), { method: "POST", body: JSON.stringify({
      command: nodeBinary, args: ["-e", waitForMarker(marker)], cwd: sibling, title: "unrelated alias cache " + index,
    }) })
    assert.equal(created.location.directory, key)
    assert.equal(created.data.status, "running")
    aliasProcesses.push({ key, marker, created })
  }
  async function aliasActivity() {
    const observations = []
    for (const { key, created } of aliasProcesses) {
      const shells = await api("/api/shell?" + query(key))
      const ptys = await api("/api/pty?" + query(key))
      assert.equal(shells.location.directory, key)
      assert.equal(ptys.location.directory, key)
      assert(ptys.data.some((entry) => entry.id === created.data.id && entry.status === "running"))
      for (const other of aliasProcesses.filter((entry) => entry.key !== key)) {
        assert(!ptys.data.some((entry) => entry.id === other.created.data.id), "one alias must not answer for the other cache")
      }
      observations.push({ key, shells, ptys })
    }
    return observations
  }
  const beforeReplacement = await cacheSnapshot()
  const replacementKeys = targetKeys(beforeReplacement, [reviewPath])
  assert.equal(replacementKeys.length, 1, "successful disposal requires one attributable target cache")
  if (aliasKeys.length) releaseEvidence.cache_aliases.activity_before = await aliasActivity()
  releaseEvidence.replacement = await runCommand("review-worktree", reviewRequest(newOID))
  const afterReplacement = await cacheSnapshot()
  preservedCaches(beforeReplacement, afterReplacement, replacementKeys)
  releaseEvidence.replacement_caches = { before: beforeReplacement, target_keys: replacementKeys, after: afterReplacement }
  if (aliasKeys.length) releaseEvidence.cache_aliases.activity_after_replacement = await aliasActivity()
  assert.equal(releaseEvidence.replacement.head_oid, newOID)
  assert.equal(result("git", ["symbolic-ref", "-q", "HEAD"], reviewPath, env).status, 1)
  releaseEvidence.new_review = await nativeLookup(reviewPath, newOID)
  assert.notEqual(releaseEvidence.old_review.locate.results.find((entry) => entry.path === "route.go").content.worktree_file_sha256,
    releaseEvidence.new_review.locate.results.find((entry) => entry.path === "route.go").content.worktree_file_sha256)
  pass("native lookup, active/shell/PTY/persistent-terminal refusals and engine review replacement at a different full OID with fresh root/revision/hash evidence")

  // Unexpected descendants are preserved in a separate disposable fixture;
  // intentionally leave it for owned-host shutdown, not manual eviction.
  const descendantReview = path.join(root, ".orchestrator", "worktrees", "review-issue-2")
  fixtureState.run.issues.push({ ...fixtureState.run.issues[0], number: 2, plan_id: "synthetic-descendant", branch: "unused-smoke-2", worktree: ".orchestrator/worktrees/issue-2", pr_number: 2 })
  await writeFile(stateFile, JSON.stringify(fixtureState))
  await runCommand("review-worktree", reviewRequest(oldOID, 2))
  const descendant = path.join(descendantReview, ".scratch", "loaded-descendant")
  await mkdir(descendant, { recursive: true })
  await activePlugin(descendant)
  await refuseRelease("review-worktree", reviewRequest(newOID, 2), /unexpected loaded descendant/)
  assert(existsSync(descendant), "refused descendant must remain intact")

  fixtureState.run.issues[0].phase = "abandoned"
  await writeFile(stateFile, JSON.stringify(fixtureState))
  const cleanupRequest = { schema_version: 1, issue_number: 1, statement: "cleanup-issue" }
  const activeExecutor = await start(writable, agentModels["orch-implementer"], lookupParent.session.id, "orch-implementer")
  await refuseRelease("cleanup", cleanupRequest, /active session|unfinished background job/)
  assert(existsSync(writable) && existsSync(reviewPath))
  assert(command("git", ["ls-remote", "--heads", "origin", "smoke-issue"], root, env).trim(), "activity refusal must precede even remote-branch deletion")
  await finishChild(activeExecutor)
  const completed = releaseEvidence.completed_children = {
    before: [await nativeLookup(writable, newOID, "orch-implementer"), await nativeLookup(reviewPath, newOID, "orch-reviewer")],
    connected_before: await Promise.all([connected(writable), connected(reviewPath)]),
  }
  const activeBeforeCleanup = (await api("/api/session/active")).data
  for (const lookup of completed.before) {
    assert.equal(lookup.session.outcome, "succeeded", "both assigned children must have completed successfully")
    assert(!activeBeforeCleanup[lookup.session.id], "both assigned children must be completed before release")
  }
  releaseEvidence.before_cleanup = await api("/api/debug/location")
  assert(hasLocation(releaseEvidence.before_cleanup, writable) && hasLocation(releaseEvidence.before_cleanup, reviewPath))
  const beforeCleanup = await cacheSnapshot()
  const cleanupKeys = targetKeys(beforeCleanup, [writable, reviewPath])
  assert.equal(cleanupKeys.length, 2)
  releaseEvidence.cleanup = await runCommand("cleanup", cleanupRequest)
  assert.equal(releaseEvidence.cleanup.phase, "cleaned")
  assert.equal(existsSync(writable), false)
  assert.equal(existsSync(reviewPath), false)
  releaseEvidence.after_cleanup = await api("/api/debug/location")
  const afterCleanup = await cacheSnapshot()
  preservedCaches(beforeCleanup, afterCleanup, cleanupKeys)
  releaseEvidence.cleanup_caches = { before: beforeCleanup, target_keys: cleanupKeys, after: afterCleanup }
  assert(!releaseEvidence.after_cleanup.some((entry) => [writable, reviewPath].some((directory) => path.resolve(entry.directory) === path.resolve(directory))))
  assert(hasLocation(releaseEvidence.after_cleanup, root) && hasLocation(releaseEvidence.after_cleanup, sibling))
  completed.after = []
  for (const previous of completed.before) {
    const session = (await api("/api/session/" + previous.session.id)).data
    const history_sha256 = hash(JSON.stringify((await api("/api/session/" + session.id + "/message")).data))
    assert.equal(session.location.directory, previous.session.location.directory, "cleanup must not move completed sessions")
    assert.equal(session.parentID, previous.session.parentID)
    assert.equal(session.outcome, "succeeded")
    assert.equal(history_sha256, previous.history_sha256, "cleanup must preserve completed session history")
    completed.after.push({ session, history_sha256 })
  }
  completed.git_registrations_after = command("git", ["worktree", "list", "--porcelain"], root, env)
  for (const directory of [writable, reviewPath]) assert(!completed.git_registrations_after.includes(directory.replaceAll("\\", "/")))
  pass("both completed child sessions remain assigned with unchanged history; checkout-bound Memhub connected before batched release; both Git checkouts removed")
  releaseEvidence.primary_after = await nativeLookup(root, oldOID)
  releaseEvidence.unrelated_after = await nativeLookup(sibling, oldOID)
  if (aliasKeys.length) {
    releaseEvidence.cache_aliases.activity_after_cleanup = await aliasActivity()
    releaseEvidence.cache_aliases.lookup_after = await nativeLookup(aliasKeys[1], oldOID)
    for (const { key, marker, created } of aliasProcesses) {
      await writeFile(path.join(sibling, ".scratch", marker), "finish")
      await eventually(async () => (await api("/api/pty?" + query(key))).data.find((entry) => entry.id === created.data.id)?.status === "exited", "unrelated alias PTY cooperative exit")
    }
    releaseEvidence.cache_aliases.exit = "cooperative natural exit; both alias caches and native lookups preserved"
    pass("single-target replacement and executor/review cleanup preserve every unrelated cache key/identity and independent live alias PTYs/lookups")
  }
  const unrelatedAfter = await start(unmanagedGit)
  noOrchMemoryContext(unrelatedAfter.turn)
  unrelatedAfter.turn = await tool(unrelatedAfter.turn, "execute", { code: "return await tools.memhub.status({})" })
  releaseEvidence.unmanaged_after = JSON.parse(evidence.last_tool.content)
  assert.equal(releaseEvidence.unmanaged_after.tasks_total, unmanagedTasks, "unrelated full-profile lookup must remain usable without losing its synthetic memory")
  await finish(unrelatedAfter.session, unrelatedAfter.turn)
  releaseEvidence.cleanup_artifacts = {
    executor_exists: existsSync(writable), review_exists: existsSync(reviewPath),
    remote_heads: command("git", ["ls-remote", "--heads", "origin", "smoke-issue"], root, env).trim(),
    local_ref_exit: result("git", ["show-ref", "--verify", "--quiet", "refs/heads/smoke-issue"], root, env).status,
  }
  assert.equal(releaseEvidence.cleanup_artifacts.executor_exists, false)
  assert.equal(releaseEvidence.cleanup_artifacts.review_exists, false)
  assert.equal(releaseEvidence.cleanup_artifacts.remote_heads, "")
  assert.equal(releaseEvidence.cleanup_artifacts.local_ref_exit, 1)
  pass("engine executor/review cleanup after completed native lookups; descendant/activity refusals preserve fixtures; primary and unrelated lookup services still usable")
  if (process.platform === "win32") {
    // Ambiguous target caches remain untouched until normal owned-host shutdown.
    const number = 5
    const directory = path.join(root, ".orchestrator", "worktrees", "review-issue-" + number)
    fixtureState.run.issues.push({ ...fixtureState.run.issues[0], number, plan_id: "synthetic-target-alias",
      phase: "pr-open", branch: "unused-target-alias", worktree: ".orchestrator/worktrees/issue-" + number, pr_number: number })
    await writeFile(stateFile, JSON.stringify(fixtureState))
    await runCommand("review-worktree", reviewRequest(oldOID, number))
    for (const spelling of [directory, path.join(path.dirname(directory), path.basename(directory).toUpperCase())]) {
      await activePlugin(spelling)
    }
    const before = await cacheSnapshot()
    const targets = targetKeys(before, [directory])
    assert.equal(targets.length, 2, "ambiguous-target probe requires two real cache keys")
    const registrations = command("git", ["worktree", "list", "--porcelain"], root, env)
    await refuseRelease("review-worktree", reviewRequest(newOID, number), /ambiguous target cache aliases/)
    const after = await cacheSnapshot()
    assert.deepEqual(after, before, "ambiguous target refusal must preserve every loaded key and identity")
    assert.equal(command("git", ["worktree", "list", "--porcelain"], root, env), registrations)
    assert.equal(command("git", ["rev-parse", "HEAD"], directory, env).trim(), oldOID)
    releaseEvidence.ambiguous_target = { directory, keys: targets, before, after, registrations_preserved: true }
    pass("real Windows target casing aliases refuse before disposal/Git; both caches retained for normal host shutdown")
  }
  evidence.checks.push("real native children for all five roles; concurrently live dirty checkout bindings")
  assert.equal(requests, 0)
  evidence.behavior_checks_passed = true
} catch (error) {
  failure = error
} finally {
  controller?.socket.close()
  const shutdown = evidence.owned_shutdown = { verified: false }
  try {
    if (server?.pid && server.exitCode === null && server.signalCode === null) {
      if (stopHost) {
        const stopped = await stopHost()
        shutdown.method = "native service stop with isolated record identity verified"
        shutdown.exit = stopped.status
        assert.equal(stopped.status, 0, "could not stop owned private server")
      } else if (process.platform === "win32") {
        // Startup failed before authenticated discovery. Only our just-spawned
        // process tree is eligible; never invoke managed discovery as fallback.
        const stopped = spawnSync("taskkill", ["/pid", String(server.pid), "/t", "/f"], { encoding: "utf8", windowsHide: true })
        shutdown.method = "owned startup-failure process tree"
        shutdown.exit = stopped.status
        assert.equal(stopped.status, 0, "could not stop owned startup-failure process tree")
      } else server.kill("SIGTERM")
    }
    if (serverExit) {
      shutdown.process = { pid: server.pid, ...await within(serverExit, "owned server shutdown") }
      assert(shutdown.method === "native service stop with isolated record identity verified" &&
        !shutdown.process.error && (shutdown.process.code !== null || shutdown.process.signal),
      "owned native shutdown could not be confirmed")
      assert(!await present(path.join(state, "opencode", "service.json")), "owned private service record still exists")
      shutdown.service_record_removed = true
    } else shutdown.method = "no private host started"
    assert(evidence.direct_memhub_shutdown?.verified !== false, "owned direct Memhub shutdown is unconfirmed")
    shutdown.process_inventory = verifyStopped(temp)
    shutdown.verified = true
  } catch (error) {
    evidence.shutdown_error = { message: error.message, stack: error.stack }
    failure ??= error
  }
  try {
    if (provider.listening) await within(new Promise((resolve, reject) =>
      provider.close((error) => error ? reject(error) : resolve())), "isolated provider trap shutdown")
  } catch (error) {
    shutdown.verified = false
    failure ??= error
  }
  evidence.provider_requests = requests
  evidence.owned_server_stopped = shutdown.verified
  try {
    if (evidence.orch && evidence.memhub) {
      evidence.artifacts_after = await verifyArtifacts(selection, evidence.orch, evidence.memhub)
      pass("selected adapter identity files and binaries unchanged; private Memhub restored")
    }
  } catch (error) {
    evidence.artifact_error = { message: error.message, stack: error.stack }
    failure ??= error
  }
  evidence.passed = !failure && evidence.behavior_checks_passed === true && requests === 0 &&
    shutdown.verified && evidence.artifacts_after?.unchanged === true
  if (!evidence.passed) {
    failure ??= new Error("smoke qualification did not complete")
    evidence.error = { message: failure.message, stack: failure.stack }
  }
  try {
    await storage.finish(evidence, logs)
  } catch (error) {
    if (failure) throw new AggregateError([failure, error], "Smoke failed and its pending fixture was not disposed")
    throw error
  }
}

if (failure) throw failure
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
