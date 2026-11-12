# Handoff Document

Rewrite the "Current Status" section when stopping mid-task or handing off, per `.AGENTS/AGENTS.md`.

## Current Status (this session)

**2026-11-12. Branch `test/129cd-fuzz-and-device-undo`, from `main` at `321acf71` (#159 merged). Closes Phases 129c and
129d, the first of the owner's v0.3.0 backlog (finish every incomplete v0.3.0 phase, then v0.4.0). Next: pr-check, push,
branch-containers, PR, merge when green (authorized today only).**

### What works

- `FuzzReadParams` (batfish), `FuzzExpandAliases` and `FuzzDeviceRoot` (engine), each mutation-checked; the first found
  FAILURE_PATTERNS 1180 (batfish assertion refusals named the method not at all, or twice), fixed with
  `TestReadParams_NamesTheMethodOnce`.
- Records only (no PR needed): the 23 attestation problems are 0: Phase 129 excepted under five controls in
  SECURITY_ATTESTATION.md (an umbrella, its code is in 129a-d), 129a-d gate items written from verified evidence, 119c's
  two items cite checkable tests. A stale "no test yet" note in 129d was corrected (one already ran).

### Open

- The v0.3.0 backlog after this: 42 (3; Fable drafts in `.IGNORE/claude-api/out/stubs1/`), 88 (1), 127d (1), 117a (1,
  needs a ServiceNow instance and the IOS lab), 127c (3, a GNS3 stub there), 131 (5), 87 (6), 122a (7), 127a (11, grants
  and data-capability stubs there), 127b (13), 112 (22, vboxmanage/cloudinit/bsdinstall/winunattend stubs there). The
  stubs were written by Fable through the API on 2026-11-11 and are unproven: verify each before use.
- Watch in CI: `cmd/controller`'s `TestControllerLeaderElection_ReleaseGate`. From earlier: 1085, 1139, 1143, 1146.

### Files changed this session

`internal/catalog/net/batfish/{params.go,assertions.go,params_fuzz_internal_test.go}`,
`internal/engine/{yaml_aliases_fuzz_internal_test.go,device_root_fuzz_internal_test.go}`,
`changelog/batfish-refusals-name-the-method.fixed.md`, `FAILURE_PATTERNS*.md` (1180), `HANDOFF_*.md`.

Lab: no change.
