# Handoff Document

Rewrite the "Current Status" section when stopping mid-task or handing off, per `.AGENTS/AGENTS.md`.

## Current Status (this session)

**Branch `feature/Transport-Foundation-the-Circuit-Breaker`, off `main`. HEAD is `dc8e2df`. All of
Phase 72's actual code is committed, across two commits the user made themselves (no live go-ahead
was ever given to the assistant this session, so the assistant itself never ran `git commit`,
matching the standing rule): `835431b` (Workstream A, the `pkg/retry.Do` consolidation) and
`dc8e2df` (Workstreams B-F: the hop chain, hierarchical bastion config, engine wiring, the CI
matrix, and chaos/fuzz/adversarial/hardening/docs). The only thing left uncommitted at the moment
this section was written is this session's own documentation bookkeeping: two new
`FAILURE_PATTERNS.md`/`FAILURE_PATTERNS_ARCHIVE.md` entries (#168, #169, below) and this
handoff rotation itself. No commit message is needed for the code — it is already in history: see
`git show dc8e2df` for the full message.**

This session implemented **Phase 72: Transport Foundation (the Circuit Breaker, `retry.Do`, and the
Hop Chain)** end to end — the foundational phase of Part XV (the Transport Layer) that Phases 73-77
depend on.

### What landed

**Workstream A — `pkg/retry.Do[T]`/`Sleep`.** One generic, context-aware retry loop replacing three
independent hand-rolled ones (`internal/lock/queue.go`'s `acquireWithContention`,
`internal/lock/nats.go`'s timer half, `pkg/remoteexec/dial.go`'s `dialWithRetry`). Circuit-breaker
`Allow`/`RecordFailure` calls stayed at the SSH-dial call site, deliberately, since they are
dial-specific, not generic retry behavior. All three migrations were behavior-preserving — existing
tests passed unmodified, which is the actual proof. A new `internal/archtest` rule now asserts no
second retry loop or second breaker exists anywhere in the module.

**Workstream B — the N-hop SSH tunnel (`pkg/remoteexec`, `internal/transport`).**
`transport.Target` gained `Route []Hop` (`Host`, `Port`, `DeviceName`, a resolved
`credential.Credential`); an empty `Route` is exactly today's behavior, so every existing caller is
unedited. `pkg/remoteexec` gained the matching hop-chain shape and does the real tunneling: hop 1
dials with the existing `dialWithRetry`, each subsequent hop opens a `direct-tcpip` channel through
the previous hop's already-authenticated `*ssh.Client` via `DialContext`, then a fresh
`ssh.NewClientConn` runs a genuinely independent SSH handshake over that channel. Host key
verification and `InsecureSkipHostKeyVerify` are both per-hop, not global. `internal/transport/ssh`
stayed a thin translator, looping the same single-target conversion it already did.

**Workstream C — hierarchical bastion configuration (new storage + resolver).** `Group` and
`Inventory` both gained a `properties` field (`field.JSON`, matching `Device`'s existing shape),
with real migrations for both dialects
(`internal/ent/migrate/migrations/{sqlite/0017,postgres/0014}_add_group_inventory_properties.sql`).
`internal/inventory.Repository.GroupAncestry` (`internal/inventory/ent_group_ancestry.go`) is the
first real BFS walk of Group's parent/child DAG this codebase has ever needed — group nesting is a
graph, not a tree, so it needed a deterministic tiebreak (ascending name) for siblings at equal
distance. `engine.ResolveRoute` (`internal/engine/hop_resolve.go`) feeds the ancestry chain through
the existing `policy.Resolve`, most-specific-wins, capped at 16 hops
(`maxRouteHops`), rejected at resolve time before any per-hop lookup happens.

**Workstream D — engine wiring.** `NewTransportActionExecutor` gained a fourth constructor
parameter, an inventory-lookup dependency (`hopChainInventory`), needed because resolving hop *N*
requires looking up hop *N*'s own device (for `SSHHost()`/`SSHPort()`) and hop *N*'s own credential
independently of the primary target's. Every composition root that builds one was updated
(`cmd/pleiades/run.go` and others). The secret-masking union at
`internal/engine/action_ssh.go` was extended to cover every hop's `Password`/`PrivateKeyPEM`/
`Passphrase`, not just the target's — the same class of gap `FAILURE_PATTERNS.md` #22 already
recorded for `MarshalJSON`, recurring in a new shape. `internal/adapters/native`'s per-task
subprocess path deliberately skips route resolution rather than half-implementing it (no live
inventory connection there, a credential store scoped to one device) — recorded as a named gap, not
silently worked around.

**Workstream E — the CI matrix.** `.github/workflows/ci.yml` went from one `ubuntu-latest` job to
three: ubuntu (blocking, unchanged, still the only leg with Docker and therefore the only one
running the container-backed conformance tests) and macOS (blocking: build, vet, and every
non-Docker-dependent unit test, verified concretely — `pkg/remoteexec`'s own suite uses an
in-process fake SSH server, no Docker, so it genuinely runs for real on macOS) both required;
Windows is advisory/non-blocking (`remoteexectest` shells out to `/bin/sh`, which doesn't exist
there). A new Makefile target names the non-Docker package set explicitly rather than by a fragile
glob, so a future container-backed test doesn't silently join or leave a leg it shouldn't. A code
comment next to the matrix records the `_windows`/`_linux`/`_darwin` implicit-build-constraint trap
(`FAILURE_PATTERNS.md` #51) for whoever reaches for a platform-suffixed file in Phase 73/75.

**Workstream F — chaos, fuzz, stress, adversarial, hardening, docs.** A real Toxiproxy-fronted SSH
container severed at three moments (mid-dial: retry then breaker trip; after session establishment:
error, no retry, per the existing no-retry-after-send rule; mid-tunneled-command on the bastion hop:
error naming the failed hop, never a silent zero-value `Result`) — all under `-race` and `goleak`.
Route parsing is fuzzed against deeply nested/self-referential/absurdly long chains: never panics,
fails closed naming the offending hop, and fails at parse time rather than dial time. 300 concurrent
hop-chained sessions ran clean under `-race`, with a per-hop cost benchmark. An adversarial pair
proves per-hop key confusion (bastion's key known, tunneled endpoint's deliberately absent from
`known_hosts`) fails closed and is attributable to the right hop, then proves the same chain
succeeds once the real key is added. `PATTERNS.md`'s Circuit Breaker entry moved from `POTENTIALLY`
to **YES**, correctly naming `pkg/remoteexec` (not the stale spec's assumed
`internal/transport/resilience`) as where it actually lives — see the "stale spec" note below.
`docs/10-running-in-production.md` and a changelog fragment
(`changelog/ssh-bastion-hop-chains.added.md`) cover the bastion/hop-chain configuration, the
per-hop credential rule, and the per-hop host-key requirement.

### Two real bugs these gates found, both fixed and recorded

See `FAILURE_PATTERNS.md` #168 and #169 (full detail in `FAILURE_PATTERNS_ARCHIVE.md`):

1. **#168 — `Connect`'s per-hop loop only kept the last `*ssh.Client`, leaking every earlier hop's
   connection.** Found by a real `goleak`-based container test failure, not by inspection. `Conn`
   gained a `chain []*ssh.Client`; `Close()` walks it in reverse; `Connect` also gained a `defer`
   cleanup for the partial-failure case (a later hop fails after earlier ones succeeded).
2. **#169 — a `goleak` check in the new Toxiproxy chaos test depended on a `sync.Once`-populated
   package-level baseline an unrelated test happened to populate first**, so the check passed only
   when run as part of the full suite and failed when run in isolation. Fixed with a local
   `goleak.IgnoreCurrent()` snapshot taken after the test's own setup completed, independent of
   execution order.

### Read this first

**A stale spec section is a starting hypothesis, not an instruction to follow literally — verify
against the real code first.** Phase 72's own checklist in `.SPECIFICATION/IMPLEMENTATION.md` said
to extract the circuit breaker out of `internal/transport/ssh` into a new
`internal/transport/resilience` package. Reading the actual code first showed the breaker already
lived in `pkg/remoteexec` (built after the checklist was written) and was already unexported/
encapsulated, so the concern the checklist was guarding against was already handled. Relocating
working, already-encapsulated code to satisfy a stale doc's literal file path would have been pure
churn. Decision, made explicit in the plan before any code was written: leave it where it is. This
matches the Architecture Mismatch/Map Verification protocol in `.AGENTS/AGENTS.md` — it existed for
exactly this situation.

**`AllowTcpForwarding no` is the default in the SSH test image** (`lscr.io/linuxserver/openssh-server`),
which silently breaks any hop-chain test relying on `direct-tcpip` tunneling until it's overridden.
Found by starting a real probe container and reading its `sshd_config` directly, not by guessing.
Fixed by mounting a `.conf` snippet at `/config/sshd/sshd_config.d/allow-tcp-forwarding.conf` via
testcontainers' `Files` field — verify this with a real throwaway `ssh -L` test before trusting it,
the same way this session did, if a future phase (73/75/77) touches this container setup again.

**`command | tee file` reports `tee`'s exit code, not the piped command's.** Cost real time this
session: a `make ci 2>&1 | tee log` was assumed to have succeeded because the harness reported exit
code 0, when the real failure was buried in the log body (a known-flaky `cmd/runner` container
test — confirmed via `flaky-packages.json` and five clean isolated reruns, not a regression). Always
read the log tail directly, or use `PIPESTATUS`/`set -o pipefail`, never trust a piped command's
reported exit code.

**`LOCALSTACK_AUTH_TOKEN` must be exported before a full `coverage-check`/`-race` run, or unrelated
AWS-backed packages report false regressions.** Unchanged gotcha from prior sessions:
`.IGNORE/.localstack.env`'s key is `token=`, not `LOCALSTACK_AUTH_TOKEN=` — it must be manually
translated before export, or LocalStack-backed tests silently skip rather than fail, and coverage
reads as a regression that isn't one.

**No commit without the user's own live word in the current conversation.** Held throughout — both
of this session's commits (`835431b`, `dc8e2df`) were made by the user, not the assistant, even
after the assistant offered drafted commit message text both times.

**Never use the Agent or Workflow tool to delegate without being asked, even with Ultracode on.**
Held throughout this session.

### Verification state

`go build ./...`, `go vet ./...`, `gofmt -l` all clean. `go test ./internal/transport/...
./pkg/remoteexec/... ./pkg/retry/... ./internal/lock/... ./internal/engine/... -race` clean —
proves the `retry.Do` migration is behavior-preserving. `internal/ent/migrate/parity_test.go`
clean (both dialects have the new `properties` field). `go test ./internal/archtest/...` clean,
including the new no-second-breaker/no-second-retry-loop rule. `make ci` and `make push-gate` both
run to completion; the only failure seen anywhere was the pre-documented `cmd/runner`
container-port-mapping flake (`flaky-packages.json`), confirmed not a regression via five clean
isolated reruns. `make gosec`/`make govulncheck` clean. Coverage: all touched packages at or above
their recorded floor; new `coverage-floor.json` entries added for packages crossing a boundary for
the first time.

### Next steps

Phase 72 is done; Phases 73-77 of Part XV (non-network endpoints + the full hostile-bastion proof;
NETCONF/RESTCONF/gNMI; WinRM; `internal/psdiag`; SFTP) are unstarted and out of scope for this
session. Each reuses Phase 72's breaker, retry loop, and hop chain rather than building its own —
read this section before assuming any of their own scope from `.SPECIFICATION/IMPLEMENTATION.md`
alone, per the "stale spec" lesson above; each deserves its own planning pass against the real
current code first.

## Previous session (Phase 23: The RRULE Scheduler)

**Branch `feature/Phase-23-RRULE-Scheduler`, off `main`. HEAD is `17757a0` (the Filter
Infrastructure merge). Everything below is implemented, tested and verified on top of that commit,
but UNCOMMITTED: no live go-ahead has been given this session, so this session never ran
`git commit`. A commit message is provided at the end of this section, per Phase 23's own final
checklist item.**

This session implemented **Phase 23: The RRULE Scheduler** end to end.

### What landed

**The recurrence engine (`internal/schedule/rrule`), hand-rolled, no new dependency.** Follows
`pkg/filters/cron.go`'s precedent. A deliberately bounded constraint set (FREQ MINUTELY..YEARLY,
INTERVAL, COUNT, UNTIL, WKST, BYDAY with ordinals, BYMONTHDAY, BYMONTH, BYHOUR, BYMINUTE, BYSETPOS)
with everything else refused at parse: SECONDLY, BYWEEKNO, BYYEARDAY, BYSECOND, RDATE, INTERVAL=0,
COUNT above a cap, an ordinal BYDAY under a frequency where it means nothing, BYSETPOS with nothing
to select from. Two typed errors so "you wrote this wrong" and "this is valid iCalendar and we still
will not run it" are distinguishable. EXRULE/EXDATE in `exclude.go`, with TZID honoured.

**AWX parity is EARNED, not asserted.** `tools/genrrulefixtures/gen.py` expands 36 rules with
python-dateutil (the library AWX schedules on) into a committed
`internal/schedule/rrule/testdata/awx_parity.json`. **Python is not a build or CI dependency** and
nothing in `make ci` runs it; regeneration is manual, like `go generate ./internal/ent`. This caught
two real defects a hand-written test would not have (see FAILURE_PATTERNS #164, #165 and
LESSONS_LEARNED #151): sub-daily frequencies took their time of day from DTSTART so `FREQ=HOURLY`
expanded every period to the same instant, and the walk originally ran in the target zone so DST
normalisation fed back into the iteration. The fix for the second was structural -- the walk now runs
in civil time and localises only at emission, reproducing PEP 495 fold=0, which is where Go and
dateutil genuinely disagree.

**Persistence.** Two ent entities. `Schedule` splits AWX's single rrule blob into rrule, timezone and
dtstart so the zone and anchor are queryable without parsing the rule. `ScheduleOccurrence` is the
audit trail AND the duplicate-fire guard: a unique index on (schedule, occurrence_at), claimed by an
insert BEFORE anything launches. `outcome` has three values, not two -- `claimed` is a real state so
a controller that dies mid-launch leaves something visible rather than nothing. Migrations generated
for BOTH dialects (sqlite 0016, postgres 0013); `go generate ./internal/ent` alone would have shipped
tables that never exist in a real deployment.

**The scanner** (`internal/schedule/scanner.go`) is shaped exactly like `dispatch.Reaper`: a
leader-gated ticker taking `isLeader func() bool`, so `internal/schedule` imports neither
`internal/election` nor `internal/lock`. It finally gates the `pleiades-scheduler-leader` lease
`cmd/controller` has elected and ignored since Phase 4. Missed runs COALESCE: one job for the most
recent missed occurrence, a durable skipped row for each earlier one, and a single counted row beyond
a cap so a recovery cannot become its own outage.

**Firing reuses the manual launch path completely.** `api.Dispatcher.LaunchScheduled` satisfies a
one-method `schedule.Launcher` port, so a scheduled run gets the same template resolution, credential
binding, job creation and JetStream publication a person pressing Launch gets. It refuses a template
bound to a prompted credential and refuses to replay a saved survey password -- both never stored, and
replaying one unattended forever is a larger version of what already stops a relaunch doing it once.

**API and UI.** Eight routes (`/schedules` CRUD, `/schedules/{id}/occurrences`,
`/schedules/preview`, `/zoneinfo`), new `schedule:read`/`schedule:write` scopes kept separate from
both `template:write` and `runbook:execute`. Preview returns each occurrence in local AND UTC.
`/zoneinfo` is served from a GENERATED allowlist (`tools/genzoneinfo`, 554 zones) built from the same
archive `time/tzdata` embeds, so a zone offered is a zone that loads; the allowlist is also the
save-time validator, checked before `time.LoadLocation` ever sees an operator string.
`internal/ui/resources/schedules` moved from `StatusDeclared` to `StatusImplemented`.

### Verification

- `TestReleaseGate_ExclusionAcrossDaylightSaving` asserts the gate's literal wording, and refuses to
  pass vacuously (it fails if the offset does not actually change across the ten occurrences).
- `cmd/controller/scheduler_release_gate_test.go`: **three real controller OS processes**, a real NATS
  container and one shared database, given one overdue schedule, produce exactly one job. ~70s.
- `TestSweepCoalescesMissedRuns`: a five-hour outage on an hourly schedule gives one job and four
  durable skipped rows.
- `TestConcurrentSweepsFireOnce`: eight concurrent scanners, one winner. Its fixture had to move from
  shared-cache in-memory SQLite to a WAL file, because the former made most workers fail on
  `SQLITE_LOCKED` before reaching the claim -- the test was passing for the wrong reason
  (FAILURE_PATTERNS #166).
- Fuzzing: ~10.5M executions across `FuzzParse` and `FuzzParseRuleSet`, no crash, no hang.
- **RULE 0 for the UI: the pages were rendered and read, not merely asserted to return 200.** That
  is what found the third bug below; a `200` proves a page did not crash, not that it contains
  anything.

### Three real bugs these gates found, all fixed

1. `Scanner.due` compared `LastFired` to `DTStart` with a strict `After`, so a schedule whose first
   occurrence IS its DTStart re-selected that occurrence forever.
2. **The Schedules create form rendered zero controls.** All eight fields declared `InList` and
   none declared `InForm`, so `Field.Writable()` was false for every one. The page returned 200 with
   a heading and a working Save button over nothing, and the entire conformance suite passed --
   `TestViewConformance_FormsRenderAccessibly` loops over `FormFields()`, which was empty, so every
   assertion in it passed vacuously. Fixed, and then closed permanently: that test now fails when a
   view offering Create declares no form fields, negative-controlled by reintroducing the bug and
   confirming it fails. FAILURE_PATTERNS.md #167.
3. The new uncascaded Template→Schedule edge made `DELETE /templates/{id}` answer an opaque 500 for
   a scheduled template. Now `launch.ErrInUse` and a 409 that names what is holding it.
- `internal/archtest/scheduler_test.go`: three structural assertions that election was consumed, not
  rebuilt, including that the controller actually wires it (FAILURE_PATTERNS #52's shape).
- `go test ./...` clean; `go test -race ./internal/schedule/...` clean; `make gosec` 9 pre-existing
  waived findings and **zero new** (two findings in the new generator were fixed at source, by giving
  it a fixed output path instead of one from argv, rather than waived); `make coverage` passes with
  new floors recorded for the three new packages; `make docs-lint` clean.

### One honest caveat

`make docs-gen-check` diffs the regenerated tree against **committed** HEAD, so it necessarily fails
while this work is uncommitted. The generated output itself is correct and idempotent: `gendocs` was
run, all eight routes are present in `docs/reference/schemas/openapi.json` and
`internal/api/wellknown/openapi.json`, and running it a second time produces byte-identical files
(verified by md5). It will pass on the commit.

### Commit message

```
feat(scheduler): Phase 23's RFC 5545 scheduler, proven against AWX's own recurrence library

Adds internal/schedule: an RFC 5545 recurrence attached to a template, evaluated by
exactly one controller replica, launching through the same dispatch path a manual
launch uses.

The recurrence engine is hand-rolled rather than a new dependency, following
pkg/filters/cron.go's precedent, over a deliberately bounded constraint set refused
at save time rather than at run time -- an unbounded rule reaching the scan loop
stalls every schedule in the deployment, not just its own.

Parity with AWX is earned rather than claimed: tools/genrrulefixtures expands 36
representative rules with python-dateutil, the library AWX itself schedules on, into
a committed golden file the tests assert exact instant equality against. Python is
not a build or CI dependency. Those fixtures caught two real defects no hand-written
test would have produced: sub-daily frequencies taking their time of day from DTSTART,
and DST normalisation feeding back into the expansion's own iteration state. The
second is fixed structurally, by walking in civil time and localising only at
emission, which also reproduces the PEP 495 fold=0 semantics where Go's time.Date and
dateutil genuinely disagree.

A schedule fires at most once per occurrence, and the guarantee is a unique index on
(schedule, occurrence_at) claimed before anything launches -- not leader election,
which runs a two-second lease with no fencing token and cannot promise it. Election
is consumed rather than rebuilt: the Scanner takes an isLeader function, exactly as
dispatch.Reaper already did, and internal/archtest asserts the package cannot even
see internal/election. This finally gates the pleiades-scheduler-leader lease
cmd/controller has elected and ignored since Phase 4.

Occurrences missed while nothing was leading are coalesced to one run, with a durable
skipped row for each that did not happen, so a four-hour outage does not become
sixteen simultaneous jobs and does not become a silent gap either.

Release gates: a recurrence with an exclusion rule produces the same ten occurrences
as AWX across a daylight saving boundary; three real controller processes against one
shared database produce exactly one job for one overdue schedule; a five-hour
simulated outage produces one job and four skipped rows.
```
