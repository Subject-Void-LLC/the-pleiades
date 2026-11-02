# Handoff Document

Rewrite the "Current Status" section when stopping mid-task or handing off, per `.AGENTS/AGENTS.md`.

## Current Status (this session)

**2026-11-01. Branch `feat/c2a-rollback-ordering`, HEAD `ee47be45`, 8 commits on `main` at `40a12dac` (`git log
main..HEAD`). Phase 40a, "Rollback order without clocks", is committed. Not through the fp-reviewer or `make pr-check`
yet, not pushed, no pull request.** C1 merged as PR #99 (`40a12dac`). The owner split C2 into four pull requests:
40a (this one), 46a, the uncheckable methods, then 41, whose design decisions the owner makes first.

The branch's first commit, `8c2ccadc`, is another agent's forge symlink fix (PR #100, FAILURE_PATTERNS 792, its own
changelog fragment), folded in at the owner's request. PR #100 is still open; what happens to it is the owner's call.
Its worktree `/home/noot/auto-roboto-fix-792` was removed clean; the branch
`fix/generated-file-writes-no-symlink-follow` remains locally and on origin.

### What works (each proven by a test that ran)

- **A lease carries the fencing token of its acquisition** (`lock.Fenced`; the NATS manager's is the JetStream
  sequence of the acquiring write, held in `natsLease.fence` because `revision` moves on every KeepAlive): conformance
  subtest `FencingTokensOrderAcquisitions`, both managers.
- **The Crawl run counter** (`.pleiades/clock`, `internal/journal/clock.go`): start tick on every entry, end tick in
  the seal, rebuilt from the journals when missing or behind: `TestTickClock*`, `TestRunTicksAreStampedAndSealed`.
- **The Runner stamps an exclusive lease's token on every journal row**, and none under a shared lease
  (FAILURE_PATTERNS 793): `TestAgent_ExecuteWithLease_HandsTheLeasesFencingTokenToTheRun`,
  `TestAgent_ExecuteWithLease_ASharedLeaseHandsTheRunNoFencingToken`, `TestJournalPublisherStampsTheLeaseRevision`.
- **The planner orders by tick and token**, by clock only for older journals and shared-lease jobs, and finds an undo
  step's latest attempt the same way (794): `TestOrder_*` in `internal/rollback/order_test.go`.
- **RULE 0 proof:** `TestCLI_RollbackReleaseGate` G2 with the later run's journal times a day back, still refused;
  `TestRollbackReleaseGate_ALaterJobFromASkewedRunnerStillRefuses` (two Runners, two NATS lock managers, later job's
  rows a day behind), still refused. Both fail with the planner forced to order by clock alone.
- `internal/journal` was under its 94.1% floor at 91.3% and was fixed before the gate (796).

### Current blocker

The fp-reviewer run waits on the triad MCP servers reconnecting.

### Open

- Phase 40a is 7 of 8. "The planner orders by them" stays open: the Controller drops `Plan.OrderedByClock`, so an
  accepted Walk rollback ordered by clock says nothing, and a shared-lease refusal's reason says one journal "predates
  run ordering", which is not the cause there. The docs and the changelog fragment say "the rollback names each such
  run", which holds for the CLI only. "Provide Commit Message" is unticked.
- FAILURE_PATTERNS 551 stays open for its first reading (a later run that failed before acting); its wall-clock reading
  is closed on this branch.
- PR #100's fate (above).

### Next, in order

1. The fp-reviewer over `git diff main..HEAD` once the triad servers reconnect; fix what it finds.
2. `make pr-check` through the monitor, then push, then `make branch-containers`, then one pull request naming the
   failure-pattern classes (792 to 796, and 551's update) in its body. Merge only when its checks are green.
3. After the merge: pull `main`, delete merged branches, prune.
4. C2b: Phase 46a.

### Files changed this session

See `git log main..HEAD`. Records (uncommitted): `FAILURE_PATTERNS.md` and `FAILURE_PATTERNS_ARCHIVE.md` (793 to 796,
and an update to 551), `LESSONS_LEARNED.md` and `LESSONS_LEARNED_ARCHIVE.md` (371, 372), `HANDOFF_DOCUMENT.md`,
`HANDOFF_ARCHIVE.md`, and Phase 40a in `.SPECIFICATION/IMPLEMENTATION.md`. Changelog fragment (committed):
`rollback-order-without-clocks.fixed.md`.

Lab: no change was reported this session. Last recorded: bsd-check and win-check powered off (shut down through ACPI,
not saved).
