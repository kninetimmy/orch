import assert from "node:assert/strict"
import { spawnSync } from "node:child_process"
import { createHash } from "node:crypto"
import { constants } from "node:fs"
import fs from "node:fs/promises"
import path from "node:path"
import { inside } from "./smoke-artifacts.mjs"

const windows = process.platform === "win32"
export const sha256 = (bytes) => createHash("sha256").update(bytes).digest("hex")
export const json = (data) => JSON.stringify(data, null, 2) + "\n"
const noFollow = constants.O_NOFOLLOW ?? 0

export async function present(file) {
  try {
    return await fs.lstat(file, { bigint: true })
  } catch (error) {
    if (error.code !== "ENOENT") throw error
  }
}

export function identity(info) {
  assert(info && info.ino !== 0n, "missing filesystem identity")
  return `${info.dev}:${info.ino}:${info.birthtimeNs}`
}

function component(name) {
  assert(name && name !== "." && name !== ".." && !/[/\x00]/.test(name) &&
    (!windows || (!/[\\:"<>|?*\x00-\x1f]|[. ]$/.test(name) &&
      !/^(?:con|prn|aux|nul|com[1-9¹²³]|lpt[1-9¹²³])(?:\.|$)/i.test(name))),
  `unsafe smoke path component: ${JSON.stringify(name)}`)
}

// Node lstat covers normal symlinks and junctions. Also reject Windows reparse
// attributes it may not expose, before walking any fixture or archive payload.
export function checkWindows(directory, tree = false) {
  if (!windows) return
  const result = spawnSync(path.join(process.env.SystemRoot, "System32", "WindowsPowerShell", "v1.0", "powershell.exe"),
    ["-NoProfile", "-NonInteractive", "-Command", `
$ErrorActionPreference = 'Stop'
function Check($p, $walk) {
  $a = [IO.File]::GetAttributes($p)
  if ($a -band [IO.FileAttributes]::ReparsePoint) { throw "Windows reparse point: $p" }
  if ($walk -and ($a -band [IO.FileAttributes]::Directory)) {
    foreach ($child in [IO.Directory]::EnumerateFileSystemEntries($p)) { Check $child $true }
  }
}
$p = $env:ORCH_SMOKE_INSPECT
$root = [IO.Path]::GetPathRoot($p)
if ([IO.DriveInfo]::new($root).DriveType -notin @('Fixed', 'Removable')) { throw "Not a local disk: $root" }
$ancestors = @()
for ($dir = $p; $dir -ne $root;) {
  $dir = [IO.Path]::GetDirectoryName($dir)
  $ancestors += ,$dir
}
for ($i = $ancestors.Length - 1; $i -ge 0; $i--) { Check $ancestors[$i] $false }
Check $p ($env:ORCH_SMOKE_INSPECT_TREE -eq '1')
`], {
      env: { ...process.env, ORCH_SMOKE_INSPECT: directory, ORCH_SMOKE_INSPECT_TREE: tree ? "1" : "0" },
      encoding: "utf8", windowsHide: true, timeout: 30_000,
    })
  assert(result.status === 0 && !result.error, `cannot verify local non-redirected path ${directory}: ${result.error?.message ?? result.stderr.trim()}`)
}

export async function directory(file, expected) {
  const info = await present(file)
  assert(info?.isDirectory() && !info.isSymbolicLink(), `missing, redirected or non-directory smoke path: ${file}`)
  const id = identity(info)
  assert(expected === undefined || id === expected, `directory ownership changed: ${file}`)
  return id
}

export async function externalDirectory(value) {
  assert(typeof value === "string" && path.isAbsolute(value) &&
    (!windows || /^[a-z]:[\\/]/i.test(value)), "smoke storage needs an absolute local directory path")
  const root = path.parse(value).root
  const names = value.slice(root.length).split(windows ? /[\\/]+/ : /\/+/).filter(Boolean)
  for (const name of names) component(name)
  const absolute = path.normalize(value)
  const normalized = absolute.length > root.length && absolute.endsWith(path.sep) ? absolute.slice(0, -1) : absolute
  // Do not trust a link in an ancestor even when its destination is a directory.
  for (let i = 0; i <= names.length; i++) {
    const at = path.join(root, ...names.slice(0, i))
    await directory(at)
  }
  checkWindows(normalized, false)
  const canonical = await fs.realpath(normalized)
  if (windows) {
    assert.equal(path.parse(canonical).root.toUpperCase(), path.parse(normalized).root.toUpperCase(),
      `redirected Windows drive: ${normalized}`)
  } else {
    const result = spawnSync("/bin/df", ["-lP", canonical], { encoding: "utf8", timeout: 30_000 })
    assert(result.status === 0 && !result.error && result.stdout.trim().split("\n").length > 1,
      `cannot verify local filesystem: ${canonical}`)
  }
  for (let at = canonical; ; at = path.dirname(at)) {
    assert(!await present(path.join(at, ".git")) &&
      !(await present(path.join(at, "HEAD")) && await present(path.join(at, "objects"))),
    `smoke storage must be outside Git checkouts and repositories: ${at}`)
    if (at === path.dirname(at)) break
  }
  await directory(canonical, await directory(normalized))
  return canonical
}

function regular(info, file, links = 1n) {
  assert(info?.isFile() && !info.isSymbolicLink() && info.nlink === links, `missing, externally linked or non-regular smoke file: ${file}`)
  return identity(info)
}

async function withFile(file, flags, use, links) {
  const id = regular(await present(file), file, links)
  const handle = await fs.open(file, flags | noFollow)
  try {
    const before = await handle.stat({ bigint: true })
    assert.equal(regular(before, file, links), id, `file ownership changed: ${file}`)
    const value = await use(handle, before)
    const after = await present(file)
    assert.equal(regular(after, file, links), id, `file ownership changed: ${file}`)
    if (flags === constants.O_RDONLY) {
      assert(after.size === before.size && after.mtimeNs === before.mtimeNs, `file changed while reading: ${file}`)
    }
    return value
  } finally {
    await handle.close()
  }
}

export const readRegular = (file) => withFile(file, constants.O_RDONLY, async (handle, info) => ({
  bytes: await handle.readFile(), id: identity(info),
}))

export async function syncDirectory(dir) {
  // Node cannot open a Windows directory for fsync. Files themselves are flushed
  // on both platforms; Windows recovery is for process interruption, not power loss.
  if (windows) return
  const handle = await fs.open(dir, constants.O_RDONLY | constants.O_DIRECTORY | noFollow)
  try {
    await handle.sync()
  } finally {
    await handle.close()
  }
}

export async function writeNew(file, bytes) {
  await fs.writeFile(file, bytes, { flag: "wx", mode: 0o600, flush: true })
  await syncDirectory(path.dirname(file))
}

export async function append(file, previous, bytes) {
  assert.deepEqual(await readRegular(file), previous, `pending journal changed: ${file}`)
  await withFile(file, constants.O_WRONLY | constants.O_APPEND, async (handle, info) => {
    assert(identity(info) === previous.id && info.size === BigInt(previous.bytes.length), `pending journal changed: ${file}`)
    await handle.writeFile(bytes)
    await handle.sync()
  })
}

export async function inventory(root, rootID) {
  const id = await directory(root, rootID)
  checkWindows(root, true)
  if (process.platform === "linux") {
    // stat.dev alone misses a bind mount of another directory on the same disk.
    const mounts = (await fs.readFile("/proc/self/mountinfo", "utf8")).trimEnd().split("\n")
    for (const line of mounts) {
      const mount = line.split(" ")[4].replace(/\\([0-7]{3})/g, (_, octal) => String.fromCharCode(parseInt(octal, 8)))
      assert(!inside(root, mount), `mount inside smoke tree: ${mount}`)
    }
  }
  const rows = []
  async function walk(file, name) {
    const info = await present(file)
    assert(info && !info.isSymbolicLink() && String(info.dev) === id.split(":")[0],
      `missing, redirected or off-volume smoke entry: ${file}`)
    if (info.isDirectory()) {
      const dirID = identity(info)
      rows.push({ name, id: dirID })
      for (const child of (await fs.readdir(file)).sort()) {
        component(child)
        await directory(file, dirID)
        await walk(path.join(file, child), name ? name + "/" + child : child)
      }
      await directory(file, dirID)
    } else {
      assert(info.isFile(), `non-regular smoke entry: ${file}`)
      rows.push({ name, id: identity(info), bytes: Number(info.size), mtime: String(info.mtimeNs) })
    }
  }
  await walk(root, "")
  assert.equal(rows[0].id, id, `fixture ownership changed: ${root}`)
  const byName = new Map(rows.map((row) => [row.name, row]))
  const links = linkCounts(rows)
  const hashes = new Map()
  for (const row of rows.filter((item) => item.bytes !== undefined)) {
    await parents(root, byName, row.name)
    const file = member(root, row.name)
    row.sha256 = await withFile(file, constants.O_RDONLY, async (handle, current) => {
      assert(identity(current) === row.id && current.size === BigInt(row.bytes) && String(current.mtimeNs) === row.mtime,
        `file changed during inventory: ${file}`)
      if (!hashes.has(row.id)) {
        const hasher = createHash("sha256")
        for await (const chunk of handle.createReadStream({ autoClose: false })) hasher.update(chunk)
        hashes.set(row.id, hasher.digest("hex"))
      }
      return hashes.get(row.id)
    }, links.get(row.id))
  }
  return rows
}

// OpenCode Windows creates a service executable hard link in the private cache.
// A file is owned only if all its filesystem link names were found in this tree.
function linkCounts(rows) {
  const count = new Map()
  for (const row of rows) {
    if (row.bytes !== undefined) count.set(row.id, (count.get(row.id) ?? 0n) + 1n)
  }
  return count
}

export function contents(rows) {
  return rows.map(({ name, bytes, sha256 }) => bytes === undefined ? { name } : { name, bytes, sha256 })
}

export function treeBytes(rows) {
  return rows.reduce((sum, row) => sum + (row.bytes ?? 0), 0)
}

// Journal paths only become actionable after matching an observed source entry.
function member(root, name) {
  if (name === "") return root
  const parts = name.split("/")
  for (const part of parts) component(part)
  return path.join(root, ...parts)
}

async function parents(root, byName, name) {
  while (name) {
    name = name.slice(0, Math.max(0, name.lastIndexOf("/")))
    const row = byName.get(name)
    assert(row && row.bytes === undefined, "unverified smoke parent")
    await directory(member(root, name), row.id)
  }
}

export async function copyTree(source, rows, destination) {
  await fs.mkdir(destination, { mode: 0o700 })
  const originals = new Map(rows.map((row) => [row.name, row]))
  const links = linkCounts(rows)
  const created = new Map()
  for (const row of rows) {
    await parents(source, originals, row.name)
    await parents(destination, created, row.name)
    const from = member(source, row.name)
    const to = member(destination, row.name)
    if (row.bytes === undefined) {
      await directory(from, row.id)
      if (row.name) await fs.mkdir(to, { mode: 0o700 })
      created.set(row.name, { id: await directory(to) })
    } else {
      await withFile(from, constants.O_RDONLY, async (input, info) => {
        assert.equal(identity(info), row.id, `source ownership changed: ${from}`)
        const output = await fs.open(to, "wx", 0o600)
        try {
          for await (const chunk of input.createReadStream({ autoClose: false })) await output.writeFile(chunk)
          await output.sync()
        } finally {
          await output.close()
        }
      }, links.get(row.id))
    }
  }
  for (const [name, { id }] of [...created].reverse()) {
    const dir = member(destination, name)
    await directory(dir, id)
    await syncDirectory(dir)
  }
}

export async function removeTree(root, remaining, checkpoint) {
  const byName = new Map(remaining.map((row) => [row.name, row]))
  const links = linkCounts(remaining)
  // No recursive rm: an unknown entry cannot be removed even if it appears
  // between inventory and rmdir. Recheck parents and each identity before use.
  for (const row of [...remaining].reverse()) {
    await parents(root, byName, row.name)
    const file = member(root, row.name)
    if (row.bytes === undefined) {
      await directory(file, row.id)
      await fs.rmdir(file)
    } else {
      const info = await present(file)
      const count = links.get(row.id)
      assert(regular(info, file, count) === row.id && info.size === BigInt(row.bytes) && String(info.mtimeNs) === row.mtime,
        `source entry changed before removal: ${file}`)
      await fs.unlink(file)
      links.set(row.id, count - 1n)
    }
    await checkpoint("removed-entry", file)
  }
  await syncDirectory(path.dirname(root))
}
