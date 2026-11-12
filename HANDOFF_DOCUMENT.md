# Handoff Document

Rewrite the "Current Status" section when stopping mid-task or handing off, per `.AGENTS/AGENTS.md`.

## Current Status (this session)

**2026-11-12. Branch `fix/v030-first-tier-proofs`, PR #158 (refuse a word a program would read as an option or a
pattern, plus the first-tier proofs for Phases 38 and 88), on `main` at `0c4ee4e8` (#157). Every check passed at
`fc530436`. At `f31d10e7` `containers (5)` failed `TestCheck_RealRoutersAgainstRealBatfish` ("could not lock running
DS"), which this branch did not cause, and `coverage` failed with it. That flake is fixed (FAILURE_PATTERNS 1178).
Next: pr-check, push, branch-containers, then watch #158's checks.**

### What works

- The operand rule across the first-tier methods (`pkg/operand`, `collection.ParamFormat*`, held by validate, the
  render, the rollback planner and each method) and the QuotePS fixes: see PR #158's body and FAILURE_PATTERNS 1174 to
  1177.
- 1178: `internal/testsupport/testdata/netlab/frr/start.sh` says ready only after watchfrr is idle with every daemon Up,
  mgmtd's zebra and staticd backends have connected, and a real `configure terminal` takes the lock. It exits non-zero
  if that never happens. Proof: the batfish test 5 of 5 under `-race`, and every StartNetLab package passes.

### Current blocker

None. The lock error was never reproduced here (not even at `--cpus 0.05`), so CI is the real test of 1178.

### Open

- Watch in CI: `cmd/controller`'s `TestControllerLeaderElection_ReleaseGate` (failover 4.5 s against 3 s once under
  local load).
- From earlier: 1085, 1139 (owner decision), 1143 (bindings on inventory delete), 1146.

### Next, in order

1. `make pr-check` through the monitor on the new tip, push, `make branch-containers`.
2. Watch PR #158 until it is green. The owner merges it. Then pull main and clean up.

### Files changed this session

`internal/testsupport/testdata/netlab/frr/start.sh`, `FAILURE_PATTERNS.md`, `FAILURE_PATTERNS_ARCHIVE.md` (1178),
`HANDOFF_DOCUMENT.md`, `HANDOFF_ARCHIVE.md`.

Lab: no change.
