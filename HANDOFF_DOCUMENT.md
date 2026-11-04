# Handoff Document

Rewrite the "Current Status" section when stopping mid-task or handing off, per `.AGENTS/AGENTS.md`.

## Current Status (this session)

**2026-11-03. Branch `fix/shell-agnostic-ssh`, four commits on `main` at `4711fff8` (PR #109, Phase 41c-1, merged):
`4a7db76d`, `142d53bb`, `09acef1e`, `aeff581b`, plus the records (uncommitted). Done and reviewed (fp-reviewer; all 14
findings fixed); the login-shell matrix is clean. Not through `make pr-check`, push or a pull request yet.**

### What works (each proven by a test that ran)

- **Every SSH command reaches `/bin/sh`, whatever the login shell** (owner decision 2026-11-03: raw `ssh_exec`
  commands too, as Ansible's shell module runs its command). `Conn.run` and `Conn.Start` encode each command with
  `pkg/remoteexec/shline` (LESSONS 386's encoder); `Conn.Raw()` is chosen only by `inventory.ExecIsPOSIX` (a
  network CLI, or onboarding's `shell` fact `not posix`). A NUL is refused, and a line at or over 128 KiB is refused
  before a session opens. FAILURE_PATTERNS 858 to 863, LESSONS 387 to 389; 845's open class is closed.
- Proof: `TestEveryLoginShellHandsShTheSameCommand` (real sshd, ash/bash/zsh/tcsh/fish byte-identical, raw control
  fails under fish); `PLEIADES_TEST_LOGIN_SHELL` runs of `cmd/pleiades`, `cmd/runner`, `internal/transport/ssh`,
  `internal/catalog/file`, `pkg/scpxfer`, `pkg/sftpxfer`, `pkg/containerengine` and `internal/adapters/native`
  clean under fish, tcsh and zsh; the lab Mac (zsh) passes `TestCLI_FactsGatherOnMacOSMatchesItsOwnTools`, and
  bsd-check (FreeBSD 15.1, account switched to tcsh) passes `TestCLI_FreeBSDFileChecksMatchTheirRealRuns` and the
  FreeBSD facts gate; `TestOnlyRemoteexecOpensAnSSHSession` (archtest, with a negative control);
  `TestConn_RefusesANULAndHoldsTheArgumentLimitExactly`; `TestPing_SendsANetworkCLITheEchoAsWritten`.

### Current blocker

None. Next is the gate.

### Open

- A `generic_ssh` host logging in to fish and onboarded before this change is recorded `not posix` and stays raw
  until onboarded again (the changelog says so; the discovery has no probe version). The Ansible gate's container is
  not switched. `TestEveryLoginShellHandsShTheSameCommand` installs its shells from the Alpine mirror at test time
  (three-minute deadline, not pinned in an image).
- vuln-corpus finding `C13_auto-roboto_2026-11-03` (the NUL, 859) is submitted and pending review.
- Tool defect: argus `expand_seeds` wrote fuzz corpus files into the repository despite `persist:false` (logged in
  `~/argus/MCP_USAGE_LOG.md`, row 170).
- Carried forward: `internal/archtest` misses zero-width characters (851); a Runner publish failing mid-split leaves
  earlier batches stored (853); no fact marks a package or service as gone (for 41d); whether a Remote Management
  Users member alone can read the Windows probes is unproven.
- Owner questions carried forward: Phase 41's encryption item depends on Phase 5 (not in v0.4.0); external Collection
  Landlock has no network rules; the Controller does not check a Runner's reported order token or label; the
  `secret_mask` residuals (836); whether `file.remove`'s check should refuse rather than warn.

### Next, in order

1. `make pr-check` through the monitor, push, `make branch-containers`, then the pull request (failure-pattern classes
   in the body, no AI attribution). Merge only when green, `record-actions`, pull `main` and clean.
2. 41c-2 (files, ports, users), 41c-3, 41d to 41g, 117b, 117c, 105, then release 0.4.0 (no tag until the owner says).

### Files changed this session

`pkg/remoteexec/` (`shline/`, `conn.go`, `process.go`, `script.go`, `detach.go`, `quote.go`, `remoteexec.go`,
`remoteexectest/server.go`, tests), `pkg/inventory/execshell.go`, `pkg/sdk/connect.go`, `internal/transport/`
(`transport.go`, `ssh/ssh.go`, `ssh/login_shell_container_test.go`, tests), `internal/engine/action_ssh.go`,
`internal/catalog/exec/` (`exec.go`, `shell.go`), `internal/catalog/net/ssh/ping.go`,
`internal/inventory/onboard/probe_ssh.go`, `internal/redact/masker.go`, `internal/testsupport/` (`sshd.go`,
`dockerhost.go`), `internal/archtest/ssh_session_test.go`, release gate tests in `cmd/pleiades` and `cmd/runner`,
`internal/catalog/file/busybox_chmod_test.go`, `docs/02-get-started.md`,
`changelog/ssh-commands-run-under-sh.breaking.md`. Records: `FAILURE_PATTERNS.md` and its archive (858 to 863, 845
resolved), `LESSONS_LEARNED.md` and its archive (387 to 389), `HANDOFF_DOCUMENT.md`, `HANDOFF_ARCHIVE.md`, and the
ignored `.SPECIFICATION/SECURITY_ATTESTATION.md` (PW.5).

Lab: bsd-check's account was switched to tcsh for its gates and restored to sh, and bsd-check is powered off (ACPI, state poweroff); win-check is powered off; the lab Mac (login zsh) ran its facts gate and was cleaned after (49 GiB free).
