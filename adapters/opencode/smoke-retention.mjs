import assert from "node:assert/strict"
import { randomUUID } from "node:crypto"
import fs from "node:fs/promises"
import { createServer } from "node:net"
import { hostname, tmpdir } from "node:os"
import path from "node:path"
import { inside } from "./smoke-artifacts.mjs"
import { verifyStopped } from "./smoke-shutdown.mjs"
import {
  append, checkWindows, contents, copyTree, directory, externalDirectory, inventory,
  json, present, readRegular, removeTree, sha256, syncDirectory, treeBytes, writeNew,
} from "./smoke-storage.mjs"

const archiveVariable = "ORCH_SMOKE_ARCHIVE_ROOT"
const recoveryCommand = "npm run smoke:recover --prefix adapters/opencode"
const digest = /^[a-f0-9]{64}$/
const uuid = /^[a-f0-9]{8}(?:-[a-f0-9]{4}){3}-[a-f0-9]{12}$/
const idPattern = /^\d+:\d+:-?\d+$/
const idle = async () => {}
const stages = ["intent", "allocated", "running", "finished", "prepared", "archive", "retained"]
const localName = (root, id) => path.join(root, "run-" + id)
const evidenceFiles = ["evidence.json", "server.log"]
const encode = (data, previous = null) => JSON.stringify({ previous, sha256: sha256(JSON.stringify([previous, data])), data }) + "\n"

function decode(bytes, root, rootID) {
  const text = bytes.toString("utf8")
  assert(text.endsWith("\n"), "incomplete pending journal")
  const records = {}
  let previous = null
  let position = -1
  for (const line of text.slice(0, -1).split("\n")) {
    const record = JSON.parse(line)
    assert.equal(line + "\n", encode(record.data, previous), "corrupt pending journal")
    previous = record.sha256
    const next = stages.indexOf(record.data.stage)
    assert(next > position, "invalid pending journal stage order")
    position = next
    records[record.data.stage] = record.data
  }
  const { intent, allocated, running, finished, prepared, archive, retained } = records
  assert([1, 2].includes(intent?.version) && uuid.test(intent.run_id) && intent.storage_id === rootID &&
    intent.fixture_root === localName(root, intent.run_id) && intent.hostname === hostname(), "unverifiable pending owner or fixture path")
  assert(!allocated || idPattern.test(allocated.id), "invalid allocated fixture identity")
  assert(!running || allocated, "run never proved allocation")
  assert(!finished || (allocated && typeof finished.passed === "boolean" && typeof finished.shutdown === "boolean" &&
    (!finished.passed || (running && finished.shutdown)) &&
    evidenceFiles.every((name) => digest.test(finished.files?.[name]))), "invalid completed evidence")
  const snapshot = intent.version === 1 ? prepared : finished?.inventory
  assert(intent.version === 1 || (!prepared && Boolean(snapshot) === (finished?.shutdown === true)), "missing or misplaced source inventory")
  assert(!snapshot || (finished?.shutdown === true && Number.isSafeInteger(snapshot.bytes) && snapshot.bytes >= 0 &&
    Array.isArray(snapshot.rows) && snapshot.rows[0]?.name === "" && snapshot.rows[0].id === allocated.id), "invalid source inventory")
  assert(!archive || (snapshot && !finished.passed && path.isAbsolute(archive.root) && idPattern.test(archive.id)), "invalid archive target")
  assert(!retained || (snapshot && (finished.passed || archive) && idPattern.test(retained.id) && digest.test(retained.sha256)),
    "invalid retained evidence record")
  return { records, previous }
}

function verifyEvidenceFiles(rows, files) {
  for (const name of evidenceFiles) {
    assert(rows.some((row) => row.name === name && row.sha256 === files[name]), `missing or corrupt ${name}`)
  }
}

function receipt(evidence, intent, files) {
  const fileID = (file) => typeof file?.path === "string" && digest.test(file.sha256)
  assert(evidence.behavior_checks_passed === true && evidence.artifacts_after?.unchanged === true &&
    evidence.provider_requests === 0 && evidence.owned_server_stopped === true &&
    evidence.owned_shutdown?.verified === true && evidence.owned_shutdown.method === "native service stop with isolated record identity verified" &&
    Array.isArray(evidence.checks) && evidence.checks.length > 0 && evidence.checks.every((check) => typeof check === "string" && check) &&
    ["source-build", "explicit-artifact"].includes(evidence.mode) &&
    evidence.harness?.files?.length > 0 && evidence.harness.files.every(fileID) &&
    [evidence.runtime_binary, evidence.selected_runtime_binary, evidence.adapter?.manifest, evidence.adapter?.lockfile,
      evidence.adapter?.entrypoint, evidence.orch?.executed, evidence.memhub?.selected, evidence.memhub?.executed].every(fileID) &&
    (evidence.mode !== "explicit-artifact" || fileID(evidence.orch.selected)), "incomplete successful smoke evidence")
  // Keep identities and check outcomes, not host payloads, databases or logs.
  return {
    version: 1, run_id: intent.run_id, started_at: intent.started_at, fixture_root: intent.fixture_root,
    passed: true, mode: evidence.mode, runtime: evidence.runtime, node: evidence.node, sdk: evidence.sdk,
    harness: evidence.harness, adapter: evidence.adapter, orch: evidence.orch, memhub: evidence.memhub,
    selected_runtime_binary: evidence.selected_runtime_binary, runtime_binary: evidence.runtime_binary,
    command_binding: evidence.command_binding && {
      ...Object.fromEntries(Object.entries(evidence.command_binding).filter(([key]) => key !== "transport")),
      transport: evidence.command_binding.transport && Object.fromEntries(
        Object.entries(evidence.command_binding.transport).filter(([key]) => key !== "pages")),
    },
    checkout_release: evidence.checkout_release && {
      source_files: evidence.checkout_release.source_files, openapi_artifact: evidence.checkout_release.contract?.artifact,
    },
    artifacts_unchanged: true, checks: evidence.checks, provider_requests: evidence.provider_requests,
    owned_server_stopped: true, owned_shutdown: evidence.owned_shutdown, direct_memhub_shutdown: evidence.direct_memhub_shutdown,
    original_evidence_sha256: files["evidence.json"],
  }
}

export async function openSmokeStorage({ temp = tmpdir(), archiveRoot = process.env[archiveVariable], checkpoint = idle } = {}) {
  const base = await externalDirectory(temp)
  const root = path.join(base, "orch-opencode-smoke")
  try {
    await fs.mkdir(root, { mode: 0o700 })
    await syncDirectory(base)
  } catch (error) {
    if (error.code !== "EEXIST") throw error
  }
  assert.equal(await externalDirectory(root), root, "smoke storage redirected")
  const rootID = await directory(root)
  const pending = path.join(root, "pending.jsonl")
  let current

  async function load() {
    await directory(root, rootID)
    if (!await present(pending)) return undefined
    const entry = await readRegular(pending)
    return { entry, ...decode(entry.bytes, root, rootID) }
  }

  function diagnostic(reason, recovery, state = current?.records) {
    const fixture = state?.intent?.fixture_root ?? "unknown (inspect the pending record)"
    const bytes = state?.finished?.inventory?.bytes ?? state?.prepared?.bytes ?? "unknown"
    return new Error(`${reason}\nSmoke pending record: ${pending}\nIntended fixture: ${fixture} (${bytes} bytes)\nAction: ${recovery}`)
  }

  // Key the non-stale mutex to physical storage, not cwd, branch, a PID or a
  // clock. A port collision is a refusal (never evidence of abandonment).
  const port = 49152 + (parseInt(sha256(await directory(base)).slice(0, 4), 16) % 16384)
  const socket = createServer((connection) => connection.destroy())
  try {
    await new Promise((resolve, reject) => {
      socket.once("error", reject)
      socket.listen({ host: "127.0.0.1", port, exclusive: true }, resolve)
    })
  } catch (error) {
    let state
    try { state = (await load())?.records } catch { /* A live writer can be mid-record. */ }
    throw diagnostic(`Smoke storage is busy or its local lock is unavailable (${error.code ?? error.message}); no fixture allocated or reclaimed.`,
      "Wait for an active smoke/recovery to finish, or resolve the local lock error before retrying.", state)
  }
  socket.unref()

  async function refresh() {
    try {
      current = await load()
    } catch (error) {
      throw diagnostic(`Cannot verify smoke pending state: ${error.message}`,
        "Preserve the journal and fixture; have the operator inspect the local record and resolve ownership/evidence before retrying.")
    }
  }

  async function log(stage, data) {
    await directory(root, rootID)
    const row = encode({ stage, ...data }, current?.previous)
    if (current) await append(pending, current.entry, row)
    else await writeNew(pending, row)
    await refresh()
    assert.equal(current?.records[stage]?.stage, stage, `pending stage did not persist: ${stage}`)
  }

  async function clear() {
    await directory(root, rootID)
    assert.deepEqual(await readRegular(pending), current.entry, "pending journal changed before disposal")
    await fs.unlink(pending)
    await syncDirectory(root)
    current = undefined
  }

  function recoveryAction() {
    const { intent, allocated, running, finished, archive } = current.records
    if (running && finished?.shutdown !== true) {
      return `Do not restart or kill by PID. Inspect ${path.join(intent.fixture_root, "state", "opencode", "service.json")} and local logs; ask the responsible operator to verify owned-process shutdown and disposition separately. Automated recovery refuses without proof of shutdown and source ownership.`
    }
    if (intent.version !== 2) return "Preserve the legacy journal and scratch for operator inspection; this journal cannot prove which entries belonged to the run."
    if (!allocated) return `Run ${recoveryCommand} to verify that scratch was never allocated. If it exists without ownership proof, leave it and the journal for operator inspection.`
    const target = finished?.passed ? path.join(root, `receipt-${intent.run_id}.json`)
      : archive ? path.join(archive.root, "orch-opencode-smoke-" + intent.run_id) : `local storage in ${archiveVariable}`
    return `Check the owned scratch and the retention destination (${target}), then run ${recoveryCommand}. Preserve mismatched or incomplete evidence for inspection; do not delete the journal.`
  }

  async function requireIdle() {
    await refresh()
    if (current) throw diagnostic("Smoke recovery is required; no fixture allocated.", recoveryAction())
  }

  async function begin() {
    await requireIdle()
    const run_id = randomUUID()
    const temp = localName(root, run_id)
    await log("intent", {
      version: 2, run_id, fixture_root: temp, storage_id: rootID, hostname: hostname(),
      pid: process.pid, started_at: new Date().toISOString(),
    })
    console.log(`Smoke pending: ${pending}; intended fixture: ${temp}`)
    await checkpoint("intent", temp)
    await directory(root, rootID)
    await fs.mkdir(temp, { mode: 0o700 })
    await checkpoint("mkdir", temp)
    await syncDirectory(root)
    await log("allocated", { id: await directory(temp) })
    await checkpoint("allocated", temp)
    return temp
  }

  async function finish(evidence, logs) {
    const { intent, allocated, running } = current.records
    assert.equal(evidence.fixture_root, intent.fixture_root)
    await directory(root, rootID)
    await directory(intent.fixture_root, allocated.id)
    checkWindows(intent.fixture_root)
    const files = { "evidence.json": json(evidence), "server.log": logs }
    for (const name of evidenceFiles) {
      await directory(intent.fixture_root, allocated.id)
      await writeNew(path.join(intent.fixture_root, name), files[name])
    }
    await checkpoint("final-files-written", intent.fixture_root)
    const hashes = Object.fromEntries(evidenceFiles.map((name) => [name, sha256(files[name])]))
    const shutdown = evidence.owned_shutdown?.verified === true
    let snapshot
    if (shutdown) {
      const rows = await inventory(intent.fixture_root, allocated.id)
      verifyEvidenceFiles(rows, hashes)
      if (!running) assert(rows.length === 1 + evidenceFiles.length, "unexpected data before fixture activity")
      snapshot = { bytes: treeBytes(rows), rows }
    }
    // Shutdown and the source proof must become recoverable together. Never
    // start an ownership inventory from a stopped runner's unproven tree.
    await log("finished", { passed: evidence.passed === true, shutdown, files: hashes, inventory: snapshot })
    await checkpoint("finished", intent.fixture_root)
  }

  async function archivePath(archive, runID) {
    const canonical = await externalDirectory(archive.root)
    assert(canonical === archive.root && !inside(root, canonical) && !inside(canonical, root),
      "archive storage must be separate from smoke storage")
    await directory(canonical, archive.id)
    return path.join(canonical, "orch-opencode-smoke-" + runID)
  }

  async function dispose(recovering = false) {
    // All paths below are derived from validated state under this storage lock.
    const { intent, allocated, running } = current.records
    const { fixture_root: source, run_id: runID } = intent
    await directory(root, rootID)
    assert(intent.version === 2, "legacy pending journal has no trusted source inventory")
    if (!allocated) {
      assert(!await present(source), `scratch exists but allocation ownership was never recorded: ${source}`)
      await clear()
      console.log(`Smoke recovery: ${runID} was interrupted before allocation; no fixture or processes to dispose.`)
      return
    }
    if (!running && !current.records.finished) {
      // No process can start before the durable running stage.
      await directory(source, allocated.id)
      checkWindows(source, "data")
      assert((await fs.readdir(source)).length === 0, `unexpected data before fixture activity: ${source}`)
      if (recovering) verifyStopped(source)
      await finish({
        fixture_root: source, passed: false, checks: [], provider_requests: 0,
        error: { message: "smoke interrupted before starting fixture activity" },
        owned_server_stopped: true, owned_shutdown: { method: "no fixture activity started", verified: true },
      }, "")
    }
    const finished = current.records.finished
    assert(finished?.shutdown === true, "owned shutdown is unconfirmed")
    const { inventory: snapshot } = finished
    let { archive, retained } = current.records
    const rows = await present(source) ? await inventory(source, allocated.id) : []
    if (recovering && rows.length) {
      verifyStopped(source)
      assert(!rows.some((row) => row.name === "state/opencode/service.json"), "private service record reappeared")
    }
    const expected = snapshot.rows
    verifyEvidenceFiles(expected, finished.files)
    if (!retained) {
      assert(rows.length, "owned scratch is missing before verified retention")
      verifyEvidenceFiles(rows, finished.files)
      assert.deepEqual(rows, expected, "source inventory changed before verified retention")
    } else {
      const original = new Map(expected.map((row) => [row.name, JSON.stringify(row)]))
      assert(rows.every((row) => JSON.stringify(row) === original.get(row.name)),
        "source has unexpected or changed entries; refusing resumed removal")
    }
    let target, proof
    if (finished.passed) {
      target = path.join(root, `receipt-${runID}.json`)
      let intended
      if (!retained) {
        const { bytes } = await readRegular(path.join(source, "evidence.json"))
        assert.equal(sha256(bytes), finished.files["evidence.json"], "successful evidence changed")
        const evidence = JSON.parse(bytes)
        assert(evidence.passed === true && evidence.fixture_root === source, "successful evidence does not identify this run")
        intended = json(receipt(evidence, intent, finished.files))
        if (!await present(target)) await writeNew(target, intended)
      }
      const read = await readRegular(target)
      if (intended) assert(read.bytes.equals(Buffer.from(intended)), `receipt does not match verified smoke: ${target}`)
      proof = { id: read.id, sha256: sha256(read.bytes) }
    } else {
      if (!archive) {
        assert(archiveRoot, `configure ${archiveVariable} with an existing absolute local archive directory`)
        const canonical = await externalDirectory(archiveRoot)
        assert(!inside(root, canonical) && !inside(canonical, root), "archive storage must be separate from smoke storage")
        await log("archive", { root: canonical, id: await directory(canonical) })
        archive = current.records.archive
      }
      target = await archivePath(archive, runID)
      const payload = path.join(target, "fixture")
      const manifest = path.join(target, "manifest.json")
      const intended = json({
        version: 1, run_id: runID, started_at: intent.started_at, fixture_root: source, passed: false,
        bytes: snapshot.bytes, files: contents(expected), original_evidence_sha256: finished.files["evidence.json"],
      })
      if (!retained && !await present(target)) {
        await directory(archive.root, archive.id)
        const free = await fs.statfs(archive.root, { bigint: true })
        assert(free.bavail * free.bsize >= BigInt(snapshot.bytes + Buffer.byteLength(intended)),
          `insufficient space for smoke archive on ${archive.root}`)
        await fs.mkdir(target, { mode: 0o700 })
        const targetID = await directory(target)
        await checkpoint("archive-mkdir", target)
        await directory(target, targetID)
        checkWindows(target, "data")
        await copyTree(source, expected, payload)
        await directory(target, targetID)
        await writeNew(manifest, intended)
        await syncDirectory(archive.root)
      }
      const targetID = await directory(target, retained?.id)
      checkWindows(target, "data")
      const names = (await fs.readdir(target)).sort()
      assert.deepEqual(names, ["fixture", "manifest.json"], `incomplete or unexpected archive entries: ${target}`)
      const archived = await readRegular(manifest)
      assert(archived.bytes.equals(Buffer.from(intended)), `archive manifest differs from intended fixture: ${target}`)
      assert.deepEqual(contents(await inventory(payload)), contents(expected), `archive contents differ from intended fixture: ${target}`)
      await directory(target, targetID)
      proof = { id: targetID, sha256: sha256(archived.bytes) }
    }
    if (retained) {
      assert(retained.id === proof.id && retained.sha256 === proof.sha256, `retained evidence changed: ${target}`)
    } else {
      await checkpoint("evidence-written", target)
      await log("retained", proof)
      await checkpoint("retained", target)
    }
    const pointer = { run_id: runID, kind: finished.passed ? "receipt" : "failure-archive", path: target, sha256: proof.sha256 }
    console.log("SMOKE_EVIDENCE " + JSON.stringify(pointer))

    await directory(root, rootID)
    if (rows.length) {
      // Recheck the complete remainder after verification of the destination,
      // before the first unlink; even a new unrelated entry is a refusal.
      assert.deepEqual(await inventory(source, allocated.id), rows, "source changed during retention")
      await removeTree(source, rows, checkpoint)
    }
    assert(!await present(source), "owned scratch still exists")
    await checkpoint("scratch-removed", source)
    await clear()
    return pointer
  }

  const report = (error) => current ? diagnostic(`Smoke disposition refused: ${error.message}`, recoveryAction()) : error
  return {
    root, requireIdle,
    begin: async () => {
      try { return await begin() } catch (error) { throw report(error) }
    },
    markRunning: async () => {
      try {
        const { intent, allocated } = current.records
        await directory(root, rootID)
        await directory(intent.fixture_root, allocated.id)
        checkWindows(intent.fixture_root, "data")
        await log("running", {})
      } catch (error) { throw report(error) }
    },
    finish: async (...args) => {
      try {
        await finish(...args)
        return await dispose()
      } catch (error) { throw report(error) }
    },
    recover: async () => {
      await refresh()
      if (!current) {
        console.log(`No pending smoke at ${pending}; nothing allocated or removed.`)
        return
      }
      try { return await dispose(true) } catch (error) { throw report(error) }
    },
    async [Symbol.asyncDispose]() {
      await new Promise((resolve) => socket.close(resolve))
    },
  }
}

if (import.meta.main) {
  assert(process.argv.length === 2, "smoke recovery accepts no arguments or fixture paths")
  await using storage = await openSmokeStorage()
  await storage.recover()
}
