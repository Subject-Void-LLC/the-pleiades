# Handoff Document

Rewrite the "Current Status" section when stopping mid-task or handing off, per `.AGENTS/AGENTS.md`.

## Current Status (this session)

**2026-11-03. Branch `fix/controller-catalog`, two commits on `main` at `a29afac9`: `8d8bdd6b` (the Controller reads
built-in manifests from its embedded catalog) and `248eb894` (`file.remove`'s check warns when the device forbids the
removal), plus the records commit. Not through review, `make pr-check`, push or a pull request yet.** PR #106 (Phase 41b, `feat/facts-41b`) was merged by the owner as `a29afac9` with all checks green.

### What works (each proven by a test that ran)

- **Phase 41b is 8 of 8** (`implementation.py --json --phase 41b`: done 8, total 8, no open gates).
- **FAILURE_PATTERNS 841, fixed on this branch.** The shipped Controller links no Collection, so its device admission
  (`dispatch.methodsAdmit`) checked nothing and `POST /jobs/{id}/rollback` refused every recorded undo, `v0.3.0`
  included. `pkg/collection`'s `InstallCatalog`/`BuiltinManifest`/`ManifestOf` fall back to the module catalog
  `cmd/controller` installs at start. Tests: `TestWorker_AdmitsFromTheInstalledCatalogInABinaryWithNoRegistry`
  (mutation-checked), `TestInstallBuiltinCatalog_TheControllerTrustsABuiltInsRecordedUndo` (in the Controller's own
  test binary) and `TestTheControllerReadsNoRegistryItDoesNotFill` (archtest, mutation-checked).

### Current blocker

None. Next is review, then the gate.

### Open

- Owner decisions carried over: (a) external Collection Landlock confinement has no network rules
  (`internal/loader/confine_linux.go`), so a Collection handed credentials has unrestricted egress; (b) the Controller
  does not check a Runner's reported order token or Runner label; (c) the `secret_mask` residuals in 836; (d)
  `file.remove`'s check did not consult `remotefile.Removable`; `248eb894` now warns in the check's result when it
  says no (a warning, not a refusal, since it answers no where it cannot tell), for the owner to accept or not.
- Owner questions, with this session's answers waiting on the owner: the Mac is offline, so Phase 41c can ship claiming
  Linux, FreeBSD and Windows with the macOS gatherers as a follow-up item (owner to confirm); Phase 41's encryption
  item depends on Phase 5, which is not in v0.4.0 (owner to decide).
- FAILURE_PATTERNS 842 (macOS lock open ENOENT) is open: cause not proven, mitigated by a bounded retry; PR #106's run
  41562963070 reported 0 retries. The Mac is offline, so no hand reproduction.

### Next, in order

1. The fp-reviewer pass over this branch and its findings.
2. `make pr-check` through the monitor, push, `make branch-containers`, then the pull request (failure-pattern classes
   in the body, no AI attribution).
3. Merge only when green, `record-actions`, pull `main` and clean.
4. Phase 41c.

### Files changed this session

`pkg/collection/catalog.go` and its test, `cmd/controller/builtin_catalog.go` and its test, `cmd/controller/main.go`,
`internal/dispatch/worker_devices.go` and `worker_method_admission_test.go`, `internal/rollback/verify.go` and
`steps.go`, `internal/engine/journal_entry.go`, `internal/archtest/controller_registry_test.go`, and
`changelog/controller-builtin-catalog.fixed.md` (`8d8bdd6b`); `internal/catalog/file/remove.go` and
`predicted_test.go` (`248eb894`, no changelog fragment). Records: `FAILURE_PATTERNS.md` and `FAILURE_PATTERNS_ARCHIVE.md`
(841, 842), `CI_LESSONS.md` (24), the local Actions ledger (842 annotated), `HANDOFF_DOCUMENT.md`,
`HANDOFF_ARCHIVE.md`.

Lab: Unchanged: bsd-check and win-check are shut down.
