# Handoff Document

Rewrite the "Current Status" section when stopping mid-task or handing off, per `.AGENTS/AGENTS.md`.

## Current Status (this session)

**2026-11-12. Branch `test/127c-importer-fuzz-and-failures` (written by a subagent, rebased on `main` at `5df78219`),
closing Phase 127c. Next: pr-check, push, branch-containers, PR, merge when green (authorized today only). Then
`feat/42-oci-archtest-and-reproducible-build` (Phase 42, three items, written by a subagent; records still to write).
Two agents are working: Phase 122a's db.pg findings (`fix/122a-db-pg-findings`) and Phase 131 R4 plus Phase 88's gate
(`test/131-r4-lookups-and-88-gate`).**

### What works

- Phase 127c: fuzz targets for the GNS3 and CML decode, the failure-mode walk, a scoped security pass; FAILURE_PATTERNS
  1182 (a CML lab deleted between list and read failed the whole list), 1183 (GNS3 port out of range misreported), 1184
  (an oversize test that could not fail, a repeat of 820).
- Merged today: #158, #159 (Phase 38), #160 (129c, 129d), #161 (117a's ServiceNow half on a real instance; Cisco
  controller test settings).

### Open

- Owner questions from 127c: a re-import after a node rename makes a second device (identity by name); `WriteHosts`
  takes no lock while `import-cml --via console` reads the network mid-write.
- 117a's IOS XE half needs a real IOS XE device (asked). APIC sandbox does not answer (owner: it can wait).
- The rest of the v0.3.0 backlog: 87 (6), 127d (1), 127a (11), 127b (13), 112 (22), plus the two agents' phases.

Lab: no change.
