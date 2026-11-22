import assert from "node:assert/strict"
import { randomUUID } from "node:crypto"
import fs from "node:fs/promises"
import { hostname, tmpdir } from "node:os"
import path from "node:path"
import { acquireSmokeLock, prepareSmokeLockFile } from "./smoke-lock.mjs"
import { verifyStopped } from "./smoke-shutdown.mjs"
import {
  append, archiveSeparation, contents, copyTree, directory, externalDirectory, inventory,
  json, present, privateDirectory, readRegular, removeTree, sha256, syncDirectory, treeBytes, writeNew,
} from "./smoke-storage.mjs"

const archiveVariable = "ORCH_SMOKE_ARCHIVE_ROOT"
const recoveryCommand = "npm run smoke:recover --prefix adapters/opencode"
const digest = /^[a-f0-9]{64}$/
const uuid = /^[a-f0-9]{8}(?:-[a-f0-9]{4}){3}-[a-f0-9]{12}$/
const idPattern = /^\d+:\d+:-?\d+$/
const journalVersion = 3
const idle = async () => {}
const stages = ["intent", "allocated", "running", "finished", "prepared", "archive", "retained"]
const localName = (root, id) => path.join(root, "run-" + id)
const evidenceFiles = ["evidence.json", "server.log"]
const encode = (data, previous = null) => JSON.stringify({ previous, sha256: sha256(JSON.stringify([previous, data])), data }) + "\n"

function decode(bytes, root, rootID, lockID) {
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
  assert([1, 2, journalVersion].includes(intent?.version) && uuid.test(intent.run_id) && intent.storage_id === rootID &&
    intent.fixture_root === localName(root, intent.run_id) && intent.hostname === hostname(), "unverifiable pending owner or fixture path")
  assert(intent.version !== journalVersion || intent.lock_id === lockID, "pending journal was not written under this smoke.lock")
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
  let pending = typeof temp === "string" ? path.join(temp, "orch-opencode-smoke", "pending.jsonl") : "(unverified temp path)"
  let root, rootID, current
  const inspectStorage = "Preserve the storage as it is; ask the operator to verify private access and any pending evidence before retrying."
  try {
    const { canonical: base, id } = await externalDirectory(temp)
    root = path.join(base, "orch-opencode-smoke")
    pending = path.join(root, "pending.jsonl")
    await directory(base, id)
    try {
      await fs.mkdir(root, { mode: 0o700 })
    } catch (error) {
      if (error.code !== "EEXIST") throw error
    }
    const checked = await externalDirectory(root, true)
    assert.equal(checked.canonical, root, "smoke storage redirected")
    rootID = checked.id
    // The process that made the store may have exited before syncing its name.
    await syncDirectory(base, id)
  } catch (error) {
    throw diagnostic(`Cannot verify private smoke storage: ${error.message}`, inspectStorage)
  }
  const verifyRoot = () => privateDirectory(root, rootID)

  async function load() {
    await verifyRoot()
    if (!await present(pending)) return undefined
    const entry = await readRegular(pending)
    return { entry, ...decode(entry.bytes, root, rootID, lock.id) }
  }

  function diagnostic(reason, recovery, state = current?.records) {
    const fixture = state?.intent?.fixture_root ?? "unknown (inspect the pending record)"
    const bytes = state?.finished?.inventory?.bytes ?? state?.prepared?.bytes ?? "unknown"
    return new Error(`${reason}\nSmoke pending record: ${pending}\nIntended fixture: ${fixture} (${bytes} bytes)\nAction: ${recovery}`)
  }

  let lock, closeLock
  try {
    lock = await prepareSmokeLockFile(root, rootID, pending)
    closeLock = await acquireSmokeLock(lock.file)
    await lock.check()
  } catch (error) {
    closeLock?.()
    throw diagnostic(`Cannot verify or acquire smoke.lock: ${error.message}; no fixture allocated or reclaimed.`, inspectStorage)
  }
  if (!closeLock) {
    let state
    try { state = (await load())?.records } catch { /* A live writer can be mid-record. */ }
    throw diagnostic("Smoke storage is busy (smoke.lock held); no fixture allocated or reclaimed.",
      "Wait for the active smoke or recovery to finish, then retry.", state)
  }

  async function refresh() {
    try {
      current = await load()
    } catch (error) {
      current = undefined
      throw diagnostic(`Cannot verify smoke pending state: ${error.message}`,
        "Preserve the journal and fixture; have the operator inspect the local record and resolve ownership/evidence before retrying.")
    }
  }

  async function log(stage, data) {
    await lock.check()
    const row = encode({ stage, ...data }, current?.previous)
    if (current) await append(pending, current.entry, row)
    else await writeNew(pending, row)
    await refresh()
    assert.equal(current?.records[stage]?.stage, stage, `pending stage did not persist: ${stage}`)
  }

  async function clear() {
    await lock.check()
    assert.deepEqual(await readRegular(pending), current.entry, "pending journal changed before disposal")
    // Absence of scratch may be visible after an interrupted removal, but the
    // removal must be durable before we unlink its journal.
    await syncDirectory(root, rootID)
    await fs.unlink(pending)
    await syncDirectory(root, rootID)
    current = undefined
  }

  function recoveryAction() {
    const { intent, allocated, running, finished, archive } = current.records
    if (intent.version !== journalVersion) return "Preserve the legacy journal and scratch for operator inspection; this journal was not written under the current storage lock."
    if (running && finished?.shutdown !== true) {
      return `Do not restart or kill by PID. Inspect ${path.join(intent.fixture_root, "state", "opencode", "service.json")} and local logs; ask the responsible operator to verify owned-process shutdown and disposition separately. Automated recovery refuses without proof of shutdown and source ownership.`
    }
    if (!allocated) return `Run ${recoveryCommand} to verify that scratch was never allocated. If it exists without ownership proof, leave it and the journal for operator inspection.`
    const target = finished?.passed ? path.join(root, `receipt-${intent.run_id}.json`)
      : archive ? path.join(archive.root, "orch-opencode-smoke-" + intent.run_id) : `local storage in ${archiveVariable}`
    return `Check the owned scratch and the retention destination (${target}), then run ${recoveryCommand}. Preserve mismatched or incomplete evidence for inspection; do not delete the journal.`
  }

  async function requireIdle() {
    await refresh()
    if (current) {
      const reason = current.records.intent.version === journalVersion ? "Smoke recovery is required" : "Legacy smoke journal; activity unknown"
      throw diagnostic(`${reason}; no fixture allocated.`, recoveryAction())
    }
  }

  async function begin() {
    await requireIdle()
    const run_id = randomUUID()
    const temp = localName(root, run_id)
    await log("intent", {
      version: journalVersion, run_id, fixture_root: temp, storage_id: rootID, lock_id: lock.id, hostname: hostname(),
      pid: process.pid, started_at: new Date().toISOString(),
    })
    console.log(`Smoke pending: ${pending}; intended fixture: ${temp}`)
    await checkpoint("intent", temp)
    await verifyRoot()
    await fs.mkdir(temp, { mode: 0o700 })
    await checkpoint("mkdir", temp)
    await syncDirectory(root, rootID)
    await log("allocated", { id: await privateDirectory(temp) })
    await checkpoint("allocated", temp)
    return temp
  }

  async function finish(evidence, logs) {
    const { intent, allocated, running } = current.records
    assert.equal(evidence.fixture_root, intent.fixture_root)
    await verifyRoot()
    await privateDirectory(intent.fixture_root, allocated.id)
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

  async function archivePath(archive, runID, sourceRows) {
    const configured = archive?.root ?? archiveRoot
    assert(configured, `configure ${archiveVariable} with an existing absolute local archive directory`)
    const checked = await externalDirectory(configured, true)
    const { canonical, id } = checked
    if (archive) assert(canonical === archive.root && id === archive.id, `archive directory ownership changed: ${canonical}`)
    const target = path.join(canonical, "orch-opencode-smoke-" + runID)
    const archiveEntries = await archiveSeparation({ canonical: root, id: rootID }, checked, target, sourceRows)
    assert(archive || !await present(target), `archive already exists without a pinned destination: ${target}`)
    return { target, archiveEntries, location: { root: canonical, id } }
  }

  async function dispose(recovering = false) {
    // All paths below are derived from validated state under this storage lock.
    const { intent, allocated, running } = current.records
    const { fixture_root: source, run_id: runID } = intent
    await lock.check()
    assert(intent.version === journalVersion, "legacy pending journal was not written under the current storage lock")
    if (recovering) {
      assert.deepEqual(await readRegular(pending, { flush: true }), current.entry, "pending journal changed before recovery")
    }
    if (!allocated) {
      assert(!await present(source), `scratch exists but allocation ownership was never recorded: ${source}`)
      await clear()
      console.log(`Smoke recovery: ${runID} was interrupted before allocation; no fixture or processes to dispose.`)
      return
    }
    if (!running && !current.records.finished) {
      // No process can start before the durable running stage.
      const sourceID = await privateDirectory(source, allocated.id, "data")
      assert((await fs.readdir(source)).length === 0, `unexpected data before fixture activity: ${source}`)
      verifyStopped(source)
      await archivePath(undefined, runID, [{ id: sourceID }])
      await finish({
        fixture_root: source, passed: false, checks: [], provider_requests: 0,
        error: { message: "smoke interrupted before starting fixture activity" },
        owned_server_stopped: true, owned_shutdown: { method: "no fixture activity started", verified: true },
      }, "")
    }
    const finished = current.records.finished
    assert(finished?.shutdown === true, "owned shutdown is unconfirmed")
    const { inventory: snapshot } = finished
    const { archive, retained } = current.records
    const rows = await present(source) ? await inventory(source, allocated.id) : []
    if (recovering) {
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
      const read = await readRegular(target, { flush: true })
      if (intended) assert(read.bytes.equals(Buffer.from(intended)), `receipt does not match verified smoke: ${target}`)
      await syncDirectory(root, rootID)
      proof = { id: read.id, sha256: sha256(read.bytes) }
    } else {
      const destination = await archivePath(archive, runID, expected)
      const { archiveEntries, location } = destination
      target = destination.target
      const payload = path.join(target, "fixture")
      const manifest = path.join(target, "manifest.json")
      const intended = json({
        version: 1, run_id: runID, started_at: intent.started_at, fixture_root: source, passed: false,
        bytes: snapshot.bytes, files: contents(expected), original_evidence_sha256: finished.files["evidence.json"],
      })
      if (!retained && !await present(target)) {
        await privateDirectory(location.root, location.id)
        const free = await fs.statfs(location.root, { bigint: true })
        assert(free.bavail * free.bsize >= BigInt(snapshot.bytes + Buffer.byteLength(intended)),
          `insufficient space for smoke archive on ${location.root}`)
        if (!archive) await log("archive", location)
        await directory(location.root, location.id)
        await fs.mkdir(target, { mode: 0o700 })
        const targetID = await directory(target)
        archiveEntries([targetID])
        await checkpoint("archive-mkdir", target)
        await privateDirectory(target, targetID, "data")
        await copyTree(source, expected, payload)
        await directory(target, targetID)
        await writeNew(manifest, intended)
        await checkpoint("archive-manifest-written", target)
      } else assert(archive, `archive already exists without a pinned destination: ${target}`)
      const targetID = await privateDirectory(target, retained?.id, "data")
      archiveEntries([targetID])
      const names = (await fs.readdir(target)).sort()
      assert.deepEqual(names, ["fixture", "manifest.json"], `incomplete or unexpected archive entries: ${target}`)
      const archived = await readRegular(manifest)
      archiveEntries([archived.id])
      assert(archived.bytes.equals(Buffer.from(intended)), `archive manifest differs from intended fixture: ${target}`)
      assert.deepEqual(contents(await inventory(payload, undefined, { archiveEntries })), contents(expected),
        `archive contents differ from intended fixture: ${target}`)
      assert.deepEqual(await readRegular(manifest, { flush: true }), archived, `archive manifest changed during verification: ${target}`)
      await syncDirectory(target, targetID)
      await syncDirectory(location.root, location.id)
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

    await lock.check()
    if (rows.length) {
      // Recheck the complete remainder after verification of the destination,
      // before the first unlink; even a new unrelated entry is a refusal.
      assert.deepEqual(await inventory(source, allocated.id), rows, "source changed during retention")
      await lock.check()
      verifyStopped(source)
      await removeTree(source, rows, checkpoint)
    }
    assert(!await present(source), "owned scratch still exists")
    await checkpoint("scratch-removed", source)
    await clear()
    return pointer
  }

  let active = false
  const report = (error) => current ? diagnostic(`Smoke disposition refused: ${error.message}`, recoveryAction()) : error
  async function locked(operation) {
    assert(closeLock && !active, "smoke storage lock is closed or already in use")
    active = true
    try {
      await lock.check()
      return await operation()
    } catch (error) {
      throw report(error)
    } finally {
      active = false
    }
  }
  return {
    root,
    requireIdle: () => locked(requireIdle),
    begin: () => locked(begin),
    markRunning: () => locked(async () => {
      const { intent, allocated } = current.records
      await verifyRoot()
      await privateDirectory(intent.fixture_root, allocated.id, "data")
      await log("running", {})
    }),
    finish: (...args) => locked(async () => {
      await finish(...args)
      return await dispose()
    }),
    recover: () => locked(async () => {
      await refresh()
      if (!current) {
        console.log(`No pending smoke at ${pending}; nothing allocated or removed.`)
        return
      }
      return await dispose(true)
    }),
    async [Symbol.asyncDispose]() {
      assert(!active, "cannot close smoke storage during an operation")
      closeLock?.()
      closeLock = undefined
    },
  }
}

if (import.meta.main) {
  assert(process.argv.length === 2, "smoke recovery accepts no arguments or fixture paths")
  await using storage = await openSmokeStorage()
  await storage.recover()
}
