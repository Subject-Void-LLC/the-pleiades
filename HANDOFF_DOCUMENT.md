# Handoff Document

Rewrite the "Current Status" section when stopping mid-task or handing off, per `.AGENTS/AGENTS.md`.

## Current Status (this session)

**Branch `feature/virtualbox-support` (from `c481a03`), nothing committed yet.** The user installed
VirtualBox on the Windows host to give the agent VMs to test against, then set the rules: VMs are
provisioned by Pleiades only, anything missing is built through `pleiades forge`, and Phase 75 (WinRM,
the three execution modes) is in scope here, Phase 76 not required. So Phase 75 was built first,
because Phase 112 (VirtualBox, entered in the tracker) runs `VBoxManage` over WinRM.

### What works (unit-tested, race-clean in all 25 touched packages)

- **Security fix, measured (FAILURE_PATTERNS 354, vuln corpus C13):** `pkg/winrmsvc` and
  `pkg/winrmdism` doubled only `'` when quoting for PowerShell; U+2018 to U+201B also end a literal, and
  a service or feature name containing one ran as PowerShell. Now one `winrmexec.QuotePS` doubling all
  five, fuzzed and mutation-checked. Swept every repo in `~`: no other instance.
- **Phase 75 core, no library fork:** `pkg/winrmexec/wsman.go` builds the shell create and Command
  messages itself and posts them through the library's exported transports. `WINRS_SKIP_CMD_SHELL=TRUE`
  on every command; text is XML-escaped (no CDATA); three modes in `command.go` (`none` via
  `CommandLine`/`QuoteArg`, `cmd` as `cmd.exe /d /v:on /s /c`, `powershell` with `-NoProfile
  -NonInteractive` and an exit-code epilogue); `Execute`/`RunWithStdin`; env vars on the shell;
  `NotStartedError` for retry; length ceilings. `internal/transport` gains `Shell` and `ShellTransport`,
  `internal/transport/winrm` is the adapter, `winrm_exec` is bound in both composition roots, and
  `exec.winrm.shell` supports `none` and `env`. `windows_server` gains `WindowsShellCapable`.
- **Two measured corrections to Phase 75's plan**, both recorded there and in LESSONS 245/246: a cmd
  env value read as `%NAME%` is re-parsed (it injected), so `/v:on` and `!NAME!`, with `%NAME%` refused;
  and `WindowsShellCapable` has no parent, because a child declares its parent and nothing checks a
  method's transports (new Phase 75 item).
- Docs 10 and 11, the lab README, two changelog fragments; reference docs regenerated and stable.

### The lab setup script, rewritten to least privilege (FAILURE_PATTERNS 355)

The user asked whether the account `winrm-cert-setup.ps1` creates was scoped and whether credentials
were vaulted. It was not scoped: `winrm quickconfig` (sets `LocalAccountTokenFilterPolicy`, opens 5985
to the LAN), a full-control RootSDDL grant to the whole Remote Management Users group, a printed
password reused as the PFX passphrase, the CA and client keys left on the host (two were still there,
measured), and a standard user's Modify on `D:` and `G:`. Rewritten, with shared helpers in
`winrm-lab-common.ps1` and a teardown that revokes from `lab-state.json`; parse-clean, helpers exercised
unelevated, not yet run elevated. On vaulting, the honest answer stands: Pleiades encrypts credentials
with AES-256-GCM but keeps `master.key` beside them, which is encryption at rest, not a vault
(`PLEIADES_MASTER_KEY` can come from a real secret store; Phase 78 has one of nine sources built). New
vuln-corpus class C16. The two leftover `PleiadesGate` certificates in `LocalMachine\My` are the user's to
delete.

### The host, set up by the user, and what the first real runs found

The user ran the rewritten setup (two script bugs fixed on the way: a 51 character account
description, and a policy readback that looked for `*SID` where `secedit` writes the account's name).
The least-privilege settings all hold: `GXGR` opens a shell, no Remote Management Users, all four logon
rights denied. The first gate run against the host (Windows 11 build 26200, certificate auth on 5986)
found Phase 75's premise false: **Windows ignores `WINRS_SKIP_CMD_SHELL`** and runs every command as
`cmd.exe /C` (FAILURE_PATTERNS 356), and the service does not decode numeric character references. Now
every line is escaped until that cmd.exe passes it through unchanged (`pkg/winrmexec/cmdexe.go`), text
uses named entities only, and the Adapter unlocks a PFX credential (357). **Both gates now pass on the
host:** `TestModesReleaseGate` (`pkg/winrmexec`) and `TestWinRMModesGate_ThreeModesThroughTheBinary`
(`cmd/pleiades`), plus the existing certificate gate. To rerun them: `PLEIADES_WINRM_HOST=172.18.32.1`,
`PLEIADES_WINRM_PFX` and `PLEIADES_WINRM_PFX_PASSWORD_FILE` from `C:\Users\raymo\pleiades-gate`,
`PLEIADES_WINRM_CA` (package gate) or `SSL_CERT_FILE` holding the lab CA as PEM (binary gate).

### Next

1. Phase 75's remaining gates: `make gosec` (Schema/Injection) and a stress/benchmark (Fuzz/Stress);
   record a coverage floor for `internal/transport/winrm`. Then import the PFX into the lab project's
   encrypted store and delete `client.pfx` and its passphrase file (the user's rule: no secrets left
   lying around).
2. Phase 112: probe VBoxManage from a WinRM logon, then forge `vbox_host`, `pkg/vboxmanage` and the
   `virt.vbox.*` methods, and provision the lab (Ubuntu cloud image, Windows Server 2025, FreeBSD).
3. Open Phase 75 items: breaker to a shared package; transport validation.

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
