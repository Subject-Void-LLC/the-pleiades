# Handoff Document

Rewrite the "Current Status" section when stopping mid-task or handing off, per `.AGENTS/AGENTS.md`.

## Current Status (this session)

**2026-11-21. Branch `feat/41-facts-in-when` at `fce5dce5`, rebased on `main` at `f7324fa5` (the #220 merge). No pull
request is open for it yet. Merged since the last handoff: #210 to #220. This handoff was written by the records pass,
which ran no test and no gate; the test names below are the branch's own proof and were read, not run again.**

### What works

- Facts in conditions, Crawl tier: a task's `when`, `when_or` and `when_cel` read the evaluating device's own stored
  facts through the new `facts` root, by a literal key. The stored snapshot is overlaid with what earlier levels of the
  run emitted. An unknown key fails the task, and `is defined`, `default()` and `has()` do not hide it. Proof:
  `TestCLI_FactsInConditionsReleaseGate` (`cmd/pleiades`, a real sshd container), `internal/engine/facts_when_test.go`,
  `FuzzConditionFactReads` (`internal/engine/facts_root_fuzz_test.go`).
- A store answers present or unknown; absent only from a call of this run that asked for the key by exact name
  (`TestFactsRoot_AKeyNoObservationLookedForIsNeverAbsent`, FAILURE_PATTERNS 1342).
- `validate` refuses `facts` in params, rollback step params, computed keys, the whole root and another device's
  facts, and a no-key project for a key no earlier task gathers (`internal/validate/facts_rule_test.go`).
- Check mode reports a tainted reader unchecked (exit 3), with the observation exemption for read-only built-ins
  (`internal/engine/facts_when_check_test.go`, `TestReadOnlyFactEmittersObserveOnlyWhenTheirCheckIsTheirRun`).
- Walk tier: a fact-reading runbook is refused at launch, check, relaunch, the schedule's write, admission and in the
  Runner before its first task (`internal/api/dispatcher_fact_reads_test.go`, `internal/dispatch/facts_test.go`,
  `internal/adapters/native/facts_refusal_test.go`). A refused relaunch answers 422.
- The engine names no fact store or key (`TestEngineNamesNoFactStoreOrKey`).
- Records: FAILURE_PATTERNS 1342 to 1352, LESSONS 469 to 471, four changelog fragments. No CI lesson: nothing failed
  in pr-check or in Actions.

### Blocker

- None reported. The branch's `make pr-check` result was not given to the records pass.

### Open

- FAILURE_PATTERNS 1344, in part: the credentials path still makes a new master key in a project that lost its key
  beside sealed fact files, and the fact store then ends up mixed.
- FAILURE_PATTERNS 1351 (on `main`): `parseEnv` in `internal/engine/condition_refs.go` has no optional syntax, so a
  `.?` or `[?` condition reading `device` is not decided per device and fails at run time. No test yet.
- Phase 41's read item stays open (12 of 14 done): the Walk tier's reads through the claim-time fetch; a coverage
  record per observation, so a family's key can read absent; `inventory:read` for a run that reads facts on the Walk
  tier.
- No test holds every launch route to every refusal function (LESSONS 470); no guard keeps `fqcn:` out of test
  fixtures (1348).
- Carried from the last handoff and not re-checked here: identity facts on FreeBSD, macOS and Windows; 41c's
  read-backs; 41d to 41g; 41h's Release Gate; FAILURE_PATTERNS 1267, 1291 and 1292.

### Next steps

1. Commit these records on the branch.
2. `make pr-check` on the committed, clean tree, then push, then `make branch-containers`.
3. Open the pull request when both pass; put its number in the tracker note under Phase 41 ("PR number pending") and
   refresh the local PR log in the same command (LESSONS 455).
4. Then the open items above, 1344's credentials path first.

### Files changed

The branch (64 files before the records): `internal/engine` (the `facts` root, the view, overlay, answer, check and
condition files and their tests), `internal/validate/facts_rule.go`, `internal/dispatch/facts.go` and
`worker_devices.go`, `internal/api/dispatcher.go` and `dispatcher_launchable.go`, `internal/adapters/native/validate.go`,
`internal/facts/file_lookup.go` and `file_store.go`, `internal/runbook`, `pkg/wire/dispatch_version.go`,
`cmd/pleiades` (`facts_dir.go`, `facts_reader.go`, `run_pipeline.go`, `validate.go`, the release gate),
`internal/archtest`, `docs/04-writing-runbooks.md`, `docs/reference/task-keys.md`, `tools/gendocs/taskkeys.go`.
Records: `FAILURE_PATTERNS.md`, `FAILURE_PATTERNS_ARCHIVE.md`, `LESSONS_LEARNED.md`, `LESSONS_LEARNED_ARCHIVE.md`,
`HANDOFF_DOCUMENT.md`, `HANDOFF_ARCHIVE.md`, `changelog/facts-in-conditions.added.md` (edited),
`changelog/condition-not-true-or-false-refused.breaking.md`, `changelog/relaunch-refused-422.fixed.md`,
`changelog/facts-lost-master-key-not-replaced.fixed.md`. `.SPECIFICATION/IMPLEMENTATION.md` (git-ignored, main clone)
has a dated note under Phase 41's read item, not ticked.

Lab: no change was reported for this branch; its gate runs in containers and names no lab VM. The lab state is as the
previous handoff left it, not re-checked.
