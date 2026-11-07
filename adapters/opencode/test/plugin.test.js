import assert from "node:assert/strict"
import { mkdtemp, rm } from "node:fs/promises"
import { tmpdir } from "node:os"
import path from "node:path"
import test from "node:test"

import plugin, { definePlugin, mutationPaths, mutationTools, pluginID, runOrch } from "../src/index.js"

function pathInputs(target) {
  return [
    ["write", { path: target }, [target]],
    ["edit", { path: target }, [target]],
    ["patch", { patchText: "*** Begin Patch\n*** Add File: " + target + "\n+created\n*** End Patch" }, [target]],
    ["patch", { patchText: "*** Begin Patch\n*** Delete File: " + target + "\n*** End Patch" }, [target]],
    ["patch", { patchText: "*** Begin Patch\n*** Update File: " + target + "\n@@\n-before\n+after\n*** End Patch" }, [target]],
    ["patch", { patchText: "*** Begin Patch\n*** Update File: " + target + "\n*** Move to: destination.txt\n@@\n-before\n+after\n*** End Patch" }, [target, "destination.txt"]],
    ["patch", { patchText: "*** Begin Patch\n*** Update File: source.txt\n*** Move to: " + target + "\n@@\n-before\n+after\n*** End Patch" }, ["source.txt", target]],
  ]
}

test("exports the pinned V2 plugin contract", () => {
  assert.equal(pluginID, "orch.delivery")
  assert.equal(plugin.id, pluginID)
  assert.equal(typeof plugin.setup, "function")
  assert.deepEqual([...mutationTools], ["edit", "write", "patch"])
})

test("extracts every built-in mutation target", () => {
  assert.deepEqual(mutationPaths("edit", { path: "a.txt" }), ["a.txt"])
  assert.deepEqual(mutationPaths("write", { path: "b.txt" }), ["b.txt"])
  assert.deepEqual(
    mutationPaths("patch", {
      patchText: "*** Begin Patch\n*** Add File: a.txt\n+created\n*** Update File: b.txt\n*** Move to: c.txt\n@@\n-before\n+after\n*** Delete File: d.txt\n*** End Patch",
    }),
    ["a.txt", "b.txt", "c.txt", "d.txt"],
  )
  assert.deepEqual(mutationPaths("read", { filePath: "a.txt" }), [])
})

test("fails closed on malformed mutation inputs", () => {
  assert.throws(() => mutationPaths("edit", {}), /no path/)
  assert.throws(() => mutationPaths("write", { filePath: "legacy.txt" }), /no path/)
  assert.throws(() => mutationPaths("write", null), /not an object/)
  assert.throws(() => mutationPaths("patch", { patchText: "*** Begin Patch\n*** End Patch" }), /names no files/)
})

test("uses native trimmed headers, heredocs and both move targets", () => {
  assert.deepEqual(mutationPaths("patch", {
    patchText: "*** Begin Patch\n*** Add File: .scratch/allowed.txt\n+safe\n  *** Add File: tracked.txt  \n+overwritten\n*** End Patch",
  }), [".scratch/allowed.txt", "tracked.txt"])
  assert.deepEqual(mutationPaths("patch", {
    patchText: "cat <<'PATCH'\r\n*** Begin Patch\r\n*** Environment ID: test-fixture\r\n  *** Update File: source.txt  \r\n*** Move to: destination.txt  \r\n@@\r\n-before\r\n+after\r\n*** Delete File: removed.txt\r\n*** End Patch\r\nPATCH",
  }), ["source.txt", "destination.txt", "removed.txt"])
})

test("rejects partial invalid patches before any guard or session action", async () => {
  let hook
  let calls = 0
  await definePlugin(() => calls++).setup({
    tool: { hook: async (_, callback) => (hook = callback) },
    session: { get: async () => calls++, hook: async () => {} },
  })
  await assert.rejects(hook({ tool: "patch", input: {
    patchText: "*** Begin Patch\n*** Add File: .scratch/allowed.txt\n+safe\n  *** Unknown File: tracked.txt\n+overwritten\n*** End Patch",
  } }), /not a valid native patch/)
  assert.equal(calls, 0)
})

test("normalizes native Windows aliases for every source and move target", () => {
  for (const alias of ["/c:/guard/target.txt", "/c/guard/target.txt", "/cygdrive/c/guard/target.txt", "/mnt/c/guard/target.txt"]) {
    const normalized = process.platform === "win32" ? "C:/guard/target.txt" : alias
    for (const [tool, input, expected] of pathInputs(alias)) {
      assert.deepEqual(mutationPaths(tool, input), expected.map((target) => target === alias ? normalized : target))
    }
  }
})

test("rejects exactly the native home-expanding forms across mutations", () => {
  for (const target of ["~", "~/tracked.txt", ...(process.platform === "win32" ? ["~\\tracked.txt"] : [])]) {
    for (const [tool, input] of pathInputs(target)) {
      assert.throws(() => mutationPaths(tool, input), /home-relative paths are unsupported/)
    }
  }
  for (const target of ["~notes.txt", ...(process.platform !== "win32" ? ["~\\notes.txt"] : [])]) {
    for (const [tool, input, expected] of pathInputs(target)) {
      assert.deepEqual(mutationPaths(tool, input), expected)
    }
  }
})

test("registers pre-execution guard and session context hooks", async () => {
  const hooks = []
  const calls = []
  let switched = false
  await definePlugin((args, cwd) => {
    calls.push([args, cwd])
    return args[0] === "hook" ? "context" : ""
  }).setup({
    tool: { hook: async (name, callback) => hooks.push(["tool", name, callback]) },
    session: {
      get: async () => ({ location: { directory: "/repo" } }),
      hook: async (name, callback) => hooks.push(["session", name, callback]),
      switchModel: async () => (switched = true),
    },
  })
  assert.deepEqual(
    hooks.map(([domain, name]) => [domain, name]),
    [["tool", "execute.before"], ["session", "context"]],
  )
  await hooks[0][2]({ sessionID: "session", agent: "build", tool: "write", input: { path: "a.txt", content: "x" } })
  const event = { sessionID: "session", model: { providerID: "openai", id: "gpt", variant: "high" }, system: [] }
  await hooks[1][2](event)
  assert.deepEqual(calls, [
    [["guard", "check", "--", path.resolve("/repo", "a.txt")], "/repo"],
    [["hook", "opencode", "session-start", "--model", "openai/gpt#high"], "/repo"],
  ])
  assert.deepEqual(event.system, [{ type: "text", text: "context" }])
  assert.equal(switched, false)
})

test("does not compare a child session model to the Architect", async () => {
  let hook
  let call
  await definePlugin((args, cwd) => {
    call = [args, cwd]
    return "context"
  }).setup({
    tool: { hook: async () => {} },
    session: {
      get: async () => ({ parentID: "parent", location: { directory: "/repo" } }),
      hook: async (_, callback) => (hook = callback),
    },
  })
  const event = { sessionID: "child", model: { providerID: "other", id: "model" }, system: [] }
  await hook(event)
  assert.deepEqual(call, [["hook", "opencode", "session-start"], "/repo"])
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
  await assert.rejects(hook({ sessionID: "session", agent: "build", tool: "edit", input: { path: "tracked.txt", oldString: "a", newString: "b" } }), /denied/)
})

test("both hooks fail closed without an absolute session directory", async () => {
  for (const session of [undefined, {}, { location: {} }, { location: { directory: "relative" } }]) {
    const hooks = []
    let calls = 0
    await definePlugin(() => calls++).setup({
      tool: { hook: async (_, callback) => hooks.push(callback) },
      session: { get: async () => session, hook: async (_, callback) => hooks.push(callback) },
    })
    await assert.rejects(hooks[0]({ sessionID: "session", tool: "write", input: { path: "tracked.txt" } }), /session directory/)
    await assert.rejects(hooks[1]({ sessionID: "session", model: { providerID: "test", id: "model" }, system: [] }), /session directory/)
    assert.equal(calls, 0)
  }
})

test("missing orch is an error, not permission to continue", async () => {
  const directory = await mkdtemp(path.join(tmpdir(), "orch-plugin-unit-"))
  const previousPath = process.env.PATH
  try {
    process.env.PATH = directory
    assert.throws(() => runOrch(["guard", "check", "--", "tracked.txt"], directory), /orch:.*ENOENT/)
  } finally {
    process.env.PATH = previousPath
    assert(path.resolve(directory).startsWith(path.resolve(tmpdir()) + path.sep))
    await rm(directory, { recursive: true })
  }
})
