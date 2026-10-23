# Handoff Document

Rewrite the "Current Status" section when stopping mid-task or handing off, per `.AGENTS/AGENTS.md`.

## Current Status (this session)

**2026-10-23. Branch `feat/lab-importers`, off `main` at `62be86d4` (PR #88's merge). Phase 127c's importers and
Telnet back end, the dogfooding fixes, then an exact IOS undo, Genie's IOS parsers in Go, `http.wait` and nine of the
routing lab's gaps. Tip `356c5ef3`. Not pushed; no pull request yet. Tracker: 127c is 5 of 11 done, 127b 15 of 24.**

### Built and proven

- Everything in the previous entry (`HANDOFF_ARCHIVE.md`).
- `9ab84fe4`: `net.ios.config` keeps a checkpoint on the device and records `net.ios.replace` (`configure replace`,
  file paths only in the journal); without a checkpoint it records no undo and shows masked `undo_lines`. IOS sessions
  send `terminal width 512`. `TestConfig_CheckpointsAChangeAndItsUndoReturnsExactly` and its siblings.
- `67a70f8e`: `net.ios.parse` and `pkg/iosparse`, twelve genieparser parsers held to genieparser's fixtures
  (`LICENSE.genieparser`, `NOTICE.genieparser`). `pkg/iosconfig` fuzz 30 s, 33M execs, nothing found.
- `b53d4db2`: `http.wait` (package 9.2 s, 100% coverage); a `container_host` with no `host` declares no SSH (605).
- `fde5365c`: routing lab gaps G8, G10 to G14, G16 and G17, each with its test in IMPLEMENTATION.md's 127b follow-ups
  item; `netcfg.RefuseMasked` in every configuration writer (609).
- On the DevNet CML sandbox, `TestCLI_ImportCMLReachesIOSOverTelnet` passed in 102.5 s (140.8 s package): the checkpoint
  round trip through `pleiades rollback`, the parsed before and after, and `rollback_on_failure` undoing inside the run.
  It passed after the lab's node network stopped answering at about 17:10 UTC and a stop and start through the CML API
  restored it (LESSONS 320).

### Open

- FAILURE_PATTERNS 610 (ShowInterfaces keeps genieparser's "medium is" failure), 611 (ShowVlan's quadratic join),
  612 (checkpoint files are never pruned).
- Not built: the Walk tier's credential types carry no enable secret; `--via telnet` nodes cannot be onboarded (no
  Telnet prober); Ansible-style `until`/`retries` task keys (`http.wait` answered G6 instead); 127c's fuzz,
  adversarial, security and hardening gates.
- 598 is fixed in set-host only: the other commands using `splitPositional` still check positionals before flags.
- Still open from the routing lab: `wait.routes`' dispatcher, `validate` refusing a project `src` on the Walk tier,
  `validate` checking a structured parameter's entries, the IOS XE snapshot proof.
- LESSONS 315: rotating the sandbox VPN password or ending the reservation early is the owner's call.
  `tests/cml/.env` is valid until about 2026-10-25.
- 602, 574, 562, 560, 551, 470 and 471 as before.

### Next

1. `make pr-check` with `PR_CHECK_PARALLELISM=1`, under the memory cap.
2. Push and open the pull request; stay on it until it is green.
3. Then the `virt.cml` pull request (the owner decided on 2026-10-23 it is its own PR), which also gets `--via console`
   through the CML controller's console server.
4. Then release tooling, which must ship `LICENSE.genieparser` and the vendored UI licenses in each archive.

### Files changed (since the last records)

`internal/catalog/net/ios/` (`checkpoint.go`, `replace.go`, `parse.go`, `config.go`, `command.go` and tests);
`pkg/iosconfig`, `pkg/iosparse` (with `testdata/`), `pkg/netcli`, `pkg/netcfg/refuse.go`, `pkg/collection/shapes.go`,
`pkg/sdk/projectfiles.go`; `internal/catalog/http/wait.go`; `internal/inventory/devices/container/host.go`;
`internal/catalog/pleiades/builtin/project/write.go`; `internal/engine/runbook_context_project_write.go`;
`internal/journal/active.go`; `internal/validate/vars_rule.go` and the rules beside it; `internal/catalog/net/trace.go`,
`cli/config.go`, `netconf/config.go`, `frr/config.go`; `cmd/pleiades` (run report, `run_concurrent.go`, rollback,
journal, validate, the CML gate and new release gates); `internal/forge/catalogdata`; docs, the generated reference,
README, CLAUDE.md, `examples/routing_lab`; changelog fragments. Records: FAILURE_PATTERNS 603 to 612, LESSONS 316 to
320 (314 narrowed), changelog `ios-config-masked-refusal.changed.md`.

### Lab state

Unchanged: no lab VM was touched on this branch; `bsd-check`, `win-check`, `lab-dc1` and `lab-lin1` are saved as last
recorded. The CML sandbox lab was stopped and started once through its API.
