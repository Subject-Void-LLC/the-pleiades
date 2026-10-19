# Handoff Document

Rewrite the "Current Status" section when stopping mid-task or handing off, per `.AGENTS/AGENTS.md`.

## Current Status (this session)

**2026-10-19. Branch `feat/wait-backoff-report-contract`, off `main` at `eac1812e` (#75 merged green).** One pull
request at a time; `make pr-check` before every push; the owner merges.

### What #75 shipped

`make pr-check` ends with one report of the run (`tools/prreport`), from records each testgate pass, OS check and
step writes.

### This branch, at the owner's request ("along with the exponential backoff, make the consolidated report a stable, versioned contract for tooling")

- **`wait.connection` waits for a machine that keeps answering.**
  - `backoff: exponential` doubles the gap after each failed try, from `sleep` up to `max_sleep`, with jitter in
    the gap's upper half.
  - `steady` asks for that many fresh logins in a row, `steady_interval` apart. A refusal mid-streak starts the
    streak and the backoff again.
  - It records `tries` and `failures`. A setting that would do nothing, or cannot work, is refused before any
    try.
  - The schedule is tested with a recorded sleep, never the wall clock (FAILURE_PATTERNS 479's class). The streak
    is tested against the real SSH server, counted in its own logins.
- **A defect found on the way (FAILURE_PATTERNS 524):** the wait's SSH try borrowed the run's pooled session, so a
  second wait passed without logging in. `sdk.ConnectFresh` now always logs in.
- **The OS checks wait for a box they started with `wait.connection`'s streak**: three fresh logins, ten seconds
  apart, backing off from 2 to 30 seconds. The check's own loop is gone. The lab examples' first-boot waits ask for
  a streak too.
- **The pr-check report is a versioned contract** (`tools/internal/prreport`), with a schema published by
  `tools/gendocs` (`docs/reference/schemas/pr-check-report.json`). Schema 2:
  - each problem is an object with a stable code;
  - the report names what it expected and what is missing;
  - verdicts are `pass`, `fail`, `incomplete`, `running` and `interrupted`.

  The rules: a reader ignores unknown fields, treats unknown values as not a pass, and refuses unknown majors. The
  schema is open to match. pr-check begins with a running report, and its trap merges what ran as interrupted.
- **Another defect found on the way (FAILURE_PATTERNS 525, fixed before push):** that trap ran twice on one signal,
  and the second merge overwrote the first. The handler now disarms itself and exits.
- **Proven so far:** every touched package's tests under `-race`. The waits package and the report package are at
  100%. The interrupt and early-exit paths were proven in a real `sh`. Not yet live: `wait.connection`'s streak
  against the lab boxes. This branch's own pr-check is that proof, since it starts the saved FreeBSD and Windows
  boxes through it.

### Next

1. `make pr-check` (cap at least 3 G; govulncheck peaks near 2.7 GB), read its report, then push and open the pull
   request with `-R`.
2. **The live gates read gitignored `tests/<name>/.env` files** (agreed 2026-10-19, in the tracker):
   - a loader in `internal/testsupport/need`;
   - examples drafted in the scratchpad for ios, xr, catalyst, servicenow, winrm, localstack, cloudflare and ai;
   - new live gates for `edge.*` (Cloudflare, read-only token) and `ai.*` (local Ollama and an OpenAI-compatible
     endpoint).
3. **Every method proven on every OS it targets** (Phase 122b).
4. **Packages for Server Core:** Chocolatey, Scoop and PSResourceGet, after a design note.
5. **Phase 122b's other open items:**
   - `validate` is silent on `always:` and `rescue:`;
   - the screenshot `dest` gotcha;
   - the converter maps no Windows module;
   - and the rest listed there.

### Lab state

- `bsd-check`, `win-check`, `lab-dc1` and `lab-lin1` are saved; checks start and save the check boxes.
- The domain's runbooks are in `~/pleiades-lab/runbooks/win/` (`dc-00` to `dc-04`).

### Files changed this session

- `internal/catalog/wait/connection.go` and its tests, `pkg/sdk/connect.go` and its test;
- `internal/forge/catalogdata/collections_gating.go`, the regenerated `docs/reference` and
  `internal/api/wellknown`;
- `tools/internal/oscheck/box.go` and its test, `examples/virtualbox_lab`;
- `tools/internal/prreport` (new), `tools/prreport`, `tools/gendocs/{reportschema,prreportschema,run_test}*`, and
  doc comments in `tools/internal/{flakegate,oscheck}`;
- the Makefile, `docs/design/os-checks.md`, `CLAUDE.md`, three changelog fragments;
- `FAILURE_PATTERNS*` and `HANDOFF_*`.
