# Handoff Document

Rewrite the "Current Status" section when stopping mid-task or handing off, per `.AGENTS/AGENTS.md`.

## Current Status (this session)

**2026-11-03. Branch `feat/facts-41c-1`, 15 commits on `origin/main` at `e047af00` (PR #108 merged), `f648b8ec` to
`e9be6fd2`, plus the records (uncommitted). Done and reviewed; not through `make pr-check`, push or a pull request
yet.** PRs #107 (`fix/controller-catalog`) and #108 (`fix/records-842-image-save`) are merged.

### What works (each proven by a test that ran)

- **Phase 41c-1: `facts.gather` gains `gather_subset`** (`min`, `hardware`, `packages`, `services`, `all`, `!x`) and
  one stable fact per item, `package.<name>` and `service.<name>`, on four OSes. Linux: dpkg, rpm, apk; systemd and
  OpenRC (so Alpine). FreeBSD: pkg, rc, sysctl. macOS: sw_vers, sysctl, Homebrew (only as its owner, never root),
  pkgutil, launchd. Every SSH probe runs under `sh -c`. Windows over WinRM: CIM, both registry Uninstall views,
  `Win32_Service`; `windows_server` is `FactGathererCapable`. The converter maps `ansible.windows.setup`,
  `package_facts` and `service_facts`.
- Proof: lab gates `TestCLI_FactsGatherOnFreeBSDMatchesItsOwnTools` (bsd-check, FreeBSD 15.1),
  `TestCLI_FactsGatherOnMacOSMatchesItsOwnTools` (the lab Mac, macOS 27.0.1) and
  `TestCLI_FactsGatherOnWindowsMatchesItsOwnTools` (win-check, Server 2025); `TestCLI_FactsReleaseGate` (Alpine apk,
  through tcsh and fish logins too); `internal/catalog/facts/live` (Rocky rpm, Alpine OpenRC booted and not);
  `FuzzGatherParsers`.
- Review found FAILURE_PATTERNS 844 to 856, all fixed before push except the open parts below. LESSONS 384 to 386.
- Phase 41c is 3 of 10 (`implementation.py --json --phase 41c`: Packages, Services, Bounds ticked; Pattern Entry
  Gate, Every OS and the Fuzz/Stress gate marked partial; open gates: Pattern Entry Gate, Fuzz/Stress Test).

### Current blocker

None. Next is the gate.

### Open

- The login-shell class (FAILURE_PATTERNS 845) in about 20 other SSH send sites. Owner decision 2026-11-03: every SSH
  command runs under `sh`, raw ones too, in its own pull request next (LESSONS 386 has the encoder).
- `internal/archtest` misses zero-width characters (851). A Runner publish failing mid-split leaves earlier batches
  stored (853).
- No fact marks a package or service as gone: an uninstalled package's fact stays current and only `last_seen_at`
  stops moving (for 41d). Whether a member of Remote Management Users alone can read the Windows probes is unproven
  (the lab login is an admin).
- Owner questions carried forward: Phase 41's encryption item depends on Phase 5 (not in v0.4.0); external Collection
  Landlock has no network rules; the Controller does not check a Runner's reported order token or label; the
  `secret_mask` residuals (836); whether `file.remove`'s check should refuse rather than warn.

### Next, in order

1. `make pr-check` through the monitor, push, `make branch-containers`, then the pull request (failure-pattern classes
   in the body, no AI attribution). Merge only when green, `record-actions`, pull `main` and clean.
2. The shell-agnostic pull request. Its test switch `PLEIADES_TEST_LOGIN_SHELL` is in `git stash` ("shell-agnostic:
   PLEIADES_TEST_LOGIN_SHELL switch"); a fish run of `cmd/pleiades` is in progress, and its result goes in that PR.
3. 41c-2 (files, ports, users), 41c-3, 41d to 41g, 117b, 117c, 105, then release 0.4.0 (no tag until the owner says).

### Files changed this session

`internal/catalog/facts/` (gather, subsets, packages, services, transport, BSD, Windows, `probes_export.go`, tests,
and the new `live/` package), `internal/facts/` (`facts.go`, `kind.go`, `file_store.go`, `entstore`),
`internal/adapters/native/facts.go`, `internal/engine/facts.go`, `pkg/collection/facts.go`,
`internal/forge/playbook/` (`adjust.go`, `modules_misc.go`), `internal/forge/catalogdata/collections_gating.go`,
`internal/inventory/devices/windows/server.go`, `internal/inventory/onboard/probe_ssh.go`,
`cmd/pleiades/facts_release_gate_test.go` and `facts_lab_release_gate_test.go`, `Makefile`,
`tools/testgate/durations.json`, `CLAUDE.md`, regenerated `docs/reference` and `internal/api/wellknown`,
`changelog/facts-packages-services.added.md`. Records: `FAILURE_PATTERNS.md` and its archive (844 to 856),
`LESSONS_LEARNED.md` and its archive (384 to 386), `HANDOFF_DOCUMENT.md`, `HANDOFF_ARCHIVE.md`, and the
ignored `.SPECIFICATION/IMPLEMENTATION.md` (41c) and `SECURITY_ATTESTATION.md` (PW.5).

Lab: bsd-check and win-check were started for their gates and are powered off again; the lab Mac ran its gate.
