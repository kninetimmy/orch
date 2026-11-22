import assert from "node:assert/strict"
import fs from "node:fs"
import path from "node:path"
import { linuxProcessScope, visitProcessPaths } from "./smoke-native.mjs"

export function joined(child) {
  return new Promise((resolve) => {
    let error
    child.once("error", (cause) => { error = cause.message })
    child.once("close", (code, signal) => resolve({ code, signal, error }))
  })
}

const invisible = "could not verify complete private smoke process inventory"
const live = "a process still names this smoke fixture"
const windows = process.platform === "win32"

function namesFixture(root, arg) {
  const equals = arg.indexOf("=")
  for (let value of equals < 0 ? [arg] : [arg, arg.slice(equals + 1)]) {
    if (windows) value = value.replaceAll("/", "\\").replace(/^\\\\\?\\/, "").toLowerCase()
    if (!path.isAbsolute(value)) continue
    const normalized = path.normalize(value)
    if (normalized === root || normalized.startsWith(root + path.sep)) return true
  }
  return false
}

function linuxProcesses(matches) {
  assert(linuxProcessScope(), `${invisible}: not in the initial Linux PID/user namespaces with procfs`)
  const mounts = fs.readFileSync("/proc/self/mountinfo", "utf8").trimEnd().split("\n")
  const proc = mounts.filter((line) => line.split(" ")[4] === "/proc")
  assert(proc.length === 1 && /^proc proc (?!.*\bhidepid=(?!0(?:,|$)))/.test(proc[0].split(" - ")[1] ?? "") &&
    proc[0].split(" ")[3] === "/" && !mounts.some((line) => /^\/proc\/(?:\d+|self|thread-self)(?:\/|$)/.test(line.split(" ")[4])),
  `${invisible}: filtered or redirected Linux procfs`)
  for (const pid of fs.readdirSync("/proc").filter((name) => /^\d+$/.test(name))) {
    const processDir = "/proc/" + pid
    try {
      const status = fs.readFileSync(processDir + "/status", "utf8")
      const uid = /^Uid:\s+(\d+)\s+(\d+)\s+(\d+)\s+(\d+)$/m.exec(status)
      const state = /^State:\s+([A-Z])/m.exec(status)
      assert(uid && state, invisible)
      // Our smoke children run unprivileged as this UID. Privileged processes
      // with only a matching real UID (e.g. sudo) are not those children.
      if (Number(uid[2]) !== process.geteuid() || ["Z", "X"].includes(state[1])) continue
      const cmdline = fs.readFileSync(processDir + "/cmdline", "utf8")
      // Linux kernel threads have no executable, cwd or userspace argv.
      if (!cmdline) {
        if (/^Kthread:\s+1$/m.test(status)) continue
        // exit_mm clears argv before the task necessarily appears as a zombie.
        // Verify it has no userspace address space, not just an empty command.
        const stat = fs.readFileSync(processDir + "/stat", "utf8")
        const close = stat.lastIndexOf(") ")
        const vsize = close > 0 && stat.slice(close + 2).split(" ")[20] // field 23 (vsize)
        if (vsize === "0") continue
        throw new Error("live process has no readable argv")
      }
      assert(cmdline.endsWith("\0"), "incomplete Linux argv")
      // Linux exposes argv without the ptrace privileges required for other
      // processes' exe/cwd links. This negative check supplements owned
      // shutdown proof; it is not a search for arbitrary open files or cwd.
      assert(!cmdline.split("\0").some(matches), live)
    } catch (error) {
      // A live but unreadable process is not the same as one that exited.
      if (error.message === live) throw error
      try {
        if (/^State:\s+[ZX]/m.test(fs.readFileSync(processDir + "/status", "utf8"))) continue
      } catch (statusError) {
        if (statusError.code === "ENOENT" || statusError.code === "ESRCH") continue
      }
      const detail = error.code === "ERR_ASSERTION" ? error.message : error.code ?? error.message
      throw new Error(`${invisible}: Linux process ${pid} (${detail})`, { cause: error })
    }
  }
}

// Negative check, not authority to dispose: a durable pre-activity or
// verified-shutdown checkpoint is also required, even on an empty listing.
export function verifyStopped(root) {
  const spellings = [windows ? root.toLowerCase() : root]
  if (process.platform === "darwin" && /^\/private\/(?:var|tmp)(?:\/|$)/.test(root)) spellings.push(root.slice("/private".length))
  const matches = (arg) => spellings.some((spelling) => namesFixture(spelling, arg))
  if (windows || process.platform === "darwin") {
    let matching = false
    visitProcessPaths((arg) => { matching ||= matches(arg) })
    assert(!matching, live)
  } else if (process.platform === "linux") linuxProcesses(matches)
  else throw new Error(invisible)
  const fields = process.platform === "linux" ? "arguments" : "executable paths or arguments"
  return { scope: `invoking user's process ${fields} naming the fixture`, matching: 0 }
}
