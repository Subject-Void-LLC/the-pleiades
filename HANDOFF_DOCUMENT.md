# Handoff Document

Rewrite the "Current Status" section when stopping mid-task or handing off, per `.AGENTS/AGENTS.md`.

## Current Status (this session)

**2026-11-11. Branch `fix/facts-release-skew`, rebased on `main` at `3ea26d84` (#155 merged, go1.27.4). Nine commits;
the node-method binding (FAILURE_PATTERNS 1164) and its review fixes are committed. `make pr-check` runs next on the
rebased tip, then push, `make branch-containers` and the pull request.**

### What works

- **1020 fixed (option B)** and its catalog-method case closed by 1164; **1031 fixed (option C,
  `wire.CollectionIdentity.Deliverable`)**. Proof: `TestCatalogMethodClaimNoLongerWritesBuiltinKeys`,
  `TestFrozenRuleIsSubsetOfValid`, `TestDeliverableReadsNoUnicodeTable`.
- **1164, uncommitted:** each native job pins its node graph at first fan-out (`graph/1`), one resolver judges every
  facts batch and journal row under the dispatched node's method (`journal.NodeMethod.Judge`,
  `journal.IdentityProblem`), a changed graph fails the devices left, and a pinned dispatch is payload version 2.
  Proof: `TestNodeConsumers_JudgeIdentityAndSkewAlike`, `TestGraphDigest_GraphV1IsFrozen`,
  `TestPinnedGraph_TheWorkersPayloadRunsOnlyOnTheSameGraph`, `TestAdapter_Execute_RunsOnlyThePinnedGraph`.
- Committed fixes 1159 to 1163; review fixes 1165 to 1172 (uncommitted). LESSONS 443 to 445.

### Current blocker

None.

### Open

- Watch in CI: `cmd/controller`'s `TestControllerLeaderElection_ReleaseGate` failover took 4.5 s against 3 s once under
  load here and passed alone.
- 1172 has no test; it was checked by reading.
- Records follow #155's (FP 1148-1158, LESSONS 440-442), merged before this branch; the rebase kept both sets.
- From earlier: 1085, 1139, 1143 (bindings on inventory delete), 1146.

### Next, in order

1. `make pr-check` through the monitor on the rebased tip, push, `make branch-containers`.
2. Open the pull request when it passes, with the failure patterns checked list in its body.

### Files changed on this branch

Committed: `pkg/wire/{deliverable.go,identity.go}`, `internal/termsafe/quote.go`, `internal/facts/`,
`internal/facts/entstore/`, `internal/journal/`, `internal/rollback/`, `internal/api/device_facts.go`,
`cmd/pleiades/facts_cmd.go`, `cmd/controller/builtin_catalog.go`, docs 09, 11 and 13, changelog fragments.
Uncommitted: `cmd/controller/{main.go,node_resolver.go}`, `internal/runbook/{graph_digest.go,nodes.go,...}`,
`internal/journal/{nodes.go,notes.go,subscriber_nodes.go,...}`, `internal/facts/entstore/{nodes.go,...}`,
`internal/dispatch/{ent_store_pin.go,worker.go,worker_devices.go,...}`, `internal/ent/schema/job.go` and its generated
code, migrations sqlite `0047_job_node_graph.sql` and postgres `0044_job_node_graph.sql`, `pkg/wire/{dispatch.go,
dispatch_version.go}`, `internal/adapters/native/adapter.go`, the tests beside each, docs 09 and 13,
`changelog/node-method-binding.security.md`, `changelog/facts-cli-shows-keys-escaped.fixed.md`. Records:
`FAILURE_PATTERNS*.md` (1159 to 1172; 1020 and 1031 resolutions), `LESSONS_LEARNED*.md` (443 to 445),
`HANDOFF_DOCUMENT.md`, `HANDOFF_ARCHIVE.md`.

Lab: no change reported for this branch.
