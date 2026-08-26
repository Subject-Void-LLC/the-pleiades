# Handoff Document

Rewrite the "Current Status" section when stopping mid-task or handing off, per `.AGENTS/AGENTS.md`.

## Current Status (this session)

**Branch `feature/Phase-101c-Mesh-Enforcement`, cut from 101b's tip `0e91ca0`.** 101b's three
commits are on the branch below it and were pushed. 101c's work is described below.

### What 101c found, which is the important part

An eight-sweep read-only recon over the real source, then measurement against a real broker, found
that **Phase 101b shipped five defects and one missing fact, none of them visible in any test.**
Under a real operator-mode broker with JetStream on, nothing worked: the Controller died
provisioning the stream, the Runner authenticated and received no job ever, its heartbeat was
withheld so the fleet reported unhealthy, and a failed `job.requested` vanished silently.

The full incident is `FAILURE_PATTERNS.md` #207 and the rule is `LESSONS_LEARNED.md` #171. In short:
three grants ended in `.>` where nats.go's own templates end at the consumer name, and `>` matches
one or more tokens and never zero; `ControllerGrant` put `>` in a non-final token, which is not a
wildcard position at all; the Controller's dead letter subject was granted to nobody; and every
account the platform minted had JetStream DISABLED in its claims, because
`jwt.NewAccountClaims` defaults it off.

The sixth item was not a defect but a fact nobody had: **operator mode refuses to start JetStream
without a system account, and that system account must not itself have JetStream enabled.** Both
server refusals are quoted verbatim in `meshid.NewSystemAccount`.

**Why nothing caught it.** 101b's Release Gate ran the only NATS container in this repository that
omits `-js`, and every defect lived on the JetStream control plane. Its unit test compared the
grant against a hand-written restatement of the grant, so it asserted the grant equalled itself.

### What is on the branch

- **`internal/meshid`**: `consumerAPI` emits the exact driver subject; `consumerCreateWithFilter`
  pins the Runner's create to the fleet filter, which also closes a real escalation
  (CreateOrUpdateConsumer is an upsert, so a Runner able to create with any filter could widen the
  shared durable to `pleiades.>` and read every device's plaintext credentials); `ControllerGrant`
  names its stream operations; the Controller's dead letter subject is granted; `NewAccount`
  enables JetStream; `NewSystemAccount` is new.
- **Two Release Gates.** `TestReleaseGate_TheRealControlPlaneRunsUnderAMintedIdentity` drives the
  REAL functions (ProvisionStream, BindLockBucket, DispatchConsumerConfig, a real consumer create,
  a real FetchNoWait, a real dispatch published, pulled and acked) under minted credentials against
  a JetStream-enabled operator-mode broker: **passed 12.8s**, falsified against two defects
  individually, each failing at the right act with the right message.
  `TestReleaseGate_AnExpiringCredentialEvictsALiveConnection` answers the question the phase said
  to measure rather than assume: **expiry is enforced on a LIVE connection**, and the connection
  ends CLOSED rather than reconnecting forever, because nats.go abandons reconnection after the
  same auth error twice regardless of `MaxReconnects(-1)`. Passed 8.4s.
- **The unit test was rewritten from equality to MATCHING** against the subject the driver sends,
  with the matcher's own semantics pinned in a table including the two cases that caused the bug.
  `ControllerGrant` gained the test it never had. Falsified: restoring the old suffix fails naming
  both operations.
- **Fuzz and benchmark added** (AGENTS.md requires both before a Release Gate and `internal/meshid`
  had neither): `FuzzIssue` hardens JWT claim construction from a caller-supplied name, 31,289
  execs clean; `BenchmarkIssue` 271us/op, `BenchmarkNewAccount` 173us/op.
- **Coverage regressions from 101b, found and fixed.** 101b did not run the ratchet before
  committing. `internal/topology` had fallen 95.8 to 90.0 because the credential dial path was
  tested only from `internal/meshid` and coverage is per package; real in-package tests took it to
  94.8. `internal/crypto` and `internal/ent` likewise. Remaining gaps are recorded as deliberate
  downward floor adjustments with written reasons in `coverage-floor.json`. Ratchet now clean
  across 203 packages.
- **Two prose corrections**, both things that were already false: `dial.go`'s ClosedHandler said it
  "should never fire at all" (the expiry gate observes it firing), and
  `docs/10-running-in-production.md` said a runner identity story "does not exist yet".
- Changelog fragment `mesh-identity-enforcement.added.md`.

### What 101c has NOT done, deliberately and explicitly

**Enforcement is not on anywhere.** No chart value, no compose change, no `NATS_CREDS` env var, and
the 31 container starts across 9 test packages are untouched. So Phase 96a's and 96d's "the bus is
unauthenticated" statements are STILL TRUE as written and were deliberately left alone; the recon
settled that they belong to whichever stage flips the default, not to the stage that builds the
capability.

The remaining 101c items, in dependency order, are in the spec: the shared test broker helper in
`internal/testsupport` (taking `testing.TB`, since four of the 31 sites are benchmarks or fuzz
targets), the migration of those 31 sites, and switchable enforcement in the chart and compose
file. Two measured constraints govern that work: `testcontainers.WithCmd` REPLACES the command
while the nats module's `WithConfigFile` APPENDS `-config`, so the wrong order at any site boots an
unauthenticated broker that passes every test; and the compose healthcheck is documented to fail
under authentication in its own comment.

Revocation is specced but not built: a revocation entry keys on the user public key with a UNIX
seconds watermark, coverage only widens, and signing one needs a key the Controller does not hold,
since `meshid` signs the account with the OPERATOR key which must stay offline.

### make ci, run end to end at last, and what it actually said

**Every target passes, but not in one invocation, and the distinction matters.**

`make ci` was run end to end for the first time on this work. It **failed once at
`test-integration`**, and the identity of the failing package was **lost**, because the invocation was
piped through `tail -40`. That is the exact mistake this document warned about two sessions ago in
its own words ("a piped exit code is not evidence"), repeated by the session that wrote the warning.
The pipe both truncated the failing package off the top of the output and reported `tail`'s exit
status, so the run looked green and was not.

Re-running the same target alone, capturing the real exit code: **`REAL_EXIT=0`, 152 packages ok,
zero FAIL.** The failure did not reproduce, which is the known container-contention flake
(`FAILURE_PATTERNS.md` #61) that `flaky-packages.json` exists for. It is recorded here rather than
waved away because the specific package was never identified, so it cannot be checked against that
file's list.

`make ci` stops at its first failure, so the targets AFTER `test-integration` never ran in that
invocation. They were each run separately afterwards with real exit codes captured:
`govulncheck` 0, `helm-lint` 0, `templ-gen-check` 0, plus `gosec` (9 findings, all waived),
`docs-gen-check` clean, `docs-lint` clean (207 files), `arch` ok, and `coverage-check` clean across
203 packages. `build`, `vet`, `fmt`, `test-race` and `test-repeat` all ran and passed inside the
`make ci` invocation itself, since they precede `test-integration`.

**A separate finding worth acting on: the pre-push hook was never installed in this clone.**
`core.hooksPath` was unset and `.git/hooks/pre-push` did not exist, so the `make push-gate` that is
supposed to gate every push has never run here, on any push, by anybody. `make hooks` has now been
run, so the next push is gated. Every push before this one went out ungated.

### Next step

Phase 40 is planned; see below.

### Phase 40, planned and started

`.SPECIFICATION/IMPLEMENTATION.md` now carries Phase 40's measured starting position (seven parallel
read-only sweeps) and its Pattern Entry Gate. Two results change what the phase is:

**`design/rollback_journal_design.md` is history.** It proposes a state-restoration journal whose
rollback engine interprets old and new values. What shipped instead is task-shaped:
`sdk.RecordInverse` records `{FQCN, Params, Description}`, a directly runnable task, across 35 call
sites covering all 43 reversible methods. A rollback engine is a loop feeding those back through the
dispatcher. The note's Layer 3 is already decided, and better. Nothing reads any of it yet, so Phase
40 writes the first reader, and on the Walk tier the inverse is currently computed and discarded in
the same function (`internal/adapters/native/adapter.go:217`).

**The blocking prerequisite is resolved: the journal reads no revisions at all.** Not on cost
grounds but structural ones: `wireDevice.History()` is hardcoded `nil` and `wire.DispatchPayload` has
no history field, so a `Revision`-based journal cannot reach a Walk-tier task at any price. It is a
new entity written from `engine.NodeResult` at the executor seam.

`JournaledCapable`/`RollbackCapable` are declared NOT to be built, a deliberate departure from the
phase's own checklist: a Collection cannot declare a capability at all, and both questions already
have answers in `collection.Reversibility` and `sdk.RecordDiff`.

Six design decisions remain open before code (entry shape and store, the Crawl-tier sink, the
Walk-tier carrier given that the Runner has no database, masking, and the run id). The masking one
is first, because a journal would receive plaintext property values and plaintext resolved params,
and `Revision.old_value`/`new_value` is already an undisclosed plaintext store of encrypted-at-rest
data on both tiers.
