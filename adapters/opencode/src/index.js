import { spawnSync } from "node:child_process"
import { isAbsolute, resolve } from "node:path"
import { Plugin } from "@opencode/plugin"
import { FSUtil } from "@opencode/util/fs-util"
import { Patch } from "@opencode/util/patch"

export const pluginID = "orch.delivery"
export const mutationTools = new Set(["edit", "write", "patch"])

export function mutationPaths(tool, input) {
  if (!mutationTools.has(tool)) return []
  if (!input || typeof input !== "object") throw new Error(`orch: ${tool} input is not an object`)
  let paths
  if (tool === "edit" || tool === "write") {
    if (typeof input.path !== "string" || input.path.length === 0) {
      throw new Error(`orch: ${tool} input has no path`)
    }
    paths = [input.path]
  } else {
    if (typeof input.patchText !== "string") throw new Error("orch: patch input has no patchText")
    const parsed = Patch.parse(input.patchText)
    if (parsed._tag !== "Success") throw new Error("orch: patch input is not a valid native patch")
    paths = parsed.success.flatMap((hunk) => hunk.type === "update" && hunk.movePath ? [hunk.path, hunk.movePath] : [hunk.path])
  }
  if (paths.length === 0) throw new Error("orch: patch input names no files")
  return paths.map((path) => {
    const normalized = FSUtil.windowsPath(path)
    if (normalized === "~" || normalized.startsWith("~/") || (process.platform === "win32" && normalized.startsWith("~\\"))) {
      throw new Error("orch: home-relative paths are unsupported; use an explicit path")
    }
    return normalized
  })
}

export function runOrch(args, cwd = process.cwd()) {
  const result = spawnSync("orch", args, { cwd, encoding: "utf8", windowsHide: true })
  if (result.error) throw new Error(`orch: ${result.error.message}`)
  if (result.status !== 0) {
    throw new Error((result.stderr || result.stdout || `orch exited ${result.status}`).trim())
  }
  return result.stdout
}

function sessionDirectory(session) {
  const directory = session?.location?.directory
  if (typeof directory !== "string" || !isAbsolute(directory)) {
    throw new Error("orch: session directory is not an absolute path")
  }
  return directory
}

export function definePlugin(run = runOrch) {
  return Plugin.define({
    id: pluginID,
    setup: async (ctx) => {
      await ctx.tool.hook("execute.before", async (event) => {
        const paths = mutationPaths(event.tool, event.input)
        if (paths.length === 0) return
        const session = await ctx.session.get({ sessionID: event.sessionID })
        const directory = sessionDirectory(session)
        run(["guard", "check", "--", ...paths.map((path) => resolve(directory, path))], directory)
      })
      await ctx.session.hook("context", async (event) => {
        const session = await ctx.session.get({ sessionID: event.sessionID })
        const directory = sessionDirectory(session)
        const args = ["hook", "opencode", "session-start"]
        if (!session.parentID) {
          const model = `${event.model.providerID}/${event.model.id}${event.model.variant ? `#${event.model.variant}` : ""}`
          args.push("--model", model)
        }
        const text = run(args, directory)
        if (text) event.system.push({ type: "text", text })
      })
    },
  })
}

export default definePlugin()
