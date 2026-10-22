# Handoff Document

Rewrite the "Current Status" section when stopping mid-task or handing off, per `.AGENTS/AGENTS.md`.

## Current Status (this session)

**2026-10-22. Branch `feat/segmenter-117b`, off `main` at `ef253264` (PR #86's merge).** Phase 117b part 2, the
segmenter. Not pushed; no pull request yet. One pull request at a time; `make pr-check` before every push; the owner
merges.

### On this branch

- `1410e15e` feat(engine): `engine.TaskReads` and `TemplateReads` (`internal/engine/register_reads.go`);
  `TaskNeedsDevice` exported.
- `e2bf8a2a` fix(validate): `ConditionRule` refuses a condition reading a register no task writes (561).
- `9d2bdb57` feat(segment): `Split`, `Selectors` and `Verify` (`internal/segment`).
- `8fe2cdd0` fix(engine): an undo step's reads count (572), and so does a register tested with `"x" in stat`.
- `c187fbb5` fix(validate): a register tested with `has()` or `in` is left to the author.
- `1433bac6` fix(segment): refuse what one device's dispatch cannot keep (573).
- Proof: `TestTaskReads`, `TestConditionRule`, `TestConditionRule_AWriterTagSelectionLeftOut`,
  `TestConditionRule_RunsInValidate`, `TestSplit_CarriesWhatAnUndoStepReads`,
  `TestSplit_RefusesWhatOneDevicesDispatchCannotKeep`, `TestSplit_AParallelGroupOpensAGuardedBlock`, `TestVerify`,
  `FuzzSplit` (which now asserts an accepted plan keeps every guarded block in one segment). The 572 and 573 fixes
  were each mutation-checked. All pass locally: `internal/segment` 98.0% (new, no floor), `internal/validate` 100.0%
  (floor 100.0), `internal/engine` 95.5% (floor 95.2); `FuzzSplit` ran 60,026 and then 29,059 executions with no
  failure; examples and generated docs check clean. `make pr-check` has not run yet.
- Nothing calls `Split` or `Verify` yet; that is part 3.
- Records: FAILURE_PATTERNS 561 fixed, 572 to 574 new; LESSONS 309. Changelog:
  `changelog/validate-condition-registers.fixed.md`.

### Design decisions taken while building (mine, open to the owner)

- `Split` takes a device count per literal target (`segment.Devices`; key `""` is the job's devices), because
  "several devices" needs the inventory and the Runner must recompute the split. Part 3 carries the counts in the
  payload.
- `KindLocal` for device-less calls; consecutive ones share a segment. A rendered target is its own segment. A
  `register_mask` on several devices ends its segment.
- Reads count conditions, templates, `secret_mask` and undo steps.
- Known limit: two parallel branches on different targets alternate one segment per level.

### Part 3 must decide (from the review)

- Carrying secrets found in earlier segments (`Returns` carries only the registers later segments read).
- Check mode's set of unknown registers.
- The Runner trusting the dispatch's device counts in `Verify`. **Security note, reasoned, low:** a forged dispatch
  could pick counts that skip a cut.
- That a tag selection keeps `dag.Version`; journal grouping across per-segment RunIDs; `set_metadata` registers.
- On the Walk tier today `hosts:` is ignored (`internal/adapters/native/resolver.go` returns the dispatched device),
  while `placementOf` makes `hosts:` decide.

### Open

- 574 (open): `RollbackRule` does not hold undo steps to TemplateRule; reasoned, not reproduced. Not fixed here.
- 560 and 551 as before. 562: the owner decided no change.
- **Security finding, the owner's call:** `pleiades init` writes no `.gitignore`, and `docs/02-get-started.md` says
  `.pleiades/` is "gitignored by default". Proposed as its own PR.
- `add-registry-credential` and `add-database-connection` take no sealed file (a scope note).
- Agents left read-only `adhoc` run ids out of `AGENT_JOURNAL.md` in 4 of 6 dry-run rounds.
- Watch `cmd/runner`'s `TestBusyDeviceReleaseGate_ARunLongerThanTheAckWaitIsDeliveredOnce`.

### Next

1. `make pr-check` (the branch changes `internal/validate` and `internal/engine`, so the census runs), push, open the
   PR, CI, the owner merges.
2. The owner chose Phase 126 option (A), device to object storage. Order: 117b part 2 now, then Phase 126 (or 117b
   part 3; the owner to confirm).

### Files changed this session

`internal/engine/{action.go,condition_refs.go,handlers_test.go,register_reads.go,register_reads_test.go}`,
`internal/segment/{check.go,segment.go,split.go,segment_test.go,segment_fuzz_test.go}`,
`internal/validate/{condition_rule.go,condition_rule_test.go}`, `changelog/validate-condition-registers.fixed.md`,
and the records.

### Lab state

Not touched this session. The last record said `bsd-check`, `win-check`, `lab-dc1` and `lab-lin1` were saved.
