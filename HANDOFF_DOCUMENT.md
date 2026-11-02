# Handoff Document

Rewrite the "Current Status" section when stopping mid-task or handing off, per `.AGENTS/AGENTS.md`.

## Current Status (this session)

**2026-11-01. Branch `feat/c2a-rollback-ordering`, HEAD `14e1145d`, 12 commits on `main` at `e8b3cceb` (`git log
main..HEAD`). Phase 40a, "Rollback order without clocks", is committed with the fp-reviewer's fixes. Not through
`make pr-check` yet, not pushed, no pull request.** C1 merged as PR #99 (`40a12dac`), and the forge symlink fix merged
as PR #100 (`e8b3cceb`, FAILURE_PATTERNS 792); this branch was rebased onto it, so commit hashes cited before the
rebase changed. The owner split C2 into four pull requests: 40a (this one), 46a, the uncheckable methods, then 41,
whose design decisions the owner makes first.

### What works (each proven by a test that ran)

- **Fencing tokens with an epoch, only while held** (`lock.Fenced`, `lock.Fence`): conformance subtest
  `FencingTokensOrderAcquisitions` for both managers, `TestFencingEpoch_TwoInProcessManagersNeverShareOne`,
  `TestFence_CarriesATokenUntilLost` (FAILURE_PATTERNS 795, 798, 799).
- **The Crawl run counter and run markers** (`internal/journal/clock.go`, `clock_live.go`): a run that died before its
  seal is bounded by the next run (`TestRunMarkers_*`, 797); the rebuild reads as the strict reader does
  (`TestTickClockReadsOnlyWhatTheStrictReaderWould`, `TestLocksRefuseALink`, 802); the largest tick is refused (803).
- **The Runner stamps an exclusive, still-held lease's token**, none under a shared lease (793):
  `TestAgent_ExecuteWithLease_*`, `TestJournalPublisherStampsTheLeaseRevision`.
- **The planner orders by tick and by token within one epoch**, by clock only where no order is known (794, 801):
  `TestOrder_*` in `internal/rollback/order_test.go`.
- **RULE 0 proof:** `TestCLI_RollbackReleaseGate` G2 and `TestRollbackReleaseGate_ALaterJobFromASkewedRunnerStillRefuses`,
  each still refused with the later run's times a day back; both fail with the planner forced to order by clock alone.
- `internal/journal` is at 94.21% against its 94.1% floor (796, 806).

### Current blocker

None. Next is the gate.

### Open

- FAILURE_PATTERNS 805: `pleiades run`'s end-of-run concurrent-change warning (`journal.ChangesDuring`) still uses a
  wall-clock window.
- FAILURE_PATTERNS 804 is a suspected contention fix, not measured.
- FAILURE_PATTERNS 551 stays open for its first reading (a later run that failed before acting).
- Phase 40a: every item but "Provide Commit Message" is ticked.

### Next, in order

1. `make pr-check` through the monitor, then push, then `make branch-containers`, then one pull request naming the
   failure-pattern classes (793 to 806, and 551's update) in its body. Merge only when its checks are green.
2. After the merge: pull `main`, delete merged branches, prune.
3. C2b: Phase 46a.

### Files changed this session

See `git log main..HEAD`. Records: `FAILURE_PATTERNS.md` and `FAILURE_PATTERNS_ARCHIVE.md` (793 to 806, and an update
to 551), `LESSONS_LEARNED.md` and `LESSONS_LEARNED_ARCHIVE.md` (371 to 373, and an update to 372),
`HANDOFF_DOCUMENT.md`, `HANDOFF_ARCHIVE.md`, and Phase 40a in `.SPECIFICATION/IMPLEMENTATION.md`. Changelog fragment:
`rollback-order-without-clocks.fixed.md`.

Lab: no change was reported this session. Last recorded: bsd-check and win-check powered off (shut down through ACPI,
not saved).
