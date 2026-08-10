# Handoff Document

Rewrite the "Current Status" section when stopping mid-task or handing off, per `.AGENTS/AGENTS.md`.

## Current Status (this session)

**This session built Phase 18 (The Grand Integration Test), and it grew a real production half.**
Branch is `feature/The-Grand-Integration-Test`. **Nothing is committed**; commit messages are drafted
in Phase 18's own checklist in `.SPECIFICATION/IMPLEMENTATION.md` and running them was never requested.

**Why the phase grew.** The checklist reads as test hardening, but exploration found the test could
never have been representative: `cmd/controller` opened SQLite only (`ent.OpenEmbedded`), and
`internal/ent/migrate` registered one dialect, while `tests/e2e` started a PostgreSQL container and
brought its schema up with `client.Schema.Create` (ent's automatic diff-and-apply, which no binary
uses). The most integration-shaped test in the repository validated a database configuration that
existed nowhere. The user's decision was PostgreSQL in production behind a real database abstraction,
with SQLite retained as a second adapter, all inside Phase 18.

**What's real, part A, the database abstraction.** `internal/ent.OpenDatabase(ctx, Config{DSN})` is the
one seam every composition root now uses; it resolves a dialect from the DSN scheme and delegates to
`open_sqlite.go` or `open_postgres.go`. `OpenEmbedded` survives as the SQLite shorthand, so
`internal/crypto` and the existing ent tests did not churn. `migrate.Apply` is genuinely
dialect-agnostic now: `migrationSource` carries its own `insertVersion` statement, because `applyOne`
recorded versions with a `?` placeholder that `lib/pq` rejects, and rejects inside the same transaction
as the DDL, so the failure would have read as broken DDL (`FAILURE_PATTERNS.md` #92, verified against a
real server: `pq: syntax error at or near ","`). `internal/ent/migrate/gen` takes a dialect argument and
generated `migrations/postgres/0001_initial.sql`; the Postgres set starts squashed on purpose, since
ent can only diff against the schema it desires today. `cmd/controller` resolves `DB_DSN`, with
`DB_PATH` kept as the SQLite shorthand and both-set as a startup error.

**What's real, part B, the test.** `tests/e2e` now builds `cmd/controller` and `cmd/runner` in
`TestMain` and runs both as real subprocesses against a real PostgreSQL container and a real NATS
container, driven over a real socket with real HS256 tokens (`authtest.NewWithSecret`, added because a
random binary secret cannot survive an environment variable). It seeds five devices across two groups
through the same `OpenDatabase` seam and the same versioned migrations, with the envelope encryption
hook installed so the controller decrypts rows a different process wrote. It asserts per-device
dispatch payload contents field by field against the seeded identifiers, reads the job back out of
PostgreSQL, checks properties are ciphertext at rest with a raw query, and holds tallies at 2/1/0 so a
bug reporting one number for all three cannot pass. Every wait names a signal; there are no sleeps.

**The adversarial evidence, which is the part worth trusting.** Disabling the inventory group predicate
produced exactly the designed failure (`dispatched=4`, untargeted devices named); the old single-group
count-only test would have passed that broken code. The zero-trust assertion needed **three**
independent layers broken before an unauthenticated dispatch got through: `AuthMiddleware`,
`RequireScope`'s own identity check, and `DispatchRunbook`'s own. That is real defense in depth and is
recorded as `LESSONS_LEARNED.md` #95.

**Verified green.** `make test-integration` passes clean end to end under `-race` with `-count=1`,
confirmed on repeated runs: every package `ok`, zero failures, with `tests/e2e` at roughly 72 seconds
including the chaos suite. `build`, `vet` (both tag passes), `fmt`, `test-race`, `coverage` (99
packages, none below floor), `gosec` (one finding, individually waived), `govulncheck` (none),
`docs-lint` and `docs-gen-check` are all green. The two-adapter conformance suite passes against both
SQLite and real PostgreSQL, and the migration parity test passes on both dialects. Phase 18's checklist
is fully closed, 16 of 16.

**The supporting gates are real, not deferred.** Fuzzing: `internal/ent.FuzzResolveDSN` (roughly 879,000
executions clean) plus `pkg/wire`'s first two fuzz targets ever. Benchmark: accept-to-completion across
the whole mesh measures roughly **37 ms** against roughly **650 ms** for a real `ansible-playbook` run
over the same host count, measured as a sibling on identical hardware in the same run, with the "these
do not measure the same work" caveat written into the benchmark's own doc comment rather than buried.
Chaos: a real Toxiproxy fronting both containers, cutting each boundary in turn. The PostgreSQL half is
genuinely new coverage, since nothing else in this repository cuts a database connection, and it proves
the property that matters: with the database severed a launch is refused with 5xx, rather than accepted
with a 202 the system could never honor.

**Deployment honesty.** `docker-compose.yml`'s `DB_DSN` is read for the first time, and the compose
controller's three missing startup requirements (`MASTER_ENCRYPTION_KEY`, `JWT_SECRET`, `RUNBOOK_DIR`)
are fixed, along with the same `RUNBOOK_DIR` gap in both Dockerfiles. **Compose still cannot come up
cleanly**: its NATS healthcheck invokes a binary the image does not contain, which is Phase 20's item
and is not claimed as fixed. `FAILURE_PATTERNS.md` #93 records the whole finding.

**Next step.** Phase 18 is closed and nothing is committed; the six drafted commit messages live in
Phase 18's own checklist. `AWX_PARITY.md` gates Phase 21 on Phase 18 being green, so Phase 21 (The
`Launchable` Abstraction) is now unblocked. Two things this phase deliberately did not fix, both owned
elsewhere: the compose NATS healthcheck (Phase 20), and the fact that `cmd/runner` still composes only
`native.Adapter`, so nothing routes a dispatch to the legacy Ansible adapter (Phase 21's Kind registry).

**Files changed this session:** `internal/ent/{open,open_sqlite,open_postgres,embedded}.go` plus
`open_internal_test.go`, `open_fuzz_test.go`, `conformance_test.go`, `conformance_backends_test.go`,
`parity_integration_test.go`; `internal/ent/migrate/{apply.go,apply_test.go,parity_test.go}` and
`migrate/gen/main.go`; `internal/ent/migrate/migrations/postgres/0001_initial.sql` (new, generated);
`cmd/controller/main.go` (+`config_test.go`); `internal/auth/authtest/issuer.go`;
`internal/archtest/layering_test.go`; `pkg/wire/dispatch_fuzz_test.go` (new); all of `tests/e2e/`
(`harness_test.go`, `harness_seed_test.go`, `integration_test.go`, `integration_assert_test.go`,
`integration_bench_test.go`, `integration_chaos_test.go`, `racebudget_test.go`,
`racebudget_race_test.go`); `Makefile`; `docker-compose.yml`;
`Dockerfile.controller`; `Dockerfile.runner`; `docs/02-get-started.md`;
`docs/09-control-plane-and-api.md`; `changelog/postgres-backend.added.md` (new); plus the gitignored
`.SPECIFICATION/IMPLEMENTATION.md` and the `FAILURE_PATTERNS`/`LESSONS_LEARNED` index and archive pairs.

---

Full session-by-session history (every `## Previous session: ...` and `## Files changed in the ... session` entry) lives in [`HANDOFF_ARCHIVE.md`](HANDOFF_ARCHIVE.md), kept out of this file so it stays cheap to read every session. Read the archive only when you need a specific past session's detail.

When Current Status above is superseded, move the outgoing text into `HANDOFF_ARCHIVE.md` as a new `## Previous session: ...` entry at the top of that file (before its current first entry), then overwrite Current Status here. Never delete a past entry.
