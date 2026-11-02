# Handoff Document

Rewrite the "Current Status" section when stopping mid-task or handing off, per `.AGENTS/AGENTS.md`.

## Current Status (this session)

**2026-11-02. Branch `feat/c2d-persisted-facts`, HEAD `5763c04f`, 3 commits on `origin/main` at `f2494f59`
(`5e8f3c06`, `77ebc6dd`, `5763c04f`). Phase 41 (Crawl milestone C2d), "Facts End to End", is committed with the
fp-reviewer's fixes and ticked 10 of 14. Not through `make pr-check` yet, not pushed, no pull request.** This session
also merged C2b as PR #103 (`fb2e328b`) and C2c as PR #104 (`f2494f59`).

### What works (each proven by a test that ran)

- **A real run keeps what its tasks emitted as the device's facts**, one row per (device, key, value), written only on
  a change, with a last-seen time per value (`facts.Hash`: SHA-256 over canonical JSON of `[key, value]`). The engine
  holds a port (`engine.FactRecorder`); a check, a failed task and a skipped one store nothing.
  `TestFacts_ARealRunStoresWhatItsTasksEmitted`, `TestFacts_NothingIsStoredFromACheckAFailureOrASecret`,
  `TestEngineReachesFactsOnlyThroughItsPort`.
- **Crawl:** `.pleiades/facts/<device id>.jsonl` under a per-device lock, read with `pleiades facts <host> [<key>]`.
  `TestCLI_FactsReleaseGate` (real binary, real sshd).
- **Walk:** the Runner publishes each task's facts on `pleiades.jobs.facts.<job>`; the Controller admits them with the
  journal's check, verifies them again (`facts.Verify`) and stores them in `ent.Fact`; served at
  `GET /inventory/devices/{name}/facts` and `/facts/{key}` (`inventory:read`).
  `TestFactsReleaseGate_AControllerJobStoresWhatItsTaskObserved` (real Runner, NATS and sshd; mutation-checked).
- **Review fixes** (FAILURE_PATTERNS 829 to 835): the Runner's executor now knows the dispatch's secrets, and a name and
  shape guard runs on both tiers (829); the file store locks, survives a torn line and writes through a unique
  temporary name (830); a late value no longer becomes current (831); big integers survive the database (832).

### Current blocker

None. Next is the gate.

### Open

- Owner questions: a value that changes every run (uptime) adds a row per run, with no retention; a value masked only
  at report time (`secret_mask`) is not in the fact guard's known-secret list; `file.remove`'s own check does not ask
  `remotefile.Removable` (carried from C2b).
- Fact ordering still reads the observation time, a clock (831; LESSONS 371). Two Controller replicas storing the same
  new value at once can each write a row.
- Phase 41 still open: the executor's read half (left to Phase 25), identity facts, the Release Gate item (coverage,
  pr-check, CI), and the encryption item, which Phase 5 closes.
- prscope still selects no reverse dependents (FAILURE_PATTERNS 631).

### Next, in order

1. `make pr-check` through the monitor, push, `make branch-containers`, then one pull request naming the failure-pattern
   classes (829 to 835) in its body. Merge only when its checks are green; then tick Phase 41's Release Gate item.
2. After the merge: pull `main`, delete merged branches, prune.
3. The triad version handshake, in argus, vuln-corpus and Groundconfig (uncommitted there).
4. Release 0.4.0 waits for the owner.

### Files changed this session

See `git log origin/main..HEAD`: `internal/facts` (with `entstore` and the per-OS locks), `internal/engine` (`facts.go`,
`executor.go`, `runbook_context.go`), `internal/adapters/native` (`facts.go`, `adapter.go`), `internal/api`
(`device_facts.go`), `internal/apispec`, `internal/clispec`, `internal/topology`, `internal/meshid`, `pkg/sdk/facts.go`,
`pkg/external/context.go`, the ent schema, generated code and migrations (SQLite 0042, PostgreSQL 0039),
`cmd/controller/main.go`, `cmd/pleiades` (`facts_cmd.go`, `facts_dir.go`, `run_pipeline.go`), both release gates,
`internal/archtest/facts_port_test.go`, docs 04 and 09, and the generated references. Records: `FAILURE_PATTERNS.md`
and `FAILURE_PATTERNS_ARCHIVE.md` (829 to 835), `LESSONS_LEARNED.md` and `LESSONS_LEARNED_ARCHIVE.md` (381),
`HANDOFF_DOCUMENT.md`, `HANDOFF_ARCHIVE.md`, Phase 41 in `.SPECIFICATION/IMPLEMENTATION.md`. Changelog fragment:
`persisted-facts.added.md`.

Lab: Unchanged: bsd-check and win-check are shut down. This branch's gates run against containers.
