# Handoff Document

Rewrite the "Current Status" section when stopping mid-task or handing off, per `.AGENTS/AGENTS.md`.

## Current Status (this session)

**Two branches this session, 2026-09-29.** `feature/Phase-75-WinRM-Closeout` (Phase 75, 25/25) is committed and
**pushed** (`9b078019`, after `make push-gate` passed in 1 h 34 min on the old three-pass gate; no PR opened).
`feature/Phase-118-Verification-For-Others`, cut from that tip, holds Phase 118 and is **committed locally,
not pushed**: the user asked to "build out the new way" after "How do we make verification better and able to
work for others". Rules unchanged (method-as-key runbooks, two agents at most, heavy commands under
`~/.local/bin/capped`, the lab provisioned by The Pleiades only).

### What was done

- **Phase 75** (see the archive's previous entry): one shared circuit breaker, the SSH refused-credential
  fix, transports checked at plan time, the pywinrm and Ansible comparison, FAILURE_PATTERNS 398 to 400,
  LESSONS 261. Also this session: a failure-mode axis added to every phase's adversarial gate
  (IMPLEMENTATION.md Gate 2, and a pointer in `.AGENTS/AGENTS.md`), after the user asked whether their
  fuzz and adversarial stages were enough.
- **Phase 118, verification others can run:** `testgate` gained tiers, shards, `-strict`, `-coverage-out`,
  a skip ledger (also on the GitHub job summary) and failure output; `coverage-check -measured` checks floors
  from those numbers and fails a floored package with none; `make test-full` is one pass
  (`-tags integration -race -cover -count=1`) replacing test-race, test-integration and coverage's own run in
  `ci` and `push-gate`; CI jobs `fast`, `containers` (four shards, cached images), `coverage`, `nightly`,
  and an experimental `winrm` job; `make doctor`; `make test-clean-room`; actions pinned by commit;
  `actionlint` pinned. CONTRIBUTING.md and CLAUDE.md describe the new path.
- **The clean room found three defects no gate here could see:** a fixture `.gitignore` kept out of every
  commit (FAILURE_PATTERNS 401), three user-namespace tests that failed on Ubuntu 24.04 and in containers
  (now skip with the fix, and fail where CI requires `userns`), and `testgate` never printing why a test
  failed (402). Fixed; the second clean-room run is green. LESSONS 262.

### Open

1. **The new `make push-gate`** on the Phase 118 tip: its time against the old 1 h 34 min is Phase 118's
   Fuzz/Stress evidence, and its receipt is what a push needs (see the final message for the outcome).
2. **The first real CI run** needs a pull request; the workflow is linted but unproven on GitHub, the
   `winrm` job is experimental, and macOS `fast` is advisory until it has passed once.
3. Carried: 117a's env-gated ServiceNow gate; Phase 110's strict `make ci`; Phase 113's Pattern Entry Gate;
   Phases 12 and 70 have no Implements line.

### Decisions for the user

1. **Push Phase 118 and open its PR?** That is what runs the new CI for the first time. Phase 118 was
   committed locally without being asked, because the clean room tests the committed tree; nothing is pushed.
2. **Open Phase 75's PR** (pushed, no PR yet).
3. Optionally add a read-only `DOCKERHUB_TOKEN`/`DOCKERHUB_USERNAME` secret to raise Docker Hub's pull limit.
4. Once macOS `fast` and `winrm` pass on GitHub, make them required.

### Files changed (Phase 118)

New: `tools/doctor`, `tools/testimages`, `tools/testgate/{options,report}.go`,
`tools/internal/flakegate/report.go`, `tools/coverage-check/measured.go`, `internal/testsupport/require.go`,
their tests, `pkg/cloudinit/testdata/console-ubuntu-2404.log`, changelog `tests-run-in-ci.changed.md`.
Changed: `Makefile`, `.github/workflows/ci.yml`, `.gitignore`, `flaky-packages.json`'s header,
`tools/testgate/main.go`, `tools/coverage-check/main.go`, `tools/internal/flakegate/flakegate.go`,
`internal/testsupport/privateroot.go`, CLAUDE.md, CONTRIBUTING.md, docs/11, FAILURE_PATTERNS 401 to 402,
LESSONS 262. Local only: the roadmap (Phase 118, 10 of 12).
