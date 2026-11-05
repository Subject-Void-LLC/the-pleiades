# Handoff Document

Rewrite the "Current Status" section when stopping mid-task or handing off, per `.AGENTS/AGENTS.md`.

## Current Status (this session)

**2026-11-05. Branch `fix/facts-sweep-security`, from `main` at `f1609033` (PR #116 merged). Five commits, from the
facts review sweep of 2026-11-04 (`VERDICTS-facts.md` items 3, 2, 7, 12 and 13): `3e318af1` (the chain exempts no
unchained value), `5999dd16` (a secret-named field inside a value), `9221ff2f` (a facts batch the database can never
store), `b2f7ff6b` (the file store syncs its state and history), `185e1833` (a late change keeps a later
confirmation in the history). Not through pr-check, push or a pull request yet.**

### What works (each test failed on the old code where its entry says so)

- **The fact chain exempts nothing** (FAILURE_PATTERNS 921): `TestVerifyChain_AnUnchainedValueAtTheStartIsABreak`;
  `unchained` is gone from the CLI, `internal/apispec` and the generated OpenAPI.
- **A secret-named field at any depth of a fact's value is refused** on both tiers (920):
  `TestPrepare_ASecretNamedInsideAValueIsNotStored`, `TestVerify_ASecretNamedInsideAPublishedValueIsDropped`.
- **A facts batch with a malformed ID, or one PostgreSQL refuses as data, is acknowledged, not redelivered** (919):
  `TestSubscriber_APoisonedBatchIsAcknowledgedOnPostgres`, `TestSubscriber_ADataExceptionIsAcknowledgedAndOthersRedelivered`
  (real PostgreSQL 15). The reviewer's own claim (a NUL or huge number in a value) was refuted by measurement.
- **The CLI's fact file store syncs before it renames, and reads an empty state file as no state** (922):
  `TestFileStore_AnEmptyStateFileIsReadAsNoState`. The sync itself cannot be tested without a crash.
- **A late change no longer makes the next run record a change nobody saw** (923): the `factstest` conformance case
  (file store, ent on SQLite and on PostgreSQL), `TestResumed_OnlyAConfirmationAfterTheLateChangeWithNothingBetween`.
- Passing under `~/.local/bin/capped` 4 to 4.5G, `GOMAXPROCS=4`: `go test ./internal/facts/...` (facts, and entstore
  with its PostgreSQL container), `./internal/catalog/facts`, `./internal/apispec`, `./internal/redact`, and `-run
  Fact` for `./internal/engine` and `./cmd/pleiades`. Coverage: `internal/facts` 91.1% (floor 89.1),
  `internal/facts/entstore` 90.0% (floor 88.2).
- Docs: `docs/04-writing-runbooks.md`, `docs/09-control-plane-and-api.md`; gendocs regenerated the OpenAPI.
- Records: FAILURE_PATTERNS 919 to 923, changelog fragment `changelog/facts-store-integrity.security.md`.

### Current blocker

None. Next is pr-check for this branch.

### Open

- `internal/catalog/gate/runner.go` `deviceRunner.stop` refuses only `pid <= 0`: a hostile device printing `1` as its
  pid marker would get `kill -TERM 1` and `kill -KILL 1`, as root to init under become. No group kill, low severity,
  needs a test seam. Not fixed, not yet a failure pattern.
- Carried: `Conn.Close` returns an error after a sever; a sever timer already running when `finish` is called can
  close a non-pooled `Conn` that `stopDetached` then reuses (it redials). Routes are not followed on a Runner, by
  external Collections, or by the onboarding SSH probe; a no-`sh` device onboarded before #110 fails at `/bin/sh`
  until onboarded again, and an older Runner reads `no posix sh` as POSIX.
- Carried: FAILURE_PATTERNS 448 (`pkg/tftpxfer` on macOS); `internal/setup`'s `Fields` comment; 867, 853, 41d's
  items, vuln-corpus `C13_auto-roboto_2026-11-03` pending review, the owner decisions of 2026-11-04 (see the
  archive's earlier entries).
- API credit: $20.53 of $92 spent as of the last report. Expires 2026-11-08.

### Next, in order

1. `make pr-check` through the monitor (`--cap 5200M`), push, `make branch-containers`, the pull request. Merge only
   when green, `record-actions`, pull `main` and clean.
2. The facts sweep correctness PR: `VERDICTS-facts.md` items 1, 8, 9, 11, 14, 15 and 16.
3. Facts churn: items 4, 5, 6 and 10.
4. 41c-3, then 41d to 41g.
5. `file.remove`'s check refuses by default.
6. 117b, 117c, 105.
7. Release 0.4.0 (no tag until the owner says).

### Files changed on this branch

`internal/facts/` (`chain.go`, `chain_test.go`, `facts.go`, `facts_test.go`, `file.go`, `file_failures_test.go`,
`file_store.go`, `late.go` (new), `late_test.go` (new), `lock_other.go`, `lock_unix.go`, `lock_windows.go`,
`nested.go` (new), `nested_test.go` (new), `factstest/conformance.go`), `internal/facts/entstore/` (`store.go`,
`store_test.go`, `subscriber.go`, `postgres_poison_test.go` (new)), `internal/ent/data_exception.go` (new),
`internal/apispec/apispec.go`, `cmd/pleiades/facts_cmd.go`, `docs/04-writing-runbooks.md`,
`docs/09-control-plane-and-api.md`, `docs/reference/schemas/openapi.json`, `internal/api/wellknown/openapi.json`.
Records (uncommitted): `FAILURE_PATTERNS.md` and its archive, `HANDOFF_DOCUMENT.md`, `HANDOFF_ARCHIVE.md`,
`changelog/facts-store-integrity.security.md` (new).

Lab: no lab VM use was reported for this session.
