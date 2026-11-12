# Handoff Document

Rewrite the "Current Status" section when stopping mid-task or handing off, per `.AGENTS/AGENTS.md`.

## Current Status (this session)

**2026-11-12. Branch `test/phase-38-close`, from `main` at `8f2ffb20` (#158 merged). Closes Phase 38 (10 of 10 work
items; only its process item is left). Commits are made; `make pr-check`, push, `make branch-containers` and the pull
request are next. The owner authorized merging green PRs and deleting their branches for today only.**

### What works

- `svc.*` proven on every OS it claims. Linux: `TestCLI_SystemdServicesCycleOnARealDevice` (#158). Windows:
  `TestWinRMGate_Service*` passed on win-check today, and the new `TestWinRMGate_GenericServiceMethodsReachTheSCM`
  covers the generic dispatchers there (undo included; mutation-checked).
- FAILURE_PATTERNS 1179 (a repeat of 148): `cd` reads a relative `-` as `$OLDPWD` and searches `$CDPATH` for any other
  relative name, even after `--`, so `chdir: "-"` ran in the login's home and a `CDPATH` could move any relative chdir.
  One rule now, `remoteexec.DirOperand` (relative goes as `./dir`), at every `cd` site: `exec`, `remotefile.DirectoryEntries`,
  `identity.user`'s `homeState`, `gate`'s probe and check command. The `CDPATH` half came from fp-reviewer. Each regression
  test fails on the code before the fix.
- docs/03: `include_tasks` is unsupported, with the reason and the manual rewrite (the 2026-11-11 decision).
- Every first-tier fuzz target ran 60 s clean; counts are in IMPLEMENTATION.md's Phase 38. `pkg/operand`'s coverage
  floor is recorded at 100.0.

### Current blocker

None.

### Open

- Watch in CI: `cmd/controller`'s `TestControllerLeaderElection_ReleaseGate` (failover 4.5 s against 3 s once).
- From earlier: 1085, 1139 (owner decision), 1143 (bindings on inventory delete), 1146.

### Next, in order

1. `make pr-check` through the monitor (port 8766), push, `make branch-containers`, open the PR, merge when green
   (authorized today), then pull main and clean up.
2. Next roadmap item after Phase 38: Phase 42's 3 open items (`implementation.py --json`'s phase walk).

### Files changed this session

`pkg/remoteexec/{quote.go,quote_test.go}`, `internal/catalog/gate/{preconditions.go,runner.go,run_test.go}`,
`internal/catalog/exec/{exec.go,commandline_fuzz_test.go,testdata/fuzz/FuzzShellCommandLine/}`,
`pkg/remotefile/{remotefile.go,predict_test.go}`, `internal/catalog/identity/user/{user.go,home_state_internal_test.go}`,
`cmd/pleiades/winrm_service_feature_release_gate_test.go`, `docs/03-migrating-from-ansible.md`, `coverage-floor.json`,
`changelog/chdir-named-dash.fixed.md`, `FAILURE_PATTERNS*.md` (1179), `HANDOFF_*.md`.

Lab: win-check was started cold twice for the WinRM gates and shut down again each time (ACPI); powered off.
