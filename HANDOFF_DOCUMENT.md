# Handoff Document

Rewrite the "Current Status" section when stopping mid-task or handing off, per `.AGENTS/AGENTS.md`.

## Current Status (this session)

**2026-10-20. Branch `feat/rescue-always-design`, pull request #80, off `main` at `d33ff7e7` (#79 merged green).**
One pull request at a time; `make pr-check` before every push; the owner merges.

### What merged since the last handoff

- #76: `wait.connection`'s exponential backoff and steady streak; the pr-check report as a versioned contract.
- #77: `virt.vbox.vm.delete`'s saved-state header warning.
- #78: Windows firewall rules.
- #79: parameter type rules, the unreached mark in the run report, lab readiness retries, the disk check, flakegate
  build output.

### This branch, at the owner's request ("plan always:", then "do it on the same PR")

The design, with the owner's pasted spec and decisions, is `docs/design/block-rescue-always.md`.

- **A block's `rescue:` and `always:` run**, per device: one chain (block, rescue, always), a stack of frames per
  device, every task deciding per device (`internal/engine/handlers.go`, `handlers_run.go`).
- **A rescued run ends `rescued`** (exit 0); `--fail-on-rescue` ends it with exit 4. Answered tasks report
  `rescued_by`.
- **A block reaching another device runs as one unit** (`DAG.Units`): a failure anywhere fails it everywhere, and its
  rescue and always run once on their own targets.
- **A run that has to end still runs the `always:` of every block it is inside**, on every device.
- **`error`** (template and CEL) only in rescue; validate's `HandlersRule` refuses it elsewhere, and refuses a register
  only a rescued block writes when a later task reads it unguarded.
- **Check mode:** rescue tasks are `conditional_skip`; a failed check never routes to a rescue.
- **Journal:** skip kinds `block_failed` and `not_failed`, read by the strict reader.
- **Owner decisions:** exit 4 of its own; always: does not read `error`; a failed check reports as before; a block
  reaching other devices runs as one unit.
- **Proof:** unit scenarios; an independent recursive oracle over 400 generated nestings on one and two devices in
  every run (found 541 and 542, LESSONS 302); FuzzHandlers about 167k cases; an sshd release gate (exit 0 and 4);
  every lab `# TODO(always):` converted and proven through Pleiades by injecting a failure in its block.
- **Also:** `engine.Select` kept `reversible:` out of a tag selection (540); the journal reader names a refused link on
  FreeBSD (544).
- **pr-check** passed on `a61fede9` in 1657s, every leg green; one contention re-run
  (`TestCLI_RunExecutesExecCommand`, an sshd container's readiness deadline).

### Open, in the design note, not built

- `rollback_on_failure`.
- Walk-tier cross-device blocks (Phase 117b's segmented dispatch); a Runner still runs every task on its one device.
- The name of a run-once key; `ignore_errors` and `changed_when`.

### Next

1. Watch #80's CI; the owner merges.
2. **The live gates read gitignored `tests/<name>/.env` files** (agreed 2026-10-19, in the tracker).
3. **Every method proven on every OS it targets** (Phase 122b), and its other open items.
4. **Packages for Server Core:** Chocolatey, Scoop and PSResourceGet, after a design note.

### Lab state

- `bsd-check`, `win-check`, `lab-dc1` and `lab-lin1` are saved; checks start and save the check boxes.
- lab-dc1 takes its time from time.windows.com (`win/dc-01-build.yaml`), since a resumed domain controller was 90
  minutes slow (543).
- The lab's must-run tasks are in `always:` blocks: `ubuntu-throwaway.yaml`, `win/dc-04-linux-join.yaml`,
  `win/ca-01-build.yaml`.

### Files changed this session

- `internal/engine`: `handlers.go`, `handlers_run.go`, `unreached.go` (new), `executor.go`, `chain.go`, `dag.go`,
  `select.go`, `journal*.go`, `cel.go`, `render_params.go`, `condition_refs.go`, `rollback_key.go`, and tests
  (`handlers_test.go`, `handlers_fuzz_test.go`, `select_reversible_test.go`, fqcn conversions);
- `internal/validate/handlers.go`; `internal/journal` (`reader.go`, `reader_unix.go`, tests);
- `cmd/pleiades` (`run*.go`, `validate.go`, `rescue_release_gate_test.go`);
- `internal/forge/playbook`, `tools/gendocs/taskkeys.go`, the regenerated `docs/reference`, docs 02 and 03,
  `docs/design/block-rescue-always.md`, `examples/upgrade_ios`, one changelog fragment;
- `FAILURE_PATTERNS*` (539 to 544), `LESSONS_LEARNED*` (302), `HANDOFF_*`; the lab runbooks above.
