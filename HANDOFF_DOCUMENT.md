# Handoff Document

Rewrite the "Current Status" section when stopping mid-task or handing off, per `.AGENTS/AGENTS.md`.

## Current Status (this session)

**Branch `feature/virtualbox-support`, 2026-09-27: the WinRM gates run against the lab, then Phase 113
(the user: "ad-hoc plus run --json").** Rules unchanged: the lab is provisioned by The Pleiades only,
runbook tasks use the method-as-key form, temporary tooling becomes real methods. New: to call a method
once, use `pleiades adhoc <hosts> <method> key=value --json` (LESSONS 249), not a throwaway runbook.

### Where it stands

- **Committed, not pushed:** `ed0e1f0` (both cached answer files deleted), `d7bce43`
  (`add-credential --password-stdin`), `c96572c` (the WinRM gates), `27ff29f` (terminal-safe
  `--json`), `d962e78` (Phase 113), and the notes commit after them. `make push-gate` has not run.

- **Phase 113 is built** (tracker 10/10, commit message to give): `pleiades adhoc`, `run --json` and
  `adhoc --json`, one run pipeline (`cmd/pleiades/run_pipeline.go`) and one report model both views
  render, `redact.Value` (stats masked as data, key rules included), `writeJSON` (terminal-safe; `onboard`
  and `doc` use it too, FAILURE_PATTERNS 368). `TestCLI_AdhocReleaseGate` passes against a real sshd;
  every `cmd/pleiades` test and gate passes under `-race`; gosec and govulncheck clean.
- **Windows fixes:** the install's audit pass deletes both cached answer files (366; rebuilt base and
  `win-lab` measured clean); `add-credential --password-stdin` (367).
- **Every WinRM gate has run for real:** the five password gates against `win-lab` (the static-IP one
  converted the NAT adapter, so it did not cut its own channel), the certificate and modes gates against
  the host's 5986 listener, `pkg/winrmexec`'s modes gates against both. The service and feature gates'
  stale assertions were fixed (367). To feed a gate the vault's password without printing it, a small
  helper decrypted it into the gate's environment; its source is not in the repository.
- **Found, not fixed (369):** `virt.vbox.vm.stop` presses the power button once, and a Windows guest
  idle ten minutes spends that press waking its display. Measured: the second press shut it down in 13 s.
  This also stopped the snapshot-reset check (stop, snapshot `clean`, mark, restore, check) at its first
  step; that check has not been run.
- **Lab state:** `win-lab` (rebuilt from the new base, same address and vault password) and
  `ubuntu-lab` running; `ws2025-core-base` rebuilt from the example runbooks; scratch runbooks under
  `~/pleiades-lab/runbooks/win/`.

### Next

1. `make push-gate` (or `make ci`), then push when the user asks.
2. Fix 369 (press again while waiting, or no display timeout in the base), then run the reset check.
3. Follow-ons: `--vault`/`--inventory` flags; the docs corpus for collections (asked, not answered); a
   Crawl-tier `pleiades mcp` whose tool call is an ad-hoc run, if the user wants it.

### Files changed this session

`pkg/winunattend` (audit pass), `internal/catalog/virt/vbox/vm/install.go` and
`internal/forge/catalogdata` (install doc), `examples/virtualbox_lab` (README, install runbook comment),
`cmd/pleiades` (`addcredential.go`, `adhoc.go`, `jsonout.go`, `run.go`, `run_pipeline.go`,
`run_report.go`, `run_text.go`, `load.go`, `doc.go`, `onboard.go`, `main.go`, the WinRM gates, new
tests), `internal/redact/value.go`, `internal/clispec`, docs/reference, `internal/api/wellknown`,
`docs/02-get-started.md`, CLAUDE.md, changelog (five fragments), FAILURE_PATTERNS 366-369, LESSONS 249.
Local only: `IMPLEMENTATION.md` (Phase 113, Phase 86's ad-hoc item).
