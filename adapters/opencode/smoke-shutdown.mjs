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
      if (uid.slice(1).every((id) => Number(id) !== process.geteuid()) || ["Z", "X"].includes(state[1])) continue
      const args = fs.readFileSync(processDir + "/cmdline", "utf8").split("\0")
      // Linux kernel threads have no executable, cwd or userspace argv.
      if (!args[0] && /^Kthread:\s+1$/m.test(status)) continue
      assert(args[0] && args.at(-1) === "", invisible)
      assert(!args.some(matches) && !matches(fs.readlinkSync(processDir + "/exe")) &&
        !matches(fs.readlinkSync(processDir + "/cwd")), live)
    } catch (error) {
      // A live but unreadable process is not the same as one that exited.
      if (error.message === live) throw error
      try {
        if (/^State:\s+[ZX]/m.test(fs.readFileSync(processDir + "/status", "utf8"))) continue
      } catch (statusError) {
        if (statusError.code === "ENOENT" || statusError.code === "ESRCH") continue
      }
      throw new Error(`${invisible}: Linux process ${pid}`, { cause: error })
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
  return { scope: "invoking user's process executable paths or arguments naming the fixture", matching: 0 }
}
