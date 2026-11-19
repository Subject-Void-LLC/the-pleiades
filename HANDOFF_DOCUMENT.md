# Handoff Document

Rewrite the "Current Status" section when stopping mid-task or handing off, per `.AGENTS/AGENTS.md`.

## Current Status (this session)

**2026-11-19. Branch `docs/records-phase41` (records only, from `main` at `decfcbc7`, the #207 merge). Merged since the
last handoff: #192 to #199 (the CML fixes, already in the previous entry's queue) and this run's Phase 41 work: #200
(fact fuzzing), #201 (facts batch claims), #202 (broker-enforced Runner identity, breaking for 0.5.0), #203
(`secret_mask` by value), #204 (fact read port), #205 (Linux identity facts), #206 (keyed running-configuration
digests), #207 (package read-backs). Open: #208 (systemd read-backs, branch `feat/facts-readback-services`, CI
pending).**

### What works

- #200 to #207 merged through pull requests, whose required checks the ruleset holds. A CI run on #202 failed the
  compose upgrade gate (FAILURE_PATTERNS 1273); it was fixed on the branch before the merge.
- Runner identity: `TestReleaseGate_ARunnerPublishesOnlyUnderItsOwnName` against a real nats-server 2.14.4.
- Facts: `TestEntJobStore_ClaimBatch_ARaceWritesOneClaim`, `TestFacts_AMaskedValueIsNotStoredUnderAnotherKey`,
  `TestReadFact_*`, `FuzzNormalize`, and the read-back tests in `internal/catalog/pkg/{apt,dnf}`.
- Records: FAILURE_PATTERNS 1273 to 1278, LESSONS 454 and 455, CI_LESSONS 34 and 35, seven changelog fragments.

### Blocker

- None. #208 waits on its CI.

### Open

- Phase 41, in dependency order. Phase 41 itself first, since 41b to 41h depend on it and 41b was ticked ahead of
  it: (1) wire #204's read port (`WithFactReader` has no caller in any composition root, so no run can read a stored
  fact); (2) facts at rest (owner decision); (3) identity facts on FreeBSD, macOS and Windows and per-component
  serials for network gear (lab). Then the items an independent code check re-opened on 2026-11-19: 41b item 2 (the
  fact-declaration check misses 12 of 20 declaring methods), 41c item 7 (`net.yang.facts` and `cisco_router`'s
  `netconf: auto` are not built), 41c item 11 (no proof of Linux service facts through `pleiades`; Windows packages
  drop unreadable keys), 41h item 12 (the rename walk overclaimed). Then 41c's remaining read-backs (`svc.windows.*`,
  the file methods, and the read-back declaration for every other write-capable method), 41d, 41e, 41f, 41g and 41h's
  Release Gate.
- Fixed on this branch (`1368a18d`): #205 read `os.machine_id` and DMI values inside a container, which Phase 41's
  identity item forbids; each identity read now skips a container (WSL included).
- Lab-pending: identity facts on FreeBSD, macOS and Windows (Linux only so far); Ansible names for the four identity
  facts (none mapped until a lab comparison measures one); `config.running_digest` has not run on a real device.
- Possible security weaknesses, not yet reproduced: a symlink into a private directory can get a plain file digest;
  POSIX ACLs are ignored when judging a file world-readable; a local user can fake a Linux port's owning process.
- Owner decisions: eighteen are open, listed with options, edge cases and dependencies in the session of 2026-11-19
  (facts at rest, `net.frr.facts`' plain `config_sha256`, identity facts' Ansible names, the fact-declaration check's
  design, Windows unreadable package keys, symlink and ACL digests, port owner parsing, Walk-tier fact benchmarks,
  the device response's link to its facts, nested groups' owner (PLAN Section 3, unbuilt and unowned), lock tracing,
  `kubernetes_cluster` certificate facts, Phase 136a's unbuilt clauses, retired citations, 23 attestation gaps, the
  VirtualBox README's old fact names, Phase 131's lab scripts, and Phase 127a's archtest sweep).
- Flake, unexplained: `TestSubscriber_HoldsEachBatchToItsDevicesClaim` (`internal/facts/entstore`) failed once in
  about 35 runs (its log-value check) and not in 30 more. Watch it; no failure pattern until it recurs.
- FAILURE_PATTERNS 1267 (`internal/api/device_facts_test.go` builds its routes by hand) is still open.
- #202's residual: a Runner can still report false values under its own name; per-batch signing is Phase 105.

### Next steps

1. Merge #208 once its checks pass on its own head commit (CI_LESSONS 35); refresh the local PR log in the same command.
2. Commit these records, gate and merge them. If they merge before #208, move
   `changelog/facts-systemd-readback.added.md` onto #208's branch instead.
3. The `svc.windows.*` and file read-backs, then the manifest declaration for every other write-capable method.
4. 41d, 41e, 41f, 41g, then 41h's Release Gate.

### Files changed

`FAILURE_PATTERNS.md`, `FAILURE_PATTERNS_ARCHIVE.md`, `LESSONS_LEARNED.md`, `LESSONS_LEARNED_ARCHIVE.md`,
`CI_LESSONS.md`, `HANDOFF_DOCUMENT.md`, `HANDOFF_ARCHIVE.md`, seven new files under `changelog/`, and (git-ignored)
`.SPECIFICATION/IMPLEMENTATION.md`, `.SPECIFICATION/SECURITY_ATTESTATION.md`.

Lab: not used by this run's records. The previous entry left the CML lab `pleiades-proofs` up with NX1 (run
`fdeed9a6`) and a reservation ending about 2026-11-19 00:30Z; whether it was torn down is not recorded here. The local
VMs were not used.
