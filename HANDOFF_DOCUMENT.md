# Handoff Document

Rewrite the "Current Status" section when stopping mid-task or handing off, per `.AGENTS/AGENTS.md`.

## Current Status (this session)

**2026-10-23. Branch `feat/network-verification-127`, off `main` at `d6ceedb8` (PR #87's merge); tip after this
session's records commit.** Phases 127a (data capabilities for external Collections) and 127b (network verification:
FRR, Open vSwitch, a Batfish change gate), in one pull request by the owner's decision. Pushed; PR #88 open.

### Built and proven

- 127a, both tiers: a program defines a capability as data, a probe grants it, a person asserts or revokes it (CLI and
  `POST /inventory/devices/{name}/capabilities`), and a method finds its grant. `TestCLI_ExternalCapabilityIsProbedGrantedAndUsed`
  with `TestCLI_ExternalCollectionRunsAgainstARealDevice` (42.9 s), and `cmd/runner`'s
  `external_capability_release_gate_test.go` and external collection gate over real NATS (19.2 s).
  `examples/external_collection` now defines `example.notebook`.
- 127b: `net.frr.config`, `net.frr.interface`, `net.frr.facts`, `net.frr.command`, `wait.routes`, `net.trace`,
  `net.ovs.bridge`, `net.ovs.port`, `net.batfish.check`, the `batfish` device, `pkg/batfish`, `pkg/netcfg`,
  `pleiades import-containerlab`, `container.run`'s `memory`, `pleiades doc <capability>`, the skill section, and
  docs/16 and docs/11 sections. `TestCLI_NetworkChangeIsGatedAppliedAndUndone` (92.8 s: a bad candidate refused with
  no router changed, a good one applied and verified, a link taken down out of band undone by the block);
  `TestMethods_OnRealRouters`, `TestBridgeAndPort_OnARealSwitch`, `internal/catalog/net/batfish` (40.3 s) and
  `internal/catalog/wait` (65 s) against real containers; fuzz targets in `8a03adc1`.
- `examples/routing_lab`, built from a clean slate by a fresh agent from the docs and CLI only (24.8 min, 17 gaps in
  its `GAPS.md`), gated by `TestCLI_RoutingLabExampleValidatesOnRealRouters` (10.4 s).
- Records: FAILURE_PATTERNS 575 to 591, LESSONS 310 to 313. Changelog fragments: `network-verification.added.md`,
  `import-containerlab.added.md`, `data-capabilities.added.md`, `undo-kept-after-a-confirmed-change.fixed.md`,
  `container-memory-limit.added.md`. Roadmap (`implementation.py`): 127a 10 of 19, 127b 15 of 24; both still list the
  Pattern Entry, Fuzz/Stress, Adversarial, Schema/Injection, Release Gate and Commit Message gates open, and 127b the
  Documentation Gate.

### Blocker

None. The fourth `make pr-check` (on `5302fb6f`) failed; everything it found is fixed in `3203fbf3`:
- the network gate lost r3 over a link: net lab links now join after a node starts (FAILURE_PATTERNS 587, a repeat
  of 575, measured 0 of 40 starts over a link);
- `PredictPut` refused a group a new file takes from its directory on FreeBSD (588, a repeat of 521; passed on
  bsd-check);
- the doc test leaked a registered capability into later onboards (589, a repeat of 188);
- twelve coverage floors (tests added; `Replaced.Failure` replaces four copies of one branch) and six durations.
The fifth failed on Windows only (a read-only directory is writable there; the grant save failure now comes from a
repository double, `2bd1d6a0`). The sixth hung `internal/catalog/net/frr` under `-race`: an empty stdin's end reached
sshd before the exec request (590, a regression from 149's fix), fixed in `pkg/remoteexec` with an OpenSSH test.
Follow-up from 590: a command a cancelled context kills is reported as exit 143, not as the cancellation.
`cmd/controller`'s `TestControllerServesWithinItsWindowAndStopsPastIt` failed once on a NATS port refusing for two
minutes (the open 470/471 class) and passed alone. bsd-check's disk was cleared through the lab runbook (3 to 8 GB).

### Open

- **Owner action:** remove the 12 lab host-key lines from `~/.ssh/known_hosts` by hand: `[127.0.0.1]:2201`, `:2202`,
  `:2203` and `:2211`, lines 28 to 39 (FAILURE_PATTERNS 586).
- 577: the mask is still a denylist. 584: `validate` does not check a structured parameter's entries; not in 127b's
  follow-ups.
- The IOS XE `show running-config` proof needs a DevNet sandbox reservation.
- 127b follow-ups in IMPLEMENTATION.md: G6, G8, G10 to G14, G16, the `wait.routes` dispatcher, refusing a project
  `src` on the Walk tier, EOS and Junos with Phase 74d. `wait.routes` on a flapping link is not stress tested.
- 574, 560, 551, 470 and 471 as before.

### Next

1. The seventh `make pr-check` passed and PR #88 was opened; its CI failed only `TestCheck_RealRoutersAgainstRealBatfish` (591). After the fix, `make pr-check` with `PR_CHECK_PARALLELISM=1` on this machine, under the memory cap.
2. Push and open the pull request. No AI attribution anywhere: not in commits, the PR body or its footer.
3. Watch CI until green; the owner merges.
4. Then the release tooling PR, then the GNS3 and CML2 importers.

### Files changed on this branch

274 files against `origin/main`. Code: `internal/catalog/net/{frr,ovs,batfish}/`, `internal/catalog/net/trace.go`,
`internal/catalog/wait/routes.go`, `pkg/{batfish,netcfg}/`, `pkg/capability/`, `pkg/external/`, `pkg/inventory/`,
`internal/loader/`, `internal/inventory/{record,onboard}/`, `internal/api/devices_grants.go`, `internal/auth/`,
`pkg/containerengine/`, `internal/engine/`, `internal/testsupport/netlab.go`, and in `cmd/pleiades` the grant,
onboard, doc and `importcontainerlab` commands. Tests: the release gates in `cmd/pleiades` and `cmd/runner`. Docs:
`docs/16-network-verification.md`, `docs/11-extending-pleiades.md`, `docs/01-start-here.md`, regenerated
`docs/reference/`. Examples: `examples/routing_lab/`, `examples/external_collection/`. The changelog fragments above
and the records.

### Lab state

No lab VM work is recorded on this branch; the last record said `bsd-check`, `win-check`, `lab-dc1` and `lab-lin1`
were saved. No lab containers are running (`docker ps` empty, 2026-10-22).
