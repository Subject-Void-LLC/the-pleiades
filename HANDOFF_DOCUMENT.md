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
- **The POSIX-shell guard, everywhere:**
  - **What failed:** the next run's Windows leg failed 36 tests in `file/line`, `svc` and `svc/systemd`. #73 had
    guarded only the packages its own run reached (FAILURE_PATTERNS 517, a repeat of 510).
  - **The fix:** all 56 test functions that run commands through remoteexectest's `/bin/sh` server now call
    `need.POSIXShell`. `internal/archtest`'s `TestEveryShellServerTestNeedsAPOSIXShell` holds new ones to it. The
    13 that send no command are named in it with reasons.
- **Found by running the newly in-scope packages on FreeBSD and Windows, all fixed** (FAILURE_PATTERNS 518 to 521):
  - testgate dropped a failed table test's subtest output (`flakegate.FailureOutput`);
  - six tests set HOME without USERPROFILE; `TestEveryHomeOverrideCoversWindows` holds new ones to both;
  - `remotefile.PredictWrite` claimed an existing file's unnamed attributes survive a write;
  - two test flaws: a setgid fixture FreeBSD refused, and a directory test comparing two directories' mtimes.
- **Found building the lab's domain, fixed:** a WinRM fault arriving in an accepted reply lost its reason, and at a
  Receive looped until the deadline (`pkg/winrmexec`, FAILURE_PATTERNS 519).

### The lab's domain (lab.raymondrizzo.com), built at the owner's request

- **The runbooks** are in `~/pleiades-lab/runbooks/win/` (not the repository):
  - `dc-00-teardown`;
  - `dc-01-build` (Server 2025 Core at the 2016 functional level);
  - `dc-02-forest-upgrade` (to 2025, after a snapshot);
  - `dc-03-linux-create` (an extra-small Ubuntu member);
  - `dc-04-linux-join` (staged computer account, SSSD, an SSH logon test as a domain user).
- **Proven from nothing, back to back, every rerun a no-op.** No secret is in a file or any output: the passwords
  that cross go through `register_mask` and stdin.
- **What the passes found:**
  - promotion must restart the box itself;
  - a fresh clone's answer-file image must be deleted while the VM is off;
  - the domain's DNS records take up to about four minutes;
  - Server 2025's join hardening refuses an ordinary user's quota join (`realm` and `adcli` both), so the member
    joins as a staged computer account;
  - `always:` is parsed but not run, and `validate` says nothing (in the tracker).
- **State:** `lab-dc1` and `lab-lin1` are saved; `virt.vbox.vm.start` resumes them.

### Next

1. Gate this branch (coverage of the touched packages, then `make pr-check`), then open its pull request.
2. **One results document per pr-check run, with a reader** (planned with the owner; Phase 122b holds the plan):
   - `flakegate.Run` written by testgate's `-report-out`;
   - each OS leg reads its report back;
   - `tools/prreport` merges them into `.coverage/pr/report.json`.
3. **Every method proven on every OS it targets** (the owner, 2026-10-18: "Since we have all targeted OSes
   available, there is no longer an excuse for a module not working"):
   - an audit of which methods have run through `pleiades` on which lab box;
   - then the gaps closed method by method.

   It's tracked in Phase 122b.
4. **Packages, by the owner's direction of 2026-10-18** (the tracker's Phase 122b item holds the edge cases):
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
5. **Phase 122b's other open items:**
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
- `need.POSIXShell` in 50 test files across `cmd`, `internal` and `pkg`, and
  `internal/archtest/posix_shell_test.go`;
- `internal/archtest/home_env_test.go`, `tools/internal/flakegate/report.go`, `pkg/winrmexec/wsman.go`,
  `pkg/remotefile/predict.go` and their tests;
- the doc literals in `internal/catalog/{ai,edge,svc,file,container}` and `internal/forge/catalogdata`;
- the regenerated `docs/reference` and `internal/api/wellknown`;
- the changelog fragment;
- `FAILURE_PATTERNS*`, `LESSONS_LEARNED*`, and `HANDOFF_*`.
