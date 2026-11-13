# Handoff Document

Rewrite the "Current Status" section when stopping mid-task or handing off, per `.AGENTS/AGENTS.md`.

## Current Status (this session)

**2026-11-13. Branch `feat/127a-capability-grants` (Phase 127a, written by a subagent, rebased on `main` at `a9a3968e`).
Merged since the last handoff: #163 (Phase 42), #164 (122a, 131, 88), #165 (87; journal format 3). Queued after this PR:
`feat/127b-dispatcher-and-validate`, `feat/112-winrm-breaker`; agents are finishing `feat/112-cli-and-127d-runner` and
`feat/112-vbox-lab`. `.IGNORE/resume.md` holds the cycle and the queue; merging is authorized for 2026-11-13.**

### What works

- Phase 127a: grants bound to a definition digest (revision, lapse reasons), revoke by build, data capabilities refused in
  built-in manifests, the loader's per-namespace ownership, a probe bounded by `--timeout`, grant warnings from the shared
  validation; FAILURE_PATTERNS 1200 to 1203. PLAN.md Section 8 amended (data capabilities, a third tier).
- Before every gate: coverage of the touched packages against their floors, `internal/archtest`, `tools/gosec-check`
  (each caught a gate failure on 2026-11-12/13).

### Open

- Owner decisions recorded 2026-11-13 and being built: a loose vault refused, `--vault` moves every secret, snapshot delete
  refused on a running VM, `virt.vbox.media.prune`, a sync refuses to adopt a hand-added device.
- Blocked on the owner: an IOS XE device (117a's IOS XE half, 127b's snapshots of more than FRR); APIC sandbox down.
- Owner questions: comments naming `auto-roboto` in docker-compose.yml and tools/breakglass; 127c's rename re-import and
  WriteHosts lock.

Lab: unchanged (agents left win-check and bsd-check powered off and as found).
