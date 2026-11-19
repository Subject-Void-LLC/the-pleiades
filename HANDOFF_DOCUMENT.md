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

- Phase 41, in dependency order: (1) the post-change reading for `svc.windows.*` and the file methods; (2) the
  read-back declaration in the manifest of every other write-capable method (read back, or say why not); (3) 41d
  baselines, drift, as-of reads, events and lineage; (4) 41e the `facts` template root and targets by facts (the
  first users of #204's read port, which no composition root wires yet); (5) 41f keepalive; (6) 41g undo to a point;
  (7) 41h's Release Gate, a recorded `make ci` run holding its gates green.
- Lab-pending: identity facts on FreeBSD, macOS and Windows (Linux only so far); Ansible names for the four identity
  facts (none mapped until a lab comparison measures one); `config.running_digest` has not run on a real device.
  The tracker also names a defect in #205: inside a container it still records `os.machine_id` and DMI values, which
  Phase 41's identity item forbids.
- Owner decisions: `net.frr.facts`' plain `config_sha256` stat (left unchanged in #206; it is a guessing oracle in a
  register or report); how stored facts are protected at rest (Phase 41's encryption item; owner said on 2026-11-04
  that Phase 41 decides it).
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
