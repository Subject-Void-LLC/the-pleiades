# Handoff Document

Rewrite the "Current Status" section when stopping mid-task or handing off, per `.AGENTS/AGENTS.md`.

## Current Status (this session)

**2026-10-23. Branch `feat/lab-importers`, off `main` at `62be86d4` (PR #88's merge). Phase 127c: `pleiades
import-gns3`, `pleiades import-cml`, and, at the owner's direction ("build the telnet back end and connection
primitives"), a Telnet back end that logs in. Not pushed; no pull request yet.**

### Built and proven

- `pkg/gns3` and `import-gns3` against gns3-server 3.0.6 and 2.2.61 built from PyPI (`internal/testsupport/testdata/gns3`):
  `TestClient_AgainstRealServers`, `TestCLI_ImportGNS3ReachesANodesConsole` (a VPCS node imported and reached with
  `telnet_exec`).
- `pkg/cml` and `import-cml` against the DevNet CML sandbox (CML 2.7.2) over its VPN: `TestClient_AgainstARealController`,
  `TestCLI_ImportCMLReachesIOSOverTelnet` (IOL XE R1 over Telnet: facts and a command in user mode, config refused
  there, then made with the enable secret, read back and removed). Both skip without `tests/cml/.env` and the VPN.
- Telnet: `telnetexec.Session` (login, `ReadUntil`/`WriteLine`), `netcli.Shell`, `sdk.OpenShell`, the `telnet`
  transport on `net.ios.*`/`net.cli.*` behind `insecure_telnet`, `transport: telnet` on Cisco types, the credential's
  enable secret (`add-credential --enable-password[-stdin]`), IOS user mode and enable. `TestSession_AgainstARealTelnetd`
  against BusyBox's telnetd and `/bin/login`.
- Records: FAILURE_PATTERNS 592 to 596. Changelog fragments listed in IMPLEMENTATION.md's 127c Documentation Gate.

### Open

- `tests/cml/.env` holds the sandbox reservation's VPN and controller logins (0600, gitignored), valid until about
  2026-10-25; the owner runs `tests/cml/vpn.sh up` (openconnect, sudo). `vpn.sh up --container` runs the VPN in a
  container where sudo is unavailable, and `vpn.sh run` runs a command on its network.
- Not built: the Walk tier's credential types carry no enable secret (AWX's network credential, `authorize_password`);
  `--via telnet` nodes cannot be onboarded (no Telnet prober); 127c's fuzz, adversarial, security and hardening gates.
- The owner asked whether the Bash guard hook (`~/.claude/hooks/no-grep-go.py`) should be more specific: it blocks a
  grep whose pattern text holds `.go`, and any path under `tests/`, `tools/`, `internal/`, `pkg/`, `cmd/` (a `.json`, `.env`
  or log file there included).
- 574, 560, 551, 470 and 471 as before.

### Next

1. `make pr-check` with `PR_CHECK_PARALLELISM=1`, under the memory cap; then push and open the pull request.
2. Then the release tooling PR.

### Lab state

No lab VM work is recorded on this branch; the last record said `bsd-check`, `win-check`, `lab-dc1` and `lab-lin1`
were saved. No lab containers are running (`docker ps` empty, 2026-10-22).
