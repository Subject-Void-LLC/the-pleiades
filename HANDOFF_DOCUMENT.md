# Handoff Document

Rewrite the "Current Status" section when stopping mid-task or handing off, per `.AGENTS/AGENTS.md`.

## Current Status (this session)

**Branch `feature/Phase-96b-Stream-Ownership`, which now carries 96b AND 96c, cut from
`feature/Phase-96a-Resilience-Core` (`725d463`), itself unmerged. Working through Phase 96 end to end:
96a and 96b are committed, 96c is complete and being committed, 96d remains.**

### 96c: one outage budget, and the finding that reframes it

`PLEIADES_MAX_OUTAGE` (Helm: `mesh.maxOutageSeconds`) is one operator-facing duration, default **30
minutes**, range 1m to 12h, parsed once in `topology.ParseOutageBudget`. `topology.OutageBudget` is a
named type whose zero value is not a budget. Three retention-shaped constants that used to be
independent literals are now derived from it, with the relationships asserted by tests rather than
described in prose.

**The derivation that is deliberately NOT linear, and the reason.** `DerivedDuplicateWindow` is capped
at half the ten-minute stale-job reclaim interval. `internal/dispatch.Reaper`'s own doc comment says
its republish is safe "precisely because it is not a retry within that window", so a duplicate window
grown past the reclaim interval would silently convert that republish into a suppressed duplicate and
a stranded job would stop being recovered at all. The cap lives in the function and a test asserts it
across the minimum, default and maximum budget. `LESSONS_LEARNED.md` #165 generalizes it.

**The real D3 fix is consumer-side, and it closes an unwired primitive.** Producer-side dedup was
already correct: the fan-out stamps a retry-stable `jobID:deviceID` that reaches
`jetstream.WithMsgID`. It protected nothing, because the window was 2 minutes and the only thing that
reissues an unconfirmed dispatch is a reclaim firing after 10. Nobody had compared the two numbers.
The fix is an admission check in the Runner's raw pull loop (`internal/runner/agent_dedup.go`), keyed
on the same identity the producer and the write-ahead log already use, backed by the KV store
`FAILURE_PATTERNS.md` #179 recorded as fully built with no production caller. Marked only on success,
never on receipt, or the dead-letter path stops being reachable. A store failure runs the work rather
than skipping it. `FAILURE_PATTERNS.md` #198 and `LESSONS_LEARNED.md` #166.

**The one-way door refuses rather than warns**, and only when there is something to lose:
`ProvisionStream` reads `State.Msgs` and `FirstTime` and refuses a shortening that would delete
retained messages, naming the count and the age, with `PLEIADES_MAX_OUTAGE_ALLOW_DISCARD=true` as a
consequence-named opt-in that only the Controller reads. The decision is a pure function
(`retentionWouldDiscard`) because the refusal cannot be reached from a container test: the shortest
retention any legal budget derives is several hours.

**The chart now refuses to install a budget it would cancel, and the first thing that rule caught was
the shipped defaults.** `runner.heartbeat.livenessStaleAfterSeconds` was 60 while the budget defaults
to 1800, so Kubernetes would restart the Runner 60 to 105 seconds into an outage the budget claims to
survive. The default is now 1800 and `_validations.tpl` fails the install if it is shorter.
`FAILURE_PATTERNS.md` #199: when a change removes the reason a timeout exists, the timeout becomes
wrong in the other direction.

**Stated honestly in the docs rather than buried:** two things still cut an outage shorter than the
budget and neither is a retention setting. A Runner cannot begin new work during an outage at all
(starting a job publishes a log event first), and work already running on a device is abandoned about
a minute in when the device lease heartbeat fails. So the budget governs how long the fleet can be out
of contact and still pick up, not how long in-flight work keeps running.

### Evidence

`make ci` green for 96b in one invocation. 96c: all tests pass; `internal/topology` 96.7 against a
95.8 floor, `internal/runner` 88.0 against 86.8. `FuzzOutageBudgetDerivation` clean at 554k execs
asserting the invariant a real server enforces (Duplicates must not exceed MaxAge).
`FuzzStreamConfigDrift` clean at 629k. The chart's refusal was verified by rendering.

### Next step

**96d**: `wss://` path traversal, server-side TLS, and a `NATS_URL` scheme allowlist. Note its real
cost, recorded in the phase text: both deliverables are `nats-server` CONFIG FILE features and this
repo has no NATS config file, because compose and Helm each deliberately replace the image's shipped
one with a three-flag command that is test-pinned in three directions, under `readOnlyRootFilesystem`
with only `/data` writable.

### Environment note that costs a CI run if missed

`make ci` needs `LOCALSTACK_AUTH_TOKEN` or `internal/catalog/cloud/aws/{ec2,s3}` skip their tests and
the coverage ratchet fails with what looks exactly like a real regression. The token is in
`~/.bashrc` at line 111, BELOW the stock `[ -z "$PS1" ] && return` at line 15, so plain
`source ~/.bashrc` and `bash -lc` both leave it unset. Only `PS1=x; source ~/.bashrc` works.

### Debt, carried deliberately

- The 96a recovery-latency benchmark exists but its result has never been recorded, which
  `.AGENTS/AGENTS.md` asks for.
- `main`'s GitHub Actions is red, unrelated to this work: `pkg/remotesvc`'s
  `TestOperations_NonZeroExitIsAnError` fails at `-count=3` with `EOF` where it wants "masked", the
  same class as `FAILURE_PATTERNS.md` #188, and that package is not in `flaky-packages.json`.
- Four more multi-writer provisioning sites recorded in 96b's phase text, most seriously database
  migrations running from every Controller replica with no advisory lock.
