# Failure Patterns

A catalogue of bugs found while working this codebase. Append an entry (Symptom, Root cause, Fix,
Lesson) before debugging anything new, per `.AGENTS/AGENTS.md`.

1. Capability name collision between two packages
2. Unchecked property map assertion panicking the dispatcher
3. go vet copylocks warning from embedding a mutex by value
4. stdlib `flag` rejects the exact CLI usage the spec documents
5. A validation test that could never actually fail
6. Pre-existing data race in the log-streaming test package
7. A parallel terminology rename missed a same-meaning string outside the renamed files
8. Parallel agents' own build/test runs can observe a mid-flight, inconsistent repo state
9. Ansible-shape sniff over-fired on a bare, non-Ansible top-level list
10. Unknown runbook keys are silently dropped, which would defeat any migration guarantee
11. A non-string `target` silently disables two validation rules
12. An instruction that cannot be followed will be violated
13. An empty query result is indistinguishable from a misaimed query
14. `internal/runner`'s test package did not compile, and the one visible symptom was not the only bug
15. `lock.Manager.Acquire`'s `ttl` parameter is honored by one implementation and silently ignored by
16. `yaml.Marshal` panics, rather than returns an error, on a value it cannot encode
17. `adapters/native.Adapter` publishes to a NATS subject its own stream is not configured to capture
18. An unvalidated runbook `id:` can widen or misroute a NATS subject
19. A single `when_cel` string can hang a task's execution for minutes with no special privilege
20. JWT validation has no issuer/audience pinning, deferred pending a real token-issuing path
21. `add-host --set port=<n>` silently produced an unusable port, defaulting `SSHPort()` to 22
22. `credential.Credential` leaked every secret through `encoding/json` and `log/slog`'s JSON handler
23. A masked command's `Stdout`/`Stderr` did not cover the transport-error path, only the success path
24. `MkdirAll`'s mode argument only applies to a directory it creates, not one that already exists
25. A bulk-insert batch size tuned against one schema width silently stopped being safe after the schema grew
26. Stray git worktrees under `.claude/` were not gitignored and polluted whole-tree tooling run from repo root
27. An architecture test written to check import layering also caught a real, dormant production bug
28. A second Rule added to an already-documented O(n) call site silently paid that cost twice
29. An idempotency key derived from message content, not message identity, silently vanished real events
30. A consumer that bypasses the event envelope silently zeroed every field after an unrelated interface change
31. A single shared consumer meant every SSE log viewer saw whatever job was requested first, not the job it asked for
32. An SSE handler committed its response to 200 before it could still fail
33. Fixing #32 introduced a genuine data race between the handler and its own delivery callback
34. A shared lock's own join lost most of a concurrent burst under the default contention policy
35. A fuzz test that never called the function under test
36. A sub-second positive lock TTL reached the NATS server as an opaque API error instead of a clear one
37. A key containing consecutive dots passed this package's own key validation but silently hung the server
39. A canceled context raced a ticker-driven renewal call from two different directions at once
40. A benchmark's own container teardown was measured as part of the call it was timing
41. A new enum field had a Go-to-string direction but no string-to-Go direction, so a runbook could not actually author it
42. A composition root's new fail-closed startup requirement broke an existing test that spawns the real binary as a subprocess
43. A property named the same as the encryption marker key silently bypassed encryption of the entire map
44. A batch query's decrypt-error handling aborted the entire result set on the first bad row
45. A key-rotation write had no compare-and-swap, silently losing a concurrent legitimate write
46. An exclusive file create still left a window where a concurrent reader could observe an empty file
47. A key-rotation pass ran synchronously before the HTTP server started listening
48. A classification path resolver rejoined its whole prefix on every level, making it quadratic in path length with no bound on that length
50. A shared CLI positional-argument parser silently swallowed a real flag as a bare boolean flag's value
51. `pkg/capability`'s own Windows capability file never compiled on any non-Windows platform
52. A generated Collection catalog compiled and unit-tested cleanly while being completely unreachable from the real binary
53. A real end-to-end test writing into the live module tree can race an architecture test's `go list` scan of the same tree
54. A real end-to-end test's fixture used a device property key the code under test never read, so a passing test proved nothing about the feature it appeared to exercise
55. Two end-to-end tests in different packages each removed the *shared parent* directory their per-process fixtures lived under, so a parallel run deleted one test's package while it was still building
56. A repository port had no create operation at all, so the first sync plugin had nothing to onboard a device with
57. A file-backed repository named its own storage backend as the authoritative sync plugin, so the first real sync plugin reported every host as a conflict
58. A read-only guard that failed loudly turned a dry run into an abort on the first device
59. A migration-generation tool's zero-option schema diff silently never emits `DROP COLUMN`
60. A `coverage-floor.json` regression check flagged four packages this phase never touched
61. A full concurrent `go test ./...` run reliably flakes on containerized-dependency packages in this sandboxed environment, and reducing package-level parallelism reliably fixes it
62. Two independent unbounded-recursion sites in the DAG builder scaled stack usage directly with externally-supplied input size
63. A caller-supplied job ID was concatenated straight into a NATS subject, so `>` streamed every job's logs
64. A live SSE handler mutated its response header map while its own consumer goroutine was already writing the body
65. `GET /api/v1/jobs/{id}/logs` authenticated every caller and authorized none of them
66. An unauthorized `runbook:execute` dispatch returned HTTP 200 with a per-device failure tally
67. `cmd/demo` mounted a production SSE handler on a router with no auth, no tracing, and no rate limiting, and its own advertised URL had 400'd for a full phase without anyone noticing
68. Phase 39's "five real forged tokens... correctly rejected by the real `ValidateToken`" audit left no test file behind
69. `go test ./...`'s default parallelism raced a real scaffolded package's temp directory against `go list`'s own tree walk
70. Wrapping `http.ResponseWriter` silently dropped `http.Flusher`, so mounting the HATEOAS middleware would have killed the SSE log stream; the only reason it never did is that nothing ever mounted it
71. A response-rewriting middleware round-tripped every body through `map[string]interface{}`, silently corrupting integers, dropping links from collections, and clobbering handler-set keys
72. A caller-controlled request path was reflected straight back into the response body as a hypermedia `href`
73. The authorization generator's error was discarded, so a policy-backend outage would have been indistinguishable from a caller who is legitimately allowed to do nothing
74. A roadmap checklist item asserted a status-code bug the code did not have, and the phase that inherited it nearly fixed a symptom that does not reproduce
75. A JWT signature-forgery test tampered with base64 padding bits instead of the signature, so one run in sixteen "caught" a forgery that had never been forged
76. `DispatchRunbook`'s per-device loop read the management address from a property key ("ip") no device type in the codebase ever populates, so every real dispatch silently skipped every device while the endpoint still answered 200
77. The dispatch payload's `DeviceName` field was populated from `device.ID()`, not `device.Name()`, so a dispatched device's own display name never actually reached the wire
78. A runbook id read straight from an HTTP query parameter had no allow-list before it reached `filepath.Join`, so `internal/runbook`'s new filesystem-backed `Source` needed its own injection boundary built from nothing
79. `dispatch.JobStore.BeginFanOut`'s idempotency guard had no way back, so a Controller crash between claiming a job and completing it stranded that job in `"fanning_out"` forever
80. `BeginFanOut`'s `staleAfter` reclaim (#79's own fix) was a lease with no fencing token, so a `Worker` that was merely slow, not dead, could keep writing after being reclaimed and silently corrupt the job's terminal record
81. `wire.DispatchPayload.JobID` reached two NATS subjects by bare string concatenation on the Runner side, with no format validation, the identical shape #63 had already fixed once at the HTTP-facing boundary
82. `executeWithLease` had no panic recovery, so a panicking `ExecutionAdapter.Execute` crashed the whole Runner process and could race its own deferred lease release against a still-running heartbeat goroutine
83. `executeWithLease`'s per-execution context was an unconditional child of `Agent.Run`'s own shutdown-cancelable context, so `interruptible: false` only survived a lease-heartbeat failure, never a graceful Runner shutdown -- the far more routine trigger the PLAN.md exception actually exists for
84. `reportResult` appended to the Runner's own write-ahead log using the same context `Agent.Run`'s shutdown cancels, so a job whose execution was still in flight at shutdown had its outcome silently and permanently dropped instead of durably recorded
85. The Runner's write-ahead log minted a fresh random idempotency key on every `Append` call instead of a key stable across a JetStream redelivery of the identical job, so a crash-then-redeliver-then-reexecute sequence could publish the same logical outcome twice with no dedup catching it

---

Each line above is a failure pattern's title only. Full Symptom / Root cause / Fix / Lesson detail lives in [`FAILURE_PATTERNS_ARCHIVE.md`](FAILURE_PATTERNS_ARCHIVE.md), same entry numbers. Scan the index above before debugging anything new; open the matching archive entry only if a title looks relevant.

Append a new entry to the archive first, in full, then add its one-line, same-numbered title here. Never renumber an existing entry.
