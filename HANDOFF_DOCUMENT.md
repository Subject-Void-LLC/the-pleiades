# Handoff Document

Rewrite the "Current Status" section when stopping mid-task or handing off, per `.AGENTS/AGENTS.md`.

## Current Status (this session)

**Branch `feature/Phase-96a-Resilience-Core`, cut from `d689184` (the merge of PR #26) at the start
of this session. Nothing is committed: only the user commits, with their own live go-ahead, and this
session never ran `git commit`. This is a phase-complete handoff.**

This session split Phase 96 into four sub-phases and built the first one.

### The split (spec work, in the gitignored `.SPECIFICATION/IMPLEMENTATION.md`)

Phase 96 was one flat checklist of 22 items carrying dial options, reconnect semantics,
observability, the stream-ownership defect, budget derivation, `wss://` traversal and server-side
TLS. It is now four phases with 50 items, each carrying its own full gate set including the
Fuzz/Stress and Security items the original never had:

| Phase | Owns | Blocked on |
|---|---|---|
| **96a** (built this session) | `topology.Connect`/`DialOptions`, reconnect semantics, observability, the archtest guard | -- |
| **96b** | Controller owns stream shape, Runners attach, plus the cold-start ordering that requires | 96a |
| **96c** | ONE maximum-survivable-outage budget and the three retention constants derived from it | 96b |
| **96d** | `wss://` traversal, server-side TLS, `NATS_URL` scheme allowlist | 96a |

**96d was split out on evidence.** Both its deliverables are `nats-server` CONFIG FILE features, and
this repo has no NATS config file: compose and Helm each deliberately replace the image's shipped
`nats-server.conf` with a three-flag command that is test-pinned in three directions, under
`readOnlyRootFilesystem` with only `/data` writable. That is a config mechanism introduction, not a
value change. All 13 cross-references from Phases 83, 101 and 102 were re-pointed at the owning
sub-phase.

### What 96a landed

**The defect, measured rather than inferred.** Every `nats.Connect` in the module was bare, inheriting
`nats.go` defaults. A real broker behind a real Toxiproxy, severed for 2m3s, closed the connection
permanently and never came back through a fully healed network, silently.

- **`internal/topology/connect.go`: `Connect` is the single entry point.** It dials with the shared
  options and returns only once the connection is usable. `internal/topology/dial.go`'s `DialOptions`
  sets `MaxReconnects(-1)`, `RetryOnFailedConnect(true)`, a `Timeout` from D2b's measured 2s,
  `PingInterval`/`MaxPingsOutstanding` for a 60s black-hole window, and `CustomReconnectDelay`
  delegating to `pkg/retry.Backoff` (consumed, not reimplemented).
- **All five production dials converted**, `internal/lock` included: `cmd/runner` opens three NATS
  connections, and leaving the lease one on the defaults would have been worse than changing nothing.
- **`internal/archtest.TestOnlyTopologyDialsNats`** forbids `nats.Connect` outside `internal/topology`
  entirely, with a source-string negative control. Live-controlled: red on a reintroduced dial, green
  on restore.
- **Release gate**: D1 and D2 promoted to permanent container-backed tests, inverted, each citing the
  measurement it replaces. D1 severs for 150s (the old code died at 123s) and passed, reconnecting on
  its own at 2m38s after 11 attempts. Plus a fuzz target (634k execs clean) and a recovery-latency
  benchmark.
- **`cmd/runner`'s `err != context.Canceled`** fixed to `errors.Is`, a latent defect this phase would
  otherwise have activated.

### Four defects found by adversarial review, all fixed, all recorded

`FAILURE_PATTERNS.md` #191-#194 and `LESSONS_LEARNED.md` #161-#162. The two that matter most:

- **#191**: `nats.go` routes the initial-connect retry through `ConnectedCB`/`ReconnectErrCB`, not the
  four handlers a reader naturally reaches for, so the cold-start path was completely silent while the
  docs claimed otherwise. Five handlers now, not four.
- **#192**: a graceful `Close()` fired the disconnect and closed handlers, so every SIGTERM logged
  6 false failure lines per pod. Fixed with `NoCallbacksAfterClientClose()`.

**Both got through because the only assertion was that the callback fields were non-nil**, which is a
tautology about a struct. That is `LESSONS_LEARNED.md` #161, and the fix is
`internal/event.TestNatsBus_LogsTheConnectionLifecycle`, which captures a real logger across a real
severance and asserts a graceful close is quiet.

### The honest caveat, stated in the chart and the docs rather than buried

**The Helm chart's own defaults cancel most of this.** The Runner's liveness probe restarts the pod
60 to 105 seconds into an outage, which is SOONER than the old client gave up, and the restart
abandons in-flight work. So under the default chart you get unlimited reconnection for outages under
a minute and a pod restart for anything longer. Deriving a liveness window from a stated
maximum-survivable-outage budget is **96c's** job and was not guessed here.
`runner-deployment.yaml`'s header and `docs/10-running-in-production.md` both say this plainly.

Also corrected: the docs now name the **control plane / execution plane** boundary explicitly. Nothing
here makes a session to a device survive anything.

### State

`go build`, `go vet` (both tag sets), `gofmt` clean. `internal/topology` coverage 58.4 -> 97.0 against
its 95.8 floor; `internal/event` 86.5 against 86.2. `FAILURE_PATTERNS.md` #119's remaining open half is
now closed. Handoff rotated: two sections moved into `HANDOFF_ARCHIVE.md`.

### Next step

Run `make ci` to completion and act on it, then 96b. **Separately and already diagnosed:** `main`'s
GitHub Actions is red at `test-repeat`, `pkg/remotesvc`'s `TestOperations_NonZeroExitIsAnError` failing
at `-count=3` with `EOF` where it wants "masked". Same class as `FAILURE_PATTERNS.md` #188, a fixture
that does not survive a second run in one process. That package is not in `flaky-packages.json`, so it
fails hard. Unrelated to this branch.
