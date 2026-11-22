# Handoff Document

Rewrite the "Current Status" section when stopping mid-task or handing off, per `.AGENTS/AGENTS.md`.

## Current Status (this session)

**2026-11-22. Branch `feat/41-identity-facts` (worktree `.claude/worktrees/id-facts`), two commits on `main` at
`02d4b75b` (the #226 merge): `fa848cea` (1368's rerun result) and `8a8fd430` (identity facts on FreeBSD, macOS and
Windows). No pull request yet. This handoff was written by the records pass, which ran no test and no gate; the test
names below are the branch's own proof and were read, not run again.**

### What works

- `facts.gather` reports `os.machine_id`, `hardware.system_uuid`, `hardware.serial` and `hardware.virtualization` on
  FreeBSD, macOS and Windows, measured first on lab guests. Proof, through `pleiades`, all passed 2026-11-22:
  `TestCLI_FactsGatherOnFreeBSDReportsItsIdentity` (bsd-lab), `TestCLI_FactsGatherOnMacOSReportsItsIdentity` (the
  owner's Mac), `TestCLI_FactsGatherOnWindowsReportsItsIdentity` (win-lab), in
  `cmd/pleiades/facts_identity_lab_release_gate_test.go`.
- A VirtualBox guest on Linux reads `vm:oracle`, not `cloud:oracle` (FAILURE_PATTERNS 1369, fixed). Proof:
  `TestLinuxVirtualization_VirtualBoxIsAVirtualMachine`, and the branch binary on `ubuntu-lab`.
- FreeBSD reads its identity only when `security.jail.jailed` is exactly 0; a jail reads `container:jail`, and
  Linux's machine id read no longer runs there (FAILURE_PATTERNS 1370, fixed). Proof:
  `TestFreeBSDIdentityReads_SkipAJail`. Not proven on a real jail.
- Records: FAILURE_PATTERNS 1369 to 1371; the tracker note on Phase 41's identity item (not ticked). No lesson and no
  CI lesson: no pr-check or Actions failure was reported. The branch's changelog fragment
  (`changelog/facts-identity.added.md`) already exists.

### Blocker

- None.

### Open

- FAILURE_PATTERNS 1371, fix not started: each `pleiades run` ends by reading every other journal in the project
  (`journal.ChangesDuring`), with a 200 ms sleep before a second try at each one it cannot read, finished runs
  included.
- Phase 41's identity item: one serial per component for network gear; the Ansible names (Windows'
  `ansible_machine_id` needs the owner's decision and a measurement); a real jail; a macOS VM; a Hyper-V or VMware
  Windows guest. On SMBIOS older than 2.6 one VM can report two UUIDs across OSes.
- For the owner: `tests/mac/.env`, `tests/bsd/.env` and `tests/win/.env` are missing from the main clone (only the
  `.env.example` files exist). `tools/maccheck` and `tools/wincheck` read them from the main worktree, so pr-check has
  nothing to run its Mac, FreeBSD and Windows legs against. Not restored: they hold machine addresses only the owner
  knows.
- Carried and not re-checked: FAILURE_PATTERNS 1365 and 1366 and the rest of the last handoff's open list
  (`HANDOFF_ARCHIVE.md`).

### Next steps

1. Act on the fp-reviewer's findings for the branch.
2. Commit these records, then `make pr-check` on the committed, clean tree.
3. Push, then `make branch-containers`.
4. Open the pull request when both pass.

### Files changed

The branch: `internal/catalog/facts` (`gather.go`, `gather_bsd.go`, `gather_identity.go`, `gather_identity_bsd.go`,
`gather_identity_win.go`, `gather_win.go` and their tests), `internal/forge/catalogdata/collections_gating.go`,
`cmd/pleiades/facts_identity_lab_release_gate_test.go`, the generated module reference and catalogs,
`changelog/facts-identity.added.md` (replacing `facts-identity-linux.added.md`). Records in this pass:
`FAILURE_PATTERNS.md`, `FAILURE_PATTERNS_ARCHIVE.md`, `HANDOFF_DOCUMENT.md`, `HANDOFF_ARCHIVE.md`, and the tracker.

Lab: bsd-lab, ubuntu-lab and win-lab were started and shut down through ACPI; all are powered off. bsd-lab was
onboarded (`FactGathererCapable` added to its `discovered`). bsd-check (4096 MB) could not start: the host had
3830 MB free and keeps 1024 MB back.
