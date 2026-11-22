# Handoff Document

Rewrite the "Current Status" section when stopping mid-task or handing off, per `.AGENTS/AGENTS.md`.

## Current Status (this session)

**2026-11-22. Branch `fix/walk-launch-compile-error` at `db940fb8`, ten commits on `main` at `ae9cfa2b` (the #225
merge). No pull request was reported open for it. Merged since the last handoff: #221 to #225. This handoff was
written by the records pass, which ran no test and no gate; the test names below are the branch's own proof and were
read, not run again.**

### What works

- A runbook that does not compile is refused on the Walk tier at a launch, a check and a relaunch (422, no job) and
  at a schedule's write, with the builder's message; fan-out records the message as the failure reason of a job whose
  runbook broke after launch (FAILURE_PATTERNS 1360, resolved). Proof: `internal/api/dispatcher_compile_error_test.go`,
  `TestWorker_HandleJobRequested_ACompileErrorIsTheFailureReason` (`internal/dispatch`).
- The message is masked by shape, escaped and cut at 2048 bytes on the escaped text
  (`TestCompileError_MessageMasksASecretShapedLiteral`, `TestCompileError_MessageIsCutBetweenEscapes`; 1363).
- A rollback no longer answers with the source's raw error: compile gives the message, gone gives a sentence, and a
  storage error is logged and answers 500 (`TestRollbackJob_ARunbookTheSourceCannotReadNamesNoPath`,
  `TestRollbackJob_ARunbookThatDoesNotCompileSaysWhy`, `TestRollbackJob_ARunbookThatIsGoneSaysSo`; 1361).
- A template create over such a runbook answers 400 with the message, and `GET /runbooks/{id}` gives it
  (`TestTemplates_CreatingOneOverARunbookThatDoesNotCompileIs400`, `TestRunbookCatalog_TellsTheKindsApart`,
  `TestRunbookHandler_GetSaysWhyARunbookDoesNotCompile`; 1362).
- A schedule whose runbook broke can still be renamed or paused (`launchable.Request.Standing`), and that edit is
  written only onto the row it was judged against (`TestUpdate_AConcurrentPauseDoesNotMakeAnEnableLookStanding`,
  SQLite only; 1363).
- Records: FAILURE_PATTERNS 1361 to 1367, LESSONS 476 to 480. No CI lesson: no pr-check or Actions failure was
  reported. The branch's changelog fragment already exists.

### Not proven

- Nothing ran against a real Controller and Runner. `tests/e2e` and `cmd/runner` were not run.
- The `cmd/controller` wiring of `api.RunbookCatalog` is covered by `cmd/controller`'s tests, which passed once
  locally without `-race`, and by CI.
- The views' `Submit` paths are untested; only the extracted `launchRefusal` and `relaunchErrors` mappings are.
- `internal/api` coverage is 98.5 against a floor of 98.4.
- That nothing which used to run is now refused was read from code by the reviewer, not executed.

### Blocker

- None reported. The branch's `make pr-check` result was not given to the records pass.

### Open

- FAILURE_PATTERNS 1365: `GET /runbooks/{id}` answers 400 "invalid runbook id" for a storage error, and
  `internal/ui/resources/runbooks/runbooks.go` (near line 168) hands the source's raw error to the view layer; whether
  a path reaches a page is not traced.
- FAILURE_PATTERNS 1366: a runbook file caught mid-write is classed as a compile error, and a scheduled firing at
  that instant is skipped and not retried.
- FAILURE_PATTERNS 1356, untouched: the Controller still runs no `validate` rule.
- A template edit still does not verify its runbook (noted in 1362).
- Masking of the compile message is by shape only; `docs/09-control-plane-and-api.md` says so.
- Carried from the last handoff and not re-checked here: Phase 41's read item (the Walk tier's reads, a coverage
  record per observation); no test holds every launch route to every refusal function (LESSONS 470); no guard keeps
  `fqcn:` out of test fixtures (1348, 1357); FAILURE_PATTERNS 1085, 1267, 1291 and 1292. 1344 and 1351, open in the
  last handoff, are both marked resolved 2026-11-22 in the index.

### Next steps

1. Commit these records on the branch.
2. `make pr-check` on the committed, clean tree, then push, then `make branch-containers` (the branch changes
   `cmd/controller`, a container package).
3. Open the pull request when both pass, and refresh the local PR log in the same command (LESSONS 455).
4. Then 1365: trace the view layer's handling of a reader error, and give `GET /runbooks/{id}` a 500 for a storage
   error.
5. Run the schedule write's concurrency test on PostgreSQL.

### Files changed

The branch (34 files before these records): `internal/runbook` (`compile_error.go`, `dir_source.go`, `runbook.go`),
`internal/api` (`dispatcher.go`, `dispatcher_compile.go`, `dispatcher_launchable.go`, `dispatcher_rollback.go`,
`runbooks.go` and their tests), `internal/dispatch/definition_source.go`, `internal/launchable/router.go`,
`internal/schedule` (`admit.go`, `ent_store.go`, `admit_test.go`, `launchable_gate_test.go`),
`internal/ui/resources/jobs/relaunch.go`, `internal/ui/resources/templates/actions.go`, `cmd/controller/main.go`,
`internal/apispec`, the generated OpenAPI documents, `docs/09-control-plane-and-api.md`,
`docs/10-running-in-production.md`, `changelog/walk-launch-compile-error.breaking.md`.
Records in this pass: `FAILURE_PATTERNS.md`, `FAILURE_PATTERNS_ARCHIVE.md`, `LESSONS_LEARNED.md`,
`LESSONS_LEARNED_ARCHIVE.md`, `HANDOFF_DOCUMENT.md`, `HANDOFF_ARCHIVE.md`. No roadmap item was ticked.

Lab: no change was reported for this branch, and it names no lab VM. The lab state is as the previous handoff left
it, not re-checked.
