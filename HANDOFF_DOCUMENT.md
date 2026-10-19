# Handoff Document

Rewrite the "Current Status" section when stopping mid-task or handing off, per `.AGENTS/AGENTS.md`.

## Current Status (this session)

**2026-10-18. Branch `fix/catalog-examples`, off `main` at `858e2f32` (#73 merged green).** One pull request at a
time; `make pr-check` before every push; the owner merges.

### What #73 shipped

- `make bsd-check` and `make win-check`. `make pr-check` runs them after the Mac leg, one at a time and beside the
  Linux passes. The FreeBSD and Windows legs drive the lab's `bsd-check` and `win-check` through `pleiades`:
  - each starts its VM, and saves its state when the run ends;
  - `PLEIADES_BSD_VM_HOST` and `PLEIADES_WIN_VM_HOST` are in `tests/*/.env`.
- `win.capability.install`, `win.capability.remove` and `win.reboot`.
- `virt.vbox.vm.stop mode: save`.
- `win.schtask` fixed for real hosts.
- Phase 122b's task `timeout:` and failed-task output.
- commitgate refuses a commit that shortens a record archive.

### This branch

The owner's point, mid-session: "That never should have gotten that far. The runbook sanity check should have
caught them."

- **The test:** `internal/archtest`'s `TestEveryDocumentedExampleValidates`, which pr-check always runs, builds
  every registered method's examples through `engine.Builder` and runs `validate.Validate` over them. That's 237
  examples.
- **Nineteen examples fixed, in both copies of each Doc** (the method package and `internal/forge/catalogdata`):
  - conditions that read registers by their bare names now read under `result`;
  - the restart examples compare `file.copy`'s diff halves where they read `changed`;
  - eleven `ai.*` and `edge.*` tasks lose `hosts:`;
  - `svc.windows.restart`'s example uses `win.file.copy`;
  - `file.copy`'s example template reads `vars`;
  - `container.image.load`'s example saves first.
- **Proof on `bsd-check`:** the copy condition fired on the first write, skipped when converged, and fired on a
  change. The `file.touch` and `file.line.remove` conditions behaved as documented.
- **Records:** FAILURE_PATTERNS 515 (a repeat of 324 and 514) and LESSONS 301. The changelog fragment is
  `catalog-examples-validate.fixed.md`.
- **A box fix, found by this branch's first pr-check:**
  - **What failed:** `win-check`, resumed from saved, answered one login and then nothing for over 20 seconds,
    which failed the Windows leg (FAILURE_PATTERNS 516).
  - **The fix:** `Box.Up` now requires three liveness checks in a row after a start. A live wincheck run then
    passed.

### Next

1. Gate this branch (coverage of the touched packages, then `make pr-check`), then open its pull request.
2. **Packages, by the owner's direction of 2026-10-18** (the tracker's Phase 122b item holds the edge cases):
   - **For Server Core, three backends the user picks among, with no AppX or UWP:**
     - Chocolatey, for system services and MSIs;
     - Scoop, for portable CLI binaries;
     - PSResourceGet, native PowerShell with no third-party bootstrap.
   - **First, a design note.** It covers:
     - digest-pinned bootstraps;
     - Scoop installing into the WinRM login's profile;
     - PSResourceGet managing PowerShell modules rather than applications, and needing PowerShell 7.4 or a
       PSGallery install on 5.1.
   - **Open:** whether `win.package` (an installer by digest) stays beside them.
   - `pkg.winget.*` for Desktop Experience only, refusing Server Core by name.
   - FreeBSD's `pkg(8)`, with the generic `pkg.*` dispatching to it.
3. **Phase 122b's other open items:**
   - the converter maps no Windows module;
   - `win.feature.*` parses DISM's state without `/english`;
   - there is no archive method for Windows;
   - run output does not stream;
   - `tools/maccheck` has no coverage floor;
   - `file.copy` from the Controller (the Walk tier) is still refused.

### Lab state

- Both check boxes are saved; leave them saved between checks.
- `win-lab` (not a check box) is often running, and the host keeps 1 GB, so don't run both check boxes at once.
- Three scratch files from the proof are still in `bsd-check`'s `/tmp` (`example-proof*`); the cleanup command was
  refused.

### Files changed this session

**#73** (merged): `tools/{bsdcheck,wincheck,internal/oscheck,commitgate}`, `pkg/winrmdism`,
`internal/catalog/win/{capability,reboot.go,schtask_script.go}`, `pkg/vboxmanage`,
`internal/catalog/virt/vbox/vm/{stop,start}.go`, the Makefile, `examples/virtualbox_lab`, and the records.

**This branch:**

- `internal/archtest/examples_test.go`;
- `tools/internal/oscheck/box.go` and its test;
- the doc literals in `internal/catalog/{ai,edge,svc,file,container}` and `internal/forge/catalogdata`;
- the regenerated `docs/reference` and `internal/api/wellknown`;
- the changelog fragment;
- `FAILURE_PATTERNS*`, `LESSONS_LEARNED*`, and `HANDOFF_*`.
