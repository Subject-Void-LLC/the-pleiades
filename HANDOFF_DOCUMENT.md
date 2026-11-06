# Handoff Document

Rewrite the "Current Status" section when stopping mid-task or handing off, per `.AGENTS/AGENTS.md`.

## Current Status (this session)

**2026-11-06. Branch `feat/method-run-settings`, from `main` at `3362391a` (PR #118 merged). Eight commits:
`82c300bb` (the BusyBox gate), `6f79f42f` (run settings in `pkg/collection`), `6f3c6353` (the `method_settings` launch
field, to the Runner and the rollback route), `290c476c` (`--setting` on `run`, `adhoc` and `rollback`), `974c087f`
(`file.remove`'s `check_forbidden` is a run setting), then the fp-reviewer's follow-ups `6cebdd23` (forge scaffold,
940), `0c44e498` (rollback lock, 939) and `f5cb8a61` (strict default and no echo, 941; LESSONS 400). Not yet through
pr-check, push or a pull request.**

### What works

- **Run settings** (owner decision 2026-11-06, "Method settings, extend later"): `collection.Param.RunSetting`
  (`pkg/collection/run_settings.go`). A run gives `<method>.<parameter>` for every task that leaves it out; the task's
  own value wins. Register allows one only on an optional text parameter with choices and a default, never secret
  references, an alias, a dot or a call shape. Set by `--setting` (CLI), the runbook kind's `method_settings` launch
  field (template, saved configuration, launch, merged per key, checked against the installed module catalog through
  `launch.FieldSpec.Check`), and a rollback (`POST /jobs/{id}/rollback`, the UI form), only when the template opens the
  field. The Runner re-checks (`launch.StrictMethodSettings`, `RunSettings.Check`) and fills (`engine.WithRunSettings`).
  First reader: `file.remove`'s `check_forbidden`. Phase 46 is now 34 of 34.
- **Proof.** `TestCLI_FileRemoveCheckOnBusyBox` (real binary, real Alpine 3.22.8 BusyBox sshd, root and non-root
  `deploy`) and `TestMethodSettingsReleaseGate_ARunSettingReachesTheMethodOnARunner` (real NATS, Agent, native Adapter
  and sshd). Each gate fails with `WithRunSettings(nil)` in its tier. Unit tests in `pkg/collection`, `internal/engine`,
  `internal/launch`, `internal/launch/kinds/runbook` (the Controller's real embedded catalog), `internal/adapters/native`,
  `internal/api` (three new rollback tests), `internal/ui/resources/jobs` and `templates`, and `cmd/pleiades`.
- Coverage at or above floors: `pkg/collection` 100, `internal/launch` 91.5, `kinds/runbook` 100, `internal/engine` 95.6,
  `internal/adapters/native` 93.8, `internal/api` 98.4. `go vet -tags integration ./...`, `docs-gen-check` and
  `docs-lint` clean.
- PR #118's pull_request run 42147147309 was recorded with `record-actions` on 2026-11-06 (green, 0 new entries).

### Current blocker

None. Next is pr-check for this branch.

### Open

- (a) The CLI journal does not record which `--setting` a run used (`ParamKeys` come from the unfilled task). Harmless
  while the only setting changes checks, which are never journaled.
- (b) No end-to-end test takes a launch's `method_settings` through `job.Fields` into a dispatch, or the rollback route
  through to a Runner; the Walk gate builds the payload by hand.
- (c) A run setting declared on a concrete method behind a generic `svc.*`/`pkg.*` dispatcher is never filled when the
  task names the dispatcher.
- (d) `stash@{0}` ("WIP on ssh-sweep-b") holds `pkg/remoteexec` changes not on `main` (exact-match `withoutCommand`,
  `notSentError` for detached start failures, a `stopScript`). Later merged commits `a1243fae`, `6bef8e91` and
  `49688a6e` reworked the area. Awaiting the owner: its own PR, or drop it.
- (e) System, Organization and Project levels for run settings are not built; they join beneath the template with
  Phases 104 and 103c (notes added to both).
- (f) Carried: the owner question on dating a value `facts.Resumed` writes back (before 41d);
  `internal/catalog/gate/runner.go` `deviceRunner.stop` refuses only `pid <= 0`; API credit expires 2026-11-08; and
  the items carried in the archive's previous entries.

### Next, in order

1. `make pr-check` through the monitor, push, `make branch-containers`, the pull request. Merge only when green,
   `record-actions`, pull `main` and clean.
2. The facts churn PR, sweep items 4, 5, 6 and 10.
3. Phase 41, 41b to 41g, then 117b, 117c, 105, then release 0.4.0 (no tag until the owner says).

### Files changed on this branch

`pkg/collection/` (`doc.go`, `paramtype.go`, `run_settings.go` and its test, new), `internal/engine/`
(`check_mode.go`, `collection_action.go`, `run_settings_test.go`), `internal/launch/` (`fields.go`, `launch.go`,
`method_settings.go` and its test, `kinds/runbook/runbook.go`, `kinds/runbook/method_settings_test.go`),
`internal/adapters/native/` (`adapter.go`, `method_settings_test.go`), `internal/api/dispatcher_rollback.go` and its
test, `internal/apispec/template_endpoints.go`, `internal/ui/resources/jobs/rollback.go` and its test,
`internal/ui/resources/templates/` (`actions.go`, `saved_value_internal_test.go`), `internal/catalog/file/remove.go`,
`internal/forge/catalogdata/collections_files.go`, `internal/forge/collectionscaffold/docrender.go` and its test,
`internal/clispec/clispec.go`, `cmd/pleiades/` (`doc.go`, `run.go`, `run_pipeline.go`, `run_setting_internal_test.go`,
`file_remove_busybox_release_gate_test.go`, `testdata/alpine-sshd/Dockerfile`),
`cmd/runner/method_settings_release_gate_test.go`, `tools/gendocs/modules.go`, `docs/10-running-in-production.md`,
generated `docs/reference/cli.md`, `docs/reference/modules/file/remove.md`, `docs/reference/schemas/module-catalog.json`,
`docs/reference/schemas/openapi.json`, `internal/api/wellknown/module-catalog.json`, `internal/api/wellknown/openapi.json`.
Records (uncommitted): `FAILURE_PATTERNS.md` and its archive (939 to 941), `LESSONS_LEARNED.md` and its archive (400),
`HANDOFF_DOCUMENT.md`, `HANDOFF_ARCHIVE.md`, the new fragment `changelog/method-run-settings.added.md`.

Lab: unchanged; no lab VM use was reported for this session.
