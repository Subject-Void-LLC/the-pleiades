# Handoff Document

Rewrite the "Current Status" section when stopping mid-task or handing off, per `.AGENTS/AGENTS.md`.

## Current Status (this session)

**Phase 101 (Mesh Identity) is COMPLETE at 31 of 31, UNCOMMITTED, on
`feature/Mesh-ID-NKey-JWT`.** The only open item is "Provide Commit Message", which is below. The
branch was cut from `ee4702a`, the merge of Phase 96d.

The phase started at 14 open items across three stages and closed all of them.

### What shipped

**101b's four items were bookkeeping.** The key hierarchy, `MeshSigningKey` custody, the auth
callout rejection and the resolver-shape proof had all landed under neighbouring items and still
read `[ ]`. Only one needed code: the auth callout rejection moved into `internal/meshid`'s package
doc, so it travels with the code rather than living in a roadmap.

**The test harness is one helper.** `testsupport.StartNATS` replaces 33 container starts across 9
packages; the deprecated `RunContainer` wrapper is gone and `go mod tidy` dropped the dependency.
The `WithCmd`/`-config` ordering hazard is unrepresentable rather than documented: `natsRequest` is
a pure function that is the only writer of `Cmd`, and `StartNATS` accepts no customizer, so there is
no second writer and therefore no order.

**Identity reaches the binaries.** `topology.CredentialsFromEnv` reads `NATS_CREDS_FILE`;
`WithCredentialSource` re-reads on every reconnect; `controller mesh init|issue|show` mints the
hierarchy and issues credentials; `internal/meshkey` is custody and is `MeshSigningKey`'s first
production writer. The chart gained `nats.auth` and `mesh.credentials`, compose gained an overlay.
Everything defaults OFF.

### The four findings that changed the work

1. **A long-lived Runner would have died at 12 hours.** `WithCredentials` parsed once and cached, so
   a refreshed credential was never presented; the broker evicts an expired connection and nats.go
   stops retrying. It would have looked like an idle Runner. `WithCredentialSource` fixes it, and
   the proof is a falsifiable PAIR: the expiry gate shows a fixed credential dying, the new renewal
   gate shows a source-backed one surviving, and swapping the source back for fixed bytes fails at
   6.6s.
2. **A renewal request would have been stored on the dispatch stream.** Written as
   `pleiades.mesh.renew` it fell under the stream's `pleiades.>` root and took the stream from 0
   messages to 1. Measured, not reasoned about. It is now `pleiades-mesh.renew`, and the hyphen is
   the only part of the name doing work.
3. **One of my own assertions could not fail.** The gate asserting the operator key is not in the
   database searched the file for the plaintext seed, which is sealed before it is written, so a
   planted leak passed. It is structural now, over the key's kind prefix. That same failed
   falsification uncovered a real protection nobody had designed: `meshkey.Save` refuses a
   non-account seed because it derives the public key through a kind check.
4. **The migration caused a real goleak regression**, and it was checked rather than written off as
   the known reaper flake, because the unmigrated file passed. `defer Terminate` ran before
   `defer goleak.VerifyNone` by defer LIFO; `tb.Cleanup` runs after all defers.

5. **A forgery test caught a forgery that had never been forged**, found only because a `-race`
   sweep ran it again: it flipped the LAST base64 character, which for an Ed25519 signature is four
   meaningless padding bits, so about one run in three the broker was handed a credential nobody
   had altered and correctly accepted it. This is `FAILURE_PATTERNS.md` #75 recurring in a new
   package. Flipping the first character instead: five consecutive runs green.

Recorded as `FAILURE_PATTERNS.md` 304 to 307 and `LESSONS_LEARNED.md` 221 to 224.

### What this does NOT do, and must be said plainly

Authentication decides **who may read the message stream, not what is written to it.** A dispatch
still carries the resolved credential its job runs with, and the fleet still shares one durable
consumer, so a stolen Runner credential still reads every dispatch for the fleet. That is unchanged
and it is the largest residual risk. Reference passing is Phase 105, which also carries the embargo
on calling this platform zero trust. `docs/10-running-in-production.md`'s new mesh identity section
states this as a list of what a compromised Runner can still do, not as a reassurance.

Revocation is an OFFLINE operation by construction: the revocation list lives in the account JWT,
which only an account or operator key can sign, and both are meant to stay offline. Measured
against a real broker, and the control was run: reloading an identical configuration does NOT close
the connection, so the eviction is the revocation.

### Verification run

Green: `go build`, `vet` under both tag sets, `gofmt`, `go mod tidy`, `gosec` (23 findings, all
individually waived; one new waiver for an env var NAME, and one G304 fixed properly with `os.Root`
rather than waived), `govulncheck`, `docs-lint`, `docs-gen-check`, `templ-gen-check`, `helm-lint`
(9 configurations, 28 refusals), `internal/archtest`.

Under `-race`: `meshid`, `meshkey`, `topology`, `testsupport` and `archtest` all green.

Packages under real brokers: `meshid` 46.6s, `meshkey` 0.03s, `topology` 38.9s, `testsupport`,
`lock` 9.3s, `event` 218s, `election` 13.1s, `runner` 22.9s, `cmd/controller` 102s, `cmd/runner`
153s, and `tests/e2e`'s wss traversal gate 47.2s.

**NOT yet run: `make ci` in full.** That is the remaining step before this is verified, and on this
machine it has to run alone.

### Architecture decision worth a second opinion

`internal/testsupport` now imports `testcontainers-go` from an ordinary file. Rather than add it to
`adapterAllowlist`, which means "a shipped binary may open this driver", `internal/archtest` gained
a separate `nonShippingPackages` set spelled with `testonly_test.go`'s own constants, plus
`TestNonShippingPackagesAreProvedNonShipping`, which fails if the set is empty or if any member is
reachable from production code. Both directions falsified.

### Next

Phase 101's successors are Phase 93 (Smart Hands, which rests on the proof that minting a credential
needs no broker configuration change) and Phase 105 (reference passing, which is what removes the
secret from the dispatch payload). Phase 106d owns turning these defaults on.

