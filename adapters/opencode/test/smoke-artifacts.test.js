import assert from "node:assert/strict"
import { spawnSync } from "node:child_process"
import { chmod, mkdir, mkdtemp, readFile, realpath, rm, symlink, writeFile } from "node:fs/promises"
import { tmpdir } from "node:os"
import path from "node:path"
import { fileURLToPath } from "node:url"
import test from "node:test"

import { adapterIdentity, checkAdapterModule, copyBinary, fileIdentity, prepareOrch, prepareSourceProbe, selectArtifacts, verifyArtifacts } from "../smoke-artifacts.mjs"

async function fixture(t) {
  const root = await realpath(await mkdtemp(path.join(tmpdir(), "orch-smoke-artifacts-")))
  t.after(() => rm(root, { recursive: true }))
  const source = path.join(root, "source")
  const installed = path.join(root, "installed")
  for (const directory of [source, installed]) {
    await mkdir(path.join(directory, "src"), { recursive: true })
    await writeFile(path.join(directory, "package.json"), JSON.stringify({
      name: "@kninetimmy/orch-opencode", type: "module", version: "fixture", exports: "./src/index.js",
    }))
    await writeFile(path.join(directory, "package-lock.json"), JSON.stringify({ name: "@kninetimmy/orch-opencode", lockfileVersion: 3 }))
    await writeFile(path.join(directory, "src", "index.js"), `export default {id: "orch.delivery", setup() {}} // ${path.basename(directory)}\n`)
    for (const name of ["orch-architect", "orch-delivery", "orch-setup"]) {
      await mkdir(path.join(directory, "skills", name), { recursive: true })
      await writeFile(path.join(directory, "skills", name, "SKILL.md"), `---\nname: ${name}\ndescription: Fixture only\n---\n${name}\n`)
    }
  }
  const orch = path.join(root, "candidate-orch")
  const memhub = path.join(root, "candidate-memhub")
  for (const file of [orch, memhub]) {
    await writeFile(file, path.basename(file))
    await chmod(file, 0o755)
  }
  return { root, source, installed, orch, memhub, env: { ORCH_SMOKE_ORCH: orch, ORCH_SMOKE_ADAPTER: installed, ORCH_SMOKE_MEMHUB: memhub } }
}

test("unset selectors retain source-build mode; explicit selectors choose only external artifacts", async (t) => {
  const f = await fixture(t)
  const source = await selectArtifacts({ ORCH_SMOKE_MEMHUB: f.memhub }, f.source)
  assert.equal(source.mode, "source-build")
  assert.equal(source.orch, undefined)
  assert.equal(source.adapter.root, f.source)
  const alias = path.join(f.root, "package-alias")
  await symlink(f.installed, alias, process.platform === "win32" ? "junction" : "dir")
  const explicit = await selectArtifacts({ ...f.env, ORCH_SMOKE_ADAPTER: alias }, path.join(f.root, "nonexistent-checkout"))
  assert.equal(explicit.mode, "explicit-artifact")
  assert.equal(explicit.adapter.root, f.installed)
  assert.deepEqual(explicit.orch, await fileIdentity(f.orch))
  assert.notEqual(explicit.adapter.entrypoint.sha256, source.adapter.entrypoint.sha256)
  assert.equal(explicit.adapter.skill_files.length, 3)
  assert.match(explicit.adapter.identity_scope, /not proof of the installed dependency tree/)
})

test("selector validation rejects incomplete, empty, relative, missing and wrong-type paths without fallback", async (t) => {
  const f = await fixture(t)
  const invalid = [
    [{ ORCH_SMOKE_ORCH: f.orch }, /together/],
    [{ ORCH_SMOKE_ADAPTER: f.installed }, /together/],
    [{ ...f.env, ORCH_SMOKE_ORCH: "" }, /ORCH_SMOKE_ORCH.*absolute/],
    [{ ...f.env, ORCH_SMOKE_ADAPTER: "" }, /ORCH_SMOKE_ADAPTER.*absolute/],
    [{ ...f.env, ORCH_SMOKE_ADAPTER: "  " }, /ORCH_SMOKE_ADAPTER.*absolute/],
    [{ ...f.env, ORCH_SMOKE_ORCH: "orch" }, /ORCH_SMOKE_ORCH.*absolute/],
    [{ ...f.env, ORCH_SMOKE_ADAPTER: "adapters/opencode" }, /ORCH_SMOKE_ADAPTER.*absolute/],
    [{ ...f.env, ORCH_SMOKE_ORCH: path.join(f.root, "missing") }, /ORCH_SMOKE_ORCH.*cannot use/],
    [{ ...f.env, ORCH_SMOKE_ADAPTER: path.join(f.root, "missing") }, /ORCH_SMOKE_ADAPTER.*cannot use/],
    [{ ...f.env, ORCH_SMOKE_ORCH: f.installed }, /ORCH_SMOKE_ORCH.*regular file/],
    [{ ...f.env, ORCH_SMOKE_ADAPTER: f.orch }, /ORCH_SMOKE_ADAPTER.*regular directory/],
    [{ ...f.env, ORCH_SMOKE_MEMHUB: "" }, /ORCH_SMOKE_MEMHUB.*absolute/],
    [{ ...f.env, ORCH_SMOKE_MEMHUB: "memhub" }, /ORCH_SMOKE_MEMHUB.*absolute/],
    [{ ...f.env, ORCH_SMOKE_MEMHUB: f.installed }, /ORCH_SMOKE_MEMHUB.*regular file/],
    [{ ...f.env, ORCH_SMOKE_MEMHUB: path.join(f.root, "missing") }, /ORCH_SMOKE_MEMHUB.*cannot use/],
    [{}, /ORCH_SMOKE_MEMHUB.*absolute/],
  ]
  if (process.platform === "win32") {
    invalid.push([{ ...f.env, ORCH_SMOKE_ORCH: "\\drive-relative\\orch.exe" }, /ORCH_SMOKE_ORCH.*absolute/])
    invalid.push([{ ...f.env, ORCH_SMOKE_ADAPTER: "/drive-relative/adapter" }, /ORCH_SMOKE_ADAPTER.*absolute/])
  }
  for (const [env, error] of invalid) await assert.rejects(selectArtifacts(env, f.source), error)
})

test("invalid selections fail the real harness before host discovery or candidate execution", async (t) => {
  const f = await fixture(t)
  const harness = fileURLToPath(new URL("../smoke.mjs", import.meta.url))
  const env = { ...process.env, ...f.env, TMPDIR: f.root, TEMP: f.root, TMP: f.root, PATH: "", ORCH_SMOKE_ADAPTER: f.orch }
  const check = spawnSync(process.execPath, [harness], { env, encoding: "utf8", timeout: 30_000, windowsHide: true })
  assert.notEqual(check.status, 0)
  assert.match(check.stderr, /ORCH_SMOKE_ADAPTER.*regular directory/)
  assert.doesNotMatch(check.stderr, /No supported OpenCode|ENOENT.*spawn/)
  assert.doesNotMatch(check.stdout, /Isolated smoke|PASS/)
})

test("real smoke exits nonzero after archiving an isolated module preflight failure", async (t) => {
  const f = await fixture(t)
  const harness = fileURLToPath(new URL("../smoke.mjs", import.meta.url))
  const archive = path.join(f.root, "archive")
  await mkdir(archive)
  await writeFile(path.join(f.installed, "src", "index.js"), "export default {}")
  const env = {
    ...process.env, ...f.env, TMPDIR: f.root, TEMP: f.root, TMP: f.root, ORCH_SMOKE_ARCHIVE_ROOT: archive,
  }
  const check = spawnSync(process.execPath, [harness], { env, encoding: "utf8", timeout: 60_000, windowsHide: true })
  assert.equal(check.status, 1, check.stderr)
  assert.match(check.stderr, /ORCH_SMOKE_ADAPTER is unusable/)
  const line = check.stdout.split(/\r?\n/).find((line) => line.startsWith("SMOKE_EVIDENCE "))
  assert(line, check.stdout)
  const pointer = JSON.parse(line.slice("SMOKE_EVIDENCE ".length))
  assert.equal(pointer.kind, "failure-archive")
  assert.equal(path.dirname(pointer.path), archive)
  const manifest = await fileIdentity(path.join(pointer.path, "manifest.json"))
  assert.equal(manifest.sha256, pointer.sha256)
  const evidence = JSON.parse(await readFile(path.join(pointer.path, "fixture", "evidence.json"), "utf8"))
  assert.equal(evidence.passed, false)
  assert.equal(evidence.provider_requests, 0)
  assert.equal(evidence.owned_shutdown.method, "no private host started")
  assert.equal(evidence.owned_shutdown.verified, true)
  await assert.rejects(readFile(path.join(f.root, "orch-opencode-smoke", "pending.jsonl")), { code: "ENOENT" })
  await assert.rejects(realpath(evidence.fixture_root), { code: "ENOENT" })
})

test("unusable manifests, lockfiles, exports and missing skills fail package preflight", async (t) => {
  const f = await fixture(t)
  const manifest = path.join(f.installed, "package.json")
  const original = await readFile(manifest)
  await writeFile(manifest, "{")
  await assert.rejects(adapterIdentity(f.installed), /valid package.json/)
  await writeFile(manifest, JSON.stringify({ name: "another-plugin", type: "module" }))
  await assert.rejects(adapterIdentity(f.installed), /Orch ESM adapter|package-lock.json/)
  await writeFile(manifest, JSON.stringify({ ...JSON.parse(original), exports: "../outside.js" }))
  await assert.rejects(adapterIdentity(f.installed), /local server export/)
  await writeFile(manifest, JSON.stringify({ ...JSON.parse(original), exports: "./missing.js" }))
  await assert.rejects(adapterIdentity(f.installed), /adapter .*missing.js.*cannot use/)
  await writeFile(manifest, original)
  const lockfile = path.join(f.installed, "package-lock.json")
  await writeFile(lockfile, "null")
  await assert.rejects(adapterIdentity(f.installed), /valid package.json and package-lock.json/)
  await writeFile(lockfile, JSON.stringify({ name: "@kninetimmy/orch-opencode", lockfileVersion: 3 }))
  await rm(path.join(f.installed, "skills", "orch-setup", "SKILL.md"))
  await assert.rejects(adapterIdentity(f.installed), /needs skills\/orch-setup\/SKILL.md/)
})

test("identity reads reject escaping exports, directory links and internal link cycles", async (t) => {
  const f = await fixture(t)
  const manifest = path.join(f.installed, "package.json")
  const original = await readFile(manifest)
  await writeFile(manifest, JSON.stringify({ ...JSON.parse(original), exports: "./../source/src/index.js" }))
  await assert.rejects(adapterIdentity(f.installed), /within selected adapter root/)
  await writeFile(manifest, original)
  const type = process.platform === "win32" ? "junction" : "dir"
  for (const directory of ["src", "skills"]) {
    const alias = path.join(f.installed, directory, "escape")
    await symlink(f.source, alias, type)
    await assert.rejects(adapterIdentity(f.installed), /outside selected adapter root/)
    await rm(alias)
  }
  await symlink(path.join(f.installed, "src"), path.join(f.installed, "src", "loop"), type)
  await assert.rejects(adapterIdentity(f.installed), /cyclic directory link/)
})

test("module preflight catches missing dependencies, syntax and wrong plugin exports without setup", async (t) => {
  const f = await fixture(t)
  const adapter = await adapterIdentity(f.installed)
  const env = { HOME: f.root, USERPROFILE: f.root, XDG_CACHE_HOME: f.root }
  const entry = adapter.entrypoint.path
  await writeFile(entry, 'export default {id: "orch.delivery", setup() { throw new Error("must not run setup") }}')
  checkAdapterModule(adapter, f.root, env)
  for (const content of [
    'import "orch-smoke-deliberately-missing-dependency"; export default {}',
    "invalid javascript !!!",
    'export default {id: "another-plugin", setup() {}}',
  ]) {
    await writeFile(entry, content)
    assert.throws(() => checkAdapterModule(adapter, f.root, env), /ORCH_SMOKE_ADAPTER is unusable.*no fallback/)
  }
})

test("explicit mode never builds Orch; source mode builds exactly its private output", async (t) => {
  const f = await fixture(t)
  const explicit = await selectArtifacts(f.env, f.source)
  const destination = path.join(f.root, "private-orch")
  const copy = await prepareOrch(explicit, destination, () => assert.fail("explicit mode must never build Orch"))
  assert.equal(copy.kind, "byte-identical-private-copy")
  assert.equal(copy.byte_identical, true)
  assert.deepEqual(copy.selected, explicit.orch)
  assert.equal(copy.executed.path, destination)
  assert.equal(copy.executed.sha256, explicit.orch.sha256)
  const source = await selectArtifacts({ ORCH_SMOKE_MEMHUB: f.memhub }, f.source)
  let builds = 0
  const built = await prepareOrch(source, path.join(f.root, "built-orch"), async (output) => {
    builds++
    await writeFile(output, "source-built bytes")
  })
  assert.equal(builds, 1)
  assert.equal(built.kind, "source-build")
  assert.equal(built.selected, undefined)
  assert.notEqual(built.executed.sha256, copy.executed.sha256)
})

test("byte-copy verification refuses changed candidate bytes, not just version labels", async (t) => {
  const f = await fixture(t)
  const selected = await fileIdentity(f.orch)
  await writeFile(f.orch, "different bytes, same label")
  await assert.rejects(copyBinary(selected, path.join(f.root, "private-orch")), /copy differs from selected bytes/)
})

test("source probe preserves the full existing graph and exact checksums without editing the checkout", async (t) => {
  const f = await fixture(t)
  const repository = path.resolve(fileURLToPath(new URL("../../..", import.meta.url)))
  const before = await Promise.all(["go.mod", "go.sum"].map((file) => fileIdentity(path.join(repository, file))))
  const directory = path.join(f.root, "probe")
  const graph = await prepareSourceProbe(repository, directory)
  assert.deepEqual([graph.source_mod, graph.source_sum], before)
  assert.equal(graph.helper_sum.sha256, graph.source_sum.sha256)
  const original = await readFile(graph.source_mod.path, "utf8")
  const helper = await readFile(graph.helper_mod.path, "utf8")
  assert.equal(helper, original.replace(/^module github\.com\/kninetimmy\/orch\r?$/m, "module github.com/kninetimmy/orch/smoke")
    + `\nrequire github.com/kninetimmy/orch v0.0.0\nreplace github.com/kninetimmy/orch => ${JSON.stringify(repository.replaceAll("\\", "/"))}\n`)
  assert.deepEqual(await Promise.all(before.map((file) => fileIdentity(file.path))), before)
  await assert.rejects(prepareSourceProbe(repository, directory), /EEXIST/)
})

test("evidence verifies selected payloads and restored private copies before claiming unchanged", async (t) => {
  const f = await fixture(t)
  const selected = await selectArtifacts(f.env, f.source)
  const orch = await prepareOrch(selected, path.join(f.root, "private-orch"), () => assert.fail("no build"))
  const memhub = await copyBinary(selected.memhub, path.join(f.root, "private-memhub"))
  const evidence = await verifyArtifacts(selected, orch, memhub)
  assert.equal(evidence.unchanged, true)
  assert.equal(evidence.orch.path, f.orch)
  assert.equal(evidence.executed_orch.path, orch.executed.path)
  assert.equal(evidence.memhub.sha256, evidence.executed_memhub.sha256)
  for (const [file, error] of [[f.orch, /selected Orch changed/], [f.memhub, /selected Memhub changed/]]) {
    const bytes = await readFile(file)
    await writeFile(file, "changed selected executable")
    await assert.rejects(verifyArtifacts(selected, orch, memhub), error)
    await writeFile(file, bytes)
  }
  await writeFile(memhub.executed.path, "synthetic legacy binary")
  await assert.rejects(verifyArtifacts(selected, orch, memhub), /private Memhub bytes were not restored/)
  await writeFile(memhub.executed.path, await readFile(f.memhub))
  await writeFile(path.join(f.installed, "src", "index.js"), "changed without changing package version")
  await assert.rejects(verifyArtifacts(selected, orch, memhub), /selected adapter identity files changed/)
})
