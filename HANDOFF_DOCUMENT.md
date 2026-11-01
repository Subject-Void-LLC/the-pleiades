# Handoff Document

Rewrite the "Current Status" section when stopping mid-task or handing off, per `.AGENTS/AGENTS.md`.

## Current Status (this session)

**2026-11-01. Branch `feat/c1-language`, HEAD `56a42de4`, 27 commits on `main` at `1e97aa2e` (`git log main..HEAD`).
C1 is complete, and the fp-reviewer's findings are fixed in `2e91d59d`..`56a42de4` (FAILURE_PATTERNS 778 to 789). Not
through `make pr-check` yet, not pushed, no pull request.** Pull requests #96 to #98 merged
without a handoff rewrite; the previous status (PR #95's branch) is in `HANDOFF_ARCHIVE.md`.

### What works (each proven by a test that ran)

- **Params render from every source** on a real Debian device: `TestCLI_ParamsRenderEverySourceInItsOrder`; a value
  holding braces is never rendered twice: `TestRenderedParams_AValueHoldingBracesIsNotRenderedAgain`.
- **Handlers and `notify:`** (`internal/engine/notify.go`, `chain.go`): `TestCLI_HandlersRunOnceInTheirOrderOnlyWhenNotified`,
  `FuzzNotify`, `TestNotify_ChangedWhenDecidesWhatNotifies`. `meta: flush_handlers` is refused (deferred).
- **`serial:`** on both tiers, in device-name order (`engine.BatchDevices`): `TestCLI_SerialRunsTwoDevicesAtATime`,
  `TestCLI_SerialStopsTheRolloutAtAFailedBatch`, `TestSerialReleaseGate_TwoAtATimeAndAFailedBatchStops` (real NATS,
  Runner and sshd), `FuzzSerialList`. The Controller adds a nullable `job_task.batch` column with both migrations.
- **Timed `pause`** (`internal/engine/timed_pause.go`; Phase 27's `pause.go` is untouched): `TestPause_HoldsOnceForTheWholeRun`.
  The dotted `pleiades.builtin.fail`, `assert` and `set_fact` now validate (FAILURE_PATTERNS 768).
- **`limit`**, one matcher (`engine.ParseLimit`) for `pleiades run --limit`, `adhoc --limit` and the Controller's
  fan-out: `TestLimit_NarrowsWhatARunReaches`, `TestLimit_OnlyWhatItAdmitsIsPartOfTheJob`, `FuzzLimit`. A playbook job's
  limit is applied by the Controller (`changelog/playbook-limit-read-by-controller.breaking.md`).
- **`async:` and `poll:`** for every method, detached on SSH devices, on both tiers:
  `TestCLI_AsyncRunsInTheBackgroundAndItsLimitStopsTheDevicesWork`, `TestAsyncReleaseGate_DetachedOnARunner`. A plain
  `timeout:` now ends a command whose child holds its output (FAILURE_PATTERNS 774).
- **The review's fixes**, each test failing on the old code: an abandoned command's output is not read
  (`TestRun_ACommandWhoseServerNeverAnswersIsAbandoned`, under `-race`); a detached command runs as it would attached,
  never as an argument, and only in a base its account owns (`TestDetached_*` in `pkg/remoteexec/detach_test.go`); the
  stop's budget fits inside every kill and an external program ends its method on SIGTERM
  (`TestProgram_SIGTERMEndsTheMethodsContext`); cancelling a run stops its background jobs
  (`TestAsync_CancellingTheRunStopsABackgroundJob`); handlers and background tasks that could never run as written are
  refused; a limit of only separators is refused; a Controller serial job repairs its batches whatever its forks; the
  CLI cuts batches from the admitted hosts as the Controller does; the gates no longer race a fixed sleep;
  `internal/engine/builtin_table_test.go` holds the engine's action table to its executor. Phase 90 and 91 items carry
  this evidence now; no item changed state.

### Current blocker

None. Next is the gate.

### Open

- Phase 91: `max_fail_percentage` refused by decision; the pause prompt on a terminal and the converter's mapping of
  the timed pause; each batch accepted before the next (Part XVI); its Adversarial Pattern Justification and
  Schema/Injection Hardening gates. Phase 87's `set_fact` item is the owner's call (built as `vars.<name>`).
- No test lands an async limit inside a poll on purpose (FAILURE_PATTERNS 777, path 1).
- The playbook converter does not carry `notify:`, `handlers:`, `async:` or `serial:` yet (its notes now say so).
- Detach on FreeBSD and macOS (no `setsid`, the `set -m` path) is unproven, since the BSD leg is cut.
- `internal/inventory/ent_group_names.go` recurses per group with a fresh seen-set (O(groups x depth)).
- `internal/segment` (117b) knows neither spliced handler nodes nor a background job collected in a later segment.
- The 781 attack (a login steering root's polls under a sudo that keeps `HOME`) was reasoned, not reproduced; the fix
  refuses the shape.
- **Triad finding (this session):** vuln-corpus `eae41a4`'s "skips constant joins" is live, but the sweep still flags a
  join whose segment is another package's const or a range variable over a literal map: two known false positives in
  `internal/archtest/coverage_ledger_test.go`. And argus `expand_seeds` wrote corpus files into the target repository's
  `testdata/fuzz` despite `persist: false`; its record belongs in `~/argus`, not here.

### Next, in order

1. `make pr-check` through the monitor, then push, then `make branch-containers`, then one pull request naming the
   failure-pattern classes (768 to 789) in its body. Merge only when its checks are green.
2. After the merge: pull `main`, delete merged branches, prune.
3. The triad version handshake, in `~/argus`, `~/vuln-corpus` and `~/Groundconfig`, never committed there.
4. Release 0.4.0. No tag until the owner says so.

### Files changed this session

See `git log main..HEAD`. Records: `FAILURE_PATTERNS.md` and `FAILURE_PATTERNS_ARCHIVE.md` (768 to 789),
`LESSONS_LEARNED.md` and `LESSONS_LEARNED_ARCHIVE.md` (368 to 370), `HANDOFF_DOCUMENT.md`, `HANDOFF_ARCHIVE.md`.
Changelog fragments: `async-and-poll.added.md`, `dotted-builtins-validate.fixed.md`, `handlers-and-notify.added.md`,
`limit.added.md`, `playbook-limit-read-by-controller.breaking.md`, `serial.added.md`,
`ssh-timeout-ends-the-task.fixed.md`, `timed-pause.added.md`.

Lab: no change was reported this session. Last recorded: bsd-check and win-check powered off (shut down through ACPI,
not saved).
