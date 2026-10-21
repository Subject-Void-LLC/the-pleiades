# Handoff Document

Rewrite the "Current Status" section when stopping mid-task or handing off, per `.AGENTS/AGENTS.md`.

## Current Status (this session)

**2026-10-21. Branch `feat/queue-resilience-103a`, off `main` at `f54df9a2` (#81 merged green).** One pull request
at a time; `make pr-check` before every push; the owner merges.

### On this branch

- **Phase 103a, done (12/12):**
  - the Controller's result dead-letter grant, derived from its subscriptions (553);
  - dead letters hold identifiers only, the reason masked (554);
  - a lease records its owner, and contention names it (`lock.HeldError`);
  - a busy device ends with "device busy: held by job <id>" after about 5.5 minutes;
  - running work is reported in progress, so it is not redelivered (555).
  - FAILURE_PATTERNS 496 resolved: not a fixture stall but 396's busy-device hang, read from the nightly's
    uploaded Runner log.
  - Proof: `TestBusyDeviceReleaseGate_*` against real NATS, its lock bucket and sshd, with mutation checks.
- **Records:** 551's evidence and the clock gap; Phase 40a planned (rollback ordered without clocks).

### Open

- 551, cause unproven; Phase 40a planned, not built.
- `main`'s nightly failed on 2026-10-21 (496, fixed here) and `winrm` failed too, its log unreadable then
  ("Egress is over the account limit"); look again after merge.
- Memory: other sessions' `gopls` processes held about 4.5 GB this session; heavy runs were capped near 500 MB to
  1.6 GB, one compile at a time.
- Carried: the converter blocks `cli_command`'s `prompt`/`answer`; `TestLiveIOSDialogs` needs DevNet credentials.

### Next

1. `make pr-check`, then open the pull request.
2. Then, by 122f's order: Phase 105's remaining prerequisites (117b, 117c), or 122b's streaming output.

### Lab state

Unchanged: `bsd-check`, `win-check`, `lab-dc1` and `lab-lin1` are saved.
