# Supplied context v1

This is a scoped historical Orch component packet. Only the declared snapshot
files and TASK.md, ROLE.md, CONTEXT.md and go.mod are permitted context.
No Git history, upstream browsing, controller files, memory, prior answers,
other packets, network, credentials, app/MCP tools or additional agents are
permitted. Public history may already have been encountered; disclose recall.

Toolchain: Go 1.26+ and Git 2.53+; preparation was authored on Go 1.26.5,
Git 2.53.0.windows.1, Windows/amd64. No new dependency is required. go.mod
is a supplied standard-library component harness, not the historical whole
Orch module. Keep GOWORK=off, GOTOOLCHAIN=local, GOPROXY=off, GOSUMDB=off.

Public verification: gofmt -l on supplied .go files; go test ./... can compile
complete supplied components and any permitted worker-written tests. The
effort-window packet supplies full context files with unrelated declarations;
full interview compilation is intentionally not a public check. Its worker
may write a focused declaration harness but must not fetch omitted context.
The attributes packet uses Git attribute inspection; controller checkout
fixtures are external. Public compilation/formatting alone is not correctness.

Preparation limits: per-packet export 60 seconds, each control invocation
120 seconds (including compilation), test process 30 seconds, Git read
10 seconds, fixture Git command 5 seconds, pipe cleanup 2 seconds, overall
control suite 540 seconds. One invocation per control, no retries or repairs.
Setup failure is not a behavioral rejection. These are preparation limits,
not an approved evaluation-worker schedule or model-execution budget.

External behavioral keys, probes and controls are never worker inputs.
Packet inspection establishes bytes and destination containment only.
Runtime model-tool and OS isolation are unimplemented and execution is blocked.
No evaluation model trial is authorized by this packet.
