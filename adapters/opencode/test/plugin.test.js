import assert from "node:assert/strict"
import test from "node:test"

import plugin, { definePlugin, mutationPaths, mutationTools, pluginID } from "../src/index.js"

test("exports the pinned V2 plugin contract", () => {
  assert.equal(pluginID, "orch.delivery")
  assert.equal(plugin.id, pluginID)
  assert.equal(typeof plugin.setup, "function")
  assert.deepEqual([...mutationTools], ["edit", "write", "patch"])
})

test("extracts every built-in mutation target", () => {
  assert.deepEqual(mutationPaths("edit", { filePath: "a.txt" }), ["a.txt"])
  assert.deepEqual(mutationPaths("write", { filePath: "b.txt" }), ["b.txt"])
  assert.deepEqual(
    mutationPaths("patch", {
      patchText: "*** Begin Patch\n*** Add File: a.txt\n*** Update File: b.txt\n*** Move to: c.txt\n*** Delete File: d.txt\n*** End Patch",
    }),
    ["a.txt", "b.txt", "c.txt", "d.txt"],
  )
  assert.deepEqual(mutationPaths("read", { filePath: "a.txt" }), [])
})

test("fails closed on malformed mutation inputs", () => {
  assert.throws(() => mutationPaths("edit", {}), /no filePath/)
  assert.throws(() => mutationPaths("write", null), /not an object/)
  assert.throws(() => mutationPaths("patch", { patchText: "*** Begin Patch\n*** End Patch" }), /names no files/)
})

test("registers pre-execution guard and session context hooks", async () => {
  const hooks = []
  const calls = []
  await definePlugin((args, cwd) => {
    calls.push([args, cwd])
    return args[0] === "hook" ? "context" : ""
  }).setup({
    tool: { hook: async (name, callback) => hooks.push(["tool", name, callback]) },
    session: {
      get: async () => ({ location: { directory: "/repo" } }),
      hook: async (name, callback) => hooks.push(["session", name, callback]),
    },
  })
  assert.deepEqual(
    hooks.map(([domain, name]) => [domain, name]),
    [["tool", "execute.before"], ["session", "context"]],
  )
  await hooks[0][2]({ sessionID: "session", tool: "write", input: { filePath: "a.txt" } })
  const event = { sessionID: "session", system: [] }
  await hooks[1][2](event)
  assert.deepEqual(calls, [
    [["guard", "check", "--", "a.txt"], "/repo"],
    [["hook", "opencode", "session-start"], "/repo"],
  ])
  assert.deepEqual(event.system, [{ type: "text", text: "context" }])
})

test("a denied guard stops before OpenCode can continue", async () => {
  let hook
  const denied = definePlugin(() => {
    throw new Error("denied")
  })
  await denied.setup({
    tool: { hook: async (_, callback) => (hook = callback) },
    session: {
      get: async () => ({ location: { directory: "/repo" } }),
      hook: async () => {},
    },
  })
  await assert.rejects(hook({ sessionID: "session", tool: "edit", input: { filePath: "tracked.txt" } }), /denied/)
})
