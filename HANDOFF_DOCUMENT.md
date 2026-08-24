# Handoff Document

Rewrite the "Current Status" section when stopping mid-task or handing off, per `.AGENTS/AGENTS.md`.

## Current Status (this session)

**Phase 96 is complete, all four sub-phases, across two branches.**
`feature/Phase-96a-Resilience-Core` carries 96a in three commits.
`feature/Phase-96b-Stream-Ownership`, cut from it, carries 96b, 96c and 96d. Both are unmerged and
neither has a pull request yet. Every commit was made with the user's own live go-ahead.

Phase 96 was one flat 22-item checklist. It is now four phases with their own gate sets, and two of
them changed design under evidence before any code was written.

### 96a: the resilience core

Every `nats.Connect` was bare. Measured: a 2m3s outage killed the connection permanently and it never
recovered, silently. `topology.Connect` is now the only dial in shipping code, with
`MaxReconnects(-1)`, `RetryOnFailedConnect(true)`, `CustomReconnectDelay` consuming
`pkg/retry.Backoff`, and five lifecycle handlers. D1 passes for real: severed 150s, reconnected on its
own at 2m38s after 11 attempts.

Four defects found by adversarial review, two of them in the very behaviour the phase advertises:
`nats.go` routes the initial-connect retry through a different callback pair, so the cold-start path
was silent; and a graceful `Close()` fired the failure handlers, so every SIGTERM logged six false
lines per pod. Both survived because the only assertion was that callback fields were non-nil.
`FAILURE_PATTERNS.md` #191 to #194, `LESSONS_LEARNED.md` #161 to #162.

### 96b: stream ownership, redesigned before implementation

The spec said "Runners attach and refuse to start if the stream is absent". Rejected on six verified
findings, all in the phase text. Its load-bearing precedent
(`internal/transport/ssh/known_hosts.go`) **does not exist in the tree**. Attach-only would be
strictly worse on stream loss, because a running Controller never re-asserts. The Controller is
already five replicas. It would falsify a shipped promise that services need no ordering. And no
configuration surface existed yet, so the defect could not be observed.

Built instead: change the VERB, not the actor. `ProvisionStream` (Controller only) reshapes;
`AttachStream` creates only when absent and never reshapes. Drift detection is the real deliverable,
because ownership cannot help during a rolling upgrade. The lock KV bucket got the same treatment,
and its case was worse: a lowered TTL there "lets two runners execute against one device".
`FAILURE_PATTERNS.md` #195 to #197, `LESSONS_LEARNED.md` #163 to #164.

### 96c: one outage budget

`PLEIADES_MAX_OUTAGE` / `mesh.maxOutageSeconds`, default 30m, range 1m to 12h. Three retention
constants derive from it. One derivation is deliberately capped, not linear: the duplicate window
must stay below the stale-job reclaim interval or the Reaper's republish is suppressed as a duplicate
and stranded jobs stop being recovered.

The D3 fix is consumer-side. Producer-side dedup was already correct and protected nothing, because
the window was 2 minutes and the only thing reissuing an unconfirmed dispatch fires after 10. Nobody
had compared the two numbers. The Runner now checks an admission store in its raw pull loop, which
consumes the KV store `FAILURE_PATTERNS.md` #179 recorded as built with no production caller.

The chart now refuses to install a budget its own probes cancel, and the first thing that rule caught
was the shipped defaults: liveness was 60s against a 1800s budget.
`FAILURE_PATTERNS.md` #198 to #199, `LESSONS_LEARNED.md` #165 to #166.

### 96d: path traversal and wire TLS

`NATS_URL` is validated against an allowlist of the four schemes `nats.go` implements, enforced inside
`topology.Connect` so `cmd/demo` (which reads no environment) is covered too. A bare `host:port` used
to be accepted and silently meant unencrypted.

Client TLS via `NATS_CA_FILE`, consuming `tlscert.ServingCert.TLSClientConfig` rather than
hand-rolling a `tls.Config`. A CA file named for a plaintext URL is a startup error, mirroring the
Controller's own TLS convention. Server side: the chart's first NATS configuration file, additive to
the pinned flag list, with `nats.tls` and `nats.websocket` blocks and three refusals.

Proven against a real broker: `ws://` connects, `tls://` connects with verification against a
generated root, and an unrelated root is refused. **The `wss://`-is-not-link-resilience correction is
written into the docs**, because the opposite claim is the kind that reaches a datasheet.
`FAILURE_PATTERNS.md` #200 to #201, `LESSONS_LEARNED.md` #167 to #168.

### Next step

Merge review. Then Phase 101 (mesh identity), which is what makes any of the TLS work into actual
security: the bus is encrypted now and still unauthenticated, and the docs say so plainly.

### Environment note that costs a CI run if missed

`make ci` needs `LOCALSTACK_AUTH_TOKEN` or two AWS packages skip their tests and the coverage ratchet
fails with what looks exactly like a real regression. The token is in `~/.bashrc` at line 111, BELOW
the stock `[ -z "$PS1" ] && return` at line 15, so plain `source ~/.bashrc` and `bash -lc` both leave
it unset. Only `PS1=x; source ~/.bashrc` works.

### Debt, carried deliberately

- The 96a recovery-latency benchmark exists and has never had its result recorded, which AGENTS.md
  asks for. 96b's and 96c's were run and are in their commit messages.
- `main`'s GitHub Actions is red, unrelated: `pkg/remotesvc`'s `TestOperations_NonZeroExitIsAnError`
  fails at `-count=3`, the same class as `FAILURE_PATTERNS.md` #188, and that package is not in
  `flaky-packages.json`.
- Four more multi-writer provisioning sites recorded in 96b's phase text, most seriously database
  migrations running from every Controller replica with no advisory lock.
- The compose stack's broker healthcheck connects anonymously and without TLS, so enabling broker TLS
  there needs that probe changed. Recorded in the docs rather than fixed.
