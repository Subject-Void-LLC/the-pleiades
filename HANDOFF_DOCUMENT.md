# Handoff Document

Rewrite the "Current Status" section when stopping mid-task or handing off, per `.AGENTS/AGENTS.md`.

## Current Status (this session)

**Branch `feature/Test-Isolation-the-Lost-Race-and-the-Unreached-Endpoint`, cut off `main`
(`bfdd9a2`, the merge of PR #25). Nothing is committed: every change below is in the working tree,
and the standing rule that the assistant never runs `git commit` without the user's own live word
was held throughout. `internal/archtest/testseam_test.go`, `internal/credstore/lostrace_test.go`,
`pkg/remoteexec/sharedseam_test.go` and `changelog/duplicate-name-field-error.fixed.md` are
UNTRACKED and need `git add`; `make ci` stays green without the first of them, because its absence
removes a rule rather than failing one.**

This session closed the three follow-ups the previous one deliberately left undone, plus the macOS
CI failure the user brought mid-session. Two of the three changed shape once re-derived from
source, which is the previous handoff's own instruction, and one of the four turned out to be nine
packages rather than one.

### What was done, in the order the commits should land

1. **The red macOS leg (`internal/catalog/facts`).** One test asserted a fact was PRESENT where its
   twelve siblings assert a VALUE, so it alone had no environmental guard and was the only one of
   thirteen to fail on a host with no `/etc/os-release`. It now compares values through
   `gatherOSReleaseValue`, which is both stronger on Linux and gives it the same skip its siblings
   have, and `TestGather_LinuxAnswersEverythingThisSuiteWouldOtherwiseSkip` makes that skip a
   FAILURE on Linux so the platform the evidence comes from cannot start skipping quietly.
   `FAILURE_PATTERNS` #187, `LESSONS_LEARNED` #158.

   The diagnosis handed to the assistant was wrong and is worth not repeating: it read the suite's
   use of the live host as an accident and recommended a static fake-facts map. That suite runs a
   real in-process SSH server against a real `/bin/sh` on purpose, says so in its own header, and
   the mock would have deleted the one thing the test proves (that the glob skips the COMMANDS, not
   the output). Reproduced without a Mac by hiding `/etc/os-release` in a mount namespace.

2. **Test isolation, nine packages.** `go test -count=2` failed in nine, 24 tests, 2 of them hard
   panics. Two causes: seven packages register into a process-global registry and never remove the
   entry, two leak fixture state (a never-closed shared-cache SQLite on a fixed DSN plus hard-coded
   record names, and a package-level spy). `pkg/registry` gained `SnapshotForTest`, six owning
   packages re-export it, and each offending test gained one `t.Cleanup`. `FAILURE_PATTERNS` #188,
   `LESSONS_LEARNED` #159 and #160.

3. **`internal/ui/resources` and `internal/ui/web`** got per-invocation names and a precondition
   reset. Note `uniqueName` counts rather than using `t.Name()`: `-count` does NOT make top-level
   test names unique, only subtest names.

4. **A duplicate name is now a field error, not a 500.** Found only because the `-count=2` failure
   forced the handler's real error into the open. Both writers on organizations and templates, on
   create AND update, now map their store's `ErrExists` to `view.FieldFault`. Changelog fragment
   added.

5. **`credstore.EnsureManagedType`'s lost race.** Confirmed and worse than described: the create
   arm returned the SAME `ErrExists` sentinel a genuine custom-type collision returns, so
   `ReconcileManaged` told the operator to rename a custom credential type that does not exist,
   and failed a startup that had succeeded. Now re-reads and routes through one shared
   `adoptExistingType`, so the raced and unraced paths agree by construction.
   `FAILURE_PATTERNS` #189.

6. **`transport.DockerExecEndpoint` deleted.** 32 lines, one file. `gopls` found exactly one
   reference, its own marker method. The reason recorded last session for keeping it was wrong:
   it is under `internal/`, so nothing outside this module could ever have imported it. No
   changelog fragment, per `changelog/README.md`: nothing a user can see changed.

### Two guards, and why the gate is at `-count=3`

`make test-repeat` runs the non-Docker set at `-count=3` and is wired into `ci` and `push-gate`.
`internal/archtest/testseam_test.go` forbids production code from calling anything named
`*ForTest`, which is what makes the seam safe to have as exported surface at all (an
`export_test.go` cannot be imported across package boundaries, and four of the seven packages
isolate a table `pkg/collection` owns). Both guards were negative-controlled live against the real
tree and restored from a scratchpad copy.

**The third iteration is not padding.** `-count=2` catches everything in item 2 and is VACUOUS
against a third cause found by going further: `pkg/remoteexec.Shared` memoizes one Runner per
Options for the process lifetime, its breaker counts consecutive failures with no decay, and one
`net.ssh.ping` spends three attempts against a threshold of five, so a dial-failure test passes at
1 and 2 and fails from 3. State that accumulates toward a THRESHOLD does not collide on first
repeat, which is the assumption `-count=2` encodes.

### Read this first

**An audit finding handed to you can be confidently wrong about the mechanism AND about the fix.**
The macOS diagnosis above was both. The previous session said the same thing about its own audit.
Re-derive before acting, every time.

**A fix can make another test weaker without failing it.** Cleaning up the leaked registrations
dropped `internal/inventory/syncplugin` below its coverage floor, and the cause was
`TestNames_IsSorted` asserting over an empty slice: it had only ever been testing a sort because
OTHER tests were leaking entries into the registry it read. It now registers its own. A coverage
drop after an isolation fix is a signal to look for that shape, not a number to re-baseline.

**Scope discovered mid-flight, and deliberately not taken.** A review of the UI fix found that 8 of
9 write ports in `internal/ui/resources` pass their port's error through raw, so the same 500 is
reachable on more than the two resources fixed here, and `view.FieldFault` has had exactly one
production caller in the whole module. Fixing the other ports is a real follow-up and is NOT done.

### Verification state

`go build ./...`, `go vet ./...`, `make fmt` clean. `make test-repeat` (the whole non-Docker set at
`-count=3`) clean. `internal/catalog/net/ssh` and `pkg/remoteexec` clean at `-count=8`.
`internal/credstore` clean at `-race -count=4`. `make arch` clean. Every new test was run against
the UNFIXED code first and observed to fail with the exact reported symptom.

Coverage: `internal/credstore` 87.3 to 88.1 and `pkg/remoteexec` 97.5 to 98.0, both floors
ratcheted; every other touched package back at or above its floor. `internal/catalog/cloud/aws/{ec2,s3}`
report below floor in any run without a LocalStack token, which is environmental and pre-existing.

`make ci` had not been run to completion at the time of writing.

### Next steps

The user reviews and commits. Known open items: the 8-of-9 UI write ports above;
`transport.LocalSocketEndpoint`, now used only by two negative-control test fixtures and one step
behind `DockerExecEndpoint` on the same path; and a PRE-EXISTING duplicate entry number in
`LESSONS_LEARNED.md`, where two different rules are both numbered 13 (lines 15 and 19). That last
one is the second instance of the defect the previous session's handoff describes finding by
grepping the merged index, and it is left alone deliberately because the rule against renumbering
is explicit.

## Previous session (Phase 72: Transport Foundation)

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
