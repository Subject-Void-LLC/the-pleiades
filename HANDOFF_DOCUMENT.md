# Handoff Document

Rewrite the "Current Status" section when stopping mid-task or handing off, per `.AGENTS/AGENTS.md`.

## Current Status (this session)

**2026-11-02. Branch `feat/c2b-check-predicted-state`, HEAD `b7fa525a`, 7 commits on `origin/main` at `5f19be00`
(`68c25da5..b7fa525a`). Phase 46a, "Predicted state across a check", is committed with the fixes from three fp-reviewer
passes and two claim-refuter passes. Not through `make pr-check` yet, not pushed, no pull request.** C2a merged as PR
#101 (`1cbf20e6`) and the nats.go v1.55.0 bump as PR #102 (`5f19be00`).

### What works (each proven by a test that ran)

- **A check carries predicted removals forward.** In a run that is a check throughout, `file.remove` records the paths
  it would remove (`sdk.RecordPredicted`), keyed where the device resolves them (`remotefile.Locate`: physical path and
  each directory's `dev:ino`); a later `file.remove` finds a directory empty when every entry was predicted gone.
  `TestCLI_RollbackReleaseGate` G7 and G7b; `cmd/runner/rollback_release_gate_test.go`'s check job now predicts success.
- **Removals only, dropped for good at anything else** (FAILURE_PATTERNS 807, 808): the first task in the run that is
  not a recording `file.remove` or an in-process engine action, on any device, drops every device's predictions.
  `TestLedger_*`, `TestCheckPredictions_*`, G8, and G9, which runs each review's runbooks and asserts a check never
  passes what the real run fails (mutation checks showed each rule is needed).
- **Only a removal the device allows is recorded** (811, 812): `remotefile.Removable` (`test -w`/`-x`, the sticky rule,
  BusyBox detected and held to mountinfo). `TestRemovable`, `TestRemovableScript_RefusesWhatItCannotVouchFor`,
  `TestCheckRemove_RecordsOnlyARemovalTheRealRunCanMake`. `pkg/remotefile` passed on bsd-check at `bd024a25`.

### Current blocker

None. Next is the gate.

### Open

- The owner accepted a residual (2026-11-02), documented in docs/02 and docs/11: refusals seen only when a removal is
  tried (an immutable flag, a busy mount point, a delete-deny ACL on macOS or ZFS, an ACL under BusyBox) carry into a
  later task's check (LESSONS 376).
- Read in the code while writing 811, not tested: `file.remove`'s own check of one removal does not ask whether the
  parent allows it, so it still predicts success where the parent refuses, as on main. The branch only stops carrying
  such a removal forward. Worth a look before C2c.
- macOS was not run by hand (the Mac timed out on SSH); CI's `fast-macos` job runs `pkg/remotefile`.
- `TestCopy_ARootFileItMayWriteIsWrittenInPlace` (`internal/catalog/file`) failed once on an sshd container start
  timeout under parallel load and passed alone twice; read as contention (FAILURE_PATTERNS 61's class). Not in a
  pr-check or Actions ledger.
- Phase 46a: Security Analysis (needs `gosec` and `govulncheck` evidence) and Provide Commit Message are open; every
  other item is ticked.

### Next, in order

1. `make pr-check` through the monitor, push, `make branch-containers`, then one pull request naming the failure-pattern
   classes (807 to 816) in its body. Merge only when its checks are green.
2. After the merge: pull `main`, delete merged branches, prune.
3. C2c: the uncheckable methods.
4. C2d: Phase 41, which needs the owner's design decisions first.

### Files changed this session

See `git log origin/main..HEAD`: `internal/engine/predicted.go`, `pkg/sdk/predicted.go`, `pkg/wire/predicted.go`,
`pkg/remotefile/locate.go`, `internal/catalog/file/remove.go` and `predicted.go`, the IPC plumbing
(`internal/adapters/native`, `pkg/external`), `pkg/collection/manifest.go`, the two rollback release gates, and docs 02,
10 and 11. Records: `FAILURE_PATTERNS.md` and `FAILURE_PATTERNS_ARCHIVE.md` (807 to 816), `LESSONS_LEARNED.md` and
`LESSONS_LEARNED_ARCHIVE.md` (374 to 377), `HANDOFF_DOCUMENT.md`, `HANDOFF_ARCHIVE.md`, and Phase 46a in
`.SPECIFICATION/IMPLEMENTATION.md`. Changelog fragment: `check-predicted-state.added.md`.

Lab: bsd-check ran `pkg/remotefile` at `bd024a25` and was shut down after it (bsdcheck: "put the VM bsd-check back on
vengeance, shut down"). win-check: no change reported.
