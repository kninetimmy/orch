import { execFile } from "node:child_process"
import { isAbsolute } from "node:path"
import { childRoles, readLookupContext, sameDirectory } from "./lookup.js"

const tool = "orch_cleanup"
const statement = "cleanup-issue"

function runCleanup(request, cwd) {
  // Do not block the server: the engine calls back into its authenticated
  // host API during preflight and disposal.
  return new Promise((resolve, reject) => {
    const child = execFile("orch", ["run", "cleanup"], { cwd, windowsHide: true }, (error, stdout, stderr) => {
      if (error) reject(new Error(stderr.trim() || error.message))
      else resolve(stdout)
    })
    child.stdin.on("error", reject)
    child.stdin.end(JSON.stringify(request))
  })
}

export async function installCleanup(ctx, run, invoke = runCleanup) {
  const directory = ctx.location?.directory
  if (typeof directory !== "string" || !isAbsolute(directory) ||
      typeof ctx.tool?.transform !== "function" || typeof ctx.permission?.assert !== "function") return
  try {
    if (!sameDirectory(directory, ctx.location.project?.canonical)) return
    const scope = readLookupContext(run, directory)
    if (!scope.managed || !sameDirectory(directory, scope.memory_root)) return
  } catch {
    return // Unverified scope gets no mutator.
  }

  await ctx.tool.transform((editor) => {
    if (editor.get(tool)) return
    editor.add({
      name: tool,
      description: "Run confirmed Orch issue cleanup from the primary checkout. Call directly, not in execute. Requires the confirmed active run ID and merged or abandoned issue number. Only a cleaned result is success.",
      options: { codemode: false, permission: tool },
      input: {
        type: "object", additionalProperties: false,
        properties: {
          schema_version: { type: "integer", const: 1 },
          run_id: { type: "string", minLength: 1 },
          issue_number: { type: "integer", minimum: 1 },
          statement: { type: "string", const: statement },
        },
        required: ["schema_version", "run_id", "issue_number", "statement"],
      },
      execute: async (input, context) => {
        const { schema_version, run_id, issue_number } = input ?? {}
        if (schema_version !== 1 || typeof run_id !== "string" || !run_id.trim() ||
            !Number.isSafeInteger(issue_number) || issue_number <= 0 || input.statement !== statement) {
          throw new Error("orch: cleanup requires the confirmed run, issue and cleanup-issue statement")
        }
        const verifyCaller = async () => {
          const session = await ctx.session.get({ sessionID: context.sessionID })
          const cwd = session?.location?.directory
          if (session?.id !== context.sessionID || session.parentID || childRoles.has(context.agent) ||
              typeof cwd !== "string" || !isAbsolute(cwd) || !sameDirectory(cwd, directory)) {
            throw new Error("orch: cleanup requires a primary Architect session at the verified checkout")
          }
        }
        await verifyCaller()
        await ctx.permission.assert({
          sessionID: context.sessionID, agent: context.agent, action: tool, resources: [`${run_id}#${issue_number}`],
          source: { type: "tool", messageID: context.messageID, id: context.id },
        })
        await verifyCaller()
        context.signal?.throwIfAborted()
        const output = JSON.parse(await invoke({ schema_version, run_id, issue_number, statement }, directory))
        if (output?.schema_version !== 1 || output.issue_number !== issue_number || output.phase !== "cleaned") {
          throw new Error("orch: engine did not report this issue cleaned; inspect run status before retrying")
        }
        return { content: JSON.stringify(output) }
      },
    })
  })
}
