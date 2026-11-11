# Handoff Document

Rewrite the "Current Status" section when stopping mid-task or handing off, per `.AGENTS/AGENTS.md`.

## Current Status (this session)

**2026-11-10. Branch `feat/telemetry-boundary` (PLAN.md Section 19 item 7, what a span may carry), six commits on
`origin/main` at `0bfd7fe9`, tip `28e79e11`, in the worktree `.claude/worktrees/agent-aedef548b9236e37e`. Built and
reviewed (fp-reviewer and claim-refuter findings fixed). Not pushed, no pull request, `make pr-check` has not run. The
records below are unstaged.**

### What works

- **Spans carry identifiers only** (FAILURE_PATTERNS 1148). A masking exporter sits in front of stdout and OTLP: a fixed
  key allowlist that `PLEIADES_TELEMETRY_DENY_ATTRIBUTES` narrows, kept strings masked, declared names, events, scopes
  and status phrases only, span limits (1024 bytes), fail closed on a panic. The Runner uses `telemetry.RecordFailure`.
  Proof: `internal/telemetry`'s `TestMaskingExporterMasksEveryTextChannel`, `TestSetup_OTLPExportCarriesOnlyThePolicy`
  and the other boundary tests; `TestAgent_SpanFailuresAreFixedPhrases`.
- **Bounded before authentication**: the method is `_OTHER` outside the standard set (1149,
  `TestRouter_MethodLabelIsBounded`); a long path cannot size a span (1150, `TestRouter_ALongPathCannotSizeTheExportedSpan`).
- **The SDK's own writers are bounded**: the internal logger prints no values (1151,
  `TestSetup_TheSDKsInternalLoggerPrintsNoValues`); export errors are one masked, escaped line (1152,
  `TestSetup_ErrorReportsAreOneBoundedLine`).
- **Only `traceparent` crosses an edge, and the operator's ratio samples it** (1153, 1154):
  `TestPropagator_CarriesNoBaggage`, `TestPropagator_ExtractsNoTraceState`, `TestSetup_TheEdgeSamplesByItsOwnRatio`.
- **The archtest holds every span call site** (1155): `internal/archtest/telemetry_test.go`, with control cases.
- **gosec-check's exclusion matches only `.claude`** (1156): `TestExcludeDirFlag_LeavesOutOnlyADotClaudeDirectory`.

### Current blocker

The gate needs the main clone, which is busy: `fix/facts-release-skew` is being written there. `make gosec` and
`make pr-check` cannot run from this worktree (1157).

### Open

- 1157: gosec cannot type-check `internal/ent` from a worktree under `.claude/worktrees/`, even with no exclusion. Cause
  not found.
- 1158: a value with no secret shape under an allowed key is exported as is, and the exporter's span-name shape check
  accepts any API-scope name shaped like a path (only the archtest holds that one; the docs state the first limit only).
- Open items from the previous session are in its archive entry.

### Next, in order

1. When the main clone is free, check out `feat/telemetry-boundary` there (or in another checkout outside `.claude`)
   and commit these records.
2. `make pr-check` through the monitor on the clean committed tip.
3. Push, `make branch-containers`, then open the pull request when it passes, listing the failure pattern classes
   checked.

### Files changed on this branch

Code (committed): `internal/telemetry/` (new `masking_exporter.go`, `policy.go`, `outcome.go`, `edge.go`,
`errorhandler.go`, and `telemetry.go`), `internal/api/{middleware.go,metrics.go,router.go}`,
`internal/runner/{agent.go,agent_handle.go}`, `cmd/controller/main.go`, `cmd/runner/main.go`,
`internal/archtest/telemetry_*.go`, `tools/gosec-check/{exclude.go,main.go}`, `go.mod`, the tests beside each,
`docs/10-running-in-production.md`, and `changelog/telemetry-boundary.security.md`. Records (unstaged):
`FAILURE_PATTERNS*.md` (1148 to 1158), `LESSONS_LEARNED*.md` (440 to 442), `HANDOFF_DOCUMENT.md`, `HANDOFF_ARCHIVE.md`.

Lab: no change reported for this branch.
