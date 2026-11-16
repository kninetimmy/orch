import assert from "node:assert/strict"
import { spawnSync } from "node:child_process"
import { createHash } from "node:crypto"
import { constants } from "node:fs"
import { access, copyFile, mkdir, readFile, readdir, realpath, stat, writeFile } from "node:fs/promises"
import path from "node:path"
import { pathToFileURL } from "node:url"

export function inside(root, target) {
  const relative = path.relative(root, target)
  return relative === "" || (!path.isAbsolute(relative) && relative !== ".." && !relative.startsWith(".." + path.sep))
}

async function canonical(value, label, type, root) {
  assert(typeof value === "string" && value.trim() && path.isAbsolute(value)
    && (process.platform !== "win32" || path.parse(value).root.length > 1), `${label} must be an explicit absolute ${type ?? "file or directory"} path; no fallback`)
  if (root) assert(inside(root, value), `${label} must stay within selected adapter root ${root}`)
  let resolved, info
  try {
    resolved = await realpath(value)
    if (root) {
      assert(inside(root, resolved), `${label} resolves outside selected adapter root ${root}`)
      // path.relative folds case on Windows, including case-sensitive volumes.
      // Verify the claimed ancestor by filesystem identity before reading bytes.
      const ancestor = path.resolve(resolved, ...path.relative(root, resolved).split(path.sep).filter(Boolean).map(() => ".."))
      const a = await stat(root, { bigint: true })
      const b = await stat(ancestor, { bigint: true })
      assert(a.isDirectory() && b.isDirectory() && a.dev === b.dev && a.ino === b.ino, `${label} resolves outside selected adapter root ${root}`)
    }
    info = await stat(resolved)
  } catch (error) {
    throw new Error(`${label}: cannot use ${value}: ${error.message}`, { cause: error })
  }
  assert(type === "directory" ? info.isDirectory() : type === "file" ? info.isFile() : info.isDirectory() || info.isFile(), `${label} must be a regular ${type ?? "file or directory"}: ${resolved}`)
  return resolved
}

export async function fileIdentity(file) {
  return { path: await realpath(file), sha256: createHash("sha256").update(await readFile(file)).digest("hex") }
}

async function executable(value, label) {
  const file = await canonical(value, label, "file")
  try {
    await access(file, constants.R_OK | constants.X_OK)
  } catch (error) {
    throw new Error(`${label} must be readable and executable: ${file}`, { cause: error })
  }
  return fileIdentity(file)
}

export async function adapterIdentity(directory) {
  const root = await canonical(directory, "ORCH_SMOKE_ADAPTER", "directory")
  async function file(relative) {
    const resolved = await canonical(path.join(root, relative), `adapter ${relative}`, "file", root)
    return { relative, ...await fileIdentity(resolved) }
  }
  async function tree(relative, ancestors = []) {
    const resolved = await canonical(path.join(root, relative), `adapter ${relative}`, undefined, root)
    if ((await stat(resolved)).isFile()) return [await file(relative)]
    assert(!ancestors.includes(resolved), `adapter ${relative}: cyclic directory link`)
    const entries = await readdir(resolved)
    const files = []
    for (const entry of entries.sort()) files.push(...await tree(path.join(relative, entry), [...ancestors, resolved]))
    return files
  }
  const manifest = await file("package.json")
  const lockfile = await file("package-lock.json")
  let pkg
  try {
    pkg = JSON.parse(await readFile(manifest.path, "utf8"))
    const lock = JSON.parse(await readFile(lockfile.path, "utf8"))
    assert(lock.name === pkg.name && lock.lockfileVersion, "package-lock.json must describe this adapter package")
  } catch (error) {
    throw new Error(`ORCH_SMOKE_ADAPTER needs a valid package.json and package-lock.json: ${error.message}`, { cause: error })
  }
  assert(pkg.name === "@kninetimmy/orch-opencode" && pkg.type === "module", "ORCH_SMOKE_ADAPTER must be the Orch ESM adapter package, not a source file or another plugin")
  const entry = typeof pkg.exports === "string" ? pkg.exports : pkg.exports?.["./server"] ?? pkg.exports?.["."]
  assert(typeof entry === "string" && entry.startsWith("./"), "ORCH_SMOKE_ADAPTER needs a local server export (exports string, '.' or './server')")
  const entrypoint = await file(entry)
  const sources = await tree("src")
  const skillFiles = await tree("skills")
  for (const name of ["orch-architect", "orch-delivery", "orch-setup"]) {
    assert(skillFiles.some((item) => item.relative === path.join("skills", name, "SKILL.md")), `ORCH_SMOKE_ADAPTER needs skills/${name}/SKILL.md`)
  }
  return {
    root, name: pkg.name, version: pkg.version, entrypoint, manifest, lockfile, sources, skill_files: skillFiles,
    skills_root: await canonical(path.join(root, "skills"), "adapter skills", "directory", root),
    identity_scope: "SHA-256 of manifest, lockfile, server entrypoint, src/** and skills/** only; the lockfile is not proof of the installed dependency tree.",
  }
}

export async function selectArtifacts(env, sourceAdapter) {
  const hasOrch = Object.hasOwn(env, "ORCH_SMOKE_ORCH")
  const hasAdapter = Object.hasOwn(env, "ORCH_SMOKE_ADAPTER")
  assert(hasOrch === hasAdapter, "Set ORCH_SMOKE_ORCH and ORCH_SMOKE_ADAPTER together, or unset both for source-build mode; no fallback")
  return {
    mode: hasOrch ? "explicit-artifact" : "source-build",
    orch: hasOrch ? await executable(env.ORCH_SMOKE_ORCH, "ORCH_SMOKE_ORCH") : undefined,
    memhub: await executable(env.ORCH_SMOKE_MEMHUB, "ORCH_SMOKE_MEMHUB (compatible explicit-root lookup-only Memhub required)"),
    adapter: await adapterIdentity(hasAdapter ? env.ORCH_SMOKE_ADAPTER : sourceAdapter),
  }
}

export function checkAdapterModule(adapter, cwd, env) {
  // Import only, never setup; use the fixture's isolated homes/caches even for
  // preflight. Missing dependencies or invalid modules must not reach Orch/host.
  const check = spawnSync(process.execPath, ["--input-type=module", "--eval", `
    const { default: plugin } = await import(process.argv[1])
    if (plugin?.id !== "orch.delivery" || typeof plugin.setup !== "function") {
      throw new Error("expected an orch.delivery server plugin with setup")
    }
  `, pathToFileURL(adapter.entrypoint.path).href], { cwd, env, encoding: "utf8", windowsHide: true, timeout: 30_000 })
  assert(!check.error && check.status === 0, `ORCH_SMOKE_ADAPTER is unusable; check its server export and install its existing dependencies before qualification (no fallback): ${check.error?.message ?? check.stderr}`)
}

export async function copyBinary(selected, destination) {
  await copyFile(selected.path, destination, constants.COPYFILE_EXCL)
  const executed = await fileIdentity(destination)
  assert.equal(executed.sha256, selected.sha256, `private binary copy differs from selected bytes: ${selected.path}`)
  return { kind: "byte-identical-private-copy", selected, executed, byte_identical: true }
}

export async function prepareOrch(selection, destination, build) {
  if (selection.mode === "explicit-artifact") return copyBinary(selection.orch, destination)
  await build(destination)
  return { kind: "source-build", executed: await fileIdentity(destination) }
}

export async function prepareSourceProbe(repository, directory) {
  const sourceMod = path.join(repository, "go.mod")
  const sourceSum = path.join(repository, "go.sum")
  const graph = await readFile(sourceMod, "utf8")
  assert(/^module github\.com\/kninetimmy\/orch\r?$/m.test(graph), "probe requires the existing Orch source module")
  // Retain the complete pinned graph, not just checksums or an ad-hoc require.
  // Only the disposable helper gets a different module name and local source link.
  const module = graph.replace(/^module github\.com\/kninetimmy\/orch\r?$/m, "module github.com/kninetimmy/orch/smoke")
    + `\nrequire github.com/kninetimmy/orch v0.0.0\nreplace github.com/kninetimmy/orch => ${JSON.stringify(repository.replaceAll("\\", "/"))}\n`
  await mkdir(directory)
  await writeFile(path.join(directory, "go.mod"), module, { flag: "wx" })
  await copyFile(sourceSum, path.join(directory, "go.sum"), constants.COPYFILE_EXCL)
  return {
    source_mod: await fileIdentity(sourceMod), source_sum: await fileIdentity(sourceSum),
    helper_mod: await fileIdentity(path.join(directory, "go.mod")), helper_sum: await fileIdentity(path.join(directory, "go.sum")),
  }
}

export async function verifyArtifacts(selection, orch, memhub) {
  const after = {
    adapter: await adapterIdentity(selection.adapter.root),
    memhub: await fileIdentity(selection.memhub.path),
    ...(selection.orch ? { orch: await fileIdentity(selection.orch.path) } : {}),
    executed_orch: await fileIdentity(orch.executed.path),
    executed_memhub: await fileIdentity(memhub.executed.path),
  }
  assert.deepEqual(after.adapter, selection.adapter, "selected adapter identity files changed during smoke")
  assert.deepEqual(after.memhub, selection.memhub, "selected Memhub changed during smoke")
  if (selection.orch) assert.deepEqual(after.orch, selection.orch, "selected Orch changed during smoke")
  assert.deepEqual(after.executed_orch, orch.executed, "executed Orch bytes changed")
  assert.deepEqual(after.executed_memhub, memhub.executed, "private Memhub bytes were not restored after negative tests")
  return { unchanged: true, ...after }
}
