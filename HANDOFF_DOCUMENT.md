# Handoff Document

Rewrite the "Current Status" section when stopping mid-task or handing off, per `.AGENTS/AGENTS.md`.

## Current Status (this session)

**2026-11-06. Branch `fix/remoteexec-detach-hardening`, in the main clone, rebased on `main` at `6469b1f4` (PR #120
merged). Two commits: `1bb5288e` (three checks carried from `stash@{0}`) and `649d5890` (startup output with no
newline). Not yet through pr-check, push or a pull request.**

### What works

- **The stash is sorted.** `stash@{0}` (2026-11-04, `fix/ssh-sweep-b`) was checked hunk by hunk against `main`. Its stop
  script, ownership checks and Raw-view skip are on `main` in a stronger form (`detachStop`), and its `notSentError`
  only saves a stop round trip, so none of those is carried (LESSONS 402). Drop the stash once this branch's pull
  request merges.
- **Five defects in `pkg/remoteexec`, each with a test that fails on `main`'s code** (checked by reverting the code):
  - FAILURE_PATTERNS 943 (a repeat of 900): startup output with no trailing newline ran into each detached answer, so a
    just-started job was stopped and its task failed. Every answer now leads with a newline.
    `TestSSHContainer_ADetachedCommandSurvivesStartupOutput` runs a banner line and a no-newline banner against real
    sshd; the second failed before the fix. Found by an independent second-model review of `1bb5288e` ($1.25), then
    measured.
  - 944: `withoutCommand` matches x/crypto/ssh's whole refusal text, not a substring (`TestWithoutCommand_KeepsOtherErrors`).
  - 945: a start's answer must be this job's own directory (`TestAnsweredJobDir`).
  - 946: a status that is not a number says what the stop did, and its refusal is bounded
    (`TestDetached_AnExitStatusThatIsNotANumberSaysWhatTheStopDid`, `TestPollRC_BoundsWhatItQuotes`).
- `pkg/remoteexec` passes under `-race` at 94.9% coverage (floor 94.5). The real-sshd detach tests pass, including
  `TestSSHContainer_ADetachedCommandWithNoSetsidIsStillStopped`, and so does `TestAsyncReleaseGate_DetachedOnARunner`
  (`cmd/runner`).

### Current blocker

None. Next is pr-check for this branch.

### Open

- (a) Carried from #120: readiness paths without the fast failure (`ForHTTP` waits, `ForListeningPort`-only sites,
  Ryuk); FAILURE_PATTERNS 602 as a possible repeat of 470, unproven; and 474's exhaustion odds under the narrower
  ephemeral range (20000-32767), to watch.
- (b) FAILURE_PATTERNS 483's CI sighting stays open.
- (c) Carried from Phase 46's run settings: the CLI journal does not record which `--setting` a run used; no end-to-end
  test takes `method_settings` from a launch or the rollback route to a Runner; a run setting on a concrete method
  behind a generic `svc.*`/`pkg.*` dispatcher is never filled.
- (d) Done 2026-11-06: the local ledger's two reaper sightings (TestRoutes_WaitsForARouteOSPFLearns at d1e221fa,
  TestApply_APartitionedWinnerReleasesItsClaim at 46c5c464) are annotated pattern 470; the Actions entry for the
  former is a different failure (vtysh exec) and was left. Still open: the owner question on dating a value
  `facts.Resumed` writes back (before 41d).
- API credit: $59.20 left, expiring 2026-11-08.

### Next, in order

1. `make pr-check` through the monitor, push, `make branch-containers`, the pull request. Merge only when green,
   `record-actions`, pull `main` and clean.
2. Once it has merged, `git stash drop stash@{0}` (check first that it is still the 2026-11-04 `fix/ssh-sweep-b` entry).
3. The facts churn PR, sweep items 4, 5, 6 and 10.
4. Phase 41, 41b to 41g, then 117b, 117c, 105, then release 0.4.0 (no tag until the owner says).

### Files changed on this branch

`pkg/remoteexec/` (`conn_watch.go`, `detach.go`, `detach_script.go`, `detach_script_test.go`, `detach_test.go`,
`stream_cancel_test.go`), `internal/transport/ssh/ssh_detach_container_test.go`. Records (uncommitted):
`FAILURE_PATTERNS.md` and its archive (943 to 946), `LESSONS_LEARNED.md` and its archive (402), `HANDOFF_DOCUMENT.md`,
`HANDOFF_ARCHIVE.md`, `changelog/async-startup-output-without-newline.fixed.md`.

Machine: unchanged since PR #120 (the ephemeral port range stays 20000-32767). Lab: unchanged; no lab VM use was
reported for this session.
