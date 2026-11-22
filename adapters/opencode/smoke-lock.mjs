import assert from "node:assert/strict"
import { spawnSync } from "node:child_process"
import fs from "node:fs/promises"
import { homedir } from "node:os"
import path from "node:path"
import { fileURLToPath } from "node:url"
import { loadSmokeNative } from "./smoke-native.mjs"
import { checkWindows, emptyPrivateFile, externalDirectory, present, privateDirectory, sha256, syncDirectory, writeNew } from "./smoke-storage.mjs"

const source = fileURLToPath(new URL("smoke-lock.c", import.meta.url))
const windows = process.platform === "win32"
const prepared = path.join(windows ? process.env.LOCALAPPDATA || homedir() : homedir(), ".orch-smoke-native")
let binding

export async function smokeLockBinary() {
  const code = (await fs.readFile(source, "utf8")).replaceAll("\r\n", "\n")
  return path.join(prepared, `${process.platform}-${process.arch}-${sha256(code)}.node`)
}

export async function smokeLockEvidenceFiles() {
  return [source, await smokeLockBinary()]
}

async function checkBinary(binary) {
  await privateDirectory(prepared)
  const info = await present(binary)
  assert(info?.isFile() && info.nlink === 1n && await fs.realpath(binary) === binary,
    "smoke lock binding must be a private regular file; run npm run smoke:prepare --prefix adapters/opencode")
  if (windows) checkWindows(binary)
  else assert(info.uid === BigInt(process.geteuid()) && (info.mode & 0o022n) === 0n, "unsafe native smoke lock binding")
}

export async function acquireSmokeLock(file) {
  if (!binding) {
    const binary = await smokeLockBinary()
    assert(await present(binary), "missing native smoke lock binding; run npm run smoke:prepare --prefix adapters/opencode")
    await checkBinary(binary)
    const candidate = loadSmokeNative(binary)
    assert.equal(typeof candidate?.trySmokeLock, "function", "unsupported native smoke lock binding")
    binding = candidate
  }
  return binding.trySmokeLock(windows ? path.toNamespacedPath(file) : file)
}

export async function prepareSmokeLockFile(root, rootID, pending) {
  const file = path.join(root, "smoke.lock")
  if (!await present(file)) {
    const hasJournal = await present(pending)
    if (!await present(file)) {
      assert(!hasJournal, "pending record has no stable smoke.lock")
      try { await writeNew(file, "") } catch (error) {
        if (error.code !== "EEXIST") throw error
      }
    }
  }
  const check = async (expected) => {
    const { id } = await externalDirectory(root, true)
    assert.equal(id, rootID, "smoke lock directory ownership changed")
    if (process.platform === "linux") {
      const mounts = (await fs.readFile("/proc/self/mountinfo", "utf8")).trimEnd().split("\n")
      assert(!mounts.some((line) => line.split(" ")[4].replace(/\\([0-7]{3})/g, (_, octal) =>
        String.fromCharCode(parseInt(octal, 8))) === file), `mounted smoke lock file: ${file}`)
    }
    // Windows locks the first byte of an empty file. Do not read its data
    // through a different handle: stat and ACL/type checks suffice.
    const lock = await emptyPrivateFile(file)
    assert(expected === undefined || lock === expected, "smoke lock file ownership changed")
    return lock
  }
  // If a prior creator died after O_EXCL but before syncing the directory,
  // make the observed directory entry durable before the first journal write.
  await syncDirectory(root)
  const id = await check()
  return { file, id, check: () => check(id) }
}

async function createBuildDirectory() {
  if (await present(prepared)) return
  if (!windows) {
    try { await fs.mkdir(prepared, { mode: 0o700 }) } catch (error) {
      if (error.code !== "EEXIST") throw error
    }
    return
  }
  checkWindows(path.dirname(prepared), "path", false)
  const result = spawnSync(path.join(process.env.SystemRoot, "System32", "WindowsPowerShell", "v1.0", "powershell.exe"),
    ["-NoProfile", "-NonInteractive", "-Command", `
$ErrorActionPreference = 'Stop'
$user = [Security.Principal.WindowsIdentity]::GetCurrent().User.Value
$acl = [Security.AccessControl.DirectorySecurity]::new()
$acl.SetSecurityDescriptorSddlForm("D:P(A;OICI;FA;;;$user)(A;OICI;FA;;;SY)(A;OICI;FA;;;BA)")
[void][IO.Directory]::CreateDirectory($env:ORCH_SMOKE_NATIVE_DIR, $acl)
`], { env: { ...process.env, ORCH_SMOKE_NATIVE_DIR: prepared }, encoding: "utf8", windowsHide: true, timeout: 30_000 })
  assert(result.status === 0 && !result.error, `cannot create private native smoke build directory: ${result.error?.message ?? result.stderr.trim()}`)
}

// Build separately from either storage entrypoint. Neither entrypoint creates
// build scratch or downloads dependencies when the fixture is busy/pending.
async function prepare() {
  assert(["win32", "linux", "darwin"].includes(process.platform), "unsupported native smoke lock platform")
  const binary = await smokeLockBinary()
  await createBuildDirectory()
  await privateDirectory(prepared)
  if (!await present(binary)) {
    const scratch = await fs.mkdtemp(path.join(prepared, "build-"))
    const output = path.join(scratch, "smoke-lock.node")
    try {
      const mac = process.platform === "darwin"
      const cc = spawnSync(windows ? "gcc" : "/usr/bin/cc", [
        "-std=c11", "-O2", "-Wall", "-Wextra", "-Werror",
        ...(mac ? ["-bundle", "-undefined", "dynamic_lookup"] : windows ? ["-shared", "-static-libgcc"] : ["-shared", "-fPIC"]),
        "-o", output, source,
        ...(windows ? ["-lwtsapi32", "-lshell32", "-ladvapi32"] : []),
      ], { cwd: scratch, encoding: "utf8", windowsHide: true, timeout: 30_000 })
      assert(cc.status === 0 && !cc.error, `could not build native smoke lock (preinstalled ${windows ? "MinGW gcc" : "cc"} required): ${cc.error?.message ?? cc.stderr}`)
      assert.equal(await smokeLockBinary(), binary, "smoke lock source changed during build")
      try { await fs.link(output, binary) } catch (error) {
        if (error.code !== "EEXIST") throw error
      }
    } finally {
      if (await present(output)) await fs.unlink(output)
      await fs.rmdir(scratch)
    }
  }
  await checkBinary(binary)
  console.log(`Smoke lock binding: ${binary}`)
}

if (import.meta.main) await prepare()
