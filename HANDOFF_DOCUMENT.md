# Handoff Document

Rewrite the "Current Status" section when stopping mid-task or handing off, per `.AGENTS/AGENTS.md`.

## Current Status (this session)

**Branch `feature/virtualbox-support`.** Rules set by the user: the VirtualBox lab is provisioned by
Pleiades only, gaps are built through `pleiades forge`, and every step is checked with "is this how a
Pleiades end user would do it? if not, it's wrong" (memory `end-user-path-or-wrong`).

### Where it stands

- **Pleiades starts and manages VirtualBox VMs on the lab host as a least-privilege account**, through
  `pleiades run` and `exec.winrm.shell`, the host pinned with `set-host --set tls_ca_pem` and the
  certificate stored with `add-credential --pfx`. What the account needs, each found by a failure on
  the host, is in `examples/windows_lab/winrm-cert-setup.ps1 -AllowVirtualBox -VirtualBoxAutostart` and
  docs/10 ("A Windows host that runs VirtualBox"): COM launch on VBoxSVC/VBoxSDS; `DisableForceUnload`
  (machine-wide); VirtualBox's autostart service, because a VM cannot start from a non-admin network
  logon (VirtualBox ticket 20341). A CryptSvc grant was tried, did not help, and was removed.
- **Start path, measured:** a running VM means the chosen VBoxSVC is a service-logon one, so `startvm`
  works over WinRM; otherwise arm `--autostart-enabled`, wait for no VBoxSVC, `sc start
  VBoxAutostartSvcvengeancepleiades-gate`, wait for `running`, disarm. Probe VM `pleiades-probe`
  (diskless, 64 MB, `G:\PleiadesLab`) is powered off, autostart off.
- **`VirtualBoxCapable` is a capability of `windows_server`**, not a `vbox_host` type (the user's
  decision): `virtualbox: true`, optional `vboxmanage_path`, `vm_folder`. PLAN.md 1b revised.
- **Fixed on the way (all committed):** device CA for WinRM, including `winrm_exec` (`864b34f`);
  `pleiades set-host` and write-time validation in `add-host` (`4fa3b02`); CLIXML decoding
  (`303b268`); a certificate-auth command silent for a minute failed at 60 s (`554ddd5`); a timed-out
  command kept running on the host (`fa9acf0`).

### Next

1. `pkg/vboxmanage` (ShellNone argv through `winrmexec.CommandLine`, a fuzzed `--machinereadable`
   parser, strict VM and snapshot names) and the `virt.vbox.*` methods through `forge new-collection`,
   `vm.start` following the measured start path.
2. Media through Pleiades (a download method requiring SHA-256; Ubuntu publishes an OVA), then
   Windows Server and FreeBSD guests, the sync plugin, the gates.
3. Lab project: `~/pleiades-lab` (binary, inventory with `vengeance`, runbooks used for every probe).

### Files changed this session

`pkg/winrmexec/` (quote, command, exec, wsman, winrmexec, their tests, `modes_release_gate_test.go`,
`testdata/argvecho`), `pkg/winrmsvc`, `pkg/winrmdism`, `pkg/sdk/env.go`, `pkg/capability/capabilities_win.go`,
`pkg/collection/manifest.go`, `internal/transport/shell.go`, `internal/transport/winrm/`,
`internal/engine/` (action_shell, action_ssh, action_capability, transport_bindings, journal_entry),
`internal/catalog/exec/winrm/`, `internal/inventory/devices/windows/`, `internal/forge/catalogdata/`,
`internal/adapters/native/adapter.go`, `cmd/pleiades/run.go` and `winrm_modes_release_gate_test.go`,
`internal/archtest/transport_reachability_test.go`, docs 10 and 11, `examples/windows_lab/README.md`,
`changelog/`, `docs/reference/`, `internal/api/wellknown/`. Local only: `.SPECIFICATION/PLAN.md`
(1b, 14), `IMPLEMENTATION.md` (Phase 75 outcomes, Phase 112), FAILURE_PATTERNS 354, LESSONS 245 and 246,
`~/vuln-corpus` C13. A Pleiades lab project lives at `~/pleiades-lab` (binary, `known_hosts`).
