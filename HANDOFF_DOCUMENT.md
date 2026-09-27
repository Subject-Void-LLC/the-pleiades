# Handoff Document

Rewrite the "Current Status" section when stopping mid-task or handing off, per `.AGENTS/AGENTS.md`.

## Current Status (this session)

**Branch `feature/virtualbox-support`.** Rules set by the user: the VirtualBox lab is provisioned by
The Pleiades only, gaps are built through `pleiades forge`, and every step is checked with "is this how a
Pleiades end user would do it? if not, it's wrong" (memory `end-user-path-or-wrong`).

### Where it stands

- **The Pleiades makes, seeds, boots, trusts and manages Ubuntu VMs on the lab host**, all through the
  CLI: `examples/virtualbox_lab` is the walkthrough, run on VENGEANCE. `win.file.download` fetched the
  cloud image, `virt.vbox.vm.import_ova` + `snapshot.take` made a never-booted base, `vm.clone`
  (seeded login, NoCloud seed rendered in Go and sent on WinRM stdin), `vm.start`, `vm.host_keys`,
  `pleiades trust-host --from-console`, then `pleiades run` against `ubuntu-lab` (192.168.56.10) as root
  by key. `vm.delete` and `vm.list` also run on the real host. `ubuntu-lab` is running now.
- **Credentials (the user's choices):** `add-credential --generate` (random ed25519 key + password in
  the vault); `Manifest.SeedsLogin` and `engine.WithLoginSeeder` hand a creating method only the user
  name, public key and a fresh SHA-512 crypt hash (`pkg/shacrypt`). CLI only: the Walk tier refuses.
  A key-plus-password credential now logs in key first (`remoteexec.AuthFrom`).
- **Host keys (the user's choices):** from the serial console over WinRM (`--from-console`), and a warned
  `--first-connect`; `internal/hosttrust`. Changed keys need `--replace`.
- **Found:** a two-CPU guest hung at the initramfs raid6 benchmark in two of three first boots on this
  Hyper-V host; the lab uses one CPU (tracker). The user sees no VMs in their own VirtualBox Manager
  because the VMs belong to `pleiades-gate`; they chose to see them through The Pleiades (`vm.list`,
  and `run -v` now prints lists as YAML).
- **Not built yet:** the Walk tier's seeded login, `become`, deleting the seed ISO after first boot, the
  VirtualBox sync plugin, Windows and FreeBSD guests, the env-gated Release Gate for `virt.vbox.*`.

### Next

1. The VirtualBox sync plugin (VMs into inventory), then the Release Gate, then Windows Server guests.
2. Walk-tier seeding, and `become`.
3. Lab project: `~/pleiades-lab` (binary, inventory, runbooks used for every probe).

### Files changed this session

`pkg/cloudinit`, `pkg/iso9660`, `pkg/shacrypt`, `pkg/vboxmanage` (appliance, hardware, files,
extradata, the model host `vboxmanagetest`), `pkg/remoteexec/auth.go` and its test server,
`pkg/collection` (SeedsLogin), `pkg/wire` (seed keys), `internal/credential/generate.go`,
`internal/engine/login_seed.go`, `internal/hosttrust`, `internal/catalog/virt/vbox/*`,
`internal/catalog/win/file`, `internal/forge/catalogdata`, `cmd/pleiades` (add-credential --generate,
trust-host, run -v YAML), `internal/clispec`, `examples/virtualbox_lab`, docs 10, the Windows lab
README, `changelog/`, `docs/reference/`, `internal/api/wellknown/`, `coverage-floor.json`, CLAUDE.md
counts, and the second "The Pleiades" rename pass redone across 65 Go files. Local only:
`IMPLEMENTATION.md` (Phase 112).
