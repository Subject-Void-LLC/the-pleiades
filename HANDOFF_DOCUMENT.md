# Handoff Document

Rewrite the "Current Status" section when stopping mid-task or handing off, per `.AGENTS/AGENTS.md`.

## Current Status (this session)

**Branch `feature/Phase-110-40-Rollback`, cut from `main` at `d572759` (PR #44), 2026-09-28. The user's
order: "Phase 110: Connection Persistence ... Phase 40: The Run Journal and Rollback ... Validate with
creation and rollback of BSD VM".** Decisions the user made: Walk-tier rollback now, on both tiers; build a
windowed fan-out for the Walk tier's `forks`. Nothing is committed; the commit messages are in the final
message of the session. Rules unchanged (method-as-key runbooks, two agents at most, heavy commands under
`~/.local/bin/capped`, the lab provisioned by The Pleiades only).

### What was done

- **Phase 110 (13/15):** the Walk tier's `forks` was offered and read by nothing. Built a windowed
  fan-out: `job_task.waiting` plus a `slot` with a unique `(slot, job)` index (expand-only after the
  classifier refused the first design, FAILURE_PATTERNS 389), pumped at fan-out, after each result and by
  the leader's reaper. Gate: `TestForksWindowReleaseGate_NeverMoreThanForksAtOnce` (real NATS, real Runner,
  real sshd, overlap measured on the device, with an unwindowed control). Open: the strict `make ci`.
  The Walk tier's ignored `limit` field moved to Phase 91.
- **Phase 40 (22/22 plus the commit message):** rollback on both tiers, planned by one pure planner
  (`internal/rollback`). Methods declare which undo params are identifiers the journal may keep
  (`sdk.InverseSpec`); the journal gained `inverse_params` and seven more fields; `pleiades rollback`,
  `pleiades journal list|show`, `.pleiades/run.lock`; `POST /jobs/{id}/rollback` with its own NATS
  subject (`runner-rollback`) so an older Runner never re-runs the undone runbook (FAILURE_PATTERNS 381), a
  Jobs-view **Roll back** form, `rollback_of` on a job; `rollback:` and `reversible:` runbook keys.
  Decision recorded in `PHASE40_MASKING_DECISION.md` Section 13.
- **Two security findings, reported as found:** `net.netconf.config`'s `target` doubled as the device
  selector (fixed: reserved param names, the param is now `datastore`, FAILURE_PATTERNS 378); any Runner
  could write any job's journal (narrowed by dispatch admission; the plan's per-dispatch MAC key was
  dropped because it would put a new secret on the stream; residual in Phase 105, FAILURE_PATTERNS 379).
- **Lab, through the real binary:** a FreeBSD VM (`freebsd-03-scratch.yaml`) made and rolled back, the
  stack guard, a resume after a real failure, and `TestLab_FreeBSDVMRollback`. It found two planner
  defects and one method inconsistency (FAILURE_PATTERNS 384, 385; 388 is the hardware timing).

### Lab state

As the session found it: `ubuntu-lab`, `win-lab` and `bsd-lab` running, everything else off. ubuntu-lab
was stopped for memory and started again by rolling back that stop. `~/pleiades-lab/bin/pleiades` is this
branch's build. `~/pleiades-lab/runbooks/freebsd-03-scratch.yaml` is new. The lab gate runs as
`PLEIADES_VBOX_PROJECT=~/pleiades-lab PLEIADES_VBOX_HOST=vengeance`.

### Gate run (2026-09-28, `make -k ci`, alone, capped, on the uncommitted tree)

- **Passed:** build, vet (both tag sets), fmt, tidy-check, test-repeat, gosec (23 findings, each waived),
  govulncheck (none), docs-lint, helm-lint, templ-gen-check.
- **`docs-gen-check`** fails by construction until the work is committed (`git diff` against the index);
  `gendocs` had just been run, so the committed tree will match.
- **One real defect found and fixed:** `internal/backup`'s chaos test cut its link after a fixed 1.5 s,
  and this branch's three new Postgres migrations lengthened the reads before pg_dump past it
  (FAILURE_PATTERNS 390; it passed on the base commit, checked in a throwaway worktree). It now cuts once
  the dump's session appears.
- **Docker degraded under the load** (containers whose published ports never answered, and later even
  the reaper container failed to start). Every failure of that shape passed alone: the generic Walk gate
  (four runs, plus the whole `cmd/runner` package), meshid's renewal gate (twice), topology (three of
  four; the fourth was the same broker timeout while Docker was still slow), the plugin conformance suite
  with LocalStack, `internal/event`, and all 18 `tests/e2e` tests that failed.
- **Coverage** (the tolerant check, which reruns failures alone) found five packages below their floors,
  all new code without in-process tests: `cmd/pleiades` 63.3 (floor 69.4), `internal/journal` 75.4 (94.1),
  `internal/adapters/native` 88.8 (92.9), `internal/api` 97.3 (98.4), `internal/validate` 99.2 (100).
  Tests added; now 73.6, 94.6, 93.6, 98.4 and 100. `internal/rollback` gets its first floor, 92.6.
- **Also added:** the real-broker enforcement gate now proves the rollback consumer's grants
  (mutation-checked: without the grant a Runner cannot create it and the gate fails).
- **Not done:** a strict `make ci` on a committed tip, which is the only thing that writes a receipt and
  closes Phase 110's last item.

### Decisions for the user

1. **Phase 110's `limit`:** moved to Phase 91 by the user (2026-09-28), beside `serial:`.
2. **Strict `make ci` on the committed tip:** Phase 110's last item closes only on that pass; the push
   used `make push-gate`'s receipt.
3. Carried: the broker's dedup-window memory (previous session), Phase 80's address profile, Phase 113's
   missing Pattern Entry Gate item.

### Files changed

See `git status`. New: `internal/rollback`, `internal/journal` (reader, run lock, seal, admission, rollback
reads), `internal/dispatch` (window, rollback), `internal/api/dispatcher_rollback.go`,
`internal/adapters/native/rollback.go`, `internal/adapters/routing/rollbackonly.go`,
`cmd/pleiades/rollback.go` and `journal_cmd.go`, `cmd/controller/rollback_devices.go` and
`journal_admission.go`, `internal/ui/resources/jobs/rollback.go`, `pkg/collection/reserved.go` and
`inverse.go`, four migrations, the gates in `cmd/pleiades` and `cmd/runner`, the lab runbook and README
section. Changed: the undo declarations across the catalog, `pkg/sdk/inverse.go`, the journal projection,
`topology`, `meshid` grants, `cmd/runner`'s third loop, docs 01, 02, 09, 10, 11, generated references,
CLAUDE.md, changelog fragments, `coverage-floor.json` (the new `internal/rollback` floor), the backup chaos test, FAILURE_PATTERNS 378 to 390, LESSONS 254 to 257. Local only:
IMPLEMENTATION.md, SECURITY_ATTESTATION.md, PHASE40_MASKING_DECISION.md.
