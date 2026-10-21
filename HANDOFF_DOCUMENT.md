# Handoff Document

Rewrite the "Current Status" section when stopping mid-task or handing off, per `.AGENTS/AGENTS.md`.

## Current Status (this session)

**2026-10-21. Branch `fix/rollback-run-order`, off `main` at `f54df9a2` (#81 merged green: 15 of 15 checks, none
tolerated on a re-run).** One pull request at a time; `make pr-check` before every push; the owner merges.

### What merged (#81)

- `rollback_on_failure: true` on a block; `examples/ai_repair_lab`; `container.run` undos replay; `rollback: []`.
- IOS questions answered: `netcli.Session.Dialog`, `net.ios.command` (177th method), `prompts`/`ask` on the CLI
  methods, `pkg/aitask`, `pkg/clidialog`; the IOS upgrade examples rewritten.
- `validate`'s `param-required` rule and `collection.Param.AliasOf`.
- go1.26.10 for GO-2026-6809 (`crypto/x509`, reached through `pkg/pgclient`), which `main` had too.
- `make pr-check` runs govulncheck on the Mac when one is ready (`maccheck -phase vuln`), between the Mac's prepare
  and test phases, for this machine's platform and Go release; here otherwise.
- Records: FAILURE_PATTERNS 548 to 552.

### On this branch (records only)

- 551's record: 80 passes in a row on a quiet machine since the gate names a failed later run, against 2 in 14 under
  load; the planner allows a rollback beneath a later run only when that run recorded no change, or when the
  wall-clock journal times say it did not finish after the target began. The second is a gap either way.
- Roadmap, Phase 40a (owner: record now, plan a phase): "later" by the device lease's fencing revision on the Walk
  tier and a project counter on the Crawl tier, instead of comparing clocks.

### Open

- 551, cause unproven; Phase 40a planned, not built.
- After a toolchain bump, pr-check runs on a cold build cache: 2 h 7 min on 2026-10-21, 74 min of it the Windows
  leg. Build one or two packages at a time under the cap (`GOFLAGS=-p=2`): aws-sdk's ec2 package was killed
  compiling at 2.2 GB with four at once.
- A govulncheck scan that traces a vulnerable call needs 5 to 7 GB; pr-check sends it to the Mac.
- The converter still blocks `cli_command`'s `prompt`/`answer`; `TestLiveIOSDialogs` needs DevNet credentials
  (`PLEIADES_E2E_IOS*`); nothing has run against a real Catalyst switch.
- `TestGenericReleaseGate_GRPC` failed once in the full suite at its Java container's start, passed alone.

### Next

1. `make pr-check` on this branch and open its pull request.
2. Phase 40a, when the owner schedules it.
3. `TestLiveIOSDialogs`, once credentials are given.

### Lab state

Unchanged: `bsd-check`, `win-check`, `lab-dc1` and `lab-lin1` are saved.
