# Handoff Document

Rewrite the "Current Status" section when stopping mid-task or handing off, per `.AGENTS/AGENTS.md`.

## Current Status (this session)

**2026-10-27. Branch `feat/phase-129-blind-lab-fixes`, off `origin/main` at `21ecd314`, 32 commits: Phases 129a to 129d
as handed off on 2026-10-26, then 16 this session on top of `e3560066` (oldest first `8d868e52`, newest `e522586d`):
review fixes, the run record fix, the Mac's container half, `pkg/vmrun` and `virt.fusion.vm.*`, the drive-root fix for
`win.archive.extract`, and the Windows 11 ARM leg. Everything is committed through `e522586d` except these records. Not
pushed; no pull request yet.**

### What works

- Review fixes: `remove-host` reads the state file before it removes anything (`8d868e52`, `TestRemoveHost`); the engine
  refuses `all_at_plan_time` on a rescue task reading `device` (`33e1e945`, `ErrRescueLockAll`, rescue case in
  `TestDeviceRoot_EveryPathAndItsLimits`); the scaffold keeps an unknown `Format` and the module reference marks
  SecretRefs parameters (`e7eb6ec2`).
- testgate's run record keeps 400 lines of a failure (`2053f832`, `TestRecordFailureLines_KeepTheCauseBehindALoggedTeardown`).
- Mac container half: `maccheck -containers` (`5d7391d7`) and pr-check's split (`0a555bab`,
  `TestEveryContainerPackageIsClassified`). 18 packages passed on the Mac's Podman in 17m46s; `internal/ent` lost one
  race and passed alone.
- VMware Fusion: `pkg/vmrun` (`f4d292b4`, readers held to captures from a real Fusion 26.0.1), detached start and reset
  (`eaace13a`, `TestDetachScript_*`), `virt.fusion.vm.info`, `start` (with `keep_awake`) and `stop` plus
  `VMwareFusionCapable` and its probe (`14747aae`), the guest address asked again with `-wait` (`2846c008`, proven on the
  Mac after a resume).
- `win.archive.extract` into a drive root (`6465e007`, `TestScript_MakesADriveRootsTopFoldersFirst`), proven on the
  Windows 11 ARM guest with Go 1.26.10 arm64 and MinGit.
- `wincheck -arm64` (`b609aa03`) and the winarm leg chained on the Mac in pr-check (`4ba08943`): `winarmcheck` on 2
  packages passed in 59 s on 2026-10-27, after the fix to the binary it ran.
- Records: FAILURE_PATTERNS 668 to 679; LESSONS 338 to 342; CI_LESSONS 13 to 15; the e2e ledger entry annotated with
  668; five changelog fragments (`win-archive-extract-drive-root.fixed`, `pr-check-mac-containers.added`,
  `wincheck-arm64.added`, `testgate-record-lines.changed`, `reference-secret-refs.added`).

### Blocker

- None. `make pr-check` with the new split recipe has not run end to end yet.

### Open

- `tests/e2e`'s `TestUpgradeReleaseGate_ComposeUpgradesAndRollsBack` failed twice at `e3560066` and passed alone twice
  since; its cause is unknown (FAILURE_PATTERNS 668). The next failure's record should hold it.
- `go generate ./internal/forge/catalogdata` writes eight duplicate CML link and capture scaffolds on every run
  (FAILURE_PATTERNS 677). Check `git status` after any generate until it is fixed.
- Not built: `virt.fusion` install and create methods, and `pkg/winunattend` for arm64 and UEFI. Stale Fusion lock
  handling stays manual (674). No test holds wincheck's leg tool name (676).
- The VM `win11-arm` has 4 GB; raise it to 8 GB at its next clean shutdown.
- The Windows setup rename flake (`internal/setup`, Defender) has not recurred.
- Carried: F7, F8, F9 from the 129 review; FAILURE_PATTERNS 562, 647, 648, 649, 652 unchanged; the earlier list in the
  archive's 2026-10-26 entry.

### Next, in order

1. Commit these records.
2. `make pr-check` through `.venv/bin/python tools/monitor/monitor.py run --cap 4500M`: the first end-to-end run of the
   split recipe, with the `containers-mac` and `winarm` legs.
3. Push once and open one pull request for Phases 129a to 129d plus the Mac and Fusion work; tick each Release Gate
   with its evidence when Actions passes.
4. Then the open list above, and the order the owner set (release tooling, Phase 117b part 3, Phase 126).

### Files changed

- `8d868e52`: `internal/inventory/file_repository_remove*.go`. `33e1e945`: `internal/engine/` (`device_root.go`,
  `handlers_run.go`, tests). `e7eb6ec2`: `internal/forge/collectionscaffold/docrender.go`, `tools/gendocs/modules.go`,
  three reference pages. `2053f832`: `tools/testgate/`.
- `5d7391d7`, `0a555bab`, `b609aa03`, `4ba08943`: `tools/internal/oscheck/`, `tools/maccheck/`, `tools/wincheck/`,
  `tools/internal/flakegate/maccontainers_test.go`, `tools/internal/prreport/report.go`, `tools/monitor/`, `Makefile`,
  `tests/winarm/.env.example`, `.gitignore`.
- `f4d292b4`, `eaace13a`, `14747aae`, `2846c008`, `e522586d`: `pkg/vmrun/`, `internal/catalog/virt/fusion/vm/`,
  `pkg/capability/capabilities_vmware.go`, `internal/inventory/devices/generic/`, `internal/inventory/onboard/probe_ssh.go`,
  catalogdata, generated docs, `CLAUDE.md`, `README.md` (`4a975a97`), `docs/01-start-here.md`.
- `6465e007`: `internal/catalog/win/archive/extract*.go`.
- Records (uncommitted): `FAILURE_PATTERNS.md`, `FAILURE_PATTERNS_ARCHIVE.md`, `LESSONS_LEARNED.md`,
  `LESSONS_LEARNED_ARCHIVE.md`, `CI_LESSONS.md`, `HANDOFF_DOCUMENT.md`, `HANDOFF_ARCHIVE.md`, five new files in
  `changelog/`; git-ignored, the pr-check failure ledger and `.IGNORE/timings.md`.

### Lab state

Changed: the Mac is awake, its Podman machine stopped. The Windows 11 ARM VM `win11-arm` runs on the Mac's Fusion at
10.0.0.155, with Go 1.26.10 arm64 and MinGit installed; its lab project is `~/pleiades-fusion-lab` (hosts `mac`,
`win11-arm`), and `tests/winarm/.env` is set. Unchanged: `win-check` and `bsd-check` are saved on VENGEANCE.
