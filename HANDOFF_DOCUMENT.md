# Handoff Document

Rewrite the "Current Status" section when stopping mid-task or handing off, per `.AGENTS/AGENTS.md`.

## Current Status (this session)

**2026-11-02. Branch `feat/facts-41b`, 8 commits on `origin/main` at `5987ba53`, plus the records commit: `4f329cc6`
(testgate notes), `e0869d8f` (facts lock-retry note and floors), `f5226276` (docs/14 drift), `189be213` (fact
declarations), `462786a6` (volatile facts, provenance, run order, chain), `45193c98` (`secret_mask` fix), `cf3597dd`
(docs and regenerated references), `5212a405` (review fixes). Not through `make pr-check` yet, not pushed, no pull
request.** PR #105 merged (`5987ba53`), completing C2.

### What works (each proven by a test that ran)

- **Phase 41's Release Gate is ticked (11 of 14). Phase 41b is ticked 7 of 8**; its Fuzz/Stress/Adversarial/Schema/
  Documentation/Release gate item waits for CI.
- `FuzzPrepareDeclared` and `FuzzVerifyChain`, 30 s each, clean. `BenchmarkFileStoreRecord` about 3.0 ms/op (about
  1.9 ms before the chain), `BenchmarkFileStoreRecordVolatile` about 0.37 ms, `BenchmarkFileStoreVerify` (1000 values)
  about 5.1 ms.
- Coverage: `internal/facts` 89.1, `internal/facts/entstore` 88.2 (floors raised), `pkg/collection` 100.
- Both release gates pass: `TestCLI_FactsReleaseGate`, `TestFactsReleaseGate_AControllerJobStoresWhatItsTaskObserved`.
- Review fixes, FAILURE_PATTERNS 836 to 840: a field a later `secret_mask` marks is never stored (836); the Controller
  holds batches to the embedded module catalog (837); times are truncated to the microsecond, proven on real
  PostgreSQL (838); equal order tokens fall back to the clock (839); CLI facts are ordered when each task finishes
  (840).

### Current blocker

None. Next is the gate.

### Open

- Owner decisions: (a) external Collection Landlock confinement has filesystem rules only, no network rules
  (`internal/loader/confine_linux.go`), so a Collection handed credentials has unrestricted egress (reasoned, not
  tested); (b) the Controller does not check a Runner's reported order token or Runner label, so a hostile Runner could
  pin a device's facts with a huge token (documented in docs/09); (c) the `secret_mask` residuals in 836 (same value
  under another key; `run.publish` best effort); (d) `file.remove`'s own check does not consult
  `remotefile.Removable` (carried from C2b).
- Known limits: `InRunOrder` is not transitive when orders of several sources mix (marked, not fixed); the archtest
  attributes emitters by `Invoke`'s file only; the fact-chain route's scope is not exercised by its test (like its
  siblings); a rolling upgrade from a #105-only build would leave unchained rows, moot since #105 was never released.
- Found, not fixed (same class as 837): `dispatch.methodsAdmit` (`internal/dispatch/worker_devices.go`) uses
  `collection.Lookup` and skips a method it does not find, and the shipped Controller registers no method (nothing it
  links calls `collection.Register` outside tests; `go list -deps ./cmd/controller`). So the Controller's capability and
  transport admission (Phase 117b, FAILURE_PATTERNS 557) checks nothing in the real binary; the Runner's preflight and
  engine still refuse. Reasoned from the code and the dependency graph, not tested. Needs an audit of every
  `collection.Lookup` the Controller reaches, then the module catalog (`entstore.CatalogIndex`'s approach) in their
  place, and a test in the Controller's own binary. Its own PR, right after this one.
- macOS lock ENOENT (`TestFileStore_ManyStoresCreateOneDevicesFilesAtOnce`): cause still unproven. This PR's
  fast-macos shards print the retry count through the new testgate note; then write its FAILURE_PATTERNS entry. The lab
  Mac timed out on SSH.

### Next, in order

1. `make pr-check` through the monitor, push, `make branch-containers`, then the pull request. Its body lists the
   failure-pattern classes (836 to 840) and carries no AI attribution.
2. Quiet checks loop, `record-actions`, merge only when green; then pull `main` and clean.
3. The Controller registry audit above (its own PR), then Phase 41c.

### Files changed this session

See `git log origin/main..HEAD`: `internal/facts` (with `entstore`, `factstest`, `kind.go`, `provenance.go`,
`chain.go`), `pkg/collection` (`facts.go`, `facts_index.go`, `manifest.go`), `internal/engine` (`facts.go`,
`executor.go`), `internal/journal/clock.go`, `internal/catalog/facts` and `internal/catalog/net` (declarations),
`internal/loader`, `internal/adapters/native`, `internal/api/device_facts.go`, `internal/apispec`, `internal/clispec`,
`internal/archtest/facts_declared_test.go`, the ent schema, generated code and migrations, `cmd/controller/main.go`,
`cmd/runner/main.go`, `cmd/pleiades` (`facts_cmd.go`, `run_pipeline.go`, `doc.go`), both release gates,
`tools/testgate` and `tools/internal/flakegate` (notes), `tools/gendocs`, the Makefile, `coverage-floor.json`, docs 04,
09 and 14, and the generated references. Records: `FAILURE_PATTERNS.md` and
`FAILURE_PATTERNS_ARCHIVE.md` (836 to 840), `LESSONS_LEARNED.md` and `LESSONS_LEARNED_ARCHIVE.md` (382, 383),
`HANDOFF_DOCUMENT.md`, `HANDOFF_ARCHIVE.md`. Changelog fragments: `fact-declarations.added.md`,
`facts-secret-mask.security.md`.

Lab: Unchanged: bsd-check and win-check are shut down.
