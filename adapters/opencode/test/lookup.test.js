import assert from "node:assert/strict"
import { mkdtemp, mkdir, rm } from "node:fs/promises"
import { tmpdir } from "node:os"
import path from "node:path"
import test from "node:test"
import { checkBinding, childRoles, installLookup, lookupServer, lookupTools, readLookupContext } from "../src/lookup.js"

test("native lookup configuration, permissions and executor boundary for every role", async () => {
  const root = await mkdtemp(path.join(tmpdir(), "orch-lookup-unit-"))
  try {
    const code = path.join(root, "code")
    await mkdir(code)
    const expected = { schema_version: 2, managed: true, memhub_mode: "best-effort", memory_root: root, code_root: code, git_common_dir: root, head: "a".repeat(40) }
    const binding = { ...expected, mode: "explicit", repository_relationship: "registered_worktree" }
    let calls = 0
    const tools = new Map([...lookupTools, "task_add"].map((name) => [`${lookupServer}_${name}`, {
      id: `${lookupServer}_${name}`, execute: async () => { calls++; return { content: JSON.stringify({ binding }) } },
    }]))
    tools.set("private_recall", { id: "private_recall", execute: async () => { calls++; return {} } })
    for (const name of [...lookupTools, "task_add"]) {
      tools.set(`remote_${name}`, { id: `remote_${name}`, execute: async () => { calls++; return {} } })
    }
    const servers = new Map([["private", { type: "local", command: ["memhub", "serve"] }], ["docs", { type: "remote", url: "https://example.invalid" }]])
    const agents = [...childRoles, "build", "other"].map((id) => ({ id, permissions: [{ action: "shell", resource: "*", effect: "deny" }] }))
    const editor = (map) => ({ list: () => [...map.values()], get: (id) => map.get(id), update: (id, cb) => cb(map.get(id)) })
    const session = { parentID: "parent", location: { directory: code } }
    let oldCatalog = false
    let connected = true
    const ctx = {
      location: { directory: code },
      session: { get: async () => session },
      mcp: {
        transform: async (cb) => cb({ list: () => [...servers], get: (id) => servers.get(id), set: (id, value) => servers.set(id, value) }),
        list: async () => ({ data: [{ name: lookupServer, status: { status: connected ? "connected" : "failed" } }] }),
      },
      agent: { transform: async (cb) => cb({ list: () => agents, update: (id, fn) => fn(agents.find((agent) => agent.id === id)) }) },
      tool: { transform: async (cb) => cb(editor(tools)), list: async () => [...tools.values()].filter((entry) => oldCatalog || !entry.id.endsWith("_task_add")) },
    }
    let observed = expected
    const context = await installLookup(ctx, () => JSON.stringify(observed))
    assert.deepEqual(servers.get(lookupServer).command, ["memhub", "serve", "--memory-root", root, "--code-root", code, "--profile", "lookup-only"])
    assert.equal(servers.get(lookupServer).pool, false)
    assert.equal(servers.get("docs").url, "https://example.invalid")
    for (const agent of agents) {
      assert.deepEqual(agent.permissions[0], { action: "shell", resource: "*", effect: "deny" })
      assert(agent.permissions.some((rule) => rule.action === `${lookupServer}_*` && rule.effect === "deny"))
      assert.equal(agent.permissions.some((rule) => rule.action === `${lookupServer}_recall` && rule.effect === "allow"), childRoles.has(agent.id))
    }
    for (const agent of childRoles) {
      const identity = { agent, sessionID: agent }
      await tools.get(`${lookupServer}_status`).execute({}, identity)
      for (const name of lookupTools) await tools.get(`${lookupServer}_${name}`).execute({}, identity)
      await assert.rejects(tools.get(`${lookupServer}_task_add`).execute({}, identity), /tool denied/)
      await assert.rejects(tools.get("private_recall").execute({}, identity), /broad Memhub/)
      await assert.rejects(tools.get("remote_task_add").execute({}, identity), /broad Memhub/)
    }
    await assert.rejects(tools.get(`${lookupServer}_status`).execute({}, { agent: "other" }), /supported Orch child role/)
    await assert.rejects(tools.get("private_recall").execute({}, { agent: "general" }), /broad Memhub/)
    session.parentID = undefined
    await tools.get("private_recall").execute({}, { agent: "build" })
    await assert.rejects(tools.get(`${lookupServer}_status`).execute({}, { agent: "orch-scout" }), /supported Orch child role/)
    assert.equal(calls, childRoles.size * 5 + 1)
    session.parentID = "parent"
    session.location.directory = root
    // Engine context for a moved session, unlike plugin-instance context.
    const moved = await installLookup({ ...ctx, mcp: undefined }, () => JSON.stringify(expected))
    const event = { agent: "orch-scout", system: [] }
    await moved(event, session)
    assert.match(event.system[0].text, /host lacks native/)
    expected.memhub_mode = "off"
    await assert.rejects(tools.get(`${lookupServer}_status`).execute({}, { agent: "orch-scout", sessionID: "scout" }), /mode is off/)
    const offEvent = { agent: "orch-scout", system: [] }
    await context(offEvent, session)
    assert.match(offEvent.system[0].text, /mode is off/)
    expected.memhub_mode = "required"
    connected = false
    // Current engine mode, not the mode captured during plugin setup, decides
    // whether a failed preflight can be mediated.
    await assert.rejects(context({ agent: "orch-scout", system: [] }, session), /service is failed/)
    connected = true
    oldCatalog = true
    await assert.rejects(tools.get(`${lookupServer}_status`).execute({}, { agent: "orch-scout", sessionID: "scout" }), /advertise exactly/)
    assert.throws(() => checkBinding({ ...binding, code_root: root }, expected), /code_root identity mismatch/)
    assert.throws(() => readLookupContext(() => '{"schema_version":1}', root), /schema/)
    observed = { schema_version: 2, managed: false }
    await assert.rejects(tools.get(`${lookupServer}_status`).execute({}, { agent: "orch-scout", sessionID: "scout" }), /outside Orch management/)
  } finally {
    await rm(root, { recursive: true })
  }
})

test("off never adds a server; normalized collisions fail without overwriting", async () => {
  const root = await mkdtemp(path.join(tmpdir(), "orch-lookup-modes-"))
  try {
    const expected = { schema_version: 2, managed: true, memhub_mode: "off", memory_root: root, code_root: root, git_common_dir: root, head: "a".repeat(40) }
    let added = 0
    let collision = false
    const ctx = {
      location: { directory: root },
      mcp: {
        list: async () => ({ data: [] }),
        transform: async (cb) => cb({ list: () => collision ? [["orch.memhub", {}]] : [], set: () => added++ }),
      },
      agent: { transform: async (cb) => cb({ list: () => [] }) },
      tool: { transform: async (cb) => cb({ list: () => [] }), list: async () => [] },
    }
    await installLookup(ctx, () => JSON.stringify(expected))
    assert.equal(added, 0)
    collision = true
    const collided = await installLookup(ctx, () => JSON.stringify(expected))
    const event = { agent: "orch-scout", system: [] }
    await collided(event, { parentID: "parent", location: { directory: root } })
    assert.match(event.system[0].text, /name collision/)
    assert.equal(added, 0)
    expected.memhub_mode = "required"
    const context = await installLookup({ ...ctx, mcp: undefined }, () => JSON.stringify(expected))
    await assert.rejects(context({ agent: "orch-reviewer", system: [] }, { parentID: "parent", location: { directory: root } }), /host lacks native/)
    collision = false
    let observed = expected
    const scoped = await installLookup(ctx, () => JSON.stringify(observed))
    observed = { schema_version: 2, managed: false }
    await assert.rejects(scoped({ agent: "orch-reviewer", system: [] }, { parentID: "parent", location: { directory: root } }), /outside Orch management/)
  } finally {
    await rm(root, { recursive: true })
  }
})

test("authoritative unmanaged scope leaves every native registration, executor and child context untouched", async () => {
  const root = await mkdtemp(path.join(tmpdir(), "orch-lookup-unmanaged-"))
  try {
    let calls = 0
    const execute = async () => ++calls
    const servers = new Map([
      ["memhub", { type: "local", command: ["memhub", "serve"] }],
      ["orch.memhub", { type: "remote", url: "https://example.invalid" }],
    ])
    const agents = [...childRoles, "build", "general"].map((id) => ({ id, permissions: [] }))
    const tools = new Map(["memhub_recall", "private_task_add", "orch_memhub_status"].map((id) => [id, { id, execute }]))
    const untouched = () => assert.fail("unmanaged lookup must not even register a native transform")
    const ctx = {
      location: { directory: root },
      mcp: { transform: untouched, list: untouched },
      agent: { transform: untouched },
      tool: { transform: untouched, list: untouched },
      session: { get: untouched },
    }
    const run = (args, cwd) => {
      assert.deepEqual(args, ["hook", "opencode", "lookup-context", "--json"])
      assert.equal(cwd, root)
      return '{"schema_version":2,"managed":false}'
    }
    for (const host of [ctx, { location: ctx.location }]) {
      const context = await installLookup(host, run)
      for (const agent of ["build", "general", ...childRoles]) {
        for (const parentID of [undefined, "parent"]) {
          const event = { agent, system: [] }
          await context(event, { parentID, location: { directory: root } })
          assert.deepEqual(event.system, [])
          for (const entry of tools.values()) {
            assert.equal(entry.execute, execute)
            await entry.execute({}, { agent, sessionID: agent })
          }
        }
      }
    }
    assert.equal(calls, 2 * (childRoles.size + 2) * 2 * tools.size)
    assert.equal(servers.size, 2)
    assert(agents.every((agent) => agent.permissions.length === 0))
  } finally {
    await rm(root, { recursive: true })
  }
})

test("discovery, configuration and wire failures never become the unmanaged no-op", async (t) => {
  const root = await mkdtemp(path.join(tmpdir(), "orch-lookup-errors-"))
  try {
    const managed = { schema_version: 2, managed: true, memhub_mode: "best-effort", memory_root: root, code_root: root, git_common_dir: root, head: "a".repeat(40) }
    const cases = [
      ["missing engine", () => { throw new Error("spawn orch ENOENT") }],
      ["old engine capability", () => { throw new Error("orch hook: usage") }],
      ["discovery prose is not authority", () => { throw new Error("no .orchestrator directory found") }],
      ["invalid config", () => { throw new Error("invalid configuration") }],
      ["unreadable marker", () => { throw new Error("find repo root: permission denied") }],
      ...[
        ["malformed JSON", "{"], ["null", "null"], ["array", "[]"],
        ["v1", JSON.stringify({ ...managed, schema_version: 1 })],
        ["missing management distinction", '{"schema_version":2}'],
        ["non-boolean management", '{"schema_version":2,"managed":"false"}'],
        ["contradictory unmanaged payload", '{"schema_version":2,"managed":false,"memhub_mode":"off"}'],
        ["invalid mode", JSON.stringify({ ...managed, memhub_mode: "unknown" })],
        ["missing root", JSON.stringify({ ...managed, code_root: path.join(root, "missing") })],
        ["non-directory root", JSON.stringify({ ...managed, code_root: process.execPath })],
        ["relative root", JSON.stringify({ ...managed, code_root: "relative" })],
        ["invalid revision", JSON.stringify({ ...managed, head: "unknown" })],
      ].map(([label, text]) => [label, () => text]),
    ]
    for (const [label, run] of cases) {
      await t.test(label, async () => {
        assert.throws(() => readLookupContext(run, root))
        let calls = 0
        let parentID = "parent"
        const servers = new Map([["memhub", { type: "local", command: ["memhub", "serve"] }]])
        const agents = [{ id: "orch-scout", permissions: [] }]
        const tools = new Map([["memhub_recall", { id: "memhub_recall", execute: async () => ++calls }]])
        const ctx = {
          location: { directory: root },
          session: { get: async () => ({ parentID, location: { directory: root } }) },
          mcp: {
            transform: async (cb) => cb({ list: () => [...servers], set: () => assert.fail("failed discovery must add no service") }),
            list: async () => ({ data: [] }),
          },
          agent: { transform: async (cb) => cb({ list: () => agents, update: (_, fn) => fn(agents[0]) }) },
          tool: {
            transform: async (cb) => cb({ list: () => [...tools.values()], update: (id, fn) => fn(tools.get(id)) }),
            list: async () => [...tools.values()],
          },
        }
        const context = await installLookup(ctx, run)
        assert(agents[0].permissions.some((rule) => rule.action === "memhub_*" && rule.effect === "deny"))
        await assert.rejects(tools.get("memhub_recall").execute({}, { agent: "general" }), /broad Memhub/)
        assert.equal(calls, 0)
        const event = { agent: "orch-scout", system: [] }
        await context(event, { parentID, location: { directory: root } })
        assert.match(event.system[0].text, /Memhub child lookup unavailable/)
        parentID = undefined
        await tools.get("memhub_recall").execute({}, { agent: "build" })
        assert.equal(calls, 1, "Architect broad access remains intact even on failed discovery")
      })
    }
  } finally {
    await rm(root, { recursive: true })
  }
})
