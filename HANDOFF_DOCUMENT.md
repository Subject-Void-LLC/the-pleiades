# Handoff Document

Rewrite the "Current Status" section when stopping mid-task or handing off, per `.AGENTS/AGENTS.md`.

## Current Status (this session)

**Branch `feature/Phase-101b-Mesh-Identity`. 101b is built and release-gate proven; #205 and #206
are both fixed and proven; all of it is uncommitted.** The branch sits on `a06dff2` (101a) which is
pushed; nothing since is committed. No commit happens without the user's own live go-ahead.

### What is on the branch, in three independent pieces

**1. Phase 101b, the mesh identity mechanism.** `internal/meshid` (operator/account hierarchy on
`nats-io/jwt/v2`, Apache 2.0; account identity key offline, account SIGNING key online and
rotatable), `Issuer.Issue` minting short-lived user credentials (12h default) against `Grant`s
(`FleetRunnerGrant`, `ControllerGrant`), custody as the `MeshSigningKey` ent entity under the SAME
`EnvelopeService`/`MASTER_ENCRYPTION_KEY` (bound envelope, hook + interceptor, migrations
sqlite/0020 + postgres/0017), and `topology.WithCredentials` dialing with creds as BYTES, never a
file. `TestReleaseGate_OperatorModeMeshIdentity` passed in 12s against a real operator-mode broker
and was falsified: widening the Runner grant to `>` fails with "publishing to
\"pleiades.jobs.requested\" was permitted; a Runner can forge a job launch". The resolver property
is proven: a credential minted AFTER the broker started connects with zero broker config change.
101b's remaining closers (Adversarial Pattern Justification, Schema/Injection Hardening writeups,
Documentation Gate, coverage floors, commit message) have not been written into the spec yet.

**2. FAILURE_PATTERNS #205.** `dispatchDedupKey` now encodes through `topology.SubjectToken`, so
the Runner's duplicate suppression is client-legal for the first time. Proven against a real bucket
in `agent_dedup_container_test.go`, negative control included (the raw `jobID:deviceID` string is
rejected by the real client).

**3. FAILURE_PATTERNS #206, RESOLVED after having been reverted.** The recorded "shared-mode CAS
mystery" was three of `publishWithTTL`'s four callers still passing the raw `l.itemID`, so every
TTL-refresh publish went to a subject nothing read; the identity-encoder diagnostic that "proved
the refactor correct" made those wrong arguments accidentally right, and the `-x` probe tracked the
same missed sites, not the encoding. Full post-mortem in FP #206's Fix section and
`LESSONS_LEARNED.md` #170. The shipped fix: encode once at `tryAcquireOnce` via
`topology.SubjectToken`; the encoded key is a distinct `storedKey` TYPE, so passing an itemID where
a key belongs is now a compile error (verified by writing that exact mistake; the build refuses
it); `itemIDValid` retired; fuzz target strengthened to the total property (every itemID acquires
and releases cleanly, no allowance branches); `TestNatsLockKeyIsASingleSubjectToken` pins broker
state including that KeepAlive advances the ENCODED key's revision, the observable the missed sites
broke silently.

### Verified this session (each against real containers)

- Reconstructed the reverted #206 attempt from the transcript: churn deadlocks to its 5m timeout
  (reproduces the recorded collapse, worse). Switching ONLY the three missed sites: churn passes in
  27s. That pair is the diagnosis proven in both directions.
- Full `internal/lock` suite under `-race`: **passed, 32.6s, exit 0.**
- `FuzzLockAcquisition` 60s against a real broker: **31,289 execs, 0 failures** on the new total
  property. New regression test passes in 3.2s.
- `gofmt`/`go vet` clean on everything touched.

### Next step

Write 101b's closing gates into the spec, then `make ci` end to end (still never run this branch:
`test-race` full-suite, `test-repeat`, `govulncheck`, `helm-lint`, `templ-gen-check` remain
unproven), then the user decides commits.

### Loose ends

- **101c is now unblocked**: the per-device KV grant blocker was #206 and it is resolved. 101c
  still owns global enforcement, the 27 container-start migrations onto the shared helper, and
  revocation.
- **The shared test-broker helper is deferred to 101c** with the reason recorded in the spec.
- **Phase 78d (PFX/PKI) is planned and not built.**
- **Phase 96a/96d's "the bus is unauthenticated" statements are still true as written** until 101c
  flips enforcement; deliberately left alone.
