# Handoff Document

Rewrite the "Current Status" section when stopping mid-task or handing off, per `.AGENTS/AGENTS.md`.

## Current Status (this session)

**2026-10-30. Branch `feat/agent-friendly-frr-and-hosts`, off `main` at `ea70badf` (PR #94 merged). Uncommitted, not
pushed, no pull request; `make pr-check` has NOT run on it.**

### What works (each proven by a test that ran)

- **net.frr.config check and save (FAILURE_PATTERNS 739).** `canSave` also asks `remotefile.PredictPut` and names the
  owner to connect as; the post-reload error carries the diff; the login is recorded in the undo. Live tests against a
  real FRR container: `TestConfig_ASaveNeedsTheDirectoryToo` (`frr_dirperm_live_test.go`), `frr_login_live_test.go`,
  `frr_become_live_test.go`. Blind lab run 1's second failure ("file would belong to netops:frr") was NOT reproduced.
- **`hosts:` as a list** (`internal/engine/hosts_list.go`, `hosts_list_test.go`; validate resolves every member and
  warns per unmatched member). `add-host --tag`, `set-host --tags`. `net.batfish.check` `expect_devices` (live test
  extended).
- **`trust-host --first-connect` refuses a device not flagged ephemeral unless `--reason` is given.** `import-containerlab`
  and `import-cml` set the flag; the `routing_lab` example inventory is flagged. The owner chose "refuse". It BREAKS
  scripts, so it belongs in a minor release, not a 0.3.x patch
  (`changelog/trust-host-first-connect-ephemeral.breaking.md`).
- **`become`** (LESSONS 358): `pkg/remoteexec/become.go`, `sdk` `WithBecome`, `internal/engine/become.go`,
  `internal/validate/become_rule.go`, journal `Become`, rollback `Step.Become`, runbook writes `become` keys; docs 01, 03
  and 04. The netlab images got `sudo` with NOPASSWD rules for `netops` (root and frr on the frr image, root on ovs).
- **The `virt.clab.*` collection** (44 methods; `pkg/clabapi`; `internal/catalog/virt/clab`; capability
  `ContainerlabAPICapable`, granted by the HTTP prober when an unauthenticated `/health` has status, uptime, startTime
  and version). `TestLive_ARealLabEndToEnd` passes against the real privileged server: a real lab, a ping crossing the
  link, netem loss stopping it, the exit-status fix, the undos run. The catalog counts are now 256 methods, 254
  implemented, 232 that can predict.

### Records this session

FAILURE_PATTERNS 739 (repeat of 564 and 722), 740 (repeat of 642 and 90), 741, 742 (open); LESSONS 358 to 360.

### Open

- **For the owner to confirm (FAILURE_PATTERNS 742):** `JournalEntry.Become` is classified kind 5 (author label) in
  `internal/archtest/journal_test.go` and added to `journalAuthorLabelFields`, a design decision.
- `import-containerlab` reading the server's inspect JSON (`--api`) is not built.
- Linux bridge, address and route methods are not started. Design settled: run `ip` as the connecting account, use
  `become` for privilege, no capability beyond CommandExec.
- The playbook converter still DROPS `become` with a review note (`internal/forge/playbook/keywords.go`, `codes.go`); it
  should map it to native `become`.
- Edgeshark install and uninstall are state-aware, but their real POST is not exercised live (it pulls images).
  `virt.clab.capture.packetflix` needs Edgeshark running (the server answers 503).
- The Walk tier refuses `become` until a dispatch carries it.
- The whole `cmd/pleiades` package test run exceeded go test's 10 minute default (use testgate or pr-check). One test
  in it, `TestCLI_NetTraceFromAContainerThroughARouter`, failed with a Docker "no SSH greeting" start failure before
  any image change; the cause is unproven.
- The older items are unchanged: FAILURE_PATTERNS 560 (open until a sighting prints the engine's list) and 734;
  `make resources-record` after the next gate; Phase 131 R4 and the Phase 87 to 89 gates.

### Next, in order

1. Commit in separate commits per feature, run `make commitgate`, run `make pr-check` through the monitor, push, one
   pull request. Name the failure-pattern classes in the PR body.
2. Then the not-done list above, starting with the converter's `become` mapping.

### Files changed this session

See `git status`. Records: `FAILURE_PATTERNS.md`, `FAILURE_PATTERNS_ARCHIVE.md` (739 to 742), `LESSONS_LEARNED.md`,
`LESSONS_LEARNED_ARCHIVE.md` (358 to 360), `HANDOFF_DOCUMENT.md`, `HANDOFF_ARCHIVE.md`. Changelog fragments are in
`changelog/`.

Unchanged: bsd-check and win-check are powered off (shut down through ACPI, not saved); not touched this session.
