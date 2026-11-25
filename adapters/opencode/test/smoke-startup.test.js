import assert from "node:assert/strict"
import { readFileSync } from "node:fs"
import path from "node:path"
import test from "node:test"
import { Script } from "node:vm"
import { inside } from "../smoke-artifacts.mjs"

// Replay the actual startup block without importing the smoke entrypoint,
// which would allocate storage and launch a host.
const source = readFileSync(new URL("../smoke.mjs", import.meta.url), "utf8")
const begin = source.indexOf("  async function eventually(")
const end = source.indexOf("  stopHost = ", begin)
assert(begin >= 0 && end > begin, "private startup block is missing")
const startup = new Script("(async () => { let headers;\n" + source.slice(begin, end) + "})()", { filename: "smoke.mjs (startup)" })

const temp = path.resolve("private-smoke-fixture")
const state = path.join(temp, "state")
const baseURL = "http://127.0.0.1:12345"
const pid = 123
const password = "synthetic-private-password"
const authorization = "Basic " + Buffer.from("opencode:" + password).toString("base64")
const warming = () => new Response("warming up", { status: 503 })
const ready = () => Response.json({ pid, paths: { tmp: path.join(temp, "tmp") } })

function replay(reply) {
  const server = { pid, exitCode: null, signalCode: null }
  const record = { pid, url: baseURL, password }
  let elapsed = 0
  const attempts = []
  const deadlines = new Map()
  const advance = (ms) => {
    elapsed += ms
    for (const { controller, at } of deadlines.values()) {
      if (elapsed >= at) controller.abort()
    }
  }
  const run = () => startup.runInNewContext({
    assert, path, inside, Buffer, temp, state, baseURL, server, logs: "",
    readFile: async (file, options) => {
      assert.equal(file, path.join(state, "opencode", "service.json"))
      options?.signal?.throwIfAborted()
      const value = JSON.stringify(record)
      return options === "utf8" ? value : Buffer.from(value)
    },
    fetch: async (url, options) => {
      assert.equal(url, baseURL + "/api/info")
      assert.equal(options.method ?? "GET", "GET")
      assert.equal(options.headers.authorization, authorization)
      attempts.push(elapsed)
      assert(attempts.length <= 81, "startup did not stop retrying")
      return reply(attempts.length, { server, record, advance, signal: options.signal, redirect: options.redirect })
    },
    sleep: advance,
    delay: async (ms, value, { signal }) => {
      signal.throwIfAborted()
      advance(Math.min(ms, deadlines.get(signal).at - elapsed))
      signal.throwIfAborted()
      return value
    },
    performance: { now: () => elapsed },
    AbortSignal: {
      timeout(ms) {
        const controller = new AbortController()
        deadlines.set(controller.signal, { controller, at: elapsed + ms })
        return controller.signal
      },
    },
  })
  return { run, attempts }
}

function safeFailure(pattern) {
  return (error) => {
    assert.match(error.message, pattern)
    assert(!error.message.includes(password) && !error.message.includes(authorization.slice(6)), "diagnostic exposed credentials")
    return true
  }
}

test("an already-ready private server needs no wait or retry", async () => {
  const { run, attempts } = replay((_, { redirect }) => {
    assert.equal(redirect, "error", "startup must not follow redirects")
    return ready()
  })
  await run()
  assert.deepEqual(attempts, [0])
})

test("private startup probes immediately and retries a transient HTTP 503 after 250 ms", async () => {
  const { run, attempts } = replay((attempt) => attempt === 1 ? warming() : ready())
  await run()
  assert.deepEqual(attempts, [0, 250])
})

test("persistent startup HTTP 503 stops at the deadline with a status and attempt count", async () => {
  const { run, attempts } = replay(warming)
  await assert.rejects(run(), safeFailure(/timed out after 20000 ms \(80 attempts, last status 503\)/))
  assert.deepEqual(attempts, Array.from({ length: 80 }, (_, i) => i * 250))
})

for (const part of ["headers", "body"]) {
  test("the same deadline aborts in-flight " + part, async () => {
    let aborted = false
    const { run, attempts } = replay((attempt, { advance, signal }) => {
      if (attempt === 1) return warming()
      const stall = () => new Promise((_, reject) => {
        advance(19_749)
        assert(!signal.aborted, "the deadline has not yet elapsed")
        signal.addEventListener("abort", () => {
          aborted = true
          reject(signal.reason)
        }, { once: true })
        advance(1)
        if (!signal.aborted) reject(new Error("the in-flight response was not aborted"))
      })
      return part === "headers" ? stall() : { status: 200, ok: true, json: stall }
    })
    await assert.rejects(run(), safeFailure(part === "headers"
      ? /timed out after 20000 ms \(2 attempts, last status 503\)/
      : /timed out after 20000 ms \(2 attempts, last status 200\)/))
    assert(aborted, "the deadline must abort the in-flight response")
    assert.deepEqual(attempts, [0, 250])
  })
}

test("a changed service record prevents another request", async () => {
  const { run, attempts } = replay((_, { record }) => {
    record.password += "-replacement"
    return warming()
  })
  await assert.rejects(run(), safeFailure(/private service record changed during startup/))
  assert.deepEqual(attempts, [0])
})

test("a valid reply cannot authorize readiness if the record changes in flight", async () => {
  const { run, attempts } = replay((_, { record }) => ({
    status: 200, ok: true,
    json: () => {
      record.pid++
      return ready().json()
    },
  }))
  await assert.rejects(run(), safeFailure(/private service record changed during startup/))
  assert.deepEqual(attempts, [0])
})

test("a signal exit refuses readiness even when the child has no exit code", async () => {
  const { run, attempts } = replay((_, { server }) => {
    server.signalCode = "SIGTERM"
    return ready()
  })
  await assert.rejects(run(), safeFailure(/private server exited during startup/))
  assert.deepEqual(attempts, [0])
})

test("startup fails without retrying unexpected HTTP or transport errors", async () => {
  for (const status of [401, 500]) {
    const { run, attempts } = replay(() => new Response(authorization, { status }))
    await assert.rejects(run(), safeFailure(new RegExp("returned HTTP " + status)))
    assert.deepEqual(attempts, [0])
  }
  const { run, attempts } = replay(() => {
    throw Object.assign(new Error(authorization), { code: "ECONNREFUSED" })
  })
  await assert.rejects(run(), safeFailure(/transport failed/))
  assert.deepEqual(attempts, [0])
})

test("startup still requires the owned PID and temporary files inside the fixture", async () => {
  const cases = [
    [{ pid: pid + 1, paths: { tmp: path.join(temp, "tmp") } }, /must name our own process/],
    [{ pid, paths: { tmp: path.dirname(temp) } }, /must stay in the isolated fixture/],
  ]
  for (const [info, diagnostic] of cases) {
    const { run, attempts } = replay(() => Response.json(info))
    await assert.rejects(run(), safeFailure(diagnostic))
    assert.deepEqual(attempts, [0])
  }
})
