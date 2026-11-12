# Handoff Document

Rewrite the "Current Status" section when stopping mid-task or handing off, per `.AGENTS/AGENTS.md`.

## Current Status (this session)

**2026-11-12. Branch `feat/42-oci-archtest-and-reproducible-build` (written by a subagent, rebased on `main` at `50d767de`),
closing Phase 42. Next: pr-check, push, branch-containers, PR, merge when green (merging authorized 2026-11-12 only).
Queued after it, each needing records first: `fix/122a-db-pg-findings` (Phase 122a), `test/131-r4-lookups-and-88-gate`
(Phases 131 and 88), `feat/87-set-fact` (Phase 87; its agent is building journaled rendered undo steps). An agent is
working on Phase 127a (`feat/127a-capability-grants`).**

### What works

- Phase 42: an archtest keeps every OCI package out of `pkg/collection`'s dependency closure; a scaffolded external
  Collection builds to one digest anywhere (`-trimpath -buildvcs=false`, cgo off, the module's toolchain); `forge
  new-external --script` wraps a script with no credential path (owner, 2026-11-12: credentials for scripts wait for
  Phase 136f's broker, item added there).
- Merged today: #158 to #162 (Phases 38, 129c, 129d, 127c; 117a's ServiceNow half).

### Open

- Owner questions from 127c: a re-import after a node rename makes a second device; `WriteHosts` takes no lock while
  `import-cml --via console` reads the network mid-write.
- 117a's IOS XE half needs a real IOS XE device. APIC sandbox does not answer (it can wait).
- Remaining v0.3.0 backlog after the queue: 127d (1), 127b (13), 112 (22).

Lab: no change.
