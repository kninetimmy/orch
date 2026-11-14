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
    const expected = { schema_version: 1, memhub_mode: "best-effort", memory_root: root, code_root: code, git_common_dir: root, head: "a".repeat(40) }
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
    const context = await installLookup(ctx, () => JSON.stringify(expected))
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
    assert.throws(() => readLookupContext(() => '{"schema_version":2}', root), /schema/)
  } finally {
    await rm(root, { recursive: true })
  }
})

test("off never adds a server; normalized collisions fail without overwriting", async () => {
  const root = await mkdtemp(path.join(tmpdir(), "orch-lookup-modes-"))
  try {
    const expected = { schema_version: 1, memhub_mode: "off", memory_root: root, code_root: root, git_common_dir: root, head: "a".repeat(40) }
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
  } finally {
    await rm(root, { recursive: true })
  }
})
