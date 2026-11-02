# Handoff Document

Rewrite the "Current Status" section when stopping mid-task or handing off, per `.AGENTS/AGENTS.md`.

## Current Status (this session)

**2026-11-02. Branch `feat/c2c-uncheckable-methods`, HEAD `e7caa208`, 2 commits on `origin/main` at `fb2e328b`
(`1a342c7e`, `e7caa208`). Phase 46b (Crawl milestone C2c), "Every Method Checked or Saying Why", is committed with the
fp-reviewer's fixes and ticked 3 of 3. Not through `make pr-check` yet, not pushed, no pull request.** C2b merged as PR
#103 (`fb2e328b`).

### What works (each proven by a test that ran)

- **Nine methods gained a check**: `virt.clab.lab.restart` (its handler already predicted; only the declaration was
  missing), `lab.redeploy`, `lab.save`, `node.restart`, `node.ssh_access`, `node.disable_tx_offload`, `veth.create`,
  `vxlan.create`, and `exec.winrm.shell`. 244 of the 257 implemented methods check; 13 keep a re-confirmed
  `NoCheckReason` (`vxlan.delete`'s reworded).
- **The clab checks read and refuse what the server refuses** (FAILURE_PATTERNS 819): a lab not deployed, a login that
  is not a superuser (`GET /api/v1/users/{username}`; a bearer token answers `CannotCheck`), veth endpoints not `x:y`.
  `TestChecks_PredictWhatAlwaysChangesAndSendOnlyReads`, `TestChecks_RefuseWhatTheServerWouldRefuse` (stub), and
  `TestLive_ARealLabEndToEnd` on a real containerlab server: each check predicts, no SSH session opens, no node's PID 1
  start time changes, and it fails when `node.restart`'s check really restarts.
- **`exec.winrm.shell` has `creates` and `removes`** (Ansible `win_shell`), decided as `Test-AnsiblePath` decides
  (818), one deadline for guard and script (822), device-namespace paths refused (821). `shell_test.go`'s tests, and
  `TestWinRMGate_ShellGuards` on win-check, which fails when the guard never skips and when it reads through
  `Test-Path`.
- `internal/lock/nats.go`'s comment corrected per the owner: keep the per-call stream handle; v1.54.0's race was fixed
  upstream in v1.55.0.

### Current blocker

None. Next is the gate.

### Open

- prscope still selects no reverse dependents (FAILURE_PATTERNS 631), so pr-check does not run `tools/gendocs` for a
  catalog change; run `go test ./tools/gendocs/ ./internal/archtest/` by hand (LESSONS 380).
- Why the Administrator login on win-check read attributes through an `icacls` deny was not measured (827).
- Carried from C2b's handoff, not in this branch's facts: `file.remove`'s own check of one removal does not ask whether
  the parent allows it.

### Next, in order

1. `make pr-check` through the monitor, push, `make branch-containers`, then one pull request naming the failure-pattern
   classes (817 to 828) in its body. Merge only when its checks are green.
2. After the merge: pull `main`, delete merged branches, prune.
3. C2d: Phase 41. It needs the owner's design decisions before any code: the fact key's shape, the hash, and storage.
   Encryption is out of scope.

### Files changed this session

See `git log origin/main..HEAD`: `internal/catalog/virt/clab` (`ops_labs.go`, `ops_nodes.go`, `ops_tools.go`,
`register.go`, `session.go`, tests), `internal/catalog/exec/winrm/shell.go` and tests,
`cmd/pleiades/winrm_shell_guard_release_gate_test.go`, `internal/forge/catalogdata/collections_exec.go`,
`internal/lock/nats.go`, the generated reference and module catalogs, README.md and CLAUDE.md. Records:
`FAILURE_PATTERNS.md` and `FAILURE_PATTERNS_ARCHIVE.md` (817 to 828), `LESSONS_LEARNED.md` and
`LESSONS_LEARNED_ARCHIVE.md` (378 to 380), `HANDOFF_DOCUMENT.md`, `HANDOFF_ARCHIVE.md`, Phase 46b in
`.SPECIFICATION/IMPLEMENTATION.md`, and `.SPECIFICATION/SECURITY_ATTESTATION.md` (PW.5, PW.8). Changelog fragment:
`checks-for-uncheckable-methods.added.md`.

Lab: win-check was started for the gate and shut down through ACPI after each use. Unchanged: bsd-check is shut down.
