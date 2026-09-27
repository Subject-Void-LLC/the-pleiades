# Handoff Document

Rewrite the "Current Status" section when stopping mid-task or handing off, per `.AGENTS/AGENTS.md`.

## Current Status (this session)

**Branch `feature/virtualbox-support`, 2026-09-27: Windows Server guests (the user: "Build both
methods", the VHDX and the ISO).** Rules unchanged: the lab is provisioned by The Pleiades only, gaps
are built through `pleiades forge`, "is this how a Pleiades end user would do it?", runbook tasks use
the method-as-key form (never `fqcn:`/`params:`), and temporary tooling becomes real methods.

### Where it stands

- **Committed, not pushed:** `3d2982b` (lab runbooks in the method-as-key form), `955e62f` (Windows
  guests), `e9a3cc5` (install's wait split out), and the findings commit after them.

- **Built and tested (model host):** `pkg/winunattend` (answer files rendered in Go on a Joliet ISO);
  `virt.vbox.vm.import_disk`, `install`, `eject_seed`, `screenshot`, `log`, `send_keys`, `addresses`;
  `wait.connection`; clone seeding a Windows base (answer file on a SATA DVD, default size small); the
  engine giving a login's password only to a method declaring `SeedsLoginPassword` and only for a
  device reached over WinRM; `add-credential --generate` passwords always meeting Windows' policy;
  `mediumio` ditto lines; `ErrLocked` for VirtualBox's pending-lock answer; resuming an install that
  stopped waiting. Catalog 104 registered / 101 implemented.
- **Measured on the lab host:** VHDX import works (2 min 46 s, EFI read off the disk); EFI + 2 vCPUs
  hangs at `DXE_AP` (1 vCPU boots; measured while the user's watcher pinned VBoxSVC to the P-cores);
  Microsoft's VHDX never reads a seed DVD at first boot (FAILURE_PATTERNS 361); the ISO install runs
  unattended at 2 vCPUs on BIOS in about 8 minutes; the four windowless methods work on the host.
- **The ISO path works end to end on the lab host:** install (5 to 9 min), clone (small), first boot
  reads the seed through the `UnattendFile` registry pointer the install sets (a generalized Windows
  never searches a DVD; FAILURE_PATTERNS 364), WinRM as the vaulted Administrator at 192.168.56.30 in
  82 s, eject (the file is deleted only once the VM is stopped; 365), restart and WinRM in 17 s.
  `win-lab` is running now.
- **Open:** the VHDX's clones cannot be seeded (nothing in that image points Windows at the DVD).
- **Diagnostic leftovers in `~/pleiades-lab`:** the `win-diag` device (a throwaway password), and
  scratch runbooks under `runbooks/win/`.

### Next

1. Run the env-gated WinRM release gates against `win-lab` (the plan's payoff), and re-measure the
   EFI two-vCPU hang with the user's pinning watcher stopped.
2. The VHDX: put the answer file into the image before first boot, or document it as unseedable.
3. Follow-ons recorded in Phase 112: `--vault`/`--inventory` flags; the docs corpus for collections
   (asked, not yet answered).

### Files changed this session

`pkg/winunattend` (new), `pkg/vboxmanage` (create, keyboard, leases, EjectDVD, OSType/Firmware,
ErrLocked, the model host), `pkg/collection` (SeedsLoginPassword), `pkg/wire` (SecretSeedPassword),
`internal/engine/login_seed.go`, `internal/credential/generate.go`, `internal/catalog/virt/vbox/vm`
(seven new methods, clone's Windows seed, delete's leftovers), `internal/catalog/wait/connection.go`,
`internal/forge/catalogdata`, `cmd/pleiades/run.go`, `examples/virtualbox_lab` (method-as-key form,
Windows runbooks and README section), docs/reference, `internal/api/wellknown`, CLAUDE.md counts,
`coverage-floor.json`, changelog, FAILURE_PATTERNS 360-365, LESSONS 248. Local only:
`IMPLEMENTATION.md` (Phase 112 items, the `--vault`/`--inventory` follow-on).
