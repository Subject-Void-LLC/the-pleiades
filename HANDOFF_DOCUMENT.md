# Handoff Document

Rewrite the "Current Status" section when stopping mid-task or handing off, per `.AGENTS/AGENTS.md`.

## Current Status (this session)

**Branch `feature/Phase-117a-Runbook-Data-Flow`, cut from `main` at `8de7351` (PR #45, the Phase 110/40
work), 2026-09-29. The user's order: a runbook started by a ServiceNow (or other ticket system) event that
reads the ticket, triages, collects from Cisco/Arista/other devices, decides and writes back is a gap; plan
it, fix the two tracker release inconsistencies, make it the next worked phase, then "branch and start".**
Committed 2026-09-29 as eight commits at the user's word ("commit and push all"); the push waits on a
`make push-gate` receipt for the tip, and that run's outcome is recorded in 117a's Release Gate item. Rules unchanged
(method-as-key runbooks, two agents at most, heavy commands under `~/.local/bin/capped`, the lab
provisioned by The Pleiades only).

### What was done

- **Roadmap (gitignored files):** a new Part XVII, Orchestration Runbooks, with Phases 117a to 117d, all
  v0.3.0 and the owner's next work in that order (117a data flow on the Crawl tier, 117b segmented Walk
  dispatch, 117c credential slots scoped to targets and shared API devices, 117d service identities,
  template-bound tokens and idempotent launch). Items moved in from 87 and 107b, links added to 108, 88, 27,
  105, 116h, 42 and 115a, ten PLAN amendments, attestation links. The tracker's "112 depends on 75 (later
  release)" is fixed by moving Phase 75 to v0.3.0 (its WinRM transport ships from `main` already).
- **Phase 110's strict `make ci` on `8de7351`:** failed only on containers Docker Desktop never made
  reachable (one in the race pass, four in the coverage pass); each passed alone at once. Recorded in 110's
  item; it stays open, so "113 done but depends on 110" remains until a strict run passes.
- **Phase 117a (18/20):** four security findings measured on the unfixed tree and fixed, each with a test
  that failed first: S2 (a repointed `generic_http` device sent its credential to the new host;
  discoveries are now bound to what they probed, FAILURE_PATTERNS 392), S3 (injected secrets readable by
  `when_cel`, 393), M1 (`pleiades run` printed a method's echoed credential, 394), and the unbounded
  `http.request` body read (395). Built: the manifest's execution context (`Site`, `Device`,
  `DeviceCall`; all 104 built-ins state it) and the `hosts:` rule it drives; rendered task params
  (`vars`, `nodes`, `result`, typed single expressions, a `render` failure stage); `within:`;
  `Param.Format` with the command-text and URL rules held at plan time and again at render; `urlencode`
  and `cli_token`; `http.request`'s `json`; `pleiades run --extra-vars`; the converter deciding
  `delegate_to: localhost` by the native method's site. The release gate
  (`TestTicketRunbookReleaseGate`) runs the whole scenario through the real binary against Gitea in a
  container, a real sshd and an approved external triage program, with five negative controls.
- **ServiceNo!** (the user: "local mock service now in python called ServiceNo!"):
  `tests/serviceno/serviceno.py`, a standard-library mock of the ServiceNow Table API, run by
  `TestServiceNowRunbookReleaseGate` in a `python:3.12-alpine` container over verified TLS (so Python is
  never a build or CI dependency); the same test targets a real instance through
  `PLEIADES_SNOW_INSTANCE`/`_USER`/`_PASSWORD`. Dogfooding it found that conditions had no `result` root;
  they have one now, like rendered params.
- **117a's `make -k ci`** on the uncommitted tree, 17:38 to 19:27 UTC (1h 49m): every step but the tests
  and `docs-gen-check` (uncommitted, by construction) passed. Four tests failed and each passed alone: the SSH
  mesh, SCP and ticket runbook gates lost sshd containers, and the generic Walk gate lost a dispatch to a
  real defect. A deliberately failed dispatch's retries held the device's lock, and the next dispatch of the
  same device was contended five times in six seconds and then stranded with no result. That is Phase 103a's
  known bug, reproduced here for the first time (FAILURE_PATTERNS 396). The gate now gives each dispatch its
  own device and passed three times under `-race`; 103a's product fix is unchanged and still open. Timings
  go in `.IGNORE/timings.md` (the user's request).

### Open

1. **Phase 117a's strict `make ci`** on the committed tip (the uncommitted run above cannot pass
   `docs-gen-check`), and its commit message.
2. **The env-gated half of 117a's test plan:** the ServiceNow gate against a real developer instance
   (written, runs with the three variables set; not run), and the lab's IOS XE device (not written).
3. **Phase 110's strict `make ci`** is still open for the Docker reason above.
4. Carried: Phase 113's missing Pattern Entry Gate item; Phases 12 and 70 have no Implements line.

### Decisions for the user

1. Phase 117b next, once the branch is pushed and merged.
2. Whether to map Ansible's `uri` onto `http.request` in the converter, which is what makes
   `delegate_to: localhost` convert real ServiceNow playbooks (recorded as the follow-on in 117a).

### Files changed

See `git status`. New: `pkg/collection/execution.go`, `internal/engine/render_params.go`,
`internal/validate/template_rule.go`, `internal/redact/mapvalues.go`, `cmd/pleiades/extravars.go`,
`tests/serviceno` (ServiceNo! and its README), the
triage program under `cmd/pleiades/testdata/triage`, and the gates in `cmd/pleiades` (ticket runbook,
stale discovery, method secret mask, device-less call, shell injection). Changed: every built-in manifest
(execution context), catalogdata, the scaffold and `forge new-collection`, `internal/render`
(`Value`, `Expressions`, two filters), the engine's executor, target and journal stage lists, the generic
device types and onboarding, `http.request`, the native adapter and `cmd/runner`, the converter, generated
references, docs 01, 03 and 11, CLAUDE.md, changelog fragments, FAILURE_PATTERNS 392 to 396, LESSONS 258
to 260, the generic Walk gate's per-dispatch device. Local only: the roadmap, the specification and the attestation.
