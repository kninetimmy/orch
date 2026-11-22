import assert from "node:assert/strict"
import { spawnSync } from "node:child_process"
import { createHash } from "node:crypto"
import { constants } from "node:fs"
import fs from "node:fs/promises"
import path from "node:path"
import { inside } from "./smoke-artifacts.mjs"
import { hasResourceFork } from "./smoke-native.mjs"

const windows = process.platform === "win32"
const mac = process.platform === "darwin"
const linuxArchiveTypes = new Set(["ext2", "ext3", "ext4", "xfs", "btrfs", "tmpfs", "f2fs"])
export const sha256 = (bytes) => createHash("sha256").update(bytes).digest("hex")
export const json = (data) => JSON.stringify(data, null, 2) + "\n"
const noFollow = constants.O_NOFOLLOW ?? 0

function checkMacData(file) {
  if (!mac) return
  let present
  try { present = hasResourceFork(file) } catch (error) {
    throw new Error(`cannot verify macOS resource fork: ${file}`, { cause: error })
  }
  assert(!present, `macOS resource fork on ${file}`)
}

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

const device = (id) => id.split(":")[0]
function physicalID(id) {
  const match = /^(\d+:[1-9]\d*):-?\d+$/.exec(id)
  assert(match, "missing filesystem identity for archive separation")
  return match[1]
}

function component(name) {
  assert(name && name !== "." && name !== ".." && !/[/\x00]/.test(name) &&
    (!windows || (!/[\\:"<>|?*\x00-\x1f]|[. ]$/.test(name) &&
      !/^(?:con|prn|aux|nul|com[1-9¹²³]|lpt[1-9¹²³])(?:\.|$)/i.test(name))),
  `unsafe smoke path component: ${JSON.stringify(name)}`)
}

// Node does not expose all reparse tags or named data streams. Windows
// PowerShell's file cmdlets cannot enumerate directory streams on all versions.
export function checkWindows(file, mode = "path", privateEntry = true) {
  if (!windows) return
  const result = spawnSync(path.join(process.env.SystemRoot, "System32", "WindowsPowerShell", "v1.0", "powershell.exe"),
    ["-NoProfile", "-NonInteractive", "-Command", String.raw`
$ErrorActionPreference = 'Stop'
$mode = $env:ORCH_SMOKE_INSPECT_MODE
$privateEntry = $env:ORCH_SMOKE_INSPECT_PRIVATE -eq '1'
$user = [Security.Principal.WindowsIdentity]::GetCurrent().User.Value
# SYSTEM, built-in Administrators and the Windows servicing account.
$trusted = @($user, 'S-1-5-18', 'S-1-5-32-544', 'S-1-5-80-956008885-3418522649-1831038044-1853292631-2271478464')
if ($mode -ne 'path') {
  Add-Type -TypeDefinition @'
using System;
using System.IO;
using System.Runtime.InteropServices;
public static class SmokeStreams {
  [StructLayout(LayoutKind.Sequential, CharSet = CharSet.Unicode)]
  private struct StreamData {
    public long Size;
    [MarshalAs(UnmanagedType.ByValTStr, SizeConst = 296)] // MAX_PATH + 36
    public string Name;
  }
  [DllImport("kernel32.dll", CharSet = CharSet.Unicode, ExactSpelling = true, SetLastError = true)]
  private static extern IntPtr FindFirstStreamW(string path, int level, out StreamData data, int flags);
  [DllImport("kernel32.dll", ExactSpelling = true, SetLastError = true)]
  private static extern bool FindNextStreamW(IntPtr handle, out StreamData data);
  [DllImport("kernel32.dll", ExactSpelling = true)]
  private static extern bool FindClose(IntPtr handle);
  public static void Check(string path) {
    StreamData data;
    IntPtr handle = FindFirstStreamW(@"\\?\" + path, 0, out data, 0);
    int error;
    if (handle == new IntPtr(-1)) {
      error = Marshal.GetLastWin32Error();
    } else {
      try {
        do {
          if (data.Name != "::$DATA") throw new IOException("Windows named data stream on " + path + ": " + data.Name);
        } while (FindNextStreamW(handle, out data));
        error = Marshal.GetLastWin32Error();
      } finally {
        FindClose(handle);
      }
    }
    const int ERROR_HANDLE_EOF = 38;
    if (error != ERROR_HANDLE_EOF) throw new IOException("Cannot enumerate Windows data streams on " + path + ": Win32 error " + error);
  }
}
'@
}
function CheckAccess($p, $isDirectory, $private) {
  $sections = [Security.AccessControl.AccessControlSections]'Access, Owner'
  $acl = if ($isDirectory) { [IO.Directory]::GetAccessControl($p, $sections) }
         else { [IO.File]::GetAccessControl($p, $sections) }
  $sd = [Security.AccessControl.RawSecurityDescriptor]::new($acl.GetSecurityDescriptorBinaryForm(), 0)
  if ($sd.Owner.Value -notin $trusted) {
    throw "Unsafe Windows smoke owner: $p"
  }
  if (-not ($sd.ControlFlags -band [Security.AccessControl.ControlFlags]::DiscretionaryAclPresent) -or
      $null -eq $sd.DiscretionaryAcl) { throw "Cannot verify Windows smoke access list: $p" }

  # Ancestors may grant read/traverse and creation of new entries. Not deletion of
  # an existing child, redirection of a directory, or changes to access control.
  $allowed = 0xa01200af # GENERIC_READ/EXECUTE and their mapped rights, plus ADD_FILE/ADD_SUBDIRECTORY
  # A local volume root itself cannot be deleted/replaced. It may grant modify
  # rights on the root, but not DELETE_CHILD or security descriptor writes.
  if ($p -eq $root) { $allowed = $allowed -bor 0x40010116 } # GENERIC_WRITE and its mapped rights, plus DELETE
  foreach ($ace in $sd.DiscretionaryAcl) {
    $inheritOnly = [int]$ace.AceFlags -band [int][Security.AccessControl.AceFlags]::InheritOnly
    if ($inheritOnly -and -not $private) { continue }
    if ($ace -isnot [Security.AccessControl.CommonAce] -or $ace.IsCallback -or
        $ace.AceType -notin @('AccessAllowed', 'AccessDenied')) { throw "Cannot verify Windows smoke access entry: $p" }
    if ($ace.AceType -eq 'AccessDenied' -or $ace.SecurityIdentifier.Value -in $trusted -or
        ($inheritOnly -and $ace.SecurityIdentifier.Value -eq 'S-1-3-0')) { continue }
    if ($private -or ($ace.AccessMask -band (-bnot $allowed))) { throw "Unsafe Windows smoke permissions: $p" }
  }
}
function Check($p, $data, $walk, $private) {
  $a = [IO.File]::GetAttributes($p)
  if ($a -band [IO.FileAttributes]::ReparsePoint) { throw "Windows reparse point: $p" }
  $isDirectory = ($a -band [IO.FileAttributes]::Directory) -ne 0
  CheckAccess $p $isDirectory $private
  if ($data) { [SmokeStreams]::Check($p) }
  if ($walk -and $isDirectory) {
    foreach ($child in [IO.Directory]::EnumerateFileSystemEntries($p)) { Check $child $true $true $private }
  }
}
$p = $env:ORCH_SMOKE_INSPECT
$root = [IO.Path]::GetPathRoot($p)
$drive = [IO.DriveInfo]::new($root)
if ($drive.DriveType -notin @('Fixed', 'Removable') -or $drive.DriveFormat -notin @('NTFS', 'ReFS')) {
  throw "Not a local Windows disk with verifiable access control: $root"
}
$ancestors = @()
for ($dir = $p; $dir -ne $root;) {
  $dir = [IO.Path]::GetDirectoryName($dir)
  $ancestors += ,$dir
}
for ($i = $ancestors.Length - 1; $i -ge 0; $i--) { Check $ancestors[$i] $false $false $false }
Check $p ($mode -ne 'path') ($mode -eq 'tree') $privateEntry
`], {
      env: { ...process.env, ORCH_SMOKE_INSPECT: file, ORCH_SMOKE_INSPECT_MODE: mode, ORCH_SMOKE_INSPECT_PRIVATE: privateEntry ? "1" : "0" },
      encoding: "utf8", windowsHide: true, timeout: 30_000,
    })
  assert(result.status === 0 && !result.error, `cannot verify Windows smoke path ${file}: ${result.error?.message ?? result.stderr.trim()}`)
}

function directoryID(info, file, expected) {
  assert(info?.isDirectory() && !info.isSymbolicLink(), `missing, redirected or non-directory smoke path: ${file}`)
  const id = identity(info)
  assert(expected === undefined || id === expected, `directory ownership changed: ${file}`)
  return id
}

export async function directory(file, expected) {
  return directoryID(await present(file), file, expected)
}

function checkPosixAccess(info, file, privateEntry) {
  assert((mac || process.platform === "linux") && typeof process.geteuid === "function", "cannot verify POSIX smoke permissions on this platform")
  assert(info.uid === BigInt(process.geteuid()) || (!privateEntry && info.uid === 0n), `unsafe smoke path owner: ${file}`)
  // The sticky bit protects an owned child in a shared temp directory. Linux
  // POSIX ACL grants to named users/groups are bounded by the group mode bits.
  assert(privateEntry ? (info.mode & 0o077n) === 0n : (info.mode & 0o022n) === 0n || (info.mode & 0o1000n) !== 0n,
    `unsafe smoke path permissions: ${file}`)
  if (mac) {
    // macOS ACLs can grant access independently of the mode. Accept only no
    // ACL or deny-only ACLs. Use numeric owners/ACL UUIDs and escape filenames.
    const result = spawnSync("/bin/ls", ["-blden", file], { env: { LC_ALL: "C" }, encoding: "utf8", timeout: 30_000 })
    const [entry, ...acl] = result.stdout?.trimEnd().split("\n") ?? []
    assert(result.status === 0 && !result.error && !result.stderr && /^[d-][rwxstST-]{9}[ +@]/.test(entry) &&
      (entry[10] !== "+" || acl.length > 0) &&
      acl.every((line) => /^\s+\d+: (?:[0-9a-f-]{36}|(?:user|group):\S+) (?:inherited )?deny \S+$/i.test(line)),
    `cannot verify private macOS smoke access (unexpected or granting ACL): ${file}`)
  }
}

async function checkLocalPosix(file) {
  const options = { env: { LC_ALL: "C" }, encoding: "utf8", timeout: 30_000 }
  const df = spawnSync("/bin/df", ["-lP", file], options)
  const rows = df.stdout?.trimEnd().split("\n")
  assert(df.status === 0 && !df.error && !df.stderr && rows?.length === 2, `cannot verify local filesystem: ${file}`)
  if (mac) {
    // The macOS noowners mount option can report another account's files as ours.
    const volume = /^(.+?)\s+\d+\s+\d+\s+-?\d+\s+-?\d+%\s+(.+)$/.exec(rows[1])
    const mounts = spawnSync("/sbin/mount", [], options)
    const prefix = volume && `${volume[1]} on ${volume[2]} (`
    const matching = mounts.stdout?.split("\n").filter((line) => prefix && line.startsWith(prefix) && line.endsWith(")"))
    const flags = matching?.length === 1 && matching[0].slice(prefix.length, -1).split(", ")
    assert(mounts.status === 0 && !mounts.error && !mounts.stderr &&
      flags && ["apfs", "hfs"].includes(flags[0]) && flags.includes("local") && !flags.includes("noowners"),
    `cannot verify macOS ownership enforcement on local APFS/HFS: ${file}`)
  } else {
    const { type } = await fs.statfs(file, { bigint: true })
    // Linux kernel filesystems with POSIX ownership/modes. Do not trust
    // synthesized ownership on incompatible or userspace filesystems.
    assert([0xef53n, 0x58465342n, 0x9123683en, 0x01021994n, 0x794c7630n, 0xf2f52010n].includes(type),
      `cannot verify Linux ownership enforcement (expected ext, XFS, btrfs, tmpfs, overlayfs or f2fs): ${file}`)
  }
}

export async function privateDirectory(file, expected, mode = "path") {
  const { root, names } = storagePath(file)
  const id = await checkDirectoryPath(root, names, true, mode)
  assert(expected === undefined || expected === id, `directory ownership changed: ${file}`)
  if (mode !== "path") checkMacData(file)
  return id
}

function storagePath(value) {
  assert(typeof value === "string" && path.isAbsolute(value) &&
    (!windows || /^[a-z]:[\\/]/i.test(value)), "smoke storage needs an absolute local directory path")
  const root = path.parse(value).root
  const names = value.slice(root.length).split(windows ? /[\\/]+/ : /\/+/).filter(Boolean)
  for (const name of names) component(name)
  return { root, names }
}

async function checkDirectoryPath(root, names, privateEntry, mode = "path") {
  let id
  for (let i = 0; i <= names.length; i++) {
    const at = path.join(root, ...names.slice(0, i))
    const info = await present(at)
    id = directoryID(info, at)
    if (!windows) checkPosixAccess(info, at, privateEntry && i === names.length)
  }
  checkWindows(path.join(root, ...names), mode, privateEntry)
  return id
}

export async function externalDirectory(value, privateEntry = false) {
  let { root, names } = storagePath(value)
  const normalized = path.join(root, ...names)
  // Only these root-owned macOS system aliases may precede a storage path.
  // Check every canonical component and bind it back to the original spelling.
  if (mac && ["var", "tmp"].includes(names[0])) {
    const alias = path.join(root, names[0])
    const info = await present(alias)
    if (info?.isSymbolicLink()) {
      const target = await fs.readlink(alias)
      assert(info.uid === 0n && [`private/${names[0]}`, `/private/${names[0]}`].includes(target),
        `unexpected macOS system directory alias: ${alias}`)
      names = ["private", ...names]
    }
  }
  const id = await checkDirectoryPath(root, names, privateEntry)
  const canonical = await fs.realpath(normalized)
  if (windows) {
    assert.equal(path.parse(canonical).root.toUpperCase(), path.parse(normalized).root.toUpperCase(),
      `redirected Windows drive: ${normalized}`)
  } else await checkLocalPosix(canonical)
  for (let at = canonical; ; at = path.dirname(at)) {
    assert(!await present(path.join(at, ".git")) &&
      !(await present(path.join(at, "HEAD")) && await present(path.join(at, "objects"))),
    `smoke storage must be outside Git checkouts and repositories: ${at}`)
    if (at === path.dirname(at)) break
  }
  assert.equal(identity(await fs.stat(normalized, { bigint: true })), id, `smoke storage redirected: ${normalized}`)
  await directory(canonical, id)
  return { canonical, id }
}

async function linuxMounts() {
  if (process.platform !== "linux") return []
  const text = new TextDecoder("utf-8", { fatal: true }).decode(await fs.readFile("/proc/self/mountinfo"))
  const mounts = text.trimEnd().split("\n").map((line) => {
    const [fields, details] = line.split(" - ")
    const [, , , origin, at] = fields.split(" ")
    const type = details?.split(" ")[0]
    assert(type && origin?.startsWith("/") && at?.startsWith("/"), "cannot verify Linux mount topology")
    const point = at.replace(/\\([0-7]{3})/g, (_, octal) => String.fromCharCode(parseInt(octal, 8)))
    return { origin, point, type }
  })
  assert(mounts.some(({ point }) => point === "/"), "cannot verify Linux mount topology")
  return mounts
}

async function directoryLineage(file, id) {
  const ids = [physicalID(await directory(file, id))]
  while (file !== path.dirname(file)) {
    file = path.dirname(file)
    ids.push(physicalID(await directory(file)))
  }
  return ids
}

export async function archiveSeparation(store, archive, target, sourceRows) {
  const { canonical: from, id: fromID } = store
  const { canonical: to, id: toID } = archive
  const reason = `archive storage must be separate from smoke storage: ${to}`
  assert(!inside(from, to) && !inside(to, from), reason)
  const ancestors = await directoryLineage(from, fromID)
  const destination = await directoryLineage(to, toID)
  const source = new Set([ancestors[0], ...sourceRows.map((row) => physicalID(row.id))])
  assert(!ancestors.includes(destination[0]) && !destination.some((id) => source.has(id)), reason)
  for (const { origin, point, type } of await linuxMounts()) {
    assert(!inside(target, point), `mount inside smoke archive: ${point}`)
    // Subtree mounts hide ancestors; layered views may not share inode identities.
    if (inside(point, from) || inside(point, to)) {
      assert(origin === "/" && linuxArchiveTypes.has(type), `cannot verify archive separation through Linux mount: ${point} (${type})`)
    }
  }
  for (const id of ancestors) source.add(id)
  return (ids) => {
    assert(ids.every((id) => !source.has(physicalID(id))), reason)
    assert(ids.every((id) => device(id) === device(toID)), `off-volume entry in smoke archive: ${target}`)
  }
}

function regular(info, file, links = 1n) {
  assert(info?.isFile() && !info.isSymbolicLink() && info.nlink === links, `missing, externally linked or non-regular smoke file: ${file}`)
  return identity(info)
}

async function withFile(file, flags, use, links, privateEntry = false) {
  const id = regular(await present(file), file, links)
  const handle = await fs.open(file, flags | noFollow)
  try {
    const before = await handle.stat({ bigint: true })
    assert.equal(regular(before, file, links), id, `file ownership changed: ${file}`)
    if (privateEntry && !windows) checkPosixAccess(before, file, true)
    const value = await use(handle, before)
    const after = await present(file)
    assert.equal(regular(after, file, links), id, `file ownership changed: ${file}`)
    if (privateEntry && !windows) checkPosixAccess(after, file, true)
    if (!(flags & constants.O_WRONLY)) {
      assert(after.size === before.size && after.mtimeNs === before.mtimeNs, `file changed while reading: ${file}`)
    }
    return value
  } finally {
    await handle.close()
  }
}

export function readRegular(file, { flush = false } = {}) {
  checkWindows(file, "data")
  return withFile(file, flush ? constants.O_RDWR : constants.O_RDONLY, async (handle, info) => {
    checkMacData(file)
    const bytes = await handle.readFile()
    if (flush) await handle.sync()
    checkMacData(file)
    return { bytes, id: identity(info) }
  }, undefined, true)
}

export async function emptyPrivateFile(file) {
  checkWindows(file, "data")
  return withFile(file, constants.O_RDONLY, (_, info) => {
    assert(info.size === 0n, `smoke lock is not empty: ${file}`)
    return identity(info)
  }, undefined, true)
}

export async function syncDirectory(dir, expected) {
  // Node cannot open a Windows directory for fsync. Files themselves are flushed
  // on both platforms; Windows recovery is for process interruption, not power loss.
  if (windows) {
    if (expected !== undefined) await directory(dir, expected)
    return
  }
  try {
    const handle = await fs.open(dir, constants.O_RDONLY | constants.O_DIRECTORY | noFollow)
    try {
      const id = directoryID(await handle.stat({ bigint: true }), dir, expected)
      await handle.sync()
      await directory(dir, id)
    } finally {
      await handle.close()
    }
  } catch (error) {
    throw new Error(`cannot sync smoke directory ${dir}: ${error.message}`, { cause: error })
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
  }, undefined, true)
}

export async function inventory(root, rootID, { archiveEntries } = {}) {
  const id = await privateDirectory(root, rootID, "tree")
  // stat.dev alone misses a mount of another directory on the same disk.
  for (const { point } of await linuxMounts()) {
    assert(!inside(root, point), `mount inside smoke tree: ${point}`)
  }
  const rows = []
  async function walk(file, name) {
    const info = await present(file)
    assert(info && !info.isSymbolicLink() && String(info.dev) === device(id),
      `missing, redirected or off-volume smoke entry: ${file}`)
    checkMacData(file)
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
  archiveEntries?.(rows.map((row) => row.id))
  // A restarted reader must not rely on another process having flushed a
  // readable POSIX archive. Source inventories are still read-only.
  const flush = archiveEntries && !windows
  const byName = new Map(rows.map((row) => [row.name, row]))
  const links = linkCounts(rows)
  const hashes = new Map()
  for (const row of rows.filter((item) => item.bytes !== undefined)) {
    await parents(root, byName, row.name)
    const file = member(root, row.name)
    row.sha256 = await withFile(file, flush ? constants.O_RDWR : constants.O_RDONLY, async (handle, current) => {
      assert(identity(current) === row.id && current.size === BigInt(row.bytes) && String(current.mtimeNs) === row.mtime,
        `file changed during inventory: ${file}`)
      if (!hashes.has(row.id)) {
        const hasher = createHash("sha256")
        for await (const chunk of handle.createReadStream({ autoClose: false })) hasher.update(chunk)
        hashes.set(row.id, hasher.digest("hex"))
        if (flush) await handle.sync()
      }
      return hashes.get(row.id)
    }, links.get(row.id))
  }
  if (flush) {
    for (const row of rows.toReversed()) {
      if (row.bytes === undefined) await syncDirectory(member(root, row.name), row.id)
    }
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
      checkMacData(file)
      await fs.rmdir(file)
    } else {
      const info = await present(file)
      const count = links.get(row.id)
      assert(regular(info, file, count) === row.id && info.size === BigInt(row.bytes) && String(info.mtimeNs) === row.mtime,
        `source entry changed before removal: ${file}`)
      checkMacData(file)
      await fs.unlink(file)
      links.set(row.id, count - 1n)
    }
    await checkpoint("removed-entry", file)
  }
}
