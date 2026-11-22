import { createRequire } from "node:module"

const require = createRequire(import.meta.url)
let binding

// Only smoke-lock validates and loads the binding. Storage checks fail
// closed on macOS until the verified native probe is present.
export function loadSmokeNative(binary) {
  return binding ??= require(binary)
}

export function hasResourceFork(file) {
  return binding.hasResourceFork(file)
}

export function linuxProcessScope() {
  return binding.linuxProcessScope()
}

export function visitProcessPaths(visit) {
  return (process.platform === "win32" ? binding.windowsProcessPaths : binding.macProcessPaths)(visit)
}
