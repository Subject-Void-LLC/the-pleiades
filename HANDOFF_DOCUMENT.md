# Handoff Document

Rewrite the "Current Status" section when stopping mid-task or handing off, per `.AGENTS/AGENTS.md`.

## Current Status (this session)

**2026-11-06. Branch `fix/facts-churn`, in the main clone, on `main` at `1d0c9ad8` (PR #122 merged). Two commits:
`3660d3e5` (the fix) and `a9c1a6a2` (review fixes). Not yet through pr-check, push or a pull request.**

### What works

- **Facts hold Ansible's values and no stable fact moves on its own.** The Mac, Windows and FreeBSD lab gates pass with
  the fix and fail on `main`'s code.
  - FAILURE_PATTERNS 947: `ansible_distribution`, its version and release hold Ansible's values, and
    `ansible_os_family` is new. Held to `ansible.builtin.setup` (ansible-core 2.17.1) in 22 images
    (`TestGatherOSRelease_MatchesAnsibleOnEveryMeasuredDistribution`) and live in 4 containers
    (`TestGatherDistribution_MatchesAnsibleOnRealDistributions`).
  - 948: macOS per-login launchd jobs are left out (`TestCLI_FactsGatherOnMacOSKeepsItsServicesAcrossLogins`).
  - 949: Windows per-user service instances are left out, templates listed with `per_user`
    (`TestCLI_FactsGatherOnWindowsReportsPerUserServicesByTemplate`, opt-in `PLEIADES_WINRM_SIGN_IN=1`).
  - 950: a service's state is the volatile `service_state.<name>`
    (`TestCLI_FactsGatherOnWindowsKeepsItsServicesAcrossGathers`; checked in the store with
    `pleiades facts win-check --json`).
  - 951: no swap on macOS, 0 on Windows, Windows memory rounded up (the Windows and Mac lab gates).
  - 952: the Mac lab gate's launchd check no longer counts lines (test fix).
  - FreeBSD (bsd-check): 168 services unchanged across two gathers, family `FreeBSD`.
  - Coverage: `internal/catalog/facts` 100% (its floor), `internal/forge/catalogdata` 100%, `internal/forge/playbook` 92.4% (floor 92.1).
- **Reviews.** fp-reviewer: F1 (family guess), F3 (PowerShell partial reads), F5 (docs) and F6 (test independence)
  fixed; F2 (systemd per-login units) and F4 (BSD and macOS release) are follow-ups. A second-model review (Fable,
  $1.42) found the `debian_version` first-line read, fixed; its exit-status finding is not a defect, since
  `remoteexec.Conn.Run` returns a nonzero exit with a nil error and `Gather` ignores the status.
- Lessons 404 (measure a fact against Ansible) and 405 (gather twice, and across a sign-in).

### Current blocker

None. Next is pr-check for this branch.

### Open

- (a) 953 (open): win-check hung once at a black screen on the boot after a guest restart; cause not proven. Its
  Groundconfig entry (`knowledge/unreviewed/virtualbox_whpx_guest_restart_boot_hang.txt`) awaits the owner's review;
  its class inference (C7) does not fit CWE-459.
- (b) A worktree agent (allowed by the owner) works FAILURE_PATTERNS 448, 560, 551 and the systemd per-login units in
  order on local branch `fix/open-patterns-batch2` (`.claude/worktrees/agent-aba449b747d465567`), never pushed,
  waiting for this pull request to merge. It has taken no record numbers yet; this branch took 947 to 953 and lessons
  404 and 405.
- (c) The second backlog is in memory `open-fp-fable-backlog`: 690, 483, the BSD and macOS release, and Ansible
  fidelity gaps (Raspbian, the NA parser's VERSION fallback (the backlog's own words), CentOS 7, `ansible_pagefiletotal_mb`, the Windows
  start modes Windows flips).
- (d) The 2026-11-04 stash is gone. `stash@{0}` is now the GNS3 work set aside on `lab-captures-129b` (2026-10-25):
  do not drop it.
- (e) Carried: the open failure patterns needing hardware or owner input (448, 483, 551, 560, 602, 664, 674, 689, 690,
  726, 742, 747, 767), the run settings gaps and the owner question on `facts.Resumed` dating, as in the previous
  entry.
- API credit: $48.75 left, expiring 2026-11-08. Spent this session: research r1 $3.38 (max effort, all tokens spent
  thinking, no answer), r2 $1.74, review $1.42.

### Next, in order

1. `make pr-check` through the monitor, push, `make branch-containers`, the pull request. Merge only when green,
   `record-actions`, pull `main` and clean.
2. The worktree agent's branch, `fix/open-patterns-batch2`.
3. Phase 41, 41b to 41g, then 117b, 117c, 105, then release 0.4.0 (no tag until the owner says).

### Files changed on this branch

`internal/catalog/facts/` (`gather.go`, `gather_distribution.go` (new), `gather_subsets.go`, `gather_bsd.go`,
`gather_bsd_items.go`, `gather_win.go`, `probes_export.go`, their tests, `testdata/distribution/`,
`live/distribution_test.go`, `live/openrc_test.go`), `internal/forge/catalogdata/collections_gating.go`,
`internal/forge/playbook/modules_misc.go`, `cmd/pleiades/facts_lab_release_gate_test.go`,
`cmd/pleiades/facts_churn_lab_release_gate_test.go` (new), and the regenerated `docs/reference` and
`internal/api/wellknown`. Records (uncommitted): `FAILURE_PATTERNS.md` and its archive (947 to 953),
`LESSONS_LEARNED.md` and its archive (404, 405), `HANDOFF_DOCUMENT.md`, `HANDOFF_ARCHIVE.md`,
`changelog/facts-gather-ansible-distribution.fixed.md`, `changelog/facts-gather-os-family.added.md`,
`changelog/facts-gather-service-churn.fixed.md`.

Machine: unchanged. Lab: changed. win-check was power cycled after the hung boot (953), and autologon of the throwaway
user was cleaned by hand and checked. The lab project `~/pleiades-lab` gained `runbooks/win/usersvc-churn.yaml`, and
its fact store holds per-user instance facts from runs of `main`'s code. win-check and bsd-check are powered off (ACPI, not saved); the Mac was used only through pleiades for gathers, 49 GB free before and after, nothing left in its TMPDIR. `stash@{0}` is the GNS3 work set aside on purpose (lab-captures-129b, 2026-10-25): never drop it; the 2026-11-04 stash is already gone.
