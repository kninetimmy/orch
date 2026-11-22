import assert from "node:assert/strict"
import { spawnSync } from "node:child_process"
import path from "node:path"

export function joined(child) {
  return new Promise((resolve) => {
    let error
    child.once("error", (cause) => { error = cause.message })
    child.once("close", (code, signal) => resolve({ code, signal, error }))
  })
}

// This is a final read-only check of the owned executables in addition to the
// native stop and direct-child joins, not process ownership inferred from PIDs.
export function verifyStopped(root) {
  const windows = process.platform === "win32"
  const result = windows
    ? spawnSync(path.join(process.env.SystemRoot, "System32", "WindowsPowerShell", "v1.0", "powershell.exe"),
      ["-NoProfile", "-NonInteractive", "-Command", `
$ErrorActionPreference = 'Stop'
$prefix = $env:ORCH_SMOKE_PROCESS_ROOT + '\\'
Get-CimInstance -ClassName Win32_Process | Where-Object {
  ($_.ExecutablePath -and $_.ExecutablePath.StartsWith($prefix, [StringComparison]::OrdinalIgnoreCase)) -or
  ($_.CommandLine -and $_.CommandLine.IndexOf($prefix, [StringComparison]::OrdinalIgnoreCase) -ge 0)
} | ForEach-Object { $_.ProcessId }
`], { env: { ...process.env, ORCH_SMOKE_PROCESS_ROOT: root }, encoding: "utf8", windowsHide: true, timeout: 30_000 })
    : spawnSync("ps", ["-ax", "-ww", "-o", "command="], { encoding: "utf8", timeout: 30_000 })
  assert(result.status === 0 && !result.error, "could not verify private smoke process inventory")
  const spellings = [root]
  if (process.platform === "darwin" && /^\/private\/(?:var|tmp)\//.test(root)) spellings.push(root.slice("/private".length))
  assert(windows ? !result.stdout.trim() : !spellings.some((file) => result.stdout.includes(file + path.sep)),
    "a process still names this smoke fixture")
  return { scope: "process executable paths or commands naming the fixture", matching: 0 }
}
