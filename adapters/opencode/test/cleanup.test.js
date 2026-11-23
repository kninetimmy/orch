import assert from "node:assert/strict"
import { mkdtemp, rm } from "node:fs/promises"
import { tmpdir } from "node:os"
import path from "node:path"
import test from "node:test"
import { installCleanup } from "../src/cleanup.js"

const request = { schema_version: 1, run_id: "run-fixture", issue_number: 37, statement: "cleanup-issue" }
const context = { sessionID: "sesFixture", agent: "build", messageID: "msgFixture", id: "call-fixture" }
const cleaned = { schema_version: 1, issue_number: 37, phase: "cleaned" }

async function setup(t, invoke) {
  const directory = await mkdtemp(path.join(tmpdir(), "orch-cleanup-test-"))
  t.after(() => rm(directory, { recursive: true }))
  const fixture = { session: { id: context.sessionID, location: { directory } } }
  fixture.ctx = {
    location: { directory, project: { canonical: directory } },
    permission: { assert: async () => {} },
    session: { get: async () => fixture.session },
    tool: { transform: async (register) => register({
      get: () => undefined, add: (definition) => (fixture.tool = definition),
    }) },
  }
  fixture.register = (scope = { schema_version: 2, managed: true, memhub_mode: "off", memory_root: directory,
    code_root: directory, git_common_dir: directory, head: "a".repeat(40) }) =>
    installCleanup(fixture.ctx, () => JSON.stringify(scope), invoke)
  return fixture
}

test("primary cleanup is direct and waits for the engine's matching cleaned result", async (t) => {
  const started = Promise.withResolvers()
  const engine = Promise.withResolvers()
  const f = await setup(t, (input, cwd) => {
    started.resolve({ input, cwd })
    return engine.promise
  })
  let permission
  f.ctx.permission.assert = async (value) => (permission = value)
  await f.register()
  assert.equal(f.tool.name, "orch_cleanup")
  assert.equal(f.tool.options.codemode, false)
  let finished = false
  const result = f.tool.execute(request, context).then((output) => { finished = true; return output })
  assert.deepEqual(await started.promise, { input: request, cwd: f.session.location.directory })
  assert.equal(permission.action, f.tool.name)
  assert.deepEqual(permission.resources, ["run-fixture#37"])
  assert.equal(finished, false)
  engine.resolve(JSON.stringify(cleaned))
  assert.deepEqual(JSON.parse((await result).content), cleaned)
})

test("invalid confirmation, any child, moved session, and denied permission cannot start cleanup", async (t) => {
  let calls = 0
  const f = await setup(t, () => { calls++; return JSON.stringify(cleaned) })
  await f.register()
  const run = f.tool.execute
  await assert.rejects(run({ ...request, run_id: "" }, context), /confirmed run/)
  await assert.rejects(run({ ...request, statement: "yes" }, context), /confirmed run/)
  await assert.rejects(run({ ...request, issue_number: 0 }, context), /confirmed run/)
  const session = f.session
  f.session = { ...session, parentID: "sesParent" }
  await assert.rejects(run(request, context), /primary Architect/)
  f.session = session
  await assert.rejects(run(request, { ...context, agent: "orch-specialist" }), /primary Architect/)
  f.ctx.permission.assert = async () => { throw new Error("host refused permission") }
  await assert.rejects(run(request, context), /host refused permission/)
  f.ctx.permission.assert = async () => {
    f.session = { ...session, location: { directory: path.dirname(session.location.directory) } }
  }
  await assert.rejects(run(request, context), /primary Architect/)
  assert.equal(calls, 0)
})

test("unmanaged or non-primary locations get no tool; refused and incomplete results are not success", async (t) => {
  let value = { ...cleaned, phase: "pending" }
  const f = await setup(t, () => {
    if (value instanceof Error) throw value
    return JSON.stringify(value)
  })
  await f.register({ schema_version: 2, managed: false })
  assert.equal(f.tool, undefined)
  const directory = f.ctx.location.directory
  f.ctx.location.project.canonical = path.dirname(directory)
  await f.register()
  assert.equal(f.tool, undefined)
  f.ctx.location.project.canonical = directory
  await f.register()
  await assert.rejects(f.tool.execute(request, context), /did not report this issue cleaned/)
  value = { ...cleaned, issue_number: 38 }
  await assert.rejects(f.tool.execute(request, context), /did not report this issue cleaned/)
  value = new Error("orch run: running Code Mode job prevents removal")
  await assert.rejects(f.tool.execute(request, context), value)
})
