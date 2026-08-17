import { spawnSync } from "node:child_process"
import { Plugin } from "@opencode-ai/plugin"

export const pluginID = "orch.delivery"
export const mutationTools = new Set(["edit", "write", "patch"])

const patchDirective = /^\*\*\* (?:Add|Update|Delete) File: (.+)$/
const moveDirective = /^\*\*\* Move to: (.+)$/

export function mutationPaths(tool, input) {
  if (!mutationTools.has(tool)) return []
  if (!input || typeof input !== "object") throw new Error(`orch: ${tool} input is not an object`)
  if (tool === "edit" || tool === "write") {
    if (typeof input.path !== "string" || input.path.length === 0) {
      throw new Error(`orch: ${tool} input has no path`)
    }
    return [input.path]
  }
  if (typeof input.patchText !== "string") throw new Error("orch: patch input has no patchText")
  const paths = []
  for (const line of input.patchText.split(/\r?\n/)) {
    const match = patchDirective.exec(line) ?? moveDirective.exec(line)
    if (match) paths.push(match[1])
  }
  if (paths.length === 0) throw new Error("orch: patch input names no files")
  return paths
}

export function runOrch(args, cwd = process.cwd()) {
  const result = spawnSync("orch", args, { cwd, encoding: "utf8", windowsHide: true })
  if (result.error) throw new Error(`orch: ${result.error.message}`)
  if (result.status !== 0) {
    throw new Error((result.stderr || result.stdout || `orch exited ${result.status}`).trim())
  }
  return result.stdout
}

export function definePlugin(run = runOrch) {
  return Plugin.define({
    id: pluginID,
    setup: async (ctx) => {
      await ctx.tool.hook("execute.before", async (event) => {
        const paths = mutationPaths(event.tool, event.input)
        if (paths.length === 0) return
        const session = await ctx.session.get({ sessionID: event.sessionID })
        run(["guard", "check", "--", ...paths], session.location.directory)
      })
      await ctx.session.hook("context", async (event) => {
        const session = await ctx.session.get({ sessionID: event.sessionID })
        const text = run(["hook", "opencode", "session-start"], session.location.directory)
        if (text) event.system.push({ type: "text", text })
      })
    },
  })
}

export default definePlugin()
