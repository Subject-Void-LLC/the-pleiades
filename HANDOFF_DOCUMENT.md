# Handoff Document

Rewrite the "Current Status" section when stopping mid-task or handing off, per `.AGENTS/AGENTS.md`.

## Current Status (this session)

**2026-11-12. Branch `test/117a-servicenow-real-instance`, rebased on `main` at `697aa8d1` (#160 merged). The ServiceNow
half of Phase 117a's open item, proven on the owner's real instance, plus live-test settings for Catalyst Center, an
APIC and an SD-WAN Manager. Next: pr-check, push, branch-containers, PR, merge when green (authorized today only).
Two code-writing agents are working in worktrees: Phase 127c (`test/127c-importer-fuzz-and-failures`) and Phase 42
(`feat/42-oci-archtest-and-reproducible-build`); review, record and gate each after this PR, one at a time.**

### What works

- `TestServiceNowRunbookReleaseGate` passes on ServiceNo! and on the owner's PDI (dedicated account) after
  FAILURE_PATTERNS 1181: ServiceNo! now computes priority from impact and urgency with the lookup measured there.
- `tests/e2e`'s live Catalyst Center tests pass against the owner's endpoint; the controller is found by the endpoint's
  host, not a hard-coded sandbox name. SD-WAN Manager login works through a probe (its sandbox certificate expired
  2026-09-27 and is pinned in the example). The APIC sandbox does not answer TCP 443 (owner: it can wait).
- Test settings: `tests/{servicenow,catalyst-center,aci-simulator,cisco-sdwan}/.env.example`, each `.env` gitignored
  and chmod 600. ServiceNow, ACI and SD-WAN code belongs in external Collections (owner, 2026-11-12).

### Open

- 117a's IOS XE half needs a real IOS XE device: the DevNet always-on Catalyst 8000 (PLEIADES_E2E_IOS_USER/PASS) or a
  fresh CML reservation (tests/cml/.env is from 2026-10-27). Asked the owner.
- The rest of the v0.3.0 backlog: 42 and 127c (agents), 88 (1), 127d (1), 131 (5), 87 (6), 122a (7), 127a (11), 127b
  (13), 112 (22). Fable drafts in `.IGNORE/claude-api/out/stubs1/` are unproven.
- `feat/release-tag-only`'s worktree was removed (clean, branch kept) to make room for the agents.

Lab: no change.
