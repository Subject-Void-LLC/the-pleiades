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

### Next step

`make ci` end to end, which has still never run on this work.
