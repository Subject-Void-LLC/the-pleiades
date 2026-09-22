# Handoff Document

Rewrite the "Current Status" section when stopping mid-task or handing off, per `.AGENTS/AGENTS.md`.

## Current Status (this session)

**Phase 84 (upgrade, rollback and restore) is COMPLETE and UNCOMMITTED on branch
`feature/upgrade-rollback-restore`, cut from main at `642e626`.** The roadmap reads 12 of 12 with no
open gate. The commit message is at the bottom of this section; nothing is committed, by the user's
standing rule. Part XVI (the agentic control plane, roadmap planning only) is paused mid workflow and
resumes from `part16-work/RESUME.md` in this session's directory under
`~/.claude/projects/-home-noot-auto-roboto/`.

### Machine rule (saved to memory)

This box (VENGEANCE) has 9.7 GiB RAM and 20 CPUs. At most TWO concurrent workers. Every heavy command
runs under `~/.local/bin/capped <limit> <cmd...>`, a `systemd-run --user --scope` with a hard
MemoryMax and no swap by default. Never add MemoryHigh: the cgroup is charged for page cache, and a
MemoryHigh band throttled `make gosec` 253,604 times with no progress. `make gosec` needs about 4.5
GiB. Docker's own containers share the same VM, so the kind gate and `make ci` run alone.

### What `make ci` says, honestly

**It has NOT passed as one strict run on this machine, and the reason is the machine, not the tree.**
Every target passes; each full run loses a different container package to Docker contention
(FAILURE_PATTERNS 61), and `coverage` tolerates nothing by design.

- `build`, `devtools`, `vet` (both tag sets), `fmt`, `tidy-check`, `test-repeat`, `gosec` (22
  findings, every one individually waived), `govulncheck` (0 reachable), `docs-lint`,
  `docs-gen-check`, `helm-lint`, `templ-gen-check`: all pass.
- `test-race`: one failure, `internal/event`'s toxiproxy container failing to provision. That test
  passed alone in 10s. No change in this branch touches that package.
- `test-integration`: two failures, both real defects in tests, both fixed and rerun (below).
- `coverage`: three container packages failed in the first run and two different ones in the second,
  every one a container that would not start or a first NATS connection that timed out. The tolerant
  run (`go run ./tools/coverage-check -tolerant`, what `push-gate` uses, which reruns a failure alone
  before believing it) completed and evaluated every floor. It caught one real regression, now fixed.
- No gate receipt exists or can: the tree is dirty until this work is committed.

### Phase 84, the evidence behind the ticks

Each gate below ran in THIS tree, alone, on real infrastructure.

- **Concurrency.** `TestApplyAcrossRealProcesses` at 2, 4, 16 and 32 real processes per dialect;
  `TestControllersStartedTogetherOnAnUnmigratedDatabaseAllServe` at 2, 4 and 8 real controller
  binaries against one PostgreSQL; `TestApply_APartitionedWinnerReleasesItsClaim` (a real Toxiproxy
  partition, the claim released after 1m10s); `TestApply_ARoleTimeoutDoesNotEndTheClaimWait` (fails
  after 1.006s on the exact pre-fix order).
- **Compatibility window.** `compat_shape_internal_test.go` (every migration's real effect per
  dialect against its declaration); `TestControllerServesWithinItsWindowAndStopsPastIt`;
  `TestUpgradeGate_ThePreviousBuildKeepsServingWhileThisOneMigrates` (110 requests to the previous
  build during a migration held three seconds by a SHARE lock, none failed, 261 more through the
  drain; with the lock removed its end-of-hold check fails).
- **Upgrade paths.** Compose: `TestUpgradeReleaseGate_ComposeUpgradesAndRollsBack` (99s), which now
  also proves a backup it cannot write stops `make up` before anything migrates. Helm:
  `TestUpgradeReleaseGate_HelmUpgrade` on kind (233s: Recreate 92s with a 31s outage by design,
  RollingUpdate 94s with a previous-build pod Ready while this build's existed). Binary: the upgrade
  gate above. Install unaffected: `TestPackagingReleaseGate_KubernetesInstall` (339s).
- **Fuzzing, this tree.** `FuzzCheckGate` 1,759,988 executions clean; `FuzzVersionNumber` 1,178,398
  clean; `FuzzApplyTamperedHistory` 4,358 clean. The two inputs those found on 2026-09-21 are kept as
  seeds in `testdata/fuzz`.
- **Coverage.** `internal/ent/migrate` 87.2% against its 86.5% floor; `internal/backup` 88.1%.

### Fixed today, after the adversarial review and the gate runs

Each with a control that fails without the fix, where one can be run:

- **FAILURE_PATTERNS 293:** the partition chaos test discarded the winner's error, so any early
  failure of that process read as "no session ever ran pg_sleep" after 30 seconds. It now reports the
  winner's own error at once.
- **294:** `TestGatherCheck_OnlyReads` compared `ansible_uptime_seconds` between two reads and failed
  whenever they straddled a second. Uptime is now compared as a clock; three mutations confirm the
  rest is still exact.
- **295:** the Helm setup gate read the key-record line from `kubectl logs`, which shows the running
  container only. Every Helm install restarts its controller about three times, because it exits
  until the database's Service name resolves (main does the same), so the container that recorded the
  key could already be gone. The gate now reads the `encryption_keys` row, and logs restart counts
  and the previous container's exit.
- **296:** `SECURITY_ATTESTATION.md` claimed "if the backup fails, nothing is upgraded" with no test
  behind it. The compose gate now runs `make up` against a `BACKUP_DIR` this user cannot write;
  control: with the recipe's guard replaced by `|| true`, the upgrade proceeded and the gate failed
  in 47s. That control entry now also says where the rollback proof is narrower than the documented
  path.
- **LESSONS 215:** `tests/e2e` took 1099s of the 20 minute per-package timeout, and a package timeout
  panics without running any cleanup, stranding a kind cluster and a compose stack. Raised to 30m in
  all three copies (`Makefile`, `tools/coverage-check`, `tools/testgate`), with a control proving the
  equality test catches drift.
- **Coverage ratchet:** `internal/ent/migrate` had fallen to 85.0% because this phase's
  `PlanForNewDatabase` was used by `cmd/controller` and tested nowhere in its own package. It now has
  a test requiring it to answer exactly what an empty database answers; control: a plan missing one
  migration fails it.

Also corrected: a comment in `tamper_fuzz_internal_test.go` claimed a startup schema check was
"recorded as its own later work". Nothing records it, so the comment now says it is not built.

### Known and deliberately left

- **A planted history row is believed.** A row claiming a migration of this build that was never
  applied makes Apply skip it and succeed onto a schema missing it. Only someone who can already
  write every table can plant one. The check that closes it (comparing the live schema with the
  history at startup, as restore already does) is NOT built and is owned by NO phase. It is stated in
  Phase 84's Fuzz item; giving it a phase is a decision for the user.
- **No new contract can ship** until the apply-time guard that refuses to contract while an older
  controller runs is built (`TestANewContractNeedsTheGuard` is the tripwire).
- **A browser session** is asserted across an upgrade only by the compose gate. The Helm and binary
  gates assert the database rows and an API token minted before the upgrade, not a browser.
- **The compose rollback** follows the documented path only once the previous release is itself a
  Phase 84 build; the TRANSITIONAL branch is marked and dated.
- **`gosec-waivers.json`'s header inline-suppression count is stale** (it says 56 to 58; the tree has
  97 outside tests). It has been stale since 2026-08-15 across many phases; not this phase's doing.
- Earlier deliberate omissions stand: the dirty check in `previousRef` ignores untracked files; a
  crash-orphaned `.pleiades-new-*.db` temporary; a SQLite history key declared `ON CONFLICT IGNORE`;
  a clone wedged on a live owner is never recovered (a sync timeout is new scope).

### Part XVI, the agentic control plane (roadmap planning, no code)

Paused deliberately while the Docker gates ran. Nothing is written into IMPLEMENTATION.md or PLAN.md
yet. `part16-work/RESUME.md` holds the exact workflow resume call, `args.json` beside it (the cache
matches only identical arguments), and the post-workflow steps: write Part XVI (107a to 107l), apply
the Phase 71 correction and the PLAN.md addenda, then confirm the tracker adds no problems. Current
baseline, measured after Phase 84's edits: phases without an Implements line are 12, 70 and 96d;
security summary problems is 4; IMPLEMENTATION.md holds 35 em dashes, all pre-existing.

### The commit message

```text
feat(controller): upgrade and roll back a schema change (Phase 84)

Any number of controllers may now start against one database at the same
instant. Each migration's transaction records its version before it runs a
statement, so a second starter waits on that uncommitted row, fails on it
holding nothing, reads the history again and carries on: the same
lose-then-reload reasoning internal/tlscert applies to certificates, with
the one difference that a loser here waits, because two runs of a migration
are not interchangeable. The claim wait is explicitly unbounded, since a
role's statement_timeout would otherwise end the one wait that must never
end, and a winner cut off by a partition is released by the server's
idle-in-transaction timeout rather than by TCP keepalive hours later.

The schema now has a written, enforced compatibility window. A migration
expands by default; one that removes or narrows is a contract, declared with
the oldest build that can still serve after it, and every applied migration
records that floor. A build refuses a database past its floor at startup and
stops serving one that contracts under it, so a rolling upgrade overlaps two
builds and a rollback within the window leaves the database alone. Three
things enforce this rather than stating it: a shape test comparing each
migration's real effect per dialect with its declaration, a gate checked at
every start and every heartbeat, and an upgrade gate that runs the previous
release's real binary against the newly migrated schema while this build
migrates it.

The controller binds its listener and answers probes before it opens the
database, so a long migration is no longer a port that does not answer. It
drains for SHUTDOWN_DRAIN after reporting itself not ready, and the chart
derives its termination grace period from that. Controllers record a
heartbeat, and a sync is swept only when no live controller owns it, which
is what a rolling upgrade's new pods used to get wrong.

controller migrate --plan answers what an upgrade would do without doing it,
and compose's make up acts on its exit code: it stops the controller and the
runner, takes a backup, and starts the new build only if that backup
succeeded. make -n up, up-plan and restore now refuse rather than stopping a
live stack, and make up rebuilds the runner image it had been leaving stale.

Proven on real infrastructure: 2, 4, 8, 16 and 32 starters against one
database; a partitioned winner's claim released by the server; the previous
release serving 110 requests without a failure while this build migrated
under a held lock; a compose stack upgraded, refusing an unwritable backup,
and rolled back; a kind cluster upgraded by Recreate and by RollingUpdate;
and the migration history fuzzed against an independent oracle and through
the whole apply path over tampered histories.
```
