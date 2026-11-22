import assert from "node:assert/strict"
import { execFile as callbackExec, spawn } from "node:child_process"
import { createHash } from "node:crypto"
import { once } from "node:events"
import fs from "node:fs/promises"
import { tmpdir } from "node:os"
import path from "node:path"
import test from "node:test"
import { fileURLToPath, pathToFileURL } from "node:url"
import { promisify } from "node:util"
import { openSmokeStorage } from "../smoke-retention.mjs"

const execFile = promisify(callbackExec)
const adapter = fileURLToPath(new URL("..", import.meta.url))
const harness = path.join(adapter, "smoke.mjs")
const recovery = path.join(adapter, "smoke-retention.mjs")
const linkType = process.platform === "win32" ? "junction" : "dir"
const sha = (data) => createHash("sha256").update(data).digest("hex")

function evidence(temp, outcome) {
  const file = { path: "synthetic", sha256: "a".repeat(64) }
  return {
    fixture_root: temp, passed: outcome === "pass", mode: "source-build",
    runtime: "synthetic", behavior_checks_passed: outcome === "pass", artifacts_after: { unchanged: true },
    harness: { files: [file] }, adapter: { manifest: file, lockfile: file, entrypoint: file },
    runtime_binary: file, selected_runtime_binary: file, orch: { executed: file },
    memhub: { selected: file, executed: file }, provider_requests: 0, owned_server_stopped: outcome !== "unknown",
    owned_shutdown: { method: "native service stop with isolated record identity verified", verified: outcome !== "unknown" },
    checks: ["synthetic check"], last_request: "raw payload must not appear in the receipt",
    ...(outcome === "pass" ? {} : { error: { message: "synthetic failure" } }),
  }
}

async function fixture(t) {
  const base = await fs.realpath(await fs.mkdtemp(path.join(tmpdir(), "orch-retention-test-")))
  t.after(() => fs.rm(base, { recursive: true, force: true }))
  const temp = path.join(base, "temp")
  const archive = path.join(base, "archive")
  const outside = path.join(base, "outside")
  for (const dir of [temp, archive, outside]) await fs.mkdir(dir)
  const sentinel = path.join(outside, "sentinel")
  await fs.writeFile(sentinel, "unrelated; preserve")
  const store = path.join(temp, "orch-opencode-smoke")
  const env = { ...process.env, TMP: temp, TMPDIR: temp, TEMP: temp, ORCH_SMOKE_ARCHIVE_ROOT: archive }
  for (const name of ["ORCH_SMOKE_ORCH", "ORCH_SMOKE_ADAPTER", "ORCH_SMOKE_MEMHUB"]) delete env[name]
  return { base, temp, archive, outside, sentinel, store, env, pending: path.join(store, "pending.jsonl") }
}

async function node(f, args, options = {}) {
  try {
    return { code: 0, ...await execFile(process.execPath, args, { env: f.env, timeout: 60_000, windowsHide: true, ...options }) }
  } catch (error) {
    assert.equal(typeof error.code, "number", error.message)
    return { code: error.code, stdout: error.stdout, stderr: error.stderr }
  }
}

async function crash(f, point, outcome = "pass", module = recovery) {
  const script = `
    import { openSmokeStorage } from ${JSON.stringify(pathToFileURL(module).href)};
    import fs from "node:fs/promises";
    import path from "node:path";
    const [point, outcome] = process.argv.slice(1);
    const storage = await openSmokeStorage({ checkpoint(at) { if (point === at) process.exit(91); } });
    const temp = await storage.begin();
    await storage.markRunning();
    await fs.mkdir(path.join(temp, "empty"));
    await fs.writeFile(path.join(temp, "payload"), "fixture bytes");
    await fs.link(path.join(temp, "payload"), path.join(temp, "shadow"));
    if (point === "running") process.exit(91);
    await storage.finish((${evidence.toString()})(temp, outcome), "server logs");
    throw Error("missed checkpoint " + point);
  `
  const result = await node(f, ["--input-type=module", "--eval", script, point, outcome])
  assert.equal(result.code, 91, result.stderr || result.stdout)
  return JSON.parse((await fs.readFile(f.pending, "utf8")).split("\n")[0]).data
}

async function blocked(f, source, script = harness, pattern = /recovery is required/) {
  const before = await fs.readdir(f.store)
  const result = await node(f, [script], { cwd: path.dirname(script) })
  assert.equal(result.code, 1, result.stdout)
  assert.match(result.stderr, pattern)
  assert(result.stderr.includes(f.pending) && result.stderr.includes(source), result.stderr)
  assert.match(result.stderr, /\((?:unknown|\d+) bytes\)[\s\S]*Action:/)
  assert.deepEqual(await fs.readdir(f.store), before, "blocked invocation must not allocate scratch")
  assert.equal(await fs.readFile(f.sentinel, "utf8"), "unrelated; preserve")
  return result
}

const absent = (file) => assert.rejects(fs.lstat(file), { code: "ENOENT" })
const pointer = ({ stdout }) => JSON.parse(stdout.split(/\r?\n/).find((line) => line.startsWith("SMOKE_EVIDENCE ")).slice(15))

test("shared storage blocks another registered worktree from allocation, not mislabeling an active owner", async (t) => {
  const f = await fixture(t)
  const primary = path.join(f.base, "primary")
  const other = path.join(f.base, "worktree")
  await fs.mkdir(path.join(primary, "src"), { recursive: true })
  for (const name of ["smoke.mjs", "smoke-artifacts.mjs", "smoke-retention.mjs", "smoke-storage.mjs", "smoke-shutdown.mjs",
    "package.json", "src/lookup.js"]) await fs.copyFile(path.join(adapter, name), path.join(primary, name))
  const gitEnv = Object.fromEntries(Object.entries(f.env).filter(([name]) => !/^GIT_/i.test(name)))
  Object.assign(gitEnv, { GIT_CONFIG_GLOBAL: process.platform === "win32" ? "NUL" : "/dev/null", GIT_CONFIG_NOSYSTEM: "1" })
  const git = (...args) => execFile("git", args, { cwd: primary, env: gitEnv, windowsHide: true })
  await git("init", "-b", "main")
  await git("add", "-A")
  await git("-c", "user.name=Fixture", "-c", "user.email=fixture@example.invalid", "commit", "-m", "private fixture")
  await git("worktree", "add", "--detach", other, "HEAD")
  {
    await using active = await openSmokeStorage({ temp: f.temp, archiveRoot: f.archive })
    const owned = await active.begin()
    for (const script of ["smoke.mjs", "smoke-retention.mjs"]) {
      const result = await blocked(f, owned, path.join(other, script), /busy or its local lock is unavailable/)
      assert.doesNotMatch(result.stderr, /abandoned|recovery is required/)
    }
  }
  const archived = await node(f, [path.join(other, "smoke-retention.mjs")])
  assert.equal(archived.code, 0, archived.stderr)
  assert.equal(pointer(archived).kind, "failure-archive")
  await absent(f.pending)

  const intent = await crash(f, "intent", "pass", path.join(primary, "smoke-retention.mjs"))
  await absent(intent.fixture_root)
  await blocked(f, intent.fixture_root, path.join(other, "smoke.mjs"))
  const cleared = await node(f, [path.join(other, "smoke-retention.mjs")])
  assert.equal(cleared.code, 0, cleared.stderr)
  assert.match(cleared.stdout, /interrupted before allocation/)
  await absent(f.pending)
  await absent(intent.fixture_root)
})

test("interruption after mkdir but before ownership proof refuses to clear or allocate", async (t) => {
  const f = await fixture(t)
  const { fixture_root: source } = await crash(f, "mkdir")
  const before = await fs.readFile(f.pending)
  await blocked(f, source)
  await blocked(f, source, recovery, /allocation ownership was never recorded/)
  assert.deepEqual(await fs.readFile(f.pending), before)
  assert.deepEqual(await fs.readdir(source), [])
})

test("interrupted activity and explicitly unconfirmed shutdown both preserve the owned fixture", async (t) => {
  for (const point of ["running", "finished"]) {
    const f = await fixture(t)
    const { fixture_root: source } = await crash(f, point, "unknown")
    const before = await fs.readFile(f.pending)
    await blocked(f, source, harness, /Automated recovery refuses without proof/)
    await blocked(f, source, recovery, /owned shutdown is unconfirmed/)
    assert.deepEqual(await fs.readFile(f.pending), before)
    assert.equal(await fs.readFile(path.join(source, "payload"), "utf8"), "fixture bytes")
    assert.deepEqual(await fs.readdir(f.archive), [])
  }
})

test("pass receipts are readable without scratch; interrupted retention and repeated recovery never overwrite", async (t) => {
  const f = await fixture(t)
  const { fixture_root: source, run_id: id } = await crash(f, "evidence-written")
  const receipt = path.join(f.store, `receipt-${id}.json`)
  const before = await fs.readFile(receipt)
  const stat = await fs.stat(receipt, { bigint: true })
  await blocked(f, source)
  const child = spawn(process.execPath, ["-e", "process.stdin.resume(); process.stdout.write('ready')", path.join(source, "payload")],
    { stdio: ["pipe", "pipe", "ignore"], windowsHide: true })
  const closed = once(child, "close")
  try {
    await once(child.stdout, "data")
    await blocked(f, source, recovery, /a process still names this smoke fixture/)
    assert.equal(child.exitCode, null, "recovery must never stop a process")
    assert.equal(await fs.readFile(path.join(source, "payload"), "utf8"), "fixture bytes")
  } finally {
    child.stdin.end()
    await closed
  }
  const result = await node(f, [recovery])
  assert.equal(result.code, 0, result.stderr)
  assert.deepEqual(pointer(result), { run_id: id, kind: "receipt", path: receipt, sha256: sha(before) })
  const data = JSON.parse(before)
  assert.deepEqual(data.checks, ["synthetic check"])
  assert.equal(data.provider_requests, 0)
  assert.equal(data.owned_shutdown.verified, true)
  assert.equal(data.memhub.executed.sha256, "a".repeat(64))
  assert(!Object.hasOwn(data, "last_request"))
  await absent(source)
  await absent(f.pending)
  assert.match((await node(f, [recovery])).stdout, /nothing allocated or removed/)
  assert.deepEqual(await fs.readFile(receipt), before)
  assert.equal((await fs.stat(receipt, { bigint: true })).ino, stat.ino)
  assert.equal(await fs.readFile(f.sentinel, "utf8"), "unrelated; preserve")
})

test("failure archive verifies exact payload and resumes partial scratch removal without overwriting", async (t) => {
  const f = await fixture(t)
  const { fixture_root: source, run_id: id } = await crash(f, "removed-entry", "fail")
  const target = path.join(f.archive, "orch-opencode-smoke-" + id)
  const manifest = path.join(target, "manifest.json")
  const before = await fs.readFile(manifest)
  const data = JSON.parse(before)
  assert.equal(data.passed, false)
  assert(data.files.some((file) => file.name === "empty" && !Object.hasOwn(file, "bytes")))
  assert.deepEqual(await fs.readdir(path.join(target, "fixture", "empty")), [])
  assert.deepEqual(data.files.find((file) => file.name === "payload"), { name: "payload", bytes: 13, sha256: sha("fixture bytes") })
  assert.equal(await fs.readFile(path.join(target, "fixture", "shadow"), "utf8"), "fixture bytes")
  assert.equal(await fs.readFile(path.join(target, "fixture", "server.log"), "utf8"), "server logs")
  await absent(path.join(source, "shadow"))
  assert.equal((await fs.stat(path.join(source, "payload"), { bigint: true })).nlink, 1n,
    "interruption after removing an internal hard link must allow verified recovery of the remaining name")

  // An unrelated arrival or damaged retained archive must not remove the rest.
  const unexpected = path.join(source, "new-owner")
  await fs.writeFile(unexpected, "do not remove")
  await blocked(f, source, recovery, /unexpected or changed entries/)
  assert.equal(await fs.readFile(unexpected, "utf8"), "do not remove")
  await fs.unlink(unexpected)
  const archivedPayload = path.join(target, "fixture", "payload")
  await fs.writeFile(archivedPayload, "altered")
  await blocked(f, source, recovery, /archive contents differ/)
  assert.equal(await fs.readFile(path.join(source, "payload"), "utf8"), "fixture bytes")
  await fs.writeFile(archivedPayload, "fixture bytes")
  const result = await node(f, [recovery])
  assert.equal(result.code, 0, result.stderr)
  assert.deepEqual(pointer(result), { run_id: id, kind: "failure-archive", path: target, sha256: sha(before) })
  await absent(source)
  await absent(f.pending)
  assert.equal((await node(f, [recovery])).code, 0)
  assert.deepEqual(await fs.readdir(f.archive), [path.basename(target)])
  assert.deepEqual(await fs.readFile(manifest), before)
})

test("with scratch already removed, corrupt receipt blocks clearing pending until verified", async (t) => {
  const f = await fixture(t)
  const { fixture_root: source, run_id: id } = await crash(f, "scratch-removed")
  const target = path.join(f.store, `receipt-${id}.json`)
  const before = await fs.readFile(target)
  await absent(source)
  await fs.writeFile(target, "{")
  await blocked(f, source, recovery, /retained evidence changed/)
  await fs.writeFile(target, before)
  assert.equal((await node(f, [recovery])).code, 0)
  await absent(f.pending)
  await absent(source)
})

test("missing, redirected and full archive storage refuse without loss or overwriting partial evidence", async (t) => {
  const f = await fixture(t)
  const { fixture_root: source, run_id: id } = await crash(f, "finished", "fail")
  const alias = path.join(f.base, "redirect")
  await fs.symlink(f.outside, alias, linkType)
  const initial = await fs.readFile(f.pending, "utf8")
  let prepared
  for (const [location, pattern] of [
    ["", /configure ORCH_SMOKE_ARCHIVE_ROOT/],
    [path.join(f.base, "unavailable"), /missing, redirected or non-directory/],
    [alias, /missing, redirected or non-directory/],
    [f.store, /archive storage must be separate/],
  ]) {
    f.env.ORCH_SMOKE_ARCHIVE_ROOT = location
    await blocked(f, source, recovery, pattern)
    const current = await fs.readFile(f.pending, "utf8")
    assert(current.startsWith(initial), "source evidence must remain in the pending record")
    prepared ??= current
    assert.equal(current, prepared, "binding failures must not advance the prepared record")
  }
  {
    await using storage = await openSmokeStorage({ temp: f.temp, archiveRoot: f.archive, async checkpoint(at, target) {
      if (at === "archive-mkdir") {
        await fs.writeFile(path.join(target, "partial"), "original incomplete copy")
        throw Object.assign(new Error("ENOSPC (injected archive write failure)"), { code: "ENOSPC" })
      }
    } })
    await assert.rejects(storage.recover(), /ENOSPC/)
  }
  const target = path.join(f.archive, "orch-opencode-smoke-" + id)
  f.env.ORCH_SMOKE_ARCHIVE_ROOT = f.archive
  await blocked(f, source, recovery, /incomplete or unexpected archive entries/)
  assert.equal(await fs.readFile(path.join(target, "partial"), "utf8"), "original incomplete copy")
  assert.equal(await fs.readFile(path.join(source, "payload"), "utf8"), "fixture bytes")
  const preserved = path.join(f.archive, "operator-preserved-partial")
  await fs.rename(target, preserved)
  const resumed = await node(f, [recovery])
  assert.equal(resumed.code, 0, resumed.stderr)
  assert.equal(pointer(resumed).path, target)
  assert.equal(await fs.readFile(path.join(preserved, "partial"), "utf8"), "original incomplete copy")
  await absent(source)
})

test("corrupt journals/evidence, substituted roots, and interior links fail closed with external sentinels", async (t) => {
  const f = await fixture(t)
  const { fixture_root: source } = await crash(f, "finished")
  const pathToEvidence = path.join(source, "evidence.json")
  const journal = await fs.readFile(f.pending)
  await fs.appendFile(f.pending, "{")
  const corrupt = await fs.readFile(f.pending)
  const badJournal = await node(f, [recovery])
  assert.equal(badJournal.code, 1)
  assert.match(badJournal.stderr, /incomplete pending journal/)
  assert.deepEqual(await fs.readFile(f.pending), corrupt)
  await fs.writeFile(f.pending, journal)
  const original = await fs.readFile(pathToEvidence)
  await fs.writeFile(pathToEvidence, "{}")
  await blocked(f, source, recovery, /corrupt evidence.json/)
  await fs.writeFile(pathToEvidence, original)
  const redirected = path.join(source, "redirect")
  await fs.symlink(f.outside, redirected, linkType)
  await blocked(f, source, recovery, /reparse point|redirected or off-volume/)
  await fs.unlink(redirected)
  await fs.link(f.sentinel, redirected)
  await blocked(f, source, recovery, /externally linked or non-regular smoke file/)
  await fs.unlink(redirected)

  const originalSource = path.join(f.base, "original-source")
  await fs.rename(source, originalSource)
  await fs.mkdir(source)
  await fs.writeFile(path.join(source, "external-sentinel"), "unowned fixture replacement")
  await blocked(f, source, recovery, /directory ownership changed/)
  assert.equal(await fs.readFile(path.join(source, "external-sentinel"), "utf8"), "unowned fixture replacement")
  assert.equal(await fs.readFile(path.join(originalSource, "payload"), "utf8"), "fixture bytes")
  assert.deepEqual(await fs.readFile(f.pending), journal)
})
