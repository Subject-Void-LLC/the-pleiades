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


## Release versioning, made consistent (2026-09-23, same working copy)

**The defect was in the tracker's ordering, not in the version numbers and not in the roadmap's
order.** The dashboard showed `Phase 35 v0.3.0` at `#5` directly after `Phase 28 v0.5.0` at `#4`,
which reads as the release going backwards. Measuring first changed the fix twice.

What was checked and found sound: all 124 phases carry a `**Version:**`, all 124 parse, and no phase
ships before a phase it depends on. Reordering `IMPLEMENTATION.md` by release was considered and
rejected: it moves 118 of 124 sections and fragments 16 Parts into 44 runs. Keeping every phase where
it sits and forcing the file to read monotonically was also measured, and collapses 119 of the 124
phases into one release, which is arithmetic rather than judgement.

What was actually wrong: `todo_order()` ranked on `(in progress, phase number)` and never looked at
the release, so the queue opened with the v0.5.0 AWX parity block because Phase 24 is the lowest
number nothing blocks. 46 of 67 unfinished phases sat below a release the queue had already passed.
`rank()` now takes the release first, and the cycle break is scoped to the lowest pending release so
the mutually dependent 75/76 pair stops being exiled past Phase 100. 46 regressions became 0. No
version changed, no phase moved, no phase split.

### What else this session changed

- **The release ledger** replaces the 2026-09-22 resequencing note at the top of `IMPLEMENTATION.md`:
  one row per release, anchored to `PLAN.md` Section 7's tier ladder, with the v0.4.0 rationale and
  the v1.0.0 boundary paragraph carried over verbatim. A `### Release milestones` legend sits beside
  `### Dependency keys`, which `**Version:**` had never had.
- **`summary.version_problems`** in the tracker: missing or malformed version, a release with no
  ledger row or a row with no phases, a phase shipping before a dependency, a working order that
  moves backwards, and a finished phase depending on an unfinished one. It reports 0 today, and the
  page shows a banner when it does not. Eight new tests plus two in `RoadmapParsing`.
- **Phase 73's dependency key** lost `Phase 77`. Phase 73 is 25 of 25 done and Phase 77 has not
  started, which the new completion check caught. Reading Phase 73 showed both mentions of 77 are
  comparisons ("alongside Phase 77's SFTP", "the same reasoning Phase 77 applies"), and
  `pkg/capability/capabilities_network.go`'s own comment says `pkg/tftpxfer` already implements
  `FileTransferCapable` and Phase 77's SFTP "will join". The key was never a need.
- **`buildinfo.CurrentRelease = "0.2.0"`**, and the catalog's engine constraint derives from it. See
  `FAILURE_PATTERNS.md` 312: `>=1.0.0` on all 75 manifests would have been refused by the first
  release build.
- **`cmd/controller`'s `serviceVersion`** reads `buildinfo.Version()` instead of a hardcoded
  `v0.1.0-alpha`.
- **The versioning policy** is published in `docs/13-releases-and-stability.md`, which said "Not
  decided yet".

### Correction worth carrying

Phase 14's struck-through relocated item was read mid-session as a stale checkbox holding the phase
at 14 of 15. It is not: the tracker classifies a struck-out item as `withdrawn` and excludes it from
work counts, so Phase 14 is `done` at 14 of 14 and always was. The ad-hoc count that produced the
claim was the thing that was wrong.

### Next

`Phase 77 (SFTP/SCP)` is now `#1` in the working order, and it is the last open phase in v0.2.0.
Closing it makes v0.2.0 cuttable, at which point `buildinfo.CurrentRelease` moves to `0.3.0` in the
same change that opens that line. Nothing stamps a version at build time yet: the `Makefile` has no
`-ldflags -X buildinfo.version` release target, which Phase 20 owns.
