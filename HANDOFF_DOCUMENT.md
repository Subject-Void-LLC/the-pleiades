# Handoff Document

Rewrite the "Current Status" section when stopping mid-task or handing off, per `.AGENTS/AGENTS.md`.

## Current Status (this session)

**2026-11-06. PR #123 (`fix/facts-churn`) merged as `56538cf4`, every check green the first time. Branch
`fix/open-patterns-batch2`, in the main clone (the worktree the owner allowed is removed), nine commits on
`56538cf4`: `8a7b91fd` (448), `5d468d73` (560), `4f8a30fe` (551), `62033dae` (systemd units), then the review fixes
`501f7196`, `b39700eb`, `b4a32d91`, `07929043`, `b4338348`. Not yet through pr-check, push or a pull request.**

### What works

- **Three open failure patterns are fixed, each with its cause measured.**
  - 448: XNU gave pin/tftp's dual-stack client socket a port an IPv4 loopback socket held, and that socket took the
    replies. An IPv4 server's client is now bound to its route's source address (`pkg/tftpxfer/localaddr.go`).
    `TestTransfersSurviveIPv4SocketsHoldingManyPorts` (9 of 40 Gets timed out on the Mac with `main`'s code).
  - 560: a Docker Engine bug fixed in 28.3.0 (moby issue 50133); CI runs 28.0.4, this machine 29.8.0. `settle` after
    start, stop and restart. `TestStop_TheListShowsTheStopWhenTheMethodReturns` (200 of 200 failed on 28.0.4 with
    `main`'s code).
  - 551: the rollback planner dropped a later run's tasks that failed during their action. They now refuse unless
    `--despite-run`, as kind `unknown_effect` with `unknown_effects`, on the CLI and the Controller.
    `TestCLI_RollbackReleaseGate/G2_a_later_run_that_failed_during_its_action_refuses`.
- **systemd per-login units (954)** are left out of the services facts:
  `TestCLI_ServiceFactsHoldStillWhileLoginsComeAndGo`.
- **Review fixes** 955 to 958, found by fp-reviewer and a second model's review (rv2).
- Each new test is red on the old code and green on the fix, with and without `-race`. Exceptions:
  `TestResolveServer_TakesTheIPv4Address` and the mapped-literal subtests guard a regression only; the many-ports
  test's latest change is a timing and a skip.
- Coverage (floor in brackets): `internal/rollback` 94.6 (92.6), `pkg/tftpxfer` 94.9 (93.5),
  `internal/catalog/facts` 100 (100), `internal/apispec` 100, `internal/api` 98.4 (98.4), `internal/catalog/container`
  79.9 (no floor).
- Not run here yet: the whole `internal/archtest` and the `cmd/pleiades` gates; pr-check's census runs them.

### Current blocker

None. Next is pr-check for this branch.

### Open

- (a) Owner design calls: a later run killed mid-task before its seal that wrote no journal entry is never counted
  by the rollback planner (551); a later rollback run skipped as "cancels out" when it ran after the target (957).
- (b) 560's likely sibling: `Session.RunToExit` and `Session.RunOnce` (`pkg/containerengine`) wait on
  `WaitConditionNotRunning`; no caller lists right after, but before Docker 28.3.0 a later task's list could see a
  stale state. Not changed.
- (c) Native fact names, decided by the owner on 2026-11-06: no Ansible names in stored keys, dotted native families,
  flat keys, the device's own lowercase IDs, Ansible names only in the converter's mapping, and a case-sensitive
  `fact:` selector with an opt-in case-insensitive flag. The approved table and 12-step work list are in
  `.SPECIFICATION/FACT_NAMES.md` section 4.4 (internal, never cite publicly). Open: its phase id. Two PRs (keys and mapping, then the
  converter rewrite), before 41d.
- (d) The backlog in memory `open-fp-fable-backlog`: 690, 483, the BSD and macOS release, Ansible fidelity gaps.
- (e) 953 is still open; its Groundconfig entry `knowledge/unreviewed/virtualbox_whpx_guest_restart_boot_hang.txt`
  awaits the owner's review.
- (f) `stash@{0}` is the GNS3 work set aside on purpose (`lab-captures-129b`, 2026-10-25): never drop it.
- API credit: about $46 left, expiring 2026-11-08. Spent on this branch: research r448 $0.88, r560 $0.26, r551 $0.28,
  rsvc $0.21, review rv2 $0.90.

### Next, in order

1. `make pr-check` through the monitor, push, `make branch-containers`, the pull request. Merge only when green,
   `record-actions`, pull `main` and clean.
2. The fact-name rename, two PRs, before Phase 41d.
3. Phase 41, 41b to 41g, then 117b, 117c, 105, then release 0.4.0 (no tag until the owner says).

### Files changed on this branch

`pkg/tftpxfer/` (`tftpxfer.go`, `localaddr.go` (new), tests), `internal/catalog/container/` (`lifecycle.go`,
`stop.go`, `docs.go`, settle tests, harness), `internal/rollback/` (`history.go`, `later_run.go` (new), `plan.go`,
tests), `internal/api/dispatcher_rollback.go` and its test, `internal/apispec/template_endpoints.go`,
`internal/catalog/facts/` (`gather_services.go`, `gather_outcome.go`, `gather.go`, tests),
`internal/forge/catalogdata/` (`collections_gating.go`, `collections_container.go`), `internal/clireport` schema test,
`cmd/pleiades/` (`services_facts_release_gate_test.go` (new), a rollback gate case), `docs/10-running-in-production.md`,
and the regenerated `docs/reference` and `internal/api/wellknown`. Records (uncommitted): `FAILURE_PATTERNS.md` and its
archive (448, 551 and 560 closed; 954 to 958), `LESSONS_LEARNED.md` and its archive (405 updated; 406, 407),
`CI_LESSONS.md` (29), the Actions ledger (448 and 560 annotated), `HANDOFF_DOCUMENT.md`, `HANDOFF_ARCHIVE.md`,
`changelog/tftp-transfers-on-macos.fixed.md`, `changelog/container-state-settled.fixed.md`,
`changelog/rollback-later-failed-run.fixed.md`, `changelog/facts-gather-systemd-session-units.fixed.md`.

Machine: unchanged. Lab: unchanged; all VMs are powered off. The Mac was used by the worktree agent through pleiades
(51.5 GB free before, 51.4 GB after, its TMPDIR leftovers removed).
