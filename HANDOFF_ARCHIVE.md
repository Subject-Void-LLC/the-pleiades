# Handoff Document Archive

## Previous session: AWX_PARITY_ROADMAP.md Section 3b.1 (launch fields reach execution)

**Branch `feature/Brutalist-UI-Scaffold`. Directive: close the rest of
`.SPECIFICATION/AWX_PARITY_ROADMAP.md` Section 3b.1 ("launch fields never reached execution"), the
two remaining hops after the prior session captured `Resolved.Fields`/`ExtraVars` onto the job
record. Everything below is uncommitted, held per standing instruction (a commit message is
prepared but no commit was made).**

**Section 3b.1 is now CLOSED.** Both remaining hops built and tested against real infrastructure,
no shortcuts. Full file:line detail lives in `.SPECIFICATION/AWX_PARITY_ROADMAP.md` Section 3b.1's
own "Status: CLOSED" writeup; the summary:

1. **The wire.** `pkg/wire.DispatchPayload` gained `Fields`/`ExtraVars map[string]any` (additive,
   `omitempty`, following the `Kind`/`Interruptible` precedent), and
   `internal/dispatch/worker_devices.go`'s payload build site now reads `job.Fields`/`job.ExtraVars`
   onto it.
2. **The legacy adapter.** `internal/adapters/legacy/argv.go` (new) replaces the hardcoded
   `ansible-playbook -v -i ...` literal with real `--limit`/`--forks`/`--tags`/`--skip-tags`/`-e`
   construction and per-run `timeout` (via a context deadline around the container run, since
   `ansible-playbook` has no run-timeout flag of its own). Proven against a **real Docker
   container**: `cmd/runner/ansible_release_gate_test.go`'s new
   `TestAnsibleReleaseGate_LaunchFieldsReachRealInvocation` dispatches with `forks: 1` and
   `job_tags: [deploy]`, asserts the exact argv the real Docker daemon started the real container
   with (a new `observingOrchestrator` test double wraps the real `DockerOrchestrator`, changing
   nothing about what executes), and asserts behaviorally that `--tags` really excluded an untagged
   task from a real two-task play.
3. **The native adapter.** `engine.Executor` did **not** already have any variable-override or
   per-task-timeout plumbing (confirmed via `gopls references` on its two production call sites, not
   assumed) — this was new engine work, not just adapter work. `ExtraVars` now reaches a runbook's
   `when_cel` conditions through a new `"vars"` CEL root (`engine.WithVariables`); `timeout` is now
   a genuine **per-task** abort (`engine.WithTaskTimeout`, wrapping each device's own `ctx` inside
   `runOne`, not the whole `Run` call) — the runbook kind's FieldSpec text says "per task", the
   opposite of the playbook kind's "per run" reading of the same field name, and both adapters now
   honor their own kind's stated semantics correctly. **`forks`/`limit` are deliberately NOT wired**
   for the native adapter: `singleDeviceResolver` always resolves every task to the one device this
   Runner invocation already got dispatched, and every device-targeting task takes an exclusive
   per-device lock before running, so there is no concurrency dimension within one `Execute` call for
   `forks` to bound — wiring it into `maxConcurrency` anyway would have been a real parameter set to
   a real value with a provably zero effect, forever. This is `LESSONS_LEARNED.md` #106, found and
   documented, not shipped as a bug.

**`make ci` is green, verified more rigorously than a single pass, because the first attempts
weren't clean and each failure needed to be run down rather than dismissed.** `go build`, `go vet`,
`gofmt`, `gosec` (8 pre-existing waived findings, none new), `govulncheck` (0), `coverage-check`
(146 packages measured, none below floor), `docs-lint`, `docs-gen-check`, and `templ-gen-check` all
passed cleanly on the first try. `test-race` and `test-integration` (`go test -race ./...` and
`go test -tags integration -race ./...`, the whole module) each flaked inside `make ci` itself, and
each flake was run down individually rather than assumed benign, per standing instruction:

- `test-race` failed twice, on `TestCLI_RunExecutesSSHTransport` (`cmd/pleiades`) and
  `TestAgent_FailedExecutionEventuallyDeadLetters`/`TestAgent_ReleaseGate_
  PullsFiveDispatchesWithoutDuplicating` (`internal/runner`) across the two attempts — all Docker
  port-mapping races (`port "X/tcp" not found`), none in a package this session touched. All three
  passed cleanly in an isolated serial rerun. A full, uninterrupted `go test -race -timeout 20m ./...`
  run (not stopped at the first failing package the way `make`'s own chained targets are) then
  completed with exit 0 and zero `FAIL` lines across the entire module.
- `test-integration` failed three times across three attempts, on three **different** tests each
  time: `TestGrandIntegration_EachKindReachesItsOwnAdapter` (`tests/e2e`, 913s before failing, "job's
  log stream never mentioned 'native execution'", with the runner subprocess's own log showing a
  redelivery/device-lock-contention loop), then a clean pass, then
  `TestControllerLeaderElection_ReleaseGate` (`cmd/controller`, "SPLIT BRAIN DETECTED"). Different
  package losing the race each run is `FAILURE_PATTERNS.md` #61's own named signature for resource
  contention under this sandboxed environment's full parallel `-race` load, not a code defect, and
  that entry names `TestGrandIntegration` specifically as a repeat offender. The first failure was
  serious enough (a trivial single-`noop`-task runbook hanging) to verify past what the standing
  instruction technically requires: `git stash -u` reverted every uncommitted change from this
  session, the identical isolated test command was run against that clean baseline (pass, 12.91s),
  the stash was restored (`git stash pop`), and the identical command was run again against this
  session's own code (pass, 14.19s) — proving the hang was not reachable from this session's diff at
  all. Both later flakes (a clean full run, then the unrelated leader-election split-brain) were each
  reconfirmed passing in isolation the same way. No fix was needed or made for any of these; they are
  documented here because "make ci is green" should mean something more specific than "it printed
  PASS eventually."

**Next step.** Nothing blocking. Section 3b.2 (a job's "completed" state describing fan-out, not
execution) is next in the roadmap's own dependency order, and is explicitly a separate,
design-then-build phase (PLAN.md Sections 16-17 first) — do not start it assuming this session's
work belongs to the same commit. Tranche B's own remaining scope (B2's expandable row summary, the
real multi-badge activity strip) is still open and was not touched this session either.

**Files changed this session:** `pkg/wire/{dispatch.go,dispatch_test.go}` (Fields/ExtraVars),
`internal/dispatch/{worker_devices.go,worker_test.go}` (payload build site + tests),
`internal/adapters/legacy/{adapter.go,argv.go (new),argv_test.go (new),adapter_test.go}` (argv
construction, run timeout, tests), `internal/engine/{executor.go,cel.go,executor_variables_test.go
(new)}` (`ExecutorOption`, `WithVariables`, `WithTaskTimeout`, `"vars"` CEL root), `internal/adapters/
native/{adapter.go,fields.go (new),fields_test.go (new),adapter_test.go}` (ExtraVars/timeout wiring,
forks/limit doc comment, tests), `cmd/runner/ansible_release_gate_test.go` (`observingOrchestrator`,
new real-container test), `.SPECIFICATION/AWX_PARITY_ROADMAP.md` (Section 3b.1 closed),
`LESSONS_LEARNED`/`LESSONS_LEARNED_ARCHIVE` (#106).

---

Full session-by-session history (every `## Previous session: ...` and `## Files changed in the ... session` entry) lives in [`HANDOFF_ARCHIVE.md`](HANDOFF_ARCHIVE.md), kept out of this file so it stays cheap to read every session. Read the archive only when you need a specific past session's detail.

When Current Status above is superseded, move the outgoing text into `HANDOFF_ARCHIVE.md` as a new `## Previous session: ...` entry at the top of that file (before its current first entry), then overwrite Current Status here. Never delete a past entry.

---

Full session-by-session history for `HANDOFF_DOCUMENT.md`, most recent superseded session first. This file is not required reading; `HANDOFF_DOCUMENT.md`'s Current Status section is what "read the handoff document" means day to day. Open an entry here only when you need a specific past session's detail. See `.AGENTS/AGENTS.md`'s Mandatory Documentation Rules for how to append here.

---

## Previous session: B1/B2/B3 shipped, Section 3b diagnosed, first wire hop closed

**Branch `feature/Brutalist-UI-Scaffold`. Directive: get the front end to visual/structural
completion first, then build the APIs behind it, and update the plan documents so nothing found
along the way is lost. Everything below is uncommitted, held per standing instruction.**

**Front end: B1, B2 and B3 of `.SPECIFICATION/AWX_PARITY_ROADMAP.md`'s Tranche B, all built and
tested, no shortcuts.** B1 (typed execution fields with per-field prompt checkboxes, replacing the
old union multi-select) needed a real framework addition: `view.Descriptor.FieldsFor` and
`Descriptor.ResolveFormFields`, threaded through `internal/ui/web/resources.go`'s render and bind
paths, mirroring `RecordAction.FieldsFor`'s existing per-record pattern. New file
`internal/ui/resources/templates/defaults.go`. Also renamed the `tags` launch field to `job_tags`
(roadmap Section 1.2's prep step, needed for a lossless AWX import later) and fixed a real,
independently-found bug while in the code: `allow_simultaneous`'s edit-form prefill used `yesNo()`
("yes"/"no") where the checkbox template only renders `checked` for the literal string `"true"`, so
a `true`-valued template silently flipped to `false` on an untouched save (`FAILURE_PATTERNS.md`
#115). B2 (Activity + Last Ran columns) needed a new `dispatch.JobStore.RecentForTemplates`, batching
`ListForTemplate` across a whole list page in one query rather than one per row, since
`Projector[T].Row` has no per-page context to draw on; the Activity badge reads `FailedCount` rather
than trusting `State` alone (see the severed-link finding below). B3 (Labels) registered as the
eighth declared view, same shape as the other seven. Verified by the pre-existing
`editform_conformance_test.go` plus new tests: `internal/ui/resources/templates_defaults_test.go`
(4 tests), `internal/dispatch/worker_targeting_test.go`'s
`TestJobStore_RecentForTemplatesBatchesAcrossManyTemplates`.

**Backend: found two severed links reading the dispatch path end to end, closed the first one's
first hop.** `.SPECIFICATION/AWX_PARITY_ROADMAP.md` Section 3b has the full writeup with file:line
evidence for both; `FAILURE_PATTERNS.md` #116-117 and `LESSONS_LEARNED.md` #105 record them as
findings. In short: `launch.Template.Resolve` has always correctly computed `Resolved.Fields` and
`Resolved.ExtraVars`, and `internal/api/dispatcher.go`'s `LaunchTemplate` read them out of `resolved`
and never referenced them again — every execution field B1's new UI lets an author set was inert.
Closed this session's first hop: `dispatch.Job` gained `Fields`/`ExtraVars` columns (ent schema +
migrations `sqlite/0011` and `postgres/0008`), and `LaunchTemplate` now stamps them, tested end to
end against a real store. **Still open and NOT attempted**: the wire (`pkg/wire.DispatchPayload` has
no field for this yet) and both adapters (`internal/adapters/legacy/adapter.go`'s argv is still
hardcoded — no `--limit`/`--tags`/`--forks`/etc; the native adapter's extra-vars injection point was
not audited). Separately, confirmed but not touched: a job's `state`/tallies describe fan-out
publish outcomes, not per-device execution outcomes, and the Runner already reliably publishes real
per-device results (`internal/runner/wal.go`'s `ResultEntry`, via `topology.ResultSubject`) that
nothing on the Controller side has ever subscribed to — `ResultWAL`'s own doc comment says as much.
Both were sized and left for a dedicated design-then-build pass rather than rushed: they cross a wire
contract with a literal shape assertion and a state-machine design question (what happens if a Runner
never reports back), and attempting either under the time remaining in an already-long session was
judged the likeliest way to reproduce the exact "passed its own tests, still wrong" pattern this
project has been burned by three times.

**Next step.** Run `make ci` (serially; do not run it concurrently with further edits,
`FAILURE_PATTERNS.md` #104). Then the commit message. After that, in the order
`.SPECIFICATION/AWX_PARITY_ROADMAP.md` Section 3b lays out: the wire extension is the smallest next
piece, then the legacy adapter's argv (highest value, since its whole configuration surface is a
command line), then the native adapter, then read PLAN.md Sections 16-17 before starting the
Controller-side result subscriber. B2's own remaining scope (the expandable row summary; a real
multi-badge activity strip, which needs `internal/ui/render/views.templ`'s list-cell rendering
extended to support more than one badge per cell) is written up at the end of Section 3b's session
update, not silently dropped.

**Files changed this session:** `internal/ui/view/{view.go,pagemodels.go}` (FieldsFor seam),
`internal/ui/web/resources.go` (threaded through render/bind), `internal/launch/kinds/playbook/
playbook.go` (+tests) (tags→job_tags), `internal/ui/resources/templates/{templates.go,defaults.go
(new)}`, `internal/ui/resources/{templates_defaults_test.go (new),harness_test.go}`,
`internal/dispatch/{job.go,ent_store.go,ent_store_test.go,worker_targeting_test.go}`
(RecentForTemplates), `internal/ui/resources/labels/labels.go` (new) + `registrars.go`,
`internal/launch/template.go` (RecentJobs/JobSummary), `internal/ent/schema/job.go` +
regenerated `internal/ent/*` + `internal/ent/migrate/migrations/{sqlite/0011,postgres/0008}`
(Job.Fields/ExtraVars), `internal/api/dispatcher.go` (+`dispatcher_template_test.go`) (stamps them),
`tests/parity/fields_job_template.go` + regenerated `GAPS.md`, `.SPECIFICATION/AWX_PARITY_ROADMAP.md`
(Section 3b, new), `FAILURE_PATTERNS`/`FAILURE_PATTERNS_ARCHIVE` (#115-117),
`LESSONS_LEARNED`/`LESSONS_LEARNED_ARCHIVE` (#105).

---

## Previous session: Phase 21 shipped, the template rebuild, and the Immutable seam

**This session ran long and did three things on `feature/The-Grand-Integration-Test`: shipped and
committed Phase 21 plus the Phase 19 UI surface (commit `5dbc35b`), built the Contacts view and the
Immutable form-field seam (uncommitted), and then, on the user's audit, tore out and rebuilt the
template authoring path because the shipped version was functionally useless (uncommitted).**

**Part 1, committed as `5dbc35b`.** Templates, the open kind registry, surveys with encrypted password
answers, saved launch configs with relaunch, one launch surface (`POST /templates/{id}/launch`;
`/jobs/dispatch` removed), tenancy derived from the inventory, the server-rendered UI resource layer,
organizations/teams/users/grants, the activity stream, per-object Access sections.

**Part 2, the Contacts view.** `internal/ui/resources/contacts/` over the pre-existing `access.Contacts`
port, plus Contacts sections on Organization and Team detail pages. The owner is one select over both
owner types (`organization:3` / `team:5` values), set once. That "set once" needed a framework seam:
`view.Field.Immutable` (in the create form, absent from the edit form, refused if smuggled to an
update), which also fixed five already-shipped controls an edit silently ignored (a team's and an
inventory's organization; a template's kind, definition and inventory). `FAILURE_PATTERNS.md` #111.
Also: contact owners render as names; the unnarrowed contact listing's sort now matches its keyset
cursor; `internal/dispatch`'s coverage flake was a timer-driven test, fixed by driving `Reaper.sweep`
directly (`LESSONS_LEARNED.md` #103).

**Part 3, the rebuild, and read FAILURE_PATTERNS.md #112 before touching any of this.** The audit
found: the template form asked for a free-text "runbook id or playbook path"; existence was checked
nowhere (a bogus definition 201'd, 202'd, then died at fan-out as a failed job); and the playbook kind
was a facade behind four independent walls (no enumeration anywhere, the worker resolved every job
through the runbook source, no binary composed `routing.Router` or the legacy adapter, and the
template-side path grammar was disjoint from the resolver's id grammar, so nothing savable could ever
run). `docs/01` and FAILURE_PATTERNS #109 claimed wiring that did not exist; #109 now carries a dated
correction and the claim is finally true. What landed: `internal/playbook` (DirSource with Get+List,
exported `ValidID`); both kind validators delegate to their source package's grammar;
`launch.Catalog` wired from the real sources, consumed by the store at create AND the Templates form's
RUNS picker (one select over both kinds, `kind:definition` values, kind badge derived);
per-kind `dispatch.DefinitionSource` at fan-out; `PLAYBOOK_DIR` in both binaries; `cmd/runner`
composes `routing.Router` over native+legacy, fail-open, refusing a half-set `PLAYBOOK_DIR`/
`ANSIBLE_RUNNER_IMAGE` pair. The runner binary now genuinely links testcontainers (#109's accepted
trade, real at last).

**The gate:** `TestGrandIntegration_EachKindReachesItsOwnAdapter` (tests/e2e, `withAnsible()` harness
option) launches one runbook and one playbook template through the production binaries and asserts
each job's log stream carries its own engine's output and not the other's. Green. The playbook
fixture is `connection: local` deliberately; real SSH-over-network execution stays the job of
cmd/runner's ansible release gate, whose image builder moved to
`testsupport.BuildAnsibleRunnerImage`.

**Next step.** Serial `make ci` was running at handoff (Docker-infra flakes under parallel load are
the known noise; every affected package passes serially). Then the commit message for parts 2 and 3.
Deferred with owners: compose NATS healthcheck (Phase 20), credential binding (Phase 22), replacing
the testcontainers orchestrator (Phase 20/22), organization visibility (unowned, phase-sized: nothing
narrows reads by the caller's tenant today).

**Files changed this session (uncommitted parts):** `internal/playbook/` (new),
`internal/launch/{catalog.go,catalog_test.go,launch.go,ent_store.go,kinds/*}`,
`internal/dispatch/{definition_source.go,worker.go,worker_config.go,worker_devices.go,
worker_kinds_test.go,reaper_sweep_test.go,export_test.go}`, `internal/adapters/{legacy/playbook_source.go,
routing/router.go}`, `cmd/{controller,runner}/main.go`, `internal/ui/resources/{contacts/,templates/,
teams/,organizations/,inventories/,registrars.go,builtins.go}` plus their tests,
`internal/ui/{view,web}/` (Immutable seam), `internal/access/{contact.go,ent_contact_store.go}`,
`internal/api/{access_contacts.go,templates.go}`, `internal/testsupport/ansible_image.go` (new),
`tests/e2e/{harness_test.go,harness_seed_test.go,integration_adapters_test.go}`, `docs/{01,09,12}`,
`changelog/` (three new fragments), `coverage-floor.json`, the `FAILURE_PATTERNS`/`LESSONS_LEARNED`
pairs (#111, #112, #103, #109 correction), and the gitignored `.SPECIFICATION/{IMPLEMENTATION,
AWX_PARITY}.md`.

---

## Previous session: Phase 18, the Grand Integration Test

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

## Previous session: Phase 17, the Legacy Ansible Adapter

**This session built Phase 17 (Legacy Ansible Adapter) in full**, the next unbuilt phase after Phase 16
(Native Go Execution Adapter, landed on `main` at the start of this session). Branch is still `main`.
**Nothing is committed** (the user asked for a plan, approved it, and the session proceeded to
implement; committing was never requested). Real Go code changed, real tests pass, real containers ran.

**What's real.** `internal/adapters/legacy` (renamed and rebuilt from the old `internal/ansible`, whose
`ReceptorAdapter.StreamMockJob` fabricated events from three hardcoded arrays): a real
`ContainerOrchestrator` port with one real `testcontainers-go`-backed Docker implementation; a real
STDOUT parser targeting Ansible's actual `ansible.builtin.default` text callback at `-v` (not the `json`
callback PLAN.md's own prose might suggest, which does not exist in any maintained Ansible -- verified
empirically, `FAILURE_PATTERNS.md` #90); a real `inventory.json` generator targeting Ansible's actual
"yaml" inventory plugin schema (also verified empirically, not the dynamic-inventory-script shape that
turned out not to parse); a real playbook resolver; and `legacy.Adapter`, which ties all of it together
and genuinely implements `runner.ExecutionAdapter`, making PATTERNS.md's Strangler Fig claim true for the
first time (it was false from Phase 2 through Phase 16). The Release Gate
(`cmd/runner/ansible_release_gate_test.go`) dispatches a real job through a real NATS broker to a real
`runner.Agent` holding a real `legacy.Adapter`, which provisions a real container (built from the new,
committed `Dockerfile.legacy-ansible-runner`) on a shared Docker network with a second ephemeral `sshd`
container, runs a real playbook over a real SSH connection, and asserts the real parsed `wire.JobEvent`s
-- including a genuine wrong-password negative control. `internal/adapters/legacy` measures 90.3%
coverage. `make build vet fmt`, `go test -race` (including the release gate), `make gosec govulncheck
docs-lint docs-gen-check` all pass clean, no new findings.

**What's explicitly not real, so the next session does not assume otherwise.** `cmd/runner/main.go`
still only composes `native.Adapter` -- nothing routes a real dispatch to `legacy.Adapter` yet, since no
Phase 21 Launchable Kind registry exists to choose per job. One device per dispatch, never a whole play's
host list (Phase 24's own open problem). Events are a post-hoc batch parse, not Phase 25's future live
stream. No GitOps auto-discovery, no Galaxy/pip dependency caching, no Kubernetes container groups (Phase
26). Host key verification is disabled inside the container. A passphrase-protected SSH key is rejected,
not handled. The full list, with reasoning, is Phase 17's own closing note in
`.SPECIFICATION/IMPLEMENTATION.md`.

**Real wire-format change:** `pkg/wire.DispatchPayload` gained a `Tags []string` field (populated at
`internal/dispatch/worker_devices.go`'s one real construction site), and `wire.JobEvent`'s doc comment
was updated now that the `AnsibleEvent` struct it names is actually gone -- folded in this session,
closing an item Phase 16 deliberately deferred.

**Documentation updated for real:** `docs/03-migrating-from-ansible.md` (new "Running an unconverted
playbook" section, honestly noting no CLI/API path selects it yet), `docs/01-start-here.md`
(Implementation status narrative and tier table, correcting the prior implication that all Ansible
interop belongs to the unbuilt Run tier), a changelog fragment
(`changelog/legacy-ansible-adapter.added.md`). `go generate ./... && git diff --exit-code` is clean:
this phase adds no Collection method, CLI flag, API route, capability, device type, or sync plugin.

**Gitignored spec docs updated too** (real work, not committable): `.SPECIFICATION/IMPLEMENTATION.md`
(Phase 17's own checklist checked off with full closing notes, and a stale Phase 39 cross-reference that
had misattributed the `ansible-playbook` command-injection standing requirement to "Phase 25 alone"
corrected to name Phase 17), `.SPECIFICATION/PATTERNS.md` (Adapter, Strangler Fig, Anti-Corruption
Layer, Bulkhead, and Feature Flag entries all corrected to match the real shape built), two new
`FAILURE_PATTERNS.md`/`LESSONS_LEARNED.md` entries (#90 and #93) recording the empirical discovery that
PLAN.md's own Ansible-integration prose described tool behavior that no longer matches a real, current
`ansible-core` install.

**Next step.** Nothing wires `legacy.Adapter` into a real composition root yet. The two most natural next
phases are Phase 21 (Launchable Kind registry, needed before any real per-job adapter routing can exist)
or Phase 25 (The Ansible Callback Bridge, which replaces this phase's batch parse with real live
streaming). Neither is started.

**Files changed this session:** `internal/adapters/legacy/*` (new package, ~15 files, replacing
`internal/ansible`, deleted), `pkg/wire/dispatch.go`, `pkg/wire/job_event.go`,
`internal/dispatch/worker_devices.go` (+test), `internal/dispatch/worker_test.go`, `cmd/demo/main.go`,
`cmd/runner/main.go` (comment only), `cmd/runner/ansible_release_gate_test.go` (new),
`internal/archtest/layering_test.go`, `Dockerfile.legacy-ansible-runner` (new),
`docs/03-migrating-from-ansible.md`, `docs/01-start-here.md`, `changelog/legacy-ansible-adapter.added.md`
(new), plus the gitignored spec files listed above.

---

## Previous session: Phase 72 split into Phase 72/75/76/77

**This session split Phase 72 in `.SPECIFICATION/IMPLEMENTATION.md`, the question the previous session's
own handoff note ended on.** Branch is still `feature/The-Transport-Layer`. **Nothing is committed.** No
Go code changed; the work is specification only, and `.SPECIFICATION/` is gitignored, so this file and
`LESSONS_LEARNED.md` (unchanged this session) are the only trace of it in `git status`.

**Why:** the previous session's own note flagged it: "Phase 72 is now large enough to question. At 54
items it spans the breaker consolidation, the `Target` hop chain, three WinRM execution modes, SFTP,
`internal/psdiag`, the CI matrix, and `windows.Server`'s missing accessors." A single Release Gate
covering four independently-shippable concerns cannot close until all four do, which meant SFTP and
`internal/psdiag`'s fixture work sat blocked behind a WinRM library defect neither one has anything to do
with.

**What changed.** Phase 72 kept its number and narrowed to the shared foundations: the circuit breaker
extraction, `retry.Do`, the `transport.Target` hop chain, and the CI matrix. Three new phases took the
next free numbers after Phase 74, following the exact rule Part IX's own preamble already states for
Phase 70 and Phase 71, and the same rule the previous session already applied once to place Phase 72
through Phase 74 themselves: nothing already numbered is renumbered, new work takes the next free number
and is placed thematically.

1. **Phase 75, WinRM, the Three Execution Modes.** The three typed execution modes, the WinRM adapter,
   `windows.Server`'s missing accessors, and `WindowsShellCapable`.
2. **Phase 76, `internal/psdiag`, the Blocked-Script Diagnosis.** The classifier itself and its checked-in
   fixture transcripts. Its package has no code dependency on Phase 75 (it must never import
   `internal/transport/winrm`), but its Release Gate does, since proving the classifier needs a real WinRM
   PowerShell session to test a blocked script against. That is why it is numbered after Phase 75 rather
   than built in parallel with it.
3. **Phase 77, SFTP/SCP.** SFTP behind its own narrow interface and `linux.Server`'s `FileTransferRoot`
   accessor. Depends only on Phase 72; nothing stops it being built alongside Phase 75 or Phase 76.

Every one of the original 54 checklist items kept its exact wording and moved to exactly one of the four
phases. The five phase-closing items that spanned all four concerns in one paragraph each (Fuzz/Stress
Test, Adversarial Pattern Justification, Schema/Injection Hardening, Documentation Gate, Release Gate and
Coverage Assurance) were decomposed clause by clause into each phase's own version, using the phase's own
wording throughout and dropping only the connective text needed to make each stand alone; nothing in them
was invented. Physically, Phase 75 through Phase 77 sit between the narrowed Phase 72 and Phase 73 in the
document, not after Phase 74, matching the Part's own thematic grouping ("extends what already works")
over strict numeric order, the same latitude Part IX already takes with Phase 70 and Phase 71.

**Cross-references fixed.** Every "Phase 72" mention inside Phase 73 and Phase 74 that pointed at WinRM or
SFTP now points at Phase 75 or Phase 77; mentions of the breaker, `retry.Do`, the hop chain, or the CI
matrix still correctly point at Phase 72. The Phase 34 correction note (`IMPLEMENTATION.md:4477`) was
updated the same way. Part XV's own preamble gained a paragraph explaining the second split and its
dependency ordering, in the same style as its existing paragraph explaining why Phase 72 through Phase 74
were not inserted at Phase 35.

**Not done, and not needed:** no numbers were renumbered, no other Part's cross-references were touched
(a grep of every `.SPECIFICATION/*.md` file for "Phase 72" outside `IMPLEMENTATION.md` found none before
this session started), and no checklist item's substance changed, only its location and, for the five
composite items, its grouping.

**Next step.** Nothing in Part XV is built. Phase 72 (foundations) is still the entry point and its first
item is still the Pattern Entry Gate. The two things worth settling before writing code are unchanged from
before: where `retry.Do` lands (Phase 72 already resolves this in favor of `pkg/retry`), and whether
`transport.Target`'s non-network-endpoint field is declared in Phase 72 as explicitly unproven or left
entirely to Phase 73.

**Files changed this session:** `.SPECIFICATION/IMPLEMENTATION.md` (gitignored; Phase 72 narrowed, Phase
75 through Phase 77 added, cross-references in Phase 73, Phase 74, and the Phase 34 correction updated),
this file.

## Previous session: Part XV added to IMPLEMENTATION.md

**That session added `Part XV: The Transport Layer` to `.SPECIFICATION/IMPLEMENTATION.md`: three new
phases, 72 through 74, specifying every device transport the platform still owes itself.** No Go code
changed; the work was specification only.

**The question that started it: "we only have SSH transport built right now, right?"** Yes.
`internal/transport/ssh` is the only implementation of the `transport.Transport` port, and
`cmd/pleiades/run.go`'s `bindings` map has exactly one entry. Measured against that one transport,
`internal/catalog` registers 75 Collection methods: 59 declare `"ssh"`, 7 declare `"winrm"`, 4 declare
`"https"`, and 5 carry an empty slice. Only the 4 `"https"` methods (`net.catalyst.*`) are
`StatusImplemented`, and they bypass the transport port entirely through `pkg/catalystcenter`'s own
`net/http` client. **Seven declared methods name a transport that has never existed.**

**Read this first if you are picking up mid-stream: the plan this session started with was wrong, and
verifying it against the document is what caught it.** The original plan was to insert the transport
work at Phase 35 and renumber every later phase up by three, roughly 220 cross-references. Part IX's own
preamble (`IMPLEMENTATION.md:4846-4860`) states the opposite policy in plain words: new work takes the
next free number and is placed thematically, and nothing is renumbered. Phases 70 and 71 already follow
it, sitting physically between Phase 41 and Phase 42. The renumber would also have split Part VII, "The
Forge of Hephaestus," which runs unbroken from Phase 30 to Phase 38. Recorded as `LESSONS_LEARNED.md`
#88. The plan's arithmetic was wrong too: it assumed the highest phase was 70, and it is 71.

**What Part XV contains.** The split is by port shape, not by protocol popularity, because
`transport.Transport.Exec(ctx, target, cred, command string)` is command-oriented and about half of what
this Part owes does not fit it. `pkg/catalystcenter/client.go:1-22` already recorded that finding once,
for REST.

1. **Phase 72, Transport Foundation.** `transport.Target` gains a hop chain, so bastion and jump-host
   reachability is a property of the port rather than of each protocol. WinRM (fits the existing port
   unchanged, and is what finally makes `windows.Server` and the 7 `"winrm"` stubs reachable). SFTP/SCP
   behind `FileTransferCapable`, with its own narrow interface rather than `Exec`. Finishes the
   resilience consolidation: `pkg/retry.Backoff` already has five production callers, so what is left is
   the retry *loop* and the circuit breaker, the latter still private at
   `internal/transport/ssh/circuit_breaker.go:49`. Adds the CI matrix the rest of the Part inherits.
2. **Phase 73, Serial, the Bastion Proof, Container Exec and TFTP.** Completes `Target`'s
   non-network-endpoint half with real consumers. Local serial, serial over TCP (raw passthrough and
   RFC 2217 Telnet Com Port Control), Telnet as a byproduct, Docker exec, TFTP, and `DockerCapable`'s
   missing device type. Owns the full bastion proof: multi-container, two-hop, hostile-bastion redirect,
   against a real console-server-behind-jump-host topology. Digi RealPort is explicitly rejected and the
   reason recorded, since it is an OS driver concern and not a wire protocol.
3. **Phase 74, NETCONF, RESTCONF and gNMI.** The three protocols that all fail `Exec(command string)`
   for the same reason. Adds bearer/token and mTLS credentials, the first non-SSH auth this platform
   stores, and the missing Junos and Arista device types.

**Part XV builds pipes and stops.** It wires none of Phase 34's generated stubs to anything. Every stub
that returns "not implemented" today still does when Phase 74 closes. A follow-on phase connects them,
and that phase is not yet written.

**Also this session:** a dated correction was added to Phase 34 (`IMPLEMENTATION.md:4477`) pointing its
placeholder transport strings at their new owning phases, and reassigning two gaps it had recorded as
"accepted, pre-existing": `DockerCapable`'s missing device type now belongs to Phase 73, and
`JunosCapable`/`AristaEOSCapable`'s to Phase 74.

**One finding from Phase 34 was promoted into Part XV because it is about to become a recurring hazard.**
Phase 34 discovered that `pkg/capability/capabilities_windows.go` had never compiled on Linux, because Go
reads a `_windows` filename suffix as an implicit GOOS build constraint. Part XV is the first Part to
compile on more than one GOOS and adds packages whose natural filenames sit directly on that trap
(`serial_linux.go`, `socket_windows.go`). Every phase in the Part forbids GOOS-suffixed filenames and
says why.

**Platform requirement, recorded because it shapes several decisions.** Pleiades must act on Windows and
macOS targets, a hard requirement. It must run on Linux and should run on macOS; running the controller
on Windows is a stretch goal that may be missed. Windows targets are well served by Phase 72's WinRM.
**macOS targets are a real gap that Part XV deliberately does not close:** a Mac is reached over SSH,
which already works, but `internal/inventory/devices/` has no darwin type and `pkg/capability` has no
`DarwinCapable`, `HomebrewCapable`, or `LaunchdCapable`. That is capability-vocabulary work with no
transport component and needs its own phase. The "act on macOS" requirement is not satisfied until it
lands.

**How the phases were written, since it affects how much to trust them.** Three parallel authoring
agents, then three adversarial fact-checkers instructed to refute, then a cross-phase consistency pass.
The review found 39 defects, including four that mattered: a fabricated claim that
`internal/catalog/http/request.go` omits `SupportedTransports` (it ships an empty slice), a WinRM
injection defense resting on a property `masterzen/winrm` does not have (it asks the remote service to
run the command through `cmd.exe`), a serial Release Gate that could not pass because a socat PTY pair
cannot prove baud rate is applied, and an RFC 8342 claim that does not hold (RFC 8342 defines
datastores only; RESTCONF got them retrofitted by RFC 8527, and gNMI never adopted them). All 39 were
applied, plus 16 cross-phase fixes. Every one of the 222 `file:line` citations in the finished Part was
then machine-verified to resolve to a real file and a real line, including the two into
`golang.org/x/crypto@v0.54.0`.

**Amended later in the same session, at the product owner's request: "we need to be able to run cmd and
powershell" and "alerting the user when powershell scripts are blocked in a nice friendly way."** Phase 72
grew from 36 checklist items to 54, and from 496 lines to 978. It had treated `cmd.exe` purely as a hazard
to bypass with a single global `WINRS_SKIP_CMD_SHELL` switch, which cannot survive an operator who
legitimately wants cmd builtins. It now names three typed execution modes (`ShellNone`, `ShellCmd`,
`ShellPowerShell`), pins the WS-Man option to `TRUE` in all three so exactly one parser ever sees the
bytes and it is always the one Pleiades chose, and reuses the existing `CommandExecCapable` while adding a
`WindowsShellCapable` sibling (`ShellExecCapable` was rejected because its whole contract is a single
`ShellPath()`, and Windows has two shells whose metacharacter sets are disjoint).

**The load-bearing fact behind the diagnostics work, verified against three primary sources because
getting it wrong would ship a confident and completely wrong error message: PowerShell Execution Policy
does not block anything Pleiades sends.** Microsoft's own `about_Execution_Policies` says it "isn't a
security system that restricts user actions"; `PSAuthorizationManager.ShouldRun` calls `CheckPolicy` in
exactly one branch, `case CommandTypes.ExternalScript`; and every blocking string in `Authenticode.resx`
is parameterized on a file path. Since Pleiades sends `-EncodedCommand` and never writes a `.ps1`,
Execution Policy is out of the loop, and an operator's first guess is therefore almost always wrong.
Exactly three mechanisms can actually refuse what Pleiades sends (Constrained Language Mode, a NoLanguage
runspace, and AMSI), plus impostors that must never be reported as blocks: WinRM quotas and an audit-mode
App Control policy.

**The classifier is a shared package, `internal/psdiag`, not part of the WinRM adapter**, with an archtest
rule forbidding it from importing `internal/transport`. The reason is architectural, not tidiness:
PowerShell blocking is a property of the Windows host, not of how the script arrived. It blocks
identically over WinRM, from a local subprocess on a Windows controller, or from an agent on the box.

**Two gaps were recorded rather than built, both raised by the product owner:**
1. **Controller-side execution.** If Pleiades runs on Windows, `cmd.exe` and `powershell.exe` are local
   subprocesses, not a transport, and `PLAN.md:806` explicitly refuses to model local as a connection
   ("No `connection: local`. Execution context is first-class"). But `collection.ExecutionContext` is one
   boolean, and no file under `internal/`, `pkg/`, or `cmd/` imports `os/exec` outside tests, which Phase
   W6 counts as a security property (`IMPLEMENTATION.md:909`). That work needs a phase that does not exist
   yet. Part XV deliberately does not absorb it.
2. **Agent on a managed box is undecided, and the specs disagree.** `PLAN.md:781` carries
   `| AgentCapable | agent | Agent RPC |`; `PATTERNS.md:684` says target devices "stay agentless" and
   places `runner.Agent` mesh-side. `AgentCapable` is in zero Go files. Phase 72 records the conflict and
   explicitly may not settle it.

**Phase 72 was flagged as too large at the end of this session.** At 54 items it spanned the breaker
consolidation, the `Target` hop chain, three WinRM execution modes, SFTP, `internal/psdiag`, the CI
matrix, and `windows.Server`'s missing accessors. **The following session split it**, into a narrowed
Phase 72 plus Phase 75 through Phase 77; see Current Status above.

**Files changed this session:** `.SPECIFICATION/IMPLEMENTATION.md` (gitignored; Part XV appended, Phase
34 correction inserted), `LESSONS_LEARNED.md` (#88), this file.

## Previous session: Phase 14, The Dispatcher

**Read this first if you are picking up mid-stream: one commonly-assumed deferred item is wrong, and the
real state is better than it, not worse.** It would be easy to assume `cmd/runner` is still an empty
directory with no `main`, because `.SPECIFICATION/IMPLEMENTATION.md`'s own Phase 15 checklist still says
exactly that. It is stale, and this session did not touch it: `cmd/runner/main.go` already existed,
already builds, and already wires a real `runner.Agent` pulling from the shared dispatch consumer group
against a real `native.Adapter`, predating this session entirely (this session's only edit to that file
is a one-line doc-comment fix so it still names `wire.DispatchPayload` correctly after the type moved).
More importantly, the far end of this phase's own fan-out is not merely message-count-proven: `tests/
e2e/integration_test.go`'s `TestGrandIntegration` was updated for the new async Job-launch shape and
passes end to end against real Postgres and NATS containers (`go test ./tests/e2e/... -run
TestGrandIntegration -v`, 10.6s this session), with the real `runner.Agent` (the identical code
`cmd/runner` wires) actually consuming both devices' `wire.DispatchPayload` messages and completing them,
confirmed by 6 real log events fetched off the bus, not a count asserted against a mock. What remains
genuinely unproven is narrower than "the Runner never ran": `native.Adapter.Execute` is still three
`time.Sleep` calls publishing a fabricated `pong` (Phase 16's own open item, "Nothing in this repository
has ever contacted a device"), so "picked up and executed" here means "picked up and simulated," never a
real SSH session to a real device.

**The honest headline, since this phase's own Adversarial gate asks for it:** before this session,
`DispatchRunbook`'s per-device loop read a device's management address from a property key, `"ip"`, that
no device type in this codebase has ever populated. Every real dispatch therefore silently skipped every
device while the endpoint still answered `200 {"dispatched":0,...}`, indistinguishable from a group that
legitimately needed no work. Nothing about that shape was inherited into the fan-out this session built:
`internal/dispatch.Worker` reads the real `"host"` key, and `pkg/wire.DispatchPayload` deletes the
`DeviceIP` field outright rather than leaving it reachable. That single key mismatch, plus a second bug
in the same duplicated type (`DeviceName` populated from `device.ID()`, not `device.Name()`), are why the
DTO's move into `pkg/wire` is treated as a real fix in this session's own work, not a mechanical rename.

**Three real defects found, all recorded in `FAILURE_PATTERNS.md` before being fixed:**

1. **#76, the one that mattered most.** The `"ip"`-vs-`"host"` property key mismatch described above.
   Because a missing property and a genuine publish failure both incremented the same undifferentiated
   `errCount`, the response could not tell a caller which one had actually happened either. Fixed by
   `internal/dispatch.Worker` reading `"host"` and recording a missing property as its own
   `OutcomeSkipped` task naming the property, distinct from `OutcomeFailed`.
2. **#77.** `DeviceName` was populated from `device.ID()`, not `device.Name()`, in the pre-Phase-14
   duplicated `DispatchPayload` type. Nothing failed a build or a type check, since both accessors return
   `string`; the defect was only visible by comparing what the field actually held against
   `InventoryItem.Name()`'s documented meaning. Fixed by giving `DeviceID` and `DeviceName` their own
   fields in `wire.DispatchPayload`, each filled from its own matching accessor, and a regression test
   (`TestWorker_HandleJobRequested_DispatchesHealthyDevice`) whose fixture device's id and name
   deliberately differ, so a fixture where they happened to coincide could not mask the bug.
3. **#78.** The runbook id a dispatch request carries was, before this session, inert: no runbook
   storage existed, so the string was never used for anything but a label. The moment `internal/runbook
   .DirSource` gave it a real filesystem lookup, the same caller-controlled string became an input to
   `filepath.Join` with nothing yet validating it. Fixed by `validRunbookID`
   (`^[A-Za-z0-9_-]{1,64}$`), applied before any path is constructed, plus a belt-and-suspenders
   absolute-path-prefix re-check. `TestDirSource_Get_RejectsHostileIDs` exercises `"../../etc/passwd"`,
   an absolute path, and other hostile ids against the real `Get`, not a mocked path-builder.

Also recorded, `LESSONS_LEARNED.md` #82: a wire field must be named for the property key it actually
reads, never for the value someone hopes is there, the architectural rule #76 and #77 both fall out of.

**What was built, by area:**

1. **`internal/api/dispatcher.go`** (rewritten). `DispatchRunbook` no longer streams a device group or
   publishes a per-device event itself. It resolves the requested runbook, persists a `dispatch.Job` row,
   publishes exactly one `job.requested` event, and answers `202 Accepted` with a `Location` header
   naming the new job resource. `NewDispatcher` no longer takes an `inventory.Repository` or an
   `auth.Evaluator`: neither is used by anything left in this handler once the per-device loop moved out.
2. **`internal/dispatch`** (new package). `Worker.HandleJobRequested` (`worker.go`) is the durable
   `job.requested` consumer and the actual fan-out: it claims a job via `BeginFanOut`'s WHERE-guarded
   conditional transition (Idempotent Consumer, safe under NATS at-least-once redelivery), streams the
   target group without materializing it, admits or skips each device via `engine.LifecycleAdmits`/
   `CapabilityAdmits`, and publishes one `wire.DispatchPayload` per admitted device. `job.go` defines the
   domain `Job`/`JobTask`/`Outcome` types and the `JobStore` port; `ent_store.go` is the one real,
   ent-backed implementation.
3. **`internal/ent/schema/job.go`, `job_task.go`** (new schemas, regenerated). `Job` carries a
   `state` enum (`pending`/`fanning_out`/`completed`/`failed`, `"failed"` added as its own terminal state
   rather than reusing `"completed"` with zero tallies, so a runbook that could not even be resolved is
   distinguishable from one that legitimately ran against an empty group) and the three terminal tallies.
   `JobTask` is one immutable row per device considered, with a `reason` field the schema's own comment
   marks as forbidden from ever carrying a device's decrypted `Properties()` value.
4. **`pkg/wire/dispatch.go`** (new package). `DispatchPayload` is now the one definition crossing the
   wire, replacing the two hand-synchronized duplicates in `internal/api` and `internal/runner`; see the
   defects above for the two real bugs the move fixed. `internal/runner/agent.go` and
   `internal/adapters/native/adapter.go` were updated to the shared type (mechanical changes only,
   neither package's own behavior was rebuilt this session).
5. **`internal/engine/admission.go`** (new). `LifecycleAdmits` and `CapabilityAdmits`, factored out of
   `executor.go`'s own inline device-admission checks so `internal/dispatch.Worker` (a different package,
   with no `WorldView`/DAG in hand) can ask the identical two questions, in the identical wording, without
   re-deriving either check. This is what closes the "Call `HasCapability` before dispatch" and lifecycle
   items below without inventing a third, dispatcher-local copy of either.
6. **`internal/runbook`** (new package). `DirSource` (`dir_source.go`) resolves a runbook id to a
   compiled `*engine.DAG` plus its required capabilities, reading real YAML off disk through
   `engine.Builder.BuildFromYAML` (the identical compiler `cmd/pleiades` uses, per RULE 0), with a
   Flyweight cache keyed on the source file's mtime. This is also where defect #78 above lives and is
   fixed.
7. **`internal/api/jobs.go`** (new). `GET /api/v1/jobs/{id}`, the client-facing read side of the new
   async shape: a launch returns `202` immediately, and a caller polls this resource for progress and the
   final per-device tallies. `JobRepository` is the narrow, read-only slice of `dispatch.JobStore` this
   handler needs, following `devices.go`'s own Interface Segregation precedent.
8. **`cmd/controller/main.go`** (wired). Builds `runbook.NewDirSource` (`RUNBOOK_DIR`, defaulting to
   `inventory.DefaultRunbookDir`) and `dispatch.NewEntJobStore` over the same already-open `ent.Client`,
   subscribes `dispatch.NewWorker(...).HandleJobRequested` to `topology.JobRequestedSubject()`, and
   registers both `POST /jobs/dispatch` and `GET /jobs/{id}` on the router, all fail-closed at startup
   the same way every other dependency in this file already is.
9. **`internal/topology/topology.go`**: `JobRequestedSubject()`, the one new subject this phase's own
   Job-launch-to-Worker handoff needs.
10. **`tests/e2e/integration_test.go`**: `TestGrandIntegration` updated to launch through the new async
    shape and poll the job resource to a terminal state before asserting on log events, rather than
    asserting a synchronous `200` response's own `dispatched` count. See the "read this first" note above
    for what this proves.

**One trap worth naming for whoever touches the per-device lock item next (Phase 15).** It would be easy
to acquire a `lock.Manager` lease inside `Worker.HandleJobRequested` at fan-out time and call the item
closed. Don't, and don't reach for "`cmd/runner` doesn't run yet" as the reason either, since it does
(see the "read this first" note above). The real reason is what a running Runner's own "done" signal
means today: `native.Adapter.Execute` is still simulated, never a real action against a real device
(Phase 16's own open item), and this session's own Phase 15 predecessor still acks a failed job rather
than retrying it, so even today's simulated completion is not yet a trustworthy release trigger. A lease
acquired or released against either would be guarding nothing real. PLAN.md Section 13 places locks in
the backend precisely because multiple execution environments may target the same device; Phase 16 is the
first point where a lock would be held against something real. The full note is on Phase 15's own
relocated checklist item in `IMPLEMENTATION.md`.

**A second trap, the one #76 and #77 both are instances of.** A field or a local variable's name is not
evidence of what it actually holds; only the accessor or property key that fills it is. Both bugs
compiled cleanly and passed every existing test, because both existing tests happened to feed the exact
value the buggy code expected rather than the value a real device would actually carry (RULE 0's own
concern, restated at the wire-DTO layer this time).

**Deliberately deferred, with the reason:** the per-device lock, relocated to Phase 15 rather than closed
here (see the trap above and Phase 15's own checklist item). No collection endpoint for listing jobs:
`GET /api/v1/jobs/{id}` is the only job-reading route this phase registers; a list would need the same
Section 25 keyset-pagination primitive Phase 13's own deferred-items note already named for devices, and
this phase's Release Gate needs one job's progress, not a list of them. `internal/dispatch.Worker` cannot
graft a live OpenTelemetry span across the `job.requested` handoff the way `dispatcher.go`'s own HTTP
handler can pull one from `r.Context()`: `event.Bus.Subscribe`'s handler signature exposes only the
decoded `Event`, never the transport's raw headers, and reaching into a concrete NATS type to recover one
would violate this package's own port boundary (documented in `worker.go` as a known, accepted
limitation, not an oversight).

**Verification.** `go build ./...` and `go vet ./...` clean. `gofmt -l` clean on every file this phase
touched. `go test ./internal/dispatch/... ./pkg/wire/... ./internal/runbook/... ./internal/api/... -race
-count=1` clean, including `TestDispatcher_ReleaseGate` (23.3s) and the fuzz targets
(`FuzzDispatchRunbook`, `FuzzWorkerDeviceProperties`). `go test ./tests/e2e/... -run TestGrandIntegration
-v` passes end to end against real Postgres and NATS containers (10.6s; see the "read this first" note).
`make gosec`: 1 finding, individually waived (`internal/api/logs.go` `G705`, pre-existing, unchanged from
Phase 13). `make govulncheck`: 0 vulnerabilities called by this repository's own code. Coverage, measured
directly per package rather than trusted from the full-suite tool (`internal/election`'s own coverage
number was observed to vary 87.5%/97.5% run to run in this sandbox on code this phase never touched, a
timing-sensitive branch, not a regression): `internal/dispatch` 71.4%, `internal/runbook` 75.4%,
`internal/api` 96.6% (floor 95.0, unchanged), all three recorded into `coverage-floor.json` this session
(`internal/dispatch` and `internal/runbook` are new packages, `pkg/wire` has no statements to measure and
is recorded at the vacuous 100.0). Benchmarks (real numbers, this machine): `BenchmarkDispatchRunbook`
(the launch path alone: resolve, persist, publish, respond) 64.2/64.9/68.8 µs/op across three runs;
`BenchmarkWorker_DeviceFanOut` (the full per-job fan-out cost: a real SQLite-backed `JobStore` write per
step, real admission checks, one real bus publish per device, 50 devices) ~4.67 ms/op (~93 µs/device)
against `BenchmarkAnsiblePlaybookFanOutComparable`, a real `ansible-playbook` subprocess run as the
reference platform this project's own Performance Benchmarking rule requires, at ~1.19 s/op, roughly 256x
slower than this platform's own fan-out at this scale.

## Previous session: Phase 13, HATEOAS Generator

**This session closed Phase 13: HATEOAS Generator** (`.SPECIFICATION/IMPLEMENTATION.md`), all nine
previously-open checklist items plus the Pattern Entry Gate, Fuzz/Stress, Adversarial Pattern
Justification, Schema/Injection Hardening, and Release Gate items. Branch is
`feature/HATEOAS-Generator`, based on `e820e9d` (`origin/main`, which already contained Phase 12 via
PR #3). **Nothing is committed.** The working tree carries the whole phase.

**Read this first if you are picking up mid-stream.** The session's opening finding was wrong and was
retracted: an initial survey concluded "Phase 12 is not in this tree" and planned a merge around it.
Phase 12 was already merged upstream; the local checkout was simply stale and a `git pull` resolved it.
That is not recorded in `FAILURE_PATTERNS.md` and should not be, because a stale checkout is not a
repository defect. It is mentioned here only so the next reader does not go looking for the
branch surgery an earlier plan described.

**The honest headline, since this phase's own Adversarial gate asks for it:** the pattern
`PATTERNS.md` described as live had never run. `auth.HATEOASGenerator`'s only implementation in the
repository was a test mock, so a composition root could pass only `nil`; `api.HATEOASMiddleware` had
zero production callers across two phases; and the Release Gate's own parenthetical already admitted it
passed by omitting every link rather than the one it names. All of that is closed rather than argued
away, and the gate is now strengthened in four independent ways so the same vacuous pass cannot recur.

**One roadmap item was wrong and is corrected rather than "fixed."** The checklist asserted the
recorder "captures the status code but never forwards it," so "any handler returning a non-200 status
is currently reported as 200." A probe run against the real middleware reported the opposite in every
case: 404 arrived as 404, 500 as 500, 201 as 201. All four exit branches did forward. Reading
`hateoasRecorder.WriteHeader` in isolation gives the item's conclusion; reading the middleware that
owns it does not. Recorded as `FAILURE_PATTERNS.md` #74, because a phase writeup claiming a fix that
never happened is a false record that outlives the code.

**Six real defects found, all recorded before being fixed:**

1. **`FAILURE_PATTERNS.md` #70, the one that mattered most.** `hateoasRecorder` embeds the
   `http.ResponseWriter` *interface*, which promotes exactly three methods, so `http.Flusher` is
   dropped. `internal/api/logs.go` type-asserts `http.Flusher` and answers `500 "Streaming
   unsupported"` when it fails. **Mounting the HATEOAS middleware would have killed the SSE log
   endpoint**; the only reason it never did is that nothing ever mounted it. Proven with a control
   (the same assertion without the middleware sees a `Flusher`). This is the whole argument for
   choosing an encoder seam over Decorator, and it is why the middleware was deleted rather than
   repaired: forwarding `Flusher` would still have dropped `Hijacker` and `ReaderFrom`.
2. **#71.** Every body round-tripped through `map[string]interface{}`: `{"count":9007199254740993}`
   was served as `...992`, top-level arrays got no `_links` at all, a handler-supplied `_links` was
   silently overwritten, and `application/json; charset=utf-8` was skipped by an exact-match compare.
3. **#72.** The caller's raw `r.URL.Path` was reflected into the body as the `href` of every link, so
   the URL a hypermedia client is invited to follow was chosen by the caller. Same shape as #63's NATS
   subject rule, one boundary over.
4. **#73.** The generator's error was discarded (`x, _ :=`), making an authorization-backend outage
   indistinguishable from a caller legitimately allowed nothing.
5. **#74.** The roadmap item above.
6. **#75, adjacent rather than this phase's own, and worth reading.** Phase 12's
   `TestValidateToken_RejectsTamperedSignature` failed during this session's first full-suite run,
   against code this phase never touched. Its helper flipped the last *character* of the base64url
   signature and claimed in its own doc comment that this guaranteed different decoded bytes. It does
   not: a 32-byte HMAC encodes to 43 characters carrying 258 bits, so the final character's low 2 bits
   are padding the decoder discards, and `A` to `B` differs only there. Enumerated exhaustively, **16
   of 256 possible final signature bytes (6.2%) produce a "tampered" token that decodes byte-identical
   to the original**, meaning nothing was forged and `ValidateToken` correctly accepted a valid token.
   It reads as rare because `generateTestToken` stamps `exp` at second granularity, so `-count=400`
   inside one process re-tests one identical token and always passes. Fixed by tampering with the
   decoded bytes and asserting they changed, plus a regression test enumerating all 256 cases.

**What was built, by area:**

1. **`internal/auth/hateoas.go`** (new). The redefined port. `LinkRel` (a typed, closed relation
   vocabulary, for the same reason `Scope` is typed), `Affordance`, and
   `HATEOASGenerator.Permitted(ctx, *Identity, []Affordance) ([]LinkRel, error)`. **The return type is
   the design.** It is a subset of what was offered, so an implementation may only select: it cannot
   widen a scope, retarget a resource, or invent a relation, and it is never handed a URL and never
   returns one. `NewAdmissionHATEOASGenerator` is the first real implementation and fails closed on an
   empty chain.
2. **The one-chain wiring, which is the load-bearing decision.** `cmd/controller` builds a single
   `auth.AdmissionChain` value and gives it to two consumers: `auth.Admission` (recorded) for
   `api.RequireScope`, and the bare chain (unrecorded) for the generator. Enforcement and advertising
   are therefore the same evaluation over the same rules, and a link and a 403 cannot disagree.
   **The generator deliberately does not get the recorded wrapper**: probing every affordance per
   request would emit N audit lines and log every unheld permission as a `Warn` denial, burying the
   denials where somebody actually attempted something. Nobody asked to delete a device by loading a
   page. `TestHATEOAS_AffordanceProbingIsNotAudited` asserts one request records exactly one decision.
3. **`internal/api/respond.go`** (new). The encoder seam. `Respond` marshals the handler's own typed
   value once and never decodes it, so #71's entire class is gone. `_links` is a typed field
   (`api.LinkSet`), not a map entry, so collision is unrepresentable. **The pointer in `LinkSet` is
   load bearing**: absent means "could not be computed", `[]` means "computed, you may do nothing", and
   a plain slice with `omitempty` would collapse those together, which is #73.
4. **`internal/api/links.go`** (new). One `linkBuilder`, built once in `NewRouter`, immutable
   afterwards, indexed by route pattern. It is the single source of truth behind both `_links` and the
   `Allow` header, so the two cannot drift. Every href is built from the matched chi route pattern with
   parameters re-escaped, never from `r.URL.Path`.
5. **`internal/api/options.go`** (new). `OPTIONS` per pattern plus a replacement for chi's
   `MethodNotAllowed`, whose built-in emits the unfiltered method set. Authenticated but deliberately
   not behind `RequireScope`: a viewer asking what it may do must be answered, not 403'd.
6. **`internal/api/devices.go`** (new) and **`inventory.Repository.Retire`**. The Release Gate had no
   handler behind it and no phase owned adding one. `GET`/`DELETE /api/v1/inventory/devices/{name}`.
   `DELETE` is a retirement to `StateArchived`, not a row removal, because every `Revision` is
   `Immutable()` and the edge carries no cascade. `Retire` is implemented in both adapters, refused by
   `NewReadOnlyRepository`, and covered by four conformance tests across both backends.
7. **Resource-state filtering (`api.LinkFilter`).** An already-archived device offers no `delete` link
   to anyone, regardless of scope. This is what makes the phase HATEOAS rather than a server-side
   rendering of the caller's permission table, and it fell out of the gate test failing honestly.
8. **Every write path migrated** off `http.Error` and the `map[string]interface{}` dispatch response.

**One trap worth naming for whoever touches the device handlers next.** `deviceDTO` omits the property
bag entirely, and that is a security decision, not an oversight. `cmd/controller` installs
`crypto.DeviceEnvelopePropertiesInterceptor`, so `Properties()` returns **decrypted** values on every
read; emitting them would ship enable secrets and API keys to any caller holding `inventory:read`.
PLAN.md Section 25 assigns the masking ruleset to Phase 22 and none exists today. Do not add the
property bag before that lands.

**A second trap, measured rather than assumed.** chi decodes `%00` in a URL parameter into a real NUL
byte, but leaves `%0a`, `%0d`, and `%2f` as literal three-character text. So NUL is the one control
character that actually reaches a handler, which is why the name guard checks for a real NUL and not
for the string `"%00"`. `TestDeviceHandler_PercentEncodedControlsArriveEncoded` pins this so a future
chi or `net/url` upgrade that changes it fails loudly instead of silently widening what gets through.

**Deliberately deferred, with the reason:** no collection endpoint (`inventory.Selector` carries only
`GroupName`, so paging would mean extending the Section 25 keyset primitive and both adapters; the gate
needs one device, not a list). `api.Link` and `auth.LinkRel` stay in `internal/` (Phase 15 owns
`pkg/wire`, and they must move together because `pkg/` may not import `internal/`). No CORS: OPTIONS is
authenticated, so a browser preflight gets 401, and Phase 19 is the first phase with a real browser
client.

**Verification.** `go build ./... && go vet ./...` clean; `gofmt -l` clean. `go test ./... -race
-count=1` clean across the repository (`-p 2`; one run at `-p 4` hit `FAILURE_PATTERNS.md` #69's
`go list` tree-walk race in `internal/archtest`, unrelated to this diff and confirmed transient by an
immediate clean re-run). `make gosec`: **waivers drop from 6 to 1**, because the four on the deleted
`hateoas.go` and the one on `dispatcher.go` were retired rather than re-pointed; the survivor is the
pre-existing `logs.go` `G705`. `make govulncheck`: 0 vulnerabilities. `make coverage`: `internal/api`
95.1% (floor raised 94.0 -> 95.0), `internal/auth` 91.4% (90.5 -> 91.0), `internal/inventory` 82.4%
(82.0 -> 82.4). The same four pre-existing `coverage-floor.json` regressions from
`FAILURE_PATTERNS.md` #60 (`internal/forge/genutil`, `internal/inventory/record`, `pkg/collection`,
`tools/gencatalog`) recur at identical percentages, unrelated to this phase, exactly as in Phase 11's
own run. Fuzz: `FuzzHrefConstruction` ~168,000 executions/26s and `FuzzRespondEnvelope` ~237,000
executions/26s, zero crashes. Benchmarks (real numbers, this machine): `BenchmarkRespondWithLinks`
10.6/11.0/11.9 µs/op at 1/2/4 affordances against `BenchmarkAPIMiddleware_SecuredRoute` at 7.3 µs/op,
so hypermedia costs ~3.2 µs on the first affordance and ~425 ns per additional one;
`BenchmarkOptionsHandler` 9.0 µs/op; `BenchmarkAdmissionGenerator_Permitted` 142 ns/1.5 µs/6.3 µs at
2/8/32 candidates. **That curve is not linear at the low end and the reason is worth knowing:** a
denied candidate costs roughly ten times an allowed one, because `AdmissionChain.Evaluate` builds a
formatted error per denial, and a low-privilege caller probing a wide resource is mostly denials. That
allocation is the first thing to attack if this ever appears in a profile.

**One unrelated fix included, keep it out of the Phase 13 commits.** `make fmt` was already red on
`main` before this session touched anything: a misaligned map literal in
`internal/engine/executor_fuzz_test.go` from `c8b364a` (Phase 10). Since `fmt` is in the `ci` chain,
`make ci` was failing on `main`. Fixed as an isolated three-line change, which belongs in its own
commit per `AGENTS.md`'s one-logical-change rule.

## Previous session: Phase 12, Zero-Trust Middleware (and Phase 11 notes below)

**This session closed Phase 12: Zero-Trust Middleware** (`.SPECIFICATION/IMPLEMENTATION.md`), all nine
previously-open checklist items. Research was direct reading of `internal/auth`, `internal/api`, both
composition roots, `PATTERNS.md`'s Chain of Responsibility and Audit Trail entries, and the prior two
sessions' own handoff text, plus one targeted grep sweep for a production caller of
`auth.Admission.Evaluate`/`auth.AdmissionChain.Evaluate` outside `internal/auth`'s own tests. That grep
returned nothing, which is the one finding that shaped the whole session.

**The honest headline, stated plainly because this phase's own Adversarial Pattern Justification line
asks for it, the same way Phase 11's did:** this phase was named "Zero-Trust Middleware," and its one
already-checked item ("Inject the Phase 8 RBAC Evaluator into the `chi` routing chain") described
authentication, not authorization. `api.AuthMiddleware` validated a token and put an identity in
context; nothing downstream ever asked whether that identity was allowed to do anything.
`auth.AdmissionChain`/`auth.Admission`, the real mechanism Phase 8 built for exactly this and
`PATTERNS.md` already described in the present tense ("every API request is stripped, token-validated,
and checked against scope before it ever touches application logic"), had zero production callers
anywhere in the repository. Two real, live gaps followed directly from that, both found and fixed before
being checked off, per this repository's own rule:

1. **`GET /api/v1/jobs/{id}/logs` authenticated every caller and authorized none of them**
   (`FAILURE_PATTERNS.md` #65). Any validly signed token, including one with an empty `Scopes` slice,
   could stream any job's live logs by UUID.
2. **An unauthorized `runbook:execute` dispatch returned HTTP 200** (`FAILURE_PATTERNS.md` #66).
   `internal/api/dispatcher.go`'s per-device loop called `auth.CheckAccess` on an invariant argument
   (identical for every device on every call) and, on failure, incremented a failure counter and
   continued rather than rejecting the request.

A third, unrelated-in-mechanism but same-in-shape gap was found auditing "is there a second unguarded
entry point": **`cmd/demo` mounted a production SSE handler on a bare `chi.NewRouter()` with no auth, no
tracing, and no rate limiting** (`FAILURE_PATTERNS.md` #67), and its own advertised URL had returned
`400` for a full phase because its hardcoded job ID was never migrated to a UUID after
`FAILURE_PATTERNS.md` #63 required one. And a fourth, one layer down from all three: **the "five real
forged JWTs, correctly rejected" audit Phase 39 and this phase's own prior checklist text both cited had
never been persisted as a test anywhere in this repository** (`FAILURE_PATTERNS.md` #68) - true when
checked by hand, unprovable to the next reader or to CI.

**What was built, by area:**

1. **`internal/auth/scopes.go`** (new). `type Scope string` plus the four constants
   (`ScopeInventoryRead`/`Write`, `ScopeRunbookExecute`, `ScopeJobRead`) this platform's admission chain
   actually checks. `Identity.Scopes`, `Evaluator.CheckAccess`, and `AdmissionRequest.RequiredScope` all
   retyped from bare `string` to `Scope`, converting once at the JWT claim boundary (`jwt.go`), per
   `AGENTS.md`'s own typing rule.
2. **`internal/api/authz.go`** (new). `Admitter` (the one-method slice of `auth.Admission` this file
   needs, mirroring `TokenValidator`'s own Interface Segregation shape) and `RequireScope`, the
   middleware that is `auth.Admission.Evaluate`'s first production caller anywhere in this repository. A
   missing identity is 401 (the chain was bypassed); a denial is 403; nothing before this file ever
   returned either status for an authorization reason.
3. **`internal/api/router.go`** (rewritten). `RouterConfig.Routes` is now `[]Route`
   (`Method`/`Pattern`/`Scope`/`Handler`), not a `func(chi.Router)` callback, and `NewRouter` now returns
   `(*chi.Mux, error)`. Construction fails closed on every shape this package considers unsafe to serve:
   a nil `Auth` with no explicit `AllowUnauthenticated` opt-out, a non-empty `Routes` with a nil
   `Admission`, an empty `Route.Scope`, or a duplicate `Method`+`Pattern`. This is the same
   fail-closed-at-construction idiom `NewJWTEvaluator`/`NewStaticKeyProvider` already use, applied to the
   router itself for the first time.
4. **`internal/auth/authtest`** (new package). `Issuer`, a real token minter backed by a real, freshly
   generated HMAC secret and the real `auth.NewStaticKeyProvider`/`auth.NewJWTEvaluator` path, replacing
   `api.IdentityKeyForTest` everywhere outside `internal/api`'s own test binary.
   `internal/archtest/testonly_test.go`'s new `TestAuthtestNeverImportedByProductionCode` enforces that
   no production package ever depends on it, the enforcement a `_test.go` build tag could not give
   (`authtest` cannot be a `_test.go` file at all, since a `_test.go` file cannot be imported across
   package boundaries, which is the exact cross-package problem it exists to solve for `tests/e2e`).
   `IdentityKeyForTest` itself moved out of `middleware.go` (always-linked production code) into
   `internal/api/export_test.go` (linked only into `package api`'s own test binary), for the in-package
   tests that still use it.
5. **`cmd/controller/main.go`, `cmd/demo/main.go`**. Both build a real `auth.Admission{Chain:
   auth.AdmissionChain{auth.NewTokenScopeRule(evaluator)}, Recorder: auth.NewSlogRecorder(logger)}` and
   pass it through `RouterConfig.Admission`; both convert their route registration to the new declarative
   table with an explicit `Scope` per route. `cmd/demo` additionally moved off its own bare
   `chi.NewRouter()` entirely, mints one real signed admin token at startup via the same
   `NewStaticKeyProvider`/`NewJWTEvaluator` path (not `authtest`, which production code must never
   import), and fixed its job ID to a real UUID.
6. **`internal/api/dispatcher.go`**. The per-device `auth.CheckAccess("runbook:execute")` call is gone,
   not moved: it was an invariant, identical for every device on every call, and `api.RequireScope` now
   enforces the same scope once, at the boundary, before this handler ever runs. `api.NewDispatcher` no
   longer takes an `auth.Evaluator` at all.
7. **Tests** (all new unless noted): `internal/auth/jwt_forgery_test.go` (six forged-token cases -
   `alg: none`, RS256-against-HMAC algorithm confusion, expired, not-yet-valid, tampered signature,
   stripped signature - run against the real `ValidateToken`), `internal/api/middleware_forgery_test.go`
   (the `alg: none` and algorithm-confusion cases re-run through the real `AuthMiddleware`, the
   request-path boundary the prior session's checklist text said did not yet exist to audit),
   `internal/api/{authz_test,authz_bench_test,router_validation_test}.go`, `internal/archtest/
   testonly_test.go`. Rewritten: `internal/api/{router_test,router_bench_test,router_fuzz_test,
   defaults_test}.go` (the `Routes []Route` signature change, plus `TestRouter_
   RequireScopeEnforcesDeclaredScope`, the router-level release gate proving 401/403/200 against a real
   `authtest`-minted token), `internal/api/dispatcher_test.go` and siblings (dropped `MockAuthEvaluator`
   and the now-meaningless `TestDispatcher_UnauthorizedDeviceCountsAsFailed`), `internal/auth/{chain_test,
   ent_team_lookup_test}.go` (retyped `Scopes`/`RequiredScope` literals), `tests/e2e/integration_test.go`
   (dropped its own `mockEvaluator`, now authenticates through the real `AuthMiddleware` with a real
   `authtest`-minted token rather than `api.IdentityKeyForTest`).

**Verification.** `go build ./... && go vet ./...` clean; `gofmt -l` clean on every file this session
touched. `GOFLAGS="-p=4" go test ./... -race -count=1` clean across the whole repository, including the
real-container tests (`FAILURE_PATTERNS.md` #61's own recorded mitigation; #69, a second, unrelated race
this session hit and fixed with the identical mitigation, is new). **Real end-to-end proof against the
actual built binaries and real infrastructure (RULE 0), not only `go test`:** `TestGrandIntegration`
(`tests/e2e`, real Postgres+NATS via testcontainers, a real dispatch authenticated end to end with a real
`authtest`-minted token, PASS in 11.3s) and `TestController_JWKS_RealServer_AcceptsValidRejectsForged`
(`cmd/controller`, the real built `pleiades-controller` binary against a real NATS container, PASS in
5.5s). Per this session's own explicit decision with the user, no additional manual `curl` transcript was
taken against a hand-started binary: the automated tests above already exercise the identical real
binary and real broker a manual run would, and were judged sufficient rather than duplicated by hand,
unlike Phase 11's own gate. Fuzz: `FuzzAPIRouter` (extended with an arbitrary `Authorization` header)
~219,000 executions/21s, zero crashes. Benchmarks, real numbers on this machine: `BenchmarkRequireScope`
~263 ns/op; `BenchmarkAPIMiddleware_SecuredRoute` (the full chain with a real secured route mounted) ~8.1
µs/op against `BenchmarkAPIMiddleware`'s own ~9.1 µs/op baseline with no application route at all -
within noise of each other, so `RequireScope` adds no measurable cost on top of the pipeline Phase 11
already built. No credible published AWX/Tower figure exists for either (`AGENTS.md`'s benchmarking
rule). `make gosec`: 6 findings, all individually waived, zero new; one pre-existing waiver's line range
re-pointed (`154-159` -> `171-176`) to follow the code it describes after this phase deleted the lines
above it, per `gosec-waivers.json`'s own "must be re-reviewed, not silently re-added" rule.
`make govulncheck`: 0 reachable vulnerabilities; `go.mod` already pins `golang-jwt/jwt/v5 v5.3.1`, past
the fix for CVE-2025-30204 (a `ParseUnverified` DoS the user asked to be checked against by name); the
one non-reachable module finding (`golang.org/x/crypto`'s deprecated `openpgp`, GO-2026-5932) is
transitive and unrelated to auth. `make coverage` (run under `GOFLAGS="-p=4"`, see `FAILURE_PATTERNS.md`
#69): `internal/api` 94.5% (floor raised 93.5 -> 94.0), `internal/auth` 90.9% (floor raised 90.0 -> 90.5),
`internal/auth/authtest` excluded (test-double token issuer, the same class as `pkg/inventory/
inventorytest`). The same four pre-existing `coverage-floor.json` regressions from `FAILURE_PATTERNS.md`
#60 recurred at identical percentages, unrelated to this phase; one new package
(`internal/catalog/pleiades/builtin/wait`) reported with no floor yet, informational only, also unrelated.

**One security question the user raised directly, checked against this codebase rather than answered
from memory:** three real `golang-jwt`/`dgrijalva-jwt-go` CVEs (CVE-2025-30204, CVE-2024-51744,
CVE-2020-26160). `go.mod` is already past the fix for the first; `internal/auth`'s `ValidateToken` never
selectively unwraps a specific error (it fails closed on any non-nil error uniformly), so the trap shape
of the second cannot occur here; `dgrijalva/jwt-go` does not appear anywhere in `go.mod`/`go.sum`, direct
or transitive, so the third is inapplicable. `internal/auth/jwt_forgery_test.go` now gives the underlying
claim ("this codebase rejects a forged JWT") a persisted regression test rather than a one-time manual
check, per `LESSONS_LEARNED.md` #76.

**Follow-ups named, not built:** `auth.NewScopeRule` (the Team/RoleBinding/`ScopeResolver` axis) is
still not appended to either production `AdmissionChain`. It needs a `ScopeTarget` (which Group/Device/
Organization a request is against), and an HTTP route has none to give it until a handler resolves one;
Phase 14 owns it, once it holds a device, per this session's own `IMPLEMENTATION.md` correction to that
phase's `HasCapability` item. No issuer/audience pinning on `NewJWTEvaluator`'s own construction path is
unchanged from before this phase (`FAILURE_PATTERNS.md` #20): no token-issuing code exists anywhere in
this repository yet to define a real issuer/audience to pin against.

**Files changed:** `internal/auth/{evaluator,jwt,chain}.go`, `internal/auth/scopes.go` (new),
`internal/auth/authtest/issuer.go` (new package), `internal/auth/jwt_forgery_test.go` (new),
`internal/auth/{chain_test,ent_team_lookup_test}.go`, `internal/api/{router,dispatcher,middleware}.go`,
`internal/api/authz.go` (new), `internal/api/export_test.go` (new),
`internal/api/{authz_test,authz_bench_test,router_validation_test,middleware_forgery_test}.go` (new),
`internal/api/{router_test,router_bench_test,router_fuzz_test,defaults_test}.go`,
`internal/api/{dispatcher_test,dispatcher_bench_test,dispatcher_fuzz_test,dispatcher_selector_test}.go`,
`internal/api/testdata/fuzz/FuzzAPIRouter/00e15d22123489fd`, `internal/archtest/testonly_test.go` (new),
`cmd/controller/main.go`, `cmd/demo/main.go`, `tests/e2e/integration_test.go`,
`.SPECIFICATION/{IMPLEMENTATION,PATTERNS}.md`, `coverage-floor.json`, `gosec-waivers.json`,
`FAILURE_PATTERNS.md` (#65-#69 new), `LESSONS_LEARNED.md` (#76 new).

## Previous session: Phase 11, API Gateway & Telemetry

**This session closed Phase 11: API Gateway & Telemetry** (`.SPECIFICATION/IMPLEMENTATION.md`), all
fourteen previously-open checklist items plus the Pattern Entry Gate, Fuzz/Stress, Adversarial Pattern
Justification, Schema/Injection Hardening, and Release Gate items. Research was one Explore agent over
`PLAN.md`/`PATTERNS.md` plus direct reading of `internal/api`, `internal/event`, `internal/runner`, and
both composition roots. That research produced the one finding that shaped everything else: almost every
concrete requirement this phase owes originates in `PATTERNS.md`, not `PLAN.md`. `PLAN.md` Section 19 says
only "OTEL everywhere" and "trace IDs must propagate API -> Bus -> Lock Manager -> Runner -> Device," and
Section 25's Shared Primitives table has no row for telemetry, tracing, an HTTP server, or an ingress
rate limiter at all. `PATTERNS.md` is where `/healthz`, `/readyz`, RED, the Front Controller, `/api/v1`,
and the per-identity token bucket are actually specified.

**The honest headline, stated plainly because the phase's own Adversarial Pattern Justification line asks
for it:** this phase was named "Telemetry" and had none. What existed was a UUID in an `X-Trace-ID` header
and one Prometheus counter labeled by raw URL path. No span, no duration, no exporter, no propagation;
`otel` was in `go.mod` only as an indirect test dependency. Three of `PATTERNS.md`'s six observability
entries described behavior that did not exist anywhere. That is closed rather than argued away, and the
Release Gate was strengthened so the same gap cannot pass it again: the gate used to require a log field
*named* `trace_id`, which a UUID generator satisfies, and now requires that field to equal the trace ID of
the real OpenTelemetry span that served the request.

**Two real, pre-existing bugs were found while auditing this phase's own boundaries, both fixed, both
recorded:**

1. **A NATS subject injection that was an authorization bypass** (`FAILURE_PATTERNS.md` #63). `internal/
   api/logs.go` concatenated the caller-supplied `{id}` URL parameter straight into a NATS subject via
   `topology.LogSubject`. NATS subject wildcards are ordinary characters, so `GET /api/v1/jobs/%3E/logs`
   built the filter subject `pleiades.jobs.logs.>` and streamed **every job's live logs in the system** to
   any authenticated caller holding any scope. Found by following a `G705` gosec finding that had been
   individually waived across three phases as a low-severity XSS question, one line further up into the
   subject builder. Fixed by requiring the `{id}` to parse as a UUID at the boundary (every job ID this
   platform mints already is one), which closes the injection and the waived `G705` together.
2. **A data race between an SSE handler and its own consumer goroutine** (`FAILURE_PATTERNS.md` #64).
   `StreamLogs` set its response headers *after* starting the JetStream `Consume` callback that writes the
   body, so the callback's first write read the header map while the handler was still mutating it. The
   file already had a `writeMu` guarding writes to `w`; header mutation is not a write to `w`, so the
   mutex never covered it. It flaked roughly one `-race` run in five. Fixed by moving the four header
   assignments above `Consume` (setting a header commits nothing, so the ordering property the original
   code wanted is preserved).

**What was built, by area:**

1. **`internal/telemetry`** (new package). `Config`/`ExporterKind`/`Provider`/`Setup`/`ConfigFromEnv`, plus
   `Propagator()`, the single place this platform's trace-context wire format is decided (W3C
   `TraceContext`+`Baggage`). Exporters: `none`, `stdout`, `otlp` (HTTP). `ConfigFromEnv` reads the
   standard `OTEL_*` variables so an operator configures this like any other OTEL process, and treats a
   bare `OTEL_EXPORTER_OTLP_ENDPOINT` as implying `otlp`, since an endpoint with nothing sent to it is far
   more likely a mistake than an intention. **`none` builds a real `TracerProvider` with no span
   processor, not a no-op tracer**, deliberately: with no collector deployed the trace IDs must still be
   valid, or the `trace_id` log field, the `X-Trace-ID` header, and cross-process propagation all silently
   become all-zeros.
2. **`internal/api/middleware.go`** (rewritten). `TracingMiddleware` (renamed from `TraceIDMiddleware`)
   starts a real server span, continues an inbound `traceparent`, and renames the span to the matched chi
   route pattern on the way out (chi only knows the pattern after routing). `StructuredLoggerMiddleware`
   and `MetricsMiddleware` take injected dependencies. Package-level `promauto` registration and the
   package-level `slog.New` are both gone. `TraceIDFromContext` reads the span context and reports absence
   honestly, so a caller can tell "tracing is off" from "the ID is zeros."
3. **`internal/api/metrics.go`, `health.go`, `ratelimit.go`** (new). Full RED (`http_requests_total` with a
   `code` label, `http_request_duration_seconds`, `http_requests_in_flight`) on an injected registry,
   labeled by **route pattern, never raw path**, with an `unmatched` fallback so a 404 flood cannot mint
   label values. `/readyz` runs `ReadinessCheck`s concurrently under one deadline and reports only
   `ok`/`failed` per check, never driver error text, because the endpoint is unauthenticated. `/healthz`
   deliberately checks nothing: a liveness probe that fails on a broken dependency tells the orchestrator
   to restart a process a restart cannot fix. The rate limiter is a per-caller token bucket keyed on the
   authenticated identity when present and the source address otherwise, **never** on `X-Forwarded-For`
   (a caller-supplied key mints a fresh bucket per request, which is worse than no limiter for looking
   like one), with a capped, self-evicting caller table so the defense is not itself the exhaustion
   vector.
4. **`internal/api/router.go`** (rewritten). `NewRouter(RouterConfig)`; every field optional with a safe
   default. Middleware order is load bearing and documented: tracing outermost, then metrics, then
   logging, then `Recoverer` innermost, so a panic becomes a 500 all three observe. Routes register
   through `RouterConfig.Routes`, already mounted under `/api/v1` with auth and the limiter applied, which
   is what turns "versioned, authenticated, throttled" into a structural property rather than a rule each
   new route must remember. `/healthz`, `/readyz`, `/metrics` are the documented unversioned, unthrottled,
   unauthenticated exception.
5. **`internal/event/trace.go`** (new) and `nats.go`. `InjectTraceContext`/`ExtractTraceContext` over a
   purpose-built `natsHeaderCarrier`. **The carrier is hand-written rather than a `http.Header`
   conversion on purpose**: the two types share an underlying map, so the conversion compiles and
   round-trips perfectly between two Go processes while writing the canonicalized `Traceparent`, which the
   W3C specification does not mandate and a non-Go consumer would never find.
6. **`internal/runner/agent.go`**. `handleMessage` extracts the trace context and starts a child consumer
   span. This is what makes item 5 a feature rather than a decoration, and it is asserted as such:
   `TestAgent_ContinuesTraceFromMessageHeaders` proves the Runner's recorded span shares the API request's
   trace ID and is parented to its span.
7. **`cmd/controller/main.go`, `cmd/runner/main.go`**. Telemetry setup with bounded shutdown flush, one
   injected JSON logger, one private Prometheus registry, real readiness checks, and rate-limiter
   configuration. `log.Fatalf` replaced with a `fatal` helper (see the log-destination note below).
8. **Tests** (all new unless noted): `internal/telemetry/{telemetry_test,export_test}.go`,
   `internal/api/{ratelimit_test,ratelimit_bench_test,defaults_test}.go`,
   `internal/event/{trace_test,trace_fuzz_test,trace_bench_test}.go`,
   `internal/runner/agent_trace_test.go`. Rewritten: `internal/api/{router_test,middleware_test,
   router_fuzz_test,router_bench_test}.go`. Extended: `internal/api/logs_test.go` (the injection
   regression table), `internal/api/dispatcher_test.go` (now uses a real SDK span, since a no-op tracer's
   span context is all-zeros and a test built on one proves nothing).

**One trap worth naming for whoever touches logging next.** Installing a JSON `slog` handler on stdout and
calling `slog.SetDefault` in `cmd/controller` looked like a pure improvement and silently did two other
things. Go's standard `log` package routes through `slog.Default` at **info** level, so every
`log.Fatalf` startup failure began emitting as an `INFO` line, meaning no alert keyed on level would ever
fire for a controller that failed to start. And `cmd/controller/leader_election_release_gate_test.go`
scraped the subprocess's **stderr** for a log line (correct while `slog`'s built-in default wrote there)
and began seeing nothing, failing with a timeout that described a leader-election problem rather than a
logging one. Both are fixed; both are `LESSONS_LEARNED.md` #75.

**Verification.** `go build ./... && go vet ./...` clean; `gofmt -l` clean on every file this session
touched. `go test ./... -race -count=1 -p 4` clean across the whole repository (the `-p 4` cap is
`FAILURE_PATTERNS.md` #61's own recorded mitigation for this environment's container contention).
`internal/api` re-run four consecutive times to confirm the #64 race fix holds. `make gosec`: 6 findings,
all individually waived, zero new; the stale `internal/api/router.go` waiver was **removed** rather than
re-pointed, because this phase actually fixed it. `make govulncheck`: **found three real vulnerabilities in
the OTEL and gRPC modules this phase added** (`GO-2026-5158`, `GO-2026-4985`, `GO-2026-6061`), all fixed by
upgrading to `otel@v1.44.0`/`grpc@v1.82.1` rather than waived; now reports 0. `make coverage`:
`internal/api` 94.0% (floor raised 90.0 -> 93.5), `internal/telemetry` 96.9% (new, floor 96.0),
`internal/runner` 94.0% (new floor 93.5), `internal/event` 86.8% (floor raised 85.3 -> 86.5), `cmd/runner`
floor recorded at 0.0 to match `cmd/controller`. The same four pre-existing `coverage-floor.json`
regressions from `FAILURE_PATTERNS.md` #60 (`internal/forge/genutil`, `internal/inventory/record`,
`pkg/collection`, `tools/gencatalog`) recurred at identical percentages, unrelated to this phase. Fuzz:
`FuzzAPIRouter` ~358,000 executions/26s and `FuzzExtractTraceContext` ~503,000 executions/26s, zero
crashes. Benchmarks (real numbers, this machine): `BenchmarkAPIMiddleware` ~8.3 µs/op,
`BenchmarkRateLimiter_Allow` ~114 ns/op, `BenchmarkRateLimiter_AllowDistinctCallers` ~32.7 µs/op,
`BenchmarkInjectTraceContext` ~395 ns/op, `BenchmarkExtractTraceContext` ~437 ns/op; no credible published
AWX/Tower figure exists to compare any of these against (`AGENTS.md`'s benchmarking rule), stated plainly
rather than fabricated. **Real end-to-end proof against the actual built binary and a real NATS broker
(RULE 0), not only `go test`:** see the Release Gate entry in `IMPLEMENTATION.md` Phase 11 for the full
transcript (trace ID matching between header/log/metric, inbound `traceparent` continuation, exported
stdout spans, `/readyz` flipping to 503 on broker loss while `/healthz` stayed 200, unversioned 404 vs.
versioned 401, and a real signed token hitting the rate limiter at 200/200/429).

**`make ci` still fails on one pre-existing item this session did not touch:** `gofmt` would reformat
`internal/engine/executor_fuzz_test.go`. Confirmed unchanged by this session (`git diff` is empty for it;
it dates to commit `c8b364a`), and the Phase 9 and Phase 10 handoff entries below already named it. It is
a one-line formatting fix owned by nobody, and it has now blocked `make ci` for three sessions running;
left alone again here to keep this diff to one logical change, but it is worth someone deliberately
deciding to fix rather than inheriting a fourth time.

**Follow-ups named, not built:** `event.Bus.Subscribe`'s handler signature takes no `context.Context`, so a
`Bus` subscriber structurally cannot read message headers and therefore cannot continue a trace. It costs
nothing today (the one production consumer, `runner.Agent`, pulls raw messages by design and does read
them), so changing the port and its six test doubles now would be churn ahead of a consumer; revisit when
Phase 14/15 adds a real `Subscribe` caller. `cmd/runner` has no HTTP listener, so it has neither `/healthz`
nor `/readyz`, which `PATTERNS.md`'s probe entry requires of Runners as well as Controllers. The
production identity test hook (`api.IdentityKeyForTest`) survives unchanged: it is Phase 12's own
checklist item, and moving it behind an `export_test.go` seam is not sufficient on its own because
`tests/e2e` is a different package, so the real fix is a test-only token issuer Phase 12 should build.

**Also uncommitted, from the previous session and unrelated to this phase:** the runbook-level `hosts:`
default (`internal/engine/{dag,action,executor}.go`, `internal/validate/*`, the two example runbooks and
their README). Described in "Previous session" below; it is a separate logical change and should be a
separate commit.

**Files changed:** `internal/telemetry/{telemetry,telemetry_test,export_test}.go` (new package),
`internal/api/{middleware,router,dispatcher,logs}.go`, `internal/api/{metrics,health,ratelimit}.go` (new),
`internal/api/{router_test,middleware_test,router_fuzz_test,router_bench_test,dispatcher_test,logs_test}.go`,
`internal/api/{ratelimit_test,ratelimit_bench_test,defaults_test}.go` (new),
`internal/api/testdata/fuzz/FuzzAPIRouter/*`, `internal/event/{nats,trace}.go`,
`internal/event/{trace_test,trace_fuzz_test,trace_bench_test}.go` (new), `internal/runner/agent.go`,
`internal/runner/agent_trace_test.go` (new), `internal/runner/{agent_test,agent_bench_test,agent_fuzz_test,
agent_nats_test}.go`, `cmd/controller/{main.go,leader_election_release_gate_test.go}`, `cmd/runner/main.go`,
`tests/e2e/integration_test.go`, `.SPECIFICATION/{IMPLEMENTATION,PATTERNS}.md`, `coverage-floor.json`,
`gosec-waivers.json`, `FAILURE_PATTERNS.md` (#63, #64 new), `LESSONS_LEARNED.md` (#73, #74, #75 new),
`go.mod`/`go.sum`.

## Previous session: runbook-level `hosts:` default

**What was built:** a runbook-level `hosts:` default, not a tracked `IMPLEMENTATION.md` phase item: a
user-driven request to move `examples/upgrade_ios/pleiades/runbooks/upgrade_ios_xe*.yaml` from repeating
`target: sw1` on every task to a single `hosts: sw1` at the top, mirroring an Ansible play's own `hosts:`.
`PLAN.md` (lines 397-412, 795-804) had already sketched `hosts:` in the classic list-of-plays shape, but it
was never implemented; `WorkflowDef` had no such field.

**Design decision, made with the user before writing code (via `AskUserQuestion`):** `hosts:` is a
default, not a hard override. A task's own `Params["target"]` wins when set; `dag.Hosts` is the
fallback. This was chosen over a hard-replace semantic because the engine already lets a single runbook
mix a controller-side task (no target at all) with target-side tasks naming different devices task by
task (`PLAN.md` Section 14's mixed execution contexts), and a hard replace would have taken that away.
It also matches `AGENTS.md`'s own "most specific level wins" hierarchical-policy principle, already
established for every other multi-level setting in this codebase, applied here for the first time to
runbook-vs-task.

**Detail:**

1. **`internal/engine/dag.go`.** `WorkflowDef.Hosts string` (`hosts,omitempty` in both YAML and JSON) and
   `DAG.Hosts string`, carried through unchanged in `buildFromDef`. Both are plain strings: `Params` still
   has no template rendering, so `hosts: "{{ some_var }}"` is not reachable from this change (`docs/
   hephaestus.md` still names a Jinja-compatible renderer as a planned, unbuilt shared primitive).
2. **`internal/engine/action.go`.** `TaskTarget(dag *DAG, task *Task) string`, the single place the
   default/override resolution happens: task's own `Params["target"]` if a non-empty string, else
   `dag.Hosts`. This replaces four independent copies of the same `task.Params["target"].(string)`
   assertion that previously lived in `executor.go` (`resolveDevices`) and three `internal/validate`
   rules - a real duplication, not a hypothetical one, so consolidating it into one function was in scope
   for this change rather than a separate cleanup. A non-string `Params["target"]` still falls back to
   `dag.Hosts` exactly like an absent one: `FAILURE_PATTERNS.md` #11 (a malformed target silently reads as
   absent) is unchanged by that session, still open, and deliberately not folded into this change.
3. **`internal/engine/executor.go`** (`resolveDevices`) and **`internal/validate/{capability_rule,
   blast_radius,lifecycle_rule}.go`** now call `TaskTarget` instead of their own inline assertion.
   `lifecycle_rule.go` gained its first `internal/engine` import as a result.
4. **`examples/upgrade_ios/pleiades/runbooks/{upgrade_ios_xe,upgrade_ios_xe_sugar}.yaml`**: `hosts: sw1`
   added once at the top, `target: sw1` removed from every task (9 tasks per file). Confirmed both files
   still compile to DAGs that `pleiades validate` reports identical findings against, the invariant
   `examples/upgrade_ios/README.md` already documents for this file pair.
5. **`examples/upgrade_ios/README.md`**: one new bullet under "What is identical" documenting `hosts:`
   and its default/override relationship to a task's own `target:`.
6. **Tests** (all new): `internal/engine/action_test.go` (`TestTaskTarget`, table-driven over the
   default/override/malformed cases), `internal/engine/tasktree_test.go` (`TestWorkflowDef_Hosts_
   JSONRoundTrip`/`YAMLRoundTrip`, `TestDAGBuilder_Hosts`), `internal/engine/executor_test.go`
   (`TestExecutor_RunbookHostsIsDefaultTarget`, a real `Executor.Run` proving both the fallback and the
   override dispatch to the right device), `internal/validate/{capability_rule_test,lifecycle_rule_test}.go`
   (`Test*Rule_FallsBackToRunbookHosts`), `internal/validate/blast_radius_test.go` (two new table cases).

**Follow-ups named, not built:** `FAILURE_PATTERNS.md` #11 (malformed `target` silently reads as absent)
is now one call site instead of four but is still unfixed. Template rendering for `hosts:`/`params:`
(a Jinja-compatible renderer) is still the pre-existing, separately-tracked gap `docs/hephaestus.md`
already names.

**Files changed:** `internal/engine/{dag,action,executor}.go`, `internal/engine/{action_test,
tasktree_test,executor_test}.go`, `internal/validate/{capability_rule,blast_radius,lifecycle_rule}.go`,
`internal/validate/{capability_rule_test,blast_radius_test,lifecycle_rule_test}.go`, `examples/
upgrade_ios/pleiades/runbooks/{upgrade_ios_xe,upgrade_ios_xe_sugar}.yaml`, `examples/upgrade_ios/
README.md`.

## Previous session: Phase 10, Workflow DAG Builder

**What was built:** see "Phase 10: Workflow DAG Builder session" immediately below for the complete
file-by-file summary. Everything from "Previous session: Phase 9, Google CEL Engine" onward describes
earlier sessions and is unchanged.

### Phase 10: Workflow DAG Builder session

**This session closed Phase 10: Workflow DAG Builder** (`.SPECIFICATION/IMPLEMENTATION.md`), all five
previously-open checklist items plus the Pattern Entry Gate, Fuzz/Stress, Adversarial Pattern
Justification, Schema/Injection Hardening, and Release Gate items. Planning followed this project's own
established ritual: direct research (small, well-bounded surface, matching Phase 9's own precedent for a
phase this size: `internal/engine/{dag,tasktree,executor,level_iterator,topology,conditional,
lock_acquisition,action,collection_action,import_tasks,task_syntax}.go`, `cmd/pleiades/run.go`, plus
`PLAN.md` Sections 14/22.1/25/35 and `PATTERNS.md`'s Builder/Composite/Checkpointing/Workflow Definition
Versioning entries), then one Plan agent pressure-tested the resulting design against the real repo before
any plan file was written — its findings (a full re-scoping of the typed-edges item, a correction removing
Checkpointing from this phase's own Pattern Entry Gate, deferring `DefinitionStore` entirely, and the
`TaskKind`-as-computed-not-stored/synthetic-fast-path design) were folded in before implementation began,
and are recorded in full in `IMPLEMENTATION.md`'s own Phase 10 entry and `LESSONS_LEARNED.md` #72.

**The one real scope call, stated plainly:** typed edges (`EdgeType`: `EdgeTypeOnSuccess`/
`EdgeTypeOnFailure`/`EdgeTypeAlways`) were added as vocabulary only. Wiring `Task.Rescue`/`Task.Always` into
real `Adjacency` edges and teaching `Executor` to route on outcome was investigated and deliberately not
built this phase: `LevelIterator` computes static, outcome-independent reachability once up front, and
`Executor.Run` aborts its whole walk on any failure rather than routing around it, so the "light" version of
this wiring would have been an active correctness regression (`Rescue` firing on the happy path, never on
failure), not merely an inert one — a pressure-test finding, not a guess. This reopens Phase W5-sized
territory and is named as a separate, explicit follow-up rather than folded in silently. `DefinitionStore`
was deferred for a related but distinct reason: it has no consumer anywhere in the repo and no entry in
Section 25's Shared Primitives table (the one place a "declare now, build later" carve-out is sanctioned),
so building it now would be exactly the "port with no callers is a decoration" failure this phase's own
Adversarial Pattern Justification line warns against. Both corrections are recorded as dated corrections in
`IMPLEMENTATION.md`/`PATTERNS.md`, not silently reinterpreted.

**What was built, by area:**

1. **`internal/engine/task_kind.go`** (new). `TaskKind` (an iota enum: `TaskKindLeaf`/`TaskKindBlock`/
   `TaskKindParallel`/`TaskKindSynthetic`/`TaskKindInvalid`), computed via `Task.Kind()` from field
   presence (`taskShape`, the one shared derivation `Kind` and `validateTask`'s own precise-conflict
   diagnosis both build on) rather than stored — a stored field would be a second source of truth that
   could drift from the fields it describes, the same reasoning `DAG.Version` (below) follows.
2. **`internal/engine/dag.go`**. `Task.Parallel []Task` (mirrors `Block`'s shape exactly, not `PLAN.md`
   Section 14's stale bare-string example) plus an unexported `synthetic bool` field, set only by
   `registerSyntheticNode`. `EdgeType`/`EdgeConfig.Type` (see scope note above). `DAG.Version string`,
   `"sha256:" + hex(sha256(json.Marshal(resolvedDef)))`, computed in `buildFromDef` after
   `resolveImportTasks` so the hash reflects the fully-resolved definition, not just one file's own bytes.
   `hasCycle` rewritten from recursive to an iterative DFS (`dfsFrame`/`dfsColor`, an explicit stack),
   removing its recursion-depth risk entirely rather than capping it.
3. **`internal/engine/tasktree.go`**. `validateTask` is now a 3-way switch (via `Kind()`/`taskShape`)
   instead of a 2-way boolean check, and gained a real rescue/always-guard rule for `Parallel` (Block-only,
   deliberately — Ansible has no established parallel-failure-handling vocabulary to mirror).
   `synthesizeChain` refactored: its per-task splice logic moved into a new `synthesizeOne`, shared by the
   ordinary list-stitching path and the new `synthesizeParallel` (a synthetic fan-out node feeding every
   `Parallel` child's own independently-synthesized chain, each child's own exit feeding a synthetic join
   node — `id+".fanout"`/`id+".join"`, registered via `registerSyntheticNode`, bypassing `validateTask`
   since a synthetic node is not user input). `LevelIterator`/`TopologicalOrder`/`reachableWithInDegree`
   needed **zero** change: `TestLevelIterator_Diamond` already proved multi-parent/multi-child grouping
   worked; only `Builder` needed to learn to produce that shape.
4. **`internal/engine/import_tasks.go`**. `maxImportDepth` renamed `maxTaskNestingDepth` (value unchanged,
   32) and now bounds two risks with one shared counter: import-hop-chain length (its original purpose) and
   plain `block`/`rescue`/`always`/`parallel` nesting depth (a second, structurally identical unbounded-
   recursion site found while researching the named checklist item, not previously called out — fixed in
   the same pass). Because `resolveImportTasksInList` runs first, unconditionally, on the complete tree
   before `tasktree.go`'s own recursion ever sees it, this one check transitively bounds both — no second
   check was added there.
5. **`internal/engine/executor.go`**. `runNode` gained exactly one addition: a fast path that returns
   immediately for a `TaskKindSynthetic` node, skipping condition-check/lock/action-dispatch/publish. This
   is the *only* `Executor` change this phase makes — explicitly not the rescue/always-routing change
   described in the scope note above.
6. **`internal/engine/task_syntax.go`**. `parallel` added to `reservedTaskKeys` and both normalizers'
   recursion lists (YAML and JSON paths) — a real gap caught by the test suite itself: without this, module-
   as-key sugar detection misread a task's `parallel:` list as an unrecognized sugar key and failed with a
   confusing "module must be an object of arguments" error.
7. **`cmd/pleiades/run.go`**. `printTaskList` now switches on `Kind()` and prints a `parallel:` section,
   mirroring `block:`.
8. **Tests** (all new unless noted): `task_kind_test.go`, `dag_internal_test.go` (package `engine`, not
   `engine_test` — the only way to hand-build a genuinely cyclic `*DAG` and prove `hasCycle`'s rewrite both
   still detects it and doesn't stack-overflow on a 500,000-node chain), `dag_test.go` (`Parallel`
   structural tests, `Version` stability/change tests, nesting-depth-bound tests, a 20,000-flat-task
   `Build()` stack-safety test), `tasktree_test.go` (parallel ID-scheme test), `executor_test.go`
   (`TestExecutor_Parallel_RunsConcurrently`, a real `Executor.Run`-level high-water-mark proof of genuine
   concurrency, and that the `ActionExecutor` is called exactly `childCount` times, never `childCount+2` —
   proving the synthetic fast path is provably inert, not just present), `task_syntax_test.go` (parallel
   sugar-conflict and nested-sugar tests), `cmd/pleiades/run_test.go` (new file; `printTaskList` had no
   prior test coverage at all). `dag_fuzz_test.go`'s seed corpus extended with `parallel` shapes and a
   500-level adversarial nesting seed. `dag_bench_test.go` gained two new benchmarks (below).

**Verification.** `go build ./... && go vet ./...` clean, `gofmt -l` clean on every file this session
touched (one pre-existing, unrelated `gofmt` finding in `executor_fuzz_test.go` confirmed via a disposable
`git stash -u` to predate this session — left alone, not this phase's to fix). `go test ./internal/engine/...
./cmd/pleiades/... ./internal/validate/... -race -count=1` clean. A full `go test ./... -count=1` was clean
except `internal/lock`'s `TestNatsLease_ExclusiveKeepAliveAfterRelease` (the established testcontainers
port-mapping flake category, `FAILURE_PATTERNS.md` #61's own precedent; passed cleanly in isolation,
re-confirmed). Fuzz: `FuzzDAGBuilder` ~380,000 executions/20s on the extended seed corpus, zero crashes.
Benchmarks (real numbers, this machine): `BenchmarkDAGBuilder_LargeFlatTaskList` (10,000 flat tasks) ~43.0
ms/op; `BenchmarkDAGBuilder_Parallel` (100 children) ~409 µs/op, against the pre-existing
`BenchmarkDAGBuilder` (5 tasks, one CEL condition) ~142 µs/op. No credible existing published AWX/Tower/
raw-topological-sort figure exists to cite for either (`AGENTS.md`'s benchmarking rule); stated plainly
rather than fabricated. `make gosec` (7 pre-existing findings, all individually waived, zero new). `make
govulncheck` (0 called vulnerabilities). `make coverage`: `internal/engine` 91.9% → 92.8% (floor raised to
92.5), `cmd/pleiades` 58.2% (pre-existing, undocumented drift above its stale 44.0 floor) → 61.5% (floor
raised to 61.0). The same four pre-existing `coverage-floor.json` regressions from `FAILURE_PATTERNS.md`
#60 (`internal/forge/genutil`, `internal/inventory/record`, `pkg/collection`, `tools/gencatalog`) recurred
at the identical percentages, reconfirmed via a fresh `git stash -u` baseline check (this session's own
new files are untracked, so a plain `git stash` without `-u` is insufficient and was caught mid-check)
— predate and are unrelated to this phase. Real, end-to-end proof against the actual built `pleiades`
binary and real code paths (RULE 0), not only `go test`: `TestExecutor_Parallel_RunsConcurrently` drives
the real `Executor.Run`/`LevelIterator`/`Builder` chain end to end with a `parallel:` runbook, and a hand-
built cyclic `*DAG` (`dag_internal_test.go`) proves `hasCycle`'s own correctness survived its rewrite,
since `Builder` structurally cannot construct a cycle through any authoring surface it exposes.

**Also touched, incidentally, while implementing this phase's real gaps (not scope creep — each is a
correctness bug this phase's own new code would otherwise have silently mismatched with):** `internal/
validate`'s four rules (`CapabilityRule`, `CollectionRule`, `LifecycleRule`, `blast_radius.go`) were audited
against `Parallel`'s new synthetic nodes and confirmed already-safe with no code change needed — every one
already treats an empty `FQCN`/nil `Params` as "no capability required, no target, skip," the identical
shape a block task's own ID has always had, so a synthetic node is nothing new to them.

**Files changed:** `internal/engine/{dag,tasktree,executor,import_tasks,task_syntax}.go`,
`internal/engine/task_kind.go` (new), `internal/engine/{dag_test,dag_fuzz_test,dag_bench_test,
tasktree_test,executor_test,task_syntax_test}.go`, `internal/engine/{dag_internal_test,task_kind_test}.go`
(new), `cmd/pleiades/run.go`, `cmd/pleiades/run_test.go` (new), `.SPECIFICATION/{IMPLEMENTATION,PATTERNS}.md`,
`coverage-floor.json` (`internal/engine`/`cmd/pleiades` floors raised), `FAILURE_PATTERNS.md` (#62 new),
`LESSONS_LEARNED.md` (#72 new).

## Previous session: Phase 9, Google CEL Engine

**What was built:** see "Phase 9: Google CEL Engine session" immediately below for the complete
file-by-file summary. Everything from "Previous session: Phase 8, RBAC & Identity Validation" onward
describes earlier sessions and is unchanged.

### Phase 9: Google CEL Engine session

**This session closed Phase 9: Google CEL Engine** (`.SPECIFICATION/IMPLEMENTATION.md`), all five
previously-open checklist items plus the Release Gate (three items — cel-go import, `engine.Evaluator`,
string-to-AST compile logic — were already `[x]` from an earlier session). Research was direct rather than
agent-delegated (small, well-bounded surface: `internal/engine/cel.go`, `conditional.go`, `executor.go`,
`workflow_context.go`, `trigger.go`, `dag.go`/`tasktree.go`, plus `PLAN.md` Sections 21/27 and every real
`Program.Eval`/`ConditionProgram.Eval`/`Conditional.Compile`/`Evaluator.Compile`/`NewCELEvaluator` call
site read directly), then one Plan agent pressure-tested the resulting design against the real repo before
any plan file was written — its findings (an exact 14-real-call-site inventory versus a much larger but
harmless 49-call-site `NewCELEvaluator` count, cel-go v0.30.0's README/source confirming compiled
`cel.Program` is safe for concurrent `Eval`, cel-go's map-macro key-iteration semantics verified by reading
`common/types/map.go` directly, and three real gaps — a benchmark whose meaning the new cache would
silently invalidate, a stale doc comment, and an untested concurrency claim) were folded in before
implementation began.

**The core gap this phase closed:** only `stat` was declared in the CEL environment, and `Program.Eval`
hardcoded every caller's input under that one name, so neither of `PLAN.md` Sections 21.4/27.3's own
illustrative `nodes.`-rooted expressions could even compile. Research found the two spec examples
themselves mutually inconsistent (`nodes.precheck.stats.devices.all(...)` vs.
`nodes.precheck.devices.exists(...)`) and inconsistent with the real `WorkflowContext.Read()` shape
(`nodeID -> deviceID -> stats`, a map keyed by device ID, no `.devices` list anywhere). Verified by reading
cel-go's own source that `all`/`exists` over a CEL map iterate its keys, `nodes.precheck.exists(d,
nodes.precheck[d].needs_reboot == true)` is real, valid, tested CEL against the actual data shape with no
`WorkflowContext` restructuring needed — so, per this project's own established practice (Phase 7's
Selector, Phase 32's Registry, Phase 18's role names), `PLAN.md` Sections 21.4/27.3 got a dated correction
to the real, working spelling rather than the engine being bent to fit illustrative prose that was never
reachable as written.

**What was built, by area:**

1. **`internal/engine/cel.go`.** `NewCELEvaluator` now declares `nodes` (`cel.MapType(cel.StringType,
   cel.DynType)`) alongside the pre-existing `stat`, both bound by `Executor.runNode` to the identical
   `WorkflowContext.Read()` snapshot — a deliberate, stated scope choice (every existing
   `stat.precheck[""].x` runbook/test keeps working unchanged; `nodes.precheck[""].x` becomes newly valid
   too; narrowing `stat` to a genuinely different, current-device-only meaning is left to a future phase).
   `Program.Eval`'s contract changed from "wrap my input under `stat`" to "my input IS the top-level CEL
   activation" (`celProgram.Eval` no longer wraps anything — a behavioral change with no Go signature
   change, so it compiles everywhere but every caller's *values* needed updating). `celEvaluator.Compile`
   is now a Flyweight cache keyed by raw expression string (mutex-guarded map, store-if-absent so
   concurrent first-time compiles of new text converge on one shared winner), safe per cel-go's own
   documented "stateless, thread-safe, and cachable" guarantee for a compiled `cel.Program`.
2. **`internal/engine/executor.go`.** `runNode` now builds the activation explicitly:
   `map[string]interface{}{"stat": tree, "nodes": tree}`, both aliased to the one `WorkflowContext.Read()`
   call.
3. **`internal/engine/workflow_context.go`, `conditional.go`.** Doc comments updated for the new
   contract (Gap B from the pressure-test); no logic change in `conditional.go` (`ConditionProgram.Eval`
   already forwarded its map verbatim).
4. **Tests.** All 14 real `Program.Eval`/`ConditionProgram.Eval` call sites needing the new
   activation-map shape updated (`cel_test.go` x2, `cel_bench_test.go` x1, `conditional_test.go` x10,
   `executor.go`'s production call site) — the pressure-test's exhaustive grep found this exact count, not
   the larger set naming whole files would have implied. New: `TestCELEngine_NodesVariable`,
   `TestCELEngine_CompileSharesProgramForIdenticalExpressions`,
   `TestCELEngine_ConcurrentCompileConvergesOnOneSharedProgram` (32-goroutine race, `-race`-clean, Gap C
   from the pressure-test), `TestExecutor_ConditionalBranch_NodesVariable` (the same three-task shape as
   the pre-existing `..._ReleaseGate` test, `nodes.` in place of `stat.`, proving the real `Executor` call
   site, not just the bare primitives). `dag_bench_test.go`'s `BenchmarkDAGBuilder` (Gap A) now varies its
   condition text per iteration so the new cache doesn't silently turn it into a cache-hit benchmark
   contradicting its own doc comment (`LESSONS_LEARNED.md` #67). `cel_fuzz_test.go`'s seed corpus gained
   three `nodes.`-rooted seeds.
5. **Docs.** `PATTERNS.md`'s Flyweight entry flipped NO → YES, narrowly, pointing at the new cache,
   without contradicting its existing (separate, still-true) device-struct reasoning. `PLAN.md` Sections
   21.4 and 27.3 dated-corrected as described above. `IMPLEMENTATION.md` Phase 9 checked off in full with
   inline evidence.

**Verification.** `go build ./... && go vet ./...` clean, `gofmt -l` clean. `go test
./internal/engine/... -race -count=1` clean. Benchmarks (real numbers, this machine):
`BenchmarkCELCompile_Cached` ~25.8 ns/op vs. `BenchmarkCELCompile_Uncached` ~57,419 ns/op (~2,200x),
the direct evidence the "compile per evaluation defeats the microsecond claim" gap is closed;
`BenchmarkCELEval` ~1,192 ns/op. No credible existing published figure for this specific comparison exists
to cite (`AGENTS.md`'s benchmarking rule); stated plainly rather than fabricated. Fuzz:
`FuzzCELCompile` ~165,000 executions/21s, zero crashes. `make gosec` (7 pre-existing findings, all
individually waived, zero new). `make govulncheck` (0 called vulnerabilities). `make coverage`:
`internal/engine` 91.5% → 91.9%, floor raised to match. A full `go test ./... -race -count=1` and a
separate `go test ./... -cover -count=1` each flaked on a different, non-overlapping subset of
`internal/event`/`internal/lock`/`tests/e2e` (real-container tests, zero overlap with this phase's diff,
confirmed by `git status`); each of the four failing tests passed cleanly in isolation, and `go test ./...
-p 4` (capping package-level parallelism) ran the identical full suite clean twice — a concrete,
actionable mitigation for this environment's container contention, newly recorded as
`FAILURE_PATTERNS.md` #61 rather than left as only a narrative mention. The four pre-existing
`coverage-floor.json` regressions from `FAILURE_PATTERNS.md` #60 (`internal/forge/genutil`,
`internal/inventory/record`, `pkg/collection`, `tools/gencatalog`) recurred at the identical percentages,
confirming (again) they predate and are unrelated to this phase. Real, end-to-end proof against the
actual built `pleiades` binary (RULE 0), not only `go test`: a `pleiades init`-scaffolded project running
a runbook that registers `precheck` and gates `reboot`/`skip-me` on `nodes.precheck[""].needs_reboot`
produced `tasks[1]: changed` and `tasks[2]: skipped (when_cel \`nodes.precheck[""].needs_reboot ==
false\` evaluated false)` via `pleiades run`, with `pleiades validate` passing clean on the same runbook.

**Files changed:** `internal/engine/{cel,executor,workflow_context,conditional}.go`,
`internal/engine/{cel_test,cel_bench_test,cel_fuzz_test,conditional_test,executor_test,dag_bench_test}.go`,
`.SPECIFICATION/{PATTERNS,PLAN,IMPLEMENTATION}.md`, `coverage-floor.json` (`internal/engine` floor raised),
`FAILURE_PATTERNS.md` (#61 new), `LESSONS_LEARNED.md` (#67 new).

## Previous session: Phase 8, RBAC & Identity Validation

**What was built:** see "Phase 8: RBAC & Identity Validation session" immediately below for the complete
file-by-file summary. Everything from "The Cisco Catalyst Center Sync Plugin session" onward describes
earlier sessions and is unchanged.

### Phase 8: RBAC & Identity Validation session

**This session closed Phase 8: RBAC & Identity Validation** (`.SPECIFICATION/IMPLEMENTATION.md`), all
twelve checklist items plus the four verification gates. Planning followed this project's established
ritual: two parallel Explore-agent research passes (the real `internal/auth`/ent code and every real call
site; the spec corpus — `PLAN.md` Section 18/32, `CODE_SCAFFOLD.md`, `IMPLEMENTATION.md` Part VIII and
Phase 6/12/14/49's cross-references, `PATTERNS.md`, prior `FAILURE_PATTERNS.md`/`LESSONS_LEARNED.md`
entries) fed a design, which one Plan agent then pressure-tested against the real repo before any plan
file was written — its findings (the migration tool's silent `WithDropColumn` default, four missed
`NewJWTEvaluator` call sites inside `internal/auth` itself, the exact `pkg/policy` combine shape, the
sticky-Deny-vs-plain-Override judgment call, `RoleBinding.scope_id`'s int type) were folded in before
implementation began.

**What was built, by area:**

1. **Role name reconciliation.** `PLAN.md` Section 18.2's prose corrected from `Viewer/Executor/Admin` to
   `Viewer/Operator/Admin` (dated correction), since the roadmap's own Phase 8 line and the code
   (`RoleViewer`/`RoleOperator`/`RoleAdmin`) already agreed; no identifier rename.
2. **`auth.KeyProvider`** (`internal/auth/keyprovider.go`, `jwks.go`): `NewStaticKeyProvider` (a
   development-only symmetric secret, rejecting nil/empty/<32-byte keys at construction) and
   `NewJWKSKeyProvider` (a real RFC 7517 JWKS fetch/cache/rotate client, hand-rolled against the standard
   library, no new dependency — caches by `kid`, one bounded refetch on an unknown `kid`, never lets a
   failed/empty refresh discard a good cache).
3. **`internal/auth/jwt.go` reworked.** `NewJWTEvaluator(provider KeyProvider, issuer, audience string)
   (Evaluator, error)` pins issuer, audience, `jwt.WithExpirationRequired()`, and
   `jwt.WithValidMethods(provider.Algorithms())`. `Evaluator`'s two existing methods keep their
   signatures unchanged, so `internal/api/dispatcher.go`/`middleware.go` needed no edits at all.
4. **`Team`/`RoleBinding` ent schema** (new `internal/ent/schema/team.go`, `role_binding.go`; edited
   `user.go`, `organization.go`). `User.role` deleted outright (zero real consumers, confirmed by grep —
   the literal orphaned-permission anti-pattern `PLAN.md` Section 18.2 forbids). Migration
   `0003_add_rbac_teams.sql`, generated via `internal/ent/migrate/gen/main.go` after adding
   `schema.WithDropColumn(true)` to its diff call (its previous zero-option call would have silently kept
   the dropped column — `FAILURE_PATTERNS.md` #59).
5. **`auth.ScopeResolver`** (`internal/auth/scope.go`, `rolebinding_repository.go` (port),
   `ent_role_binding_repository.go` (ent adapter)): PLAN.md Section 18.4's four scopes folded via
   `pkg/policy.Resolve` — Phase 6's shared resolver, the RBAC-scope call site `PLAN.md` Section 25 itself
   named, closing the last of its eight named call sites with no consumer — through
   `combineScopeDecision`, a deliberate sticky-first-Deny variant of the `simulate-locked` terminal-lock
   idiom: once any level sets Deny, no later, more specific level (even an explicit Allow) can undo it, a
   stated departure from plain `policy.Override`.
6. **`auth.AdmissionChain`/`AdmissionRule`/`Recorder`** (`internal/auth/chain.go`,
   `ent_team_lookup.go`): a fail-closed Chain of Responsibility (`NewTokenScopeRule` wrapping the
   pre-existing scope-string check, `NewScopeRule` wrapping `ScopeResolver`) composed with a `slog`-backed
   `Recorder` (Audit Trail), built generically enough for Phase 49 to append a Step-Up rule later without
   rework — Step-Up itself deliberately not built here, matching `IMPLEMENTATION.md`'s own Phase 49
   cross-reference.
7. **`cmd/controller/main.go`** wired: `loadKeyProvider()` selects `NewJWKSKeyProvider` when `JWKS_URL`
   is set, else `NewStaticKeyProvider` from `JWT_SECRET`; `JWT_ISSUER`/`JWT_AUDIENCE` via the existing
   `getenv` helper (optional, non-empty defaulted, not a new required env var, so
   `leader_election_release_gate_test.go`'s subprocess spawn kept working unmodified).

**Two real, unrelated findings from this session's own verification, not from Phase 8's diff itself.**
(1) The migration-generation tool's `WithDropColumn` default trap above (`FAILURE_PATTERNS.md` #59,
`LESSONS_LEARNED.md` #65). (2) Four packages this phase never touched
(`internal/forge/genutil`, `internal/inventory/record`, `pkg/collection`, `tools/gencatalog`) were already
below their `coverage-floor.json` floors before this session started, confirmed via a disposable
`git worktree add --detach <base-commit>` measuring the identical percentages
(`FAILURE_PATTERNS.md` #60, `LESSONS_LEARNED.md` #66) — recorded, not fixed (scope creep) and not hidden
(floors were not lowered).

**Verification.** `go build ./... && go vet ./...` clean, `gofmt -l` clean. `go test ./... -race`: clean
on a full run (a second full non-race run hit three different container-infrastructure flakes across three
separate attempts — `internal/lock`, `internal/event`, `tests/e2e`'s `TestGrandIntegration` — each
confirmed to pass in isolation and to be pre-existing/environmental, matching this project's own
documented flake category, not a regression). `make gosec` (7 pre-existing findings, all individually
waived, zero new). `make govulncheck` (0 called vulnerabilities). `make coverage`: `internal/auth` 87.5%
→ 90.8% (floor raised to 90.0); `internal/ent` 10.9% → 16.1% (floor raised to 16.0, restored by a new
`internal/ent/team_role_binding_test.go` exercising the new edges directly, matching
`group_organization_test.go`'s own convention); `internal/ent/rolebinding`/`internal/ent/team` added to
`excluded` (generated code, matching every sibling ent predicate subpackage). Real, end-to-end proof
against the actual built `pleiades-controller` binary (RULE 0), not only package tests:
`TestController_JWKS_RealServer_AcceptsValidRejectsForged` (`cmd/controller`) starts the real binary with
`JWKS_URL` pointed at a real `httptest.Server` and real NATS (testcontainers), and proves a request with
no token, and one signed by a key never published to that server, both get a real `401 Unauthorized` from
the real mounted route, while one signed by the real, published key does not. Fuzz: `FuzzParseJWK` (new,
white-box, 15s/~510K executions, zero crashes) and `FuzzJWTParsing` (extended to the new surface,
15s/~440K executions, zero crashes). Benchmark: `BenchmarkValidateToken_HMAC` (~8.9us/op) vs.
`BenchmarkValidateToken_RSA` (~48.7us/op, real JWKS fetch + RSA verification, the honest measured cost of
moving off a shared secret).

**Files changed:** `internal/auth/{keyprovider,jwks,scope,rolebinding_repository,
ent_role_binding_repository,chain,ent_team_lookup}.go` (new) plus matching `_test.go` files (new),
`internal/auth/{evaluator,jwt}.go` (jwt.go reworked; evaluator.go unchanged), `internal/auth/{jwt_test,
jwt_fuzz_test,jwt_bench_test}.go` (updated for the new constructor/claims), `internal/ent/schema/
{team,role_binding}.go` (new), `internal/ent/schema/{user,organization}.go` (edited),
`internal/ent/migrate/gen/main.go` (`WithDropColumn(true)`), `internal/ent/migrate/migrations/sqlite/
0003_add_rbac_teams.sql` (new, generated), `internal/ent/team_role_binding_test.go` (new),
`cmd/controller/main.go`, `cmd/controller/jwks_release_gate_test.go` (new),
`.SPECIFICATION/PLAN.md` (Section 18.2 dated correction), `.SPECIFICATION/PATTERNS.md` (Federated
Identity and Audit Trail POTENTIALLY→YES, Chain of Responsibility and Hierarchical Policy Resolver
entries extended, Step-Up Authentication's Phase 49 pointer made explicit),
`.SPECIFICATION/IMPLEMENTATION.md` (Phase 8 checked off in full), `FAILURE_PATTERNS.md` (#20 closed out,
#59/#60 new), `LESSONS_LEARNED.md` (#64/#65/#66 new), `coverage-floor.json` (`internal/auth`/`internal/ent`
floors raised, two new `excluded` entries).

## Previous session: The Cisco Catalyst Center Sync Plugin

**What was built:** see "The Cisco Catalyst Center Sync Plugin session" immediately below for the
complete file-by-file summary. Everything from "The Phase 7 session" onward describes earlier sessions
and is unchanged.

### The Cisco Catalyst Center Sync Plugin session

**This session built the first real inventory sync plugin, Cisco Catalyst Center, and everything it
turned out to depend on.** It is the reference design the user asked for, and it was generated by the
Forge rather than hand-written: a new `pleiades forge new-plugin` scaffold emitted the package, and the
implementation was filled into that skeleton. It is verified against Cisco's public DevNet sandbox at
`sandboxdnac.cisco.com`, not against a mock.

**What is new, in dependency order.**

1. `internal/inventory/syncplugin`: `PLAN.md` Section 6a's four-method port (`Connect`, `Discover`,
   `Classify`, `Sync`, plus `Close`), its supporting types (`Config`, `Classification`,
   `Reconciliation`, `RecordIterator`), a `pkg/registry`-backed plugin registry, and `Reconcile`, the
   shared driver every plugin delegates to so "added", "updated", and "conflict" mean the same thing
   everywhere. The port was built against two deliberately unalike consumers at once; see
   LESSONS_LEARNED.md #63 for why that mattered.
2. `Repository.Create`, on both adapters. The port had no create operation at all, so nothing could
   onboard a device (FAILURE_PATTERNS.md #56). `ErrItemNotFound`, `ErrItemExists`, and
   `record.Base.DeviceType` came with it, each closing a hole the same gap had hidden.
3. `internal/forge/pluginscaffold` and `pleiades forge new-plugin`: the Forge's eighth capability,
   modeled file-for-file on `collectionscaffold`. Its generated output compiled and passed its own
   generated tests unmodified on the first run.
4. `pkg/catalystcenter`: a read-only REST client (auth with token caching and 401 refresh, paged device
   listing, sites, tags). It lives under `pkg/` because a Collection may import only `pkg/`, a rule that
   was documented in three places and enforced nowhere until this session added
   `TestCatalogPackagesImportOnlyPkg`.
5. `internal/inventory/plugins/{staticyaml,catalystcenter}` and their composition root. `StaticYAMLPlugin`
   moved out of `internal/inventory` and now implements the real port it previously declined to.
6. The read-only flag at both levels the user asked for: `Config.ReadOnly` (devices land
   `StateSimulateLocked`; the client has no write path at all) and `inventory.NewReadOnlyRepository`
   (every write refused with `ErrInventoryReadOnly`). `pleiades inventory sync --read-only` is a dry run
   reporting `would add` / `would update`, which took a second iteration to get right
   (FAILURE_PATTERNS.md #58).
7. `pleiades inventory sync` and `pleiades inventory plugins`, the user-facing surface.
8. The collection dispatch bridge. `pkg/collection` was planning-time metadata only: 71 registered
   methods and no execution path that could call one. `collection.Descriptor` now carries `Invoke`, and
   `engine.NewCollectionActionExecutor` resolves a task's FQCN through the registry, composing over the
   existing transport and builtin executors.
9. The four `net.catalyst.*` methods, generated by `forge new-collection` and then implemented. They are
   the first entries in the catalog to reach `status: implemented`, which moved the count from 71 to 75.

**Four real defects were found by running the thing rather than by reading it**, and all four are
written up: a cross-package test-cleanup race that had been failing roughly one full-suite run in three
(#55), the missing `Create` (#56), a file repository that named its own storage backend as the
authoritative sync plugin so every host synced as a conflict (#57), and the read-only abort above (#58).
A fifth came from the fuzzer: an un-normalized base URL meant `Http://x` and `http://x` would onboard the
same controller twice. A sixth came from `goleak`: a failed `Connect` leaked the pooled connection it had
already opened.

**Verification.** `go test ./...` green, `-race` green. Live proof:
`PLEIADES_E2E_DNAC=1 go test -tags integration -race ./tests/e2e/ -run Catalyst` passes three tests
against the real sandbox (sync, idempotent re-sync, wrong-credential refusal), with `goleak`. The CLI
path was driven end to end by hand: `init`, `add-credential`, `inventory sync --read-only` (reports 5,
writes nothing), `inventory sync` (adds 5), `inventory sync` again (5 unchanged). Fuzzing ran clean at
1.5M executions on the base-URL target and 29k on the forge subcommand. `gosec` reports zero issues
across all 120 files this session touched, with no new waivers. Every measured package meets its
coverage floor; new floors were recorded for the seven new packages.

**Known gaps, stated rather than hidden.**

- `tools/gencatalog` cannot re-run on a dirty tree: `forge new-*` refuses to overwrite, which is correct
  protection for hand-edited files and means full regeneration needs the generated output removed first.
  The new artifacts this session were generated by driving the same CLI directly, one invocation at a
  time, which is what gencatalog itself does.
- A quarantined device is reported but not persisted, because building an item requires a device type and
  no generic unclassified type is registered. Section 6g's full quarantine bucket needs that type first.
- The 71 pre-existing catalog stubs still carry the older method signature. They compile and register
  correctly; they will pick up the new `Invoke`-carrying shape on the next full regeneration.
- `internal/lock` and `internal/transport/ssh` fail intermittently in this environment under parallel
  container load (testcontainers port mapping). Both pass in isolation and neither has any dependency on
  anything this session changed.

### Phase 7: The Iterator Pattern (session recap; full detail in "The Phase 7 session," immediately below)

**This session implemented Phase 7: The Iterator Pattern in full**, jumping back from Part VII (The
Forge of Hephaestus, closed through Phase 34) to close a Part II gap that had sat partially done since an
earlier session (`inventory.Iterator` and its ent hookup were already `[x]`; keyset pagination, the
`Selector`/`GetGroup` fix, and all four verification gates were not). Planning followed this project's
own established ritual: three parallel Explore-agent research passes (the real Go code, this project's own
spec docs, and Postgres/ent test conventions) fed a synthesis, direct reads of every load-bearing file
verified the research rather than trusting it, one Plan agent pressure-tested the resulting design against
the real repo, and its own findings (a call-site count that was wrong by 9, an import-aliasing trap, a
sampling-noise risk in the pprof design) were folded in before any plan file was written. All checklist
items are now `[x]`.

**Two checklist premises were checked against the real code before being trusted, and one was wrong.**
The "ctx shadowed by a context stored at construction" item's bug is real in history
(`git show de98a9a:internal/inventory/ent_repository.go`) but was already fixed by an unrelated commit
(`609dadd`) two sessions before this one landed; today's `entIterator` has no `ctx` field. Checked off with
a note, not re-implemented. The `device_id` column this phase paginates on, by contrast, really was
already built for exactly this moment: `internal/ent/schema/device.go`'s field and index doc comments
literally say "a future keyset-paginated listing (Phase 7) can/will page on this column," so this phase
needed zero ent schema change or migration, pure Go logic over existing schema.

**The literal named bug (`GetGroup` discards its group argument) is fixed by pushing a real `Group`
edge down to SQL, not the JSONB-field approach `PATTERNS.md`'s own Specification entry incorrectly
described.** `entRepository.GetGroup(ctx, sel inventory.Selector)` applies
`device.HasGroupsWith(group.NameEQ(sel.GroupName))` when `GroupName` is non-empty, closing the deferral
Phase 1's own session note recorded explicitly ("`Group`/`Organization` are honestly scoped as
schema-only substrate this phase... `Repository` exposes no traversal for either yet"). `Selector`
(`pkg/inventory/selector.go`, new) is a deliberately narrow, single-field value object, not the full
AND/OR/NOT predicate tree `CODE_SCAFFOLD.md`'s aspirational storage sketch warns a Selector must not be
("It is NOT a group name string, which cannot express Section 3 overlapping groups") — that warning is
about `PLAN.md` Section 22.3's future Virtual Groups syntax, a different, later phase's job, addressed
head-on in `IMPLEMENTATION.md`'s own Pattern Entry Gate note rather than silently sidestepped.

**A call-site count that looked complete at 4 was actually 13, caught by the Plan agent's pressure-test
before any code was written, not discovered mid-implementation.** `internal/inventory/factory_test.go`
(the exact test `IMPLEMENTATION.md`'s Phase W4 note cites as closed Release-Gate evidence),
`file_repository_test.go`, `file_repository_bench_test.go`, and `file_repository_errors_test.go` (8 call
sites across the last three, none of which imported `pkg/inventory` before this phase) all called
`GetGroup` too. Three different, already-established import-alias conventions coexist in this package's
test files (`pkginventory`, `baseinventory` in `factory_test.go`, `pkginv` in `ent_save_test.go`, none of
which this phase touched); each file's own existing convention was matched rather than a single alias
imposed everywhere.

**A previously-green integration test was proven to have never actually tested what it appeared to.**
`tests/e2e/integration_test.go`'s `TestGrandIntegration` (real Postgres, real NATS, `testcontainers-go`)
failed the moment `GetGroup` started really filtering: it had tagged devices with a `properties["group"]`
key nothing had ever read, and passed only because the old code streamed everything regardless. Fixed by
attaching the seeded devices to a real ent `Group`, the mechanism a `Selector` now actually matches
against; passes against a real Postgres container. `FAILURE_PATTERNS.md` #54, `LESSONS_LEARNED.md` #58.

**The Release Gate's literal wording ("`pprof` proves... completely flat") is met literally, not just in
spirit.** The pre-existing `TestIteratorMemoryFlatline` proved flatness via `runtime.MemStats`, a coarser
two-point signal, not `pprof`. New `TestIteratorHeapProfileStaysFlat` calls
`pprof.Lookup("heap").WriteTo(w, 1)` (Go's documented debug=1 legacy text format, not
`pprof.WriteHeapProfile`, which always writes the unparseable-without-a-new-dependency protobuf format at
debug=0) at five checkpoints across a real 50,000-device stream, with `runtime.MemProfileRate` set to 1
for the duration of the test so the normally-sampled heap profiler reports exact, not noisy, numbers.
Measured spread on a real run: 0.00 MB.

**What was built:** see "The Phase 7 session" immediately below for the complete file-by-file summary.
Everything from "The Phase 34 session" onward describes earlier sessions and is unchanged.

### The Phase 7 session

**Scope: Phase 7 in full** (`.SPECIFICATION/IMPLEMENTATION.md`), picked up after Part VII (The Forge of
Hephaestus) closed through Phase 34, per the user's explicit direction to come back to it. See "Current
Status" above for the complete summary; this heading exists so future sessions can find this session's
detail without re-reading the whole file.

**Research and design, before any code:** three parallel Explore-agent research passes (the real
`inventory.Iterator`/`Repository`/`entIterator`/`fileRepository` code and every real caller, this
project's own spec docs — `PLAN.md`, `PATTERNS.md`, `CODE_SCAFFOLD.md`, every other `IMPLEMENTATION.md`
phase referencing this substrate — and this repo's Postgres/ent test conventions, benchmark style, and
`runtime/pprof` usage, or lack of it) fed a synthesized design. Every load-bearing claim from that
synthesis was then re-verified by directly reading the actual files (`iterator.go`, `ent_repository.go`,
`file_repository.go`, `pkg/inventory/item.go`, `dispatcher.go`, `dispatcher_test.go`, all four existing
iterator test files, `device.go`/`group.go` ent schemas, the generated `device`/`group` predicate and
order helpers), not trusted from the research agents' summaries alone. One Plan agent then pressure-tested
the resulting design against the real repo (see "Current Status" above for its key findings) before the
plan file was written and approved.

**What was built, by area:**

- **`pkg/inventory/selector.go`** (new). `Selector{GroupName string}`, zero value selects every device.
  Deliberately narrow: a real, SQL-pushdown-capable Specification-shaped value object, not yet
  `PLAN.md` Section 22.3's future composable AND/OR/NOT predicate tree.
- **`internal/inventory/iterator.go`**: `Repository.GetGroup`'s second parameter changed from
  `groupName string` to `sel inventory.Selector`.
- **`internal/inventory/ent_repository.go`**: `entRepository.GetGroup` now applies
  `Order(device.ByDeviceID())` once at construction and, when `sel.GroupName != ""`,
  `Where(device.HasGroupsWith(group.NameEQ(sel.GroupName)))` (an `EXISTS` subquery, not a join — no
  duplicate-row risk). `entIterator`'s `offset int` field became `cursor string` (last-seen `device_id`);
  `Next` now batches via `Clone().Limit(batchSize)` plus a conditional `Where(device.DeviceIDGT(cursor))`,
  replacing `Limit(batchSize).Offset(offset)`. No "exhausted" flag added — EOF is still "the batch fetch
  returned zero rows," the same idempotent shape the pre-existing code already had.
- **`internal/inventory/file_repository.go`**: `fileRepository.GetGroup` signature changed identically;
  behavior did not, since `HostSpec` has no group-membership field at all. Doc comment updated to explain
  why, and `TestFileRepository_Selector_GroupNameIgnored` (`file_repository_test.go`) pins the behavior
  down so a future change cannot silently start erroring on it.
- **13 call sites updated** (production: `internal/api/dispatcher.go` — new aliased `pkginventory`
  import, `cmd/pleiades/load.go`; tests: `dispatcher_test.go`'s two mocks, and 9 call sites across
  `internal/inventory`'s `iterator_test.go`, `iterator_bench_test.go`, `iterator_fuzz_test.go`,
  `repository_conformance_test.go`, `factory_test.go` (its own `baseinventory` alias),
  `file_repository_test.go`, `file_repository_bench_test.go`, `file_repository_errors_test.go`).
- **New tests**: `internal/inventory/ent_repository_selector_test.go`
  (`TestEntRepository_GetGroup_FiltersBySelectorGroupName` — a named group returns exactly its members,
  an empty selector returns everything, a nonexistent group fails closed to zero devices, never open to
  the whole fleet; `TestEntIterator_KeysetPaginationSurvivesConcurrentWrites` — 1,500 devices with
  explicit sequential `device_id`s, a mid-stream delete of an already-yielded row and an insert ahead of
  the cursor, asserting zero duplicates/skips). `internal/api/dispatcher_selector_test.go`
  (`TestDispatcher_GetGroupSelector_FiltersAgainstRealRepository` — the same proof at the real
  HTTP-handler level, against a real ent-backed `Repository`, not the package's hand-written mocks).
  `internal/inventory/iterator_pprof_test.go` (`TestIteratorHeapProfileStaysFlat`, detailed above).
  `FuzzIteratorPagination`'s doc comment updated to describe the keyset mechanism it now fuzzes; its
  assertions were already sufficient (exact count match already proves no skip/duplicate in the
  single-writer case).
- **`tests/e2e/integration_test.go`** fixed, not just updated: see "Current Status" above.
- **Docs corrected**, matching this project's own "correct the map before/alongside the code" convention:
  `PLAN.md` Section 25's keyset-pagination row (`Build by: Phase 23` → `Phase 7`, dated
  `Correction (2026-08-05)`, Phase 23 remains a consumer). `PATTERNS.md`'s Specification entry (flipped
  from "POTENTIALLY" to "YES, narrowly," and its "Why" text corrected — group membership was never
  actually a JSONB field match, that was only ever a considered-and-rejected approach named in
  `GetGroup`'s own old comment). `PATTERNS.md`'s Repository entry (its quoted `GetGroup() (Iterator,
  error)` signature was already stale before this phase). `CODE_SCAFFOLD.md` Section C, narrowly (a new
  dated correction: `Selector`/`GetGroup` stay on `internal/inventory`, not the aspirational
  `internal/storage.DeviceRepository.Stream`; `internal/ent` is imported directly from
  `internal/inventory` today; the aspirational `StateStore` consolidation itself is left alone since no
  phase claims it as a deliverable).
- **`gosec-waivers.json`**: one waiver's line range shifted (145-150 → 146-151) when the new
  `pkginventory` import line was added to `dispatcher.go`; no code at that finding changed.

**Real, end-to-end verification against the actual built binary and a real Postgres container, not just
`go test`** (RULE 0): `TestDispatcher_GetGroupSelector_FiltersAgainstRealRepository` drives the real
`entRepository`/`entIterator`/`Dispatcher.DispatchRunbook` chain through real HTTP requests, two real ent
`Group`s, proving a named group dispatches to exactly its members and a nonexistent group dispatches to
zero, never the whole fleet. `TestGrandIntegration` (`tests/e2e`) proves the identical property against a
real Postgres container end to end, through the real NATS-backed agent pull loop.

**Fuzz/Stress:** `FuzzIteratorPagination` adapted to the keyset code, ~10,700 executions in 20s, zero
failures. `BenchmarkIterator` (10,000 devices via keyset batching): ~63ms/op. No credible existing
AWX/Tower/raw-Postgres keyset-pagination throughput figure exists to cite (`AGENTS.md`'s benchmarking
rule); stated plainly rather than fabricated, matching `internal/ent/embedded_bench_test.go`'s own
`[REFERENCE]` convention.

**Adversarial Pattern Justification:** the old `Limit(batchSize).Offset(offset)` query had no `ORDER BY`
at all, a defect independent of concurrency (SQL defines no row order without one, so two sequential
unordered queries are not even guaranteed to agree with each other on an untouched table). Not empirically
reproduced against the old code: its failure mode is implementation-defined per the SQL standard, so a
test forcing it to misbehave in one fixed direction would really only be asserting an accident of
SQLite's own undocumented rowid ordering, not a general property. The new keyset query is deterministic
by construction; `TestEntIterator_KeysetPaginationSurvivesConcurrentWrites` is the direct evidence.

**Schema/Injection Hardening:** the one new boundary, `dispatcher.go`'s HTTP `group` query parameter now
flowing into `device.HasGroupsWith(group.NameEQ(sel.GroupName))`, is fully parameterized by ent's
generated query builder (no string concatenation; confirmed by reading the generated code) — audited,
clean, no `FAILURE_PATTERNS.md` entry needed for the boundary itself.

**Release Gate:** verified against real code paths above, plus `go build ./... && go vet ./...` clean,
`gofmt -l` clean, `make gosec` (7 findings, all individually waived, none new beyond the line-shift
above), `make govulncheck` (0 called vulnerabilities), and `make coverage`/`coverage-check` (78 packages
measured, none below floor). Coverage: `internal/inventory` 81.8% → 82.5% (floor raised to 82.0);
`internal/api` 82.1% → 90.7% (floor raised to 90.0, the new real end-to-end dispatcher test's own
contribution); `pkg/inventory` unchanged at 97.7% (`Selector`'s zero-value struct added no coverable
branching statements).

`go test ./... -race -count=1`: every package this phase actually touched passes reliably, repeatedly,
in isolation (`internal/inventory`, `internal/api`, `pkg/inventory`, `cmd/pleiades`, `tests/e2e`, each
re-run individually multiple times with zero failures). The full concurrent `./...` run itself flaked on
five separate invocations across this session, twice via `coverage-check`'s own internal `go test`
call and three times via a direct `-race` run: `cmd/pleiades`'s `TestCLI_ForgeNewCollection_EndToEnd`
(four of the five, always with the exact `FAILURE_PATTERNS.md` #53 signature -- `internal/catalog/test/
e2egateNNNNN`: "cannot find package") and, once, `internal/api`'s unrelated `TestStreamLogs_ToleratesAckFailure`
(an SSE ack-timing test, 5/5 clean when re-run alone). Neither test is touched by this phase's diff;
`TestCLI_ForgeNewCollection_EndToEnd`'s failure mode is the pre-existing, already-documented race between
a real-tree-mutating e2e test and `internal/archtest`'s `go list` scan, and the lone `internal/api` flake
is consistent with system load during a many-container concurrent run, not a real regression -- both
categories this project already treats as a known, accepted risk with an established "run again" remedy,
not something this phase's own verification papers over.

**Adversarial review, before this phase was considered done.** An independent review agent (not the
implementer) audited the full diff against the real repo and found five real gaps, all fixed before this
session ended, none requiring a design change: (1) `entIterator`'s `cursor == ""` sentinel could not
distinguish "before the first row" from a legitimately empty `device_id` written outside this ent client
(the schema's `NotEmpty()` is an application-level check, not a DB `CHECK` constraint), which would have
looped `Next` forever on such a row; replaced with an explicit `started bool` field. (2) `entIterator.Next`
never checked `ctx.Err()` on its buffered fast path, a real divergence from `fileIterator.Next`'s identical
check given `fileRepository.go`'s own doc comment claims both adapters "must behave identically to
callers"; added, with `TestEntIterator_HonorsContextCancellation` as the regression test. (3)
`TestIteratorHeapProfileStaysFlat` could not actually detect its own target regression (a dropped batch
`Limit` holding the whole 50,000-row result set resident): comparing checkpoints only to each other, not to
a pre-iteration baseline, would show near-zero spread even with the whole set loaded, since every
checkpoint would sit on the same elevated plateau together; added a baseline-relative growth ceiling.
(4) `TestEntRepository_GetGroup_FiltersBySelectorGroupName`'s "named group" case asserted set membership,
not per-name count, so it could not distinguish two real devices from one device double-counted -- exactly
the duplicate-row failure mode the adjacent code comment claims immunity from; switched to a count-per-name
map. (5) `FuzzIteratorPagination`'s doc comment claimed proof against skips and duplicates, but the body
only compared a count, which cannot detect a skip-plus-duplicate pair that cancels out; switched to
collecting distinct `device_id`s into a set. The review also surfaced two real, deliberate scope
boundaries worth being explicit about rather than silent: `Selector{GroupName}` matches direct Group
membership only, not `PLAN.md` Section 3's group nesting (nothing in this codebase populates
`Group.children`/`parents` edges yet, so there is no live case to build traversal against); and a device
inserted with a `device_id` sorting *behind* the current cursor is not retroactively surfaced, the
inherent trade-off of any keyset cursor. Both are now stated directly in `ent_repository.go`'s own
comments rather than left implicit.

**Files changed:** `pkg/inventory/selector.go` (new), `internal/inventory/iterator.go`,
`internal/inventory/ent_repository.go`, `internal/inventory/file_repository.go`,
`internal/inventory/ent_repository_selector_test.go` (new), `internal/inventory/iterator_pprof_test.go`
(new), `internal/inventory/{iterator_test.go,iterator_bench_test.go,iterator_fuzz_test.go,
repository_conformance_test.go,factory_test.go,file_repository_test.go,file_repository_bench_test.go,
file_repository_errors_test.go}`, `internal/api/dispatcher.go`, `internal/api/dispatcher_test.go`,
`internal/api/dispatcher_selector_test.go` (new), `cmd/pleiades/load.go`, `tests/e2e/integration_test.go`,
`.SPECIFICATION/IMPLEMENTATION.md` (Phase 7 checked off in full), `.SPECIFICATION/PLAN.md` (Section 25
dated correction), `.SPECIFICATION/PATTERNS.md` (Specification and Repository entries corrected),
`.SPECIFICATION/CODE_SCAFFOLD.md` (Section C dated correction), `coverage-floor.json` (`internal/inventory`
and `internal/api` floors raised), `gosec-waivers.json` (one line-range shift), `FAILURE_PATTERNS.md`
(#54 new), `LESSONS_LEARNED.md` (#58 new). See "The Phase 34 session" immediately below for full detail.
Everything from "The Phase 33 session" onward describes earlier sessions and is unchanged.

### The Phase 34 session

**Scope: Phase 34 in full** (`.SPECIFICATION/IMPLEMENTATION.md`), the fifth phase of Part VII (The Forge
of Hephaestus) to close. See "Current Status" above for the complete summary; this heading exists so
future sessions can find this session's detail without re-reading the whole file.

**What was built, by area:**

- **`internal/forge/catalogdata`** (new package, 11 files: `doc.go` with the `//go:generate go run
  ../../../tools/gencatalog` directive, `collections.go` aggregating 8 per-table-section files into
  the exported `Collections` slice, `devices.go`). The single source of truth for the real catalog;
  editing it and re-running `go generate ./internal/forge/catalogdata` is this project's own established
  "edit the schema and regenerate" discipline (`internal/ent/generate.go`'s precedent), applied here for
  the first time to something other than ent.
- **`tools/gencatalog`** (new, `main.go` + `main_test.go`). Builds the real `pleiades` binary
  (`buildPleiadesBinary`, into a temp dir, cleaned up via a returned closure) and drives it through
  `forge new-collection`/`new-device` once per `catalogdata` entry (`newCollectionArgs`/`newDeviceArgs`
  construct the exact flag sets from each `Config`, `runPleiades` shells out via `exec.Command`).
  `validateCatalogEntries` runs every entry's own `Validate()` before touching the CLI or filesystem at
  all, so a data-table typo fails fast, named, before any partial (non-idempotent, since `forge new-*`
  refuses to overwrite) CLI run leaves half a catalog behind. Also regenerates
  `internal/catalog/builtins.go` (`writeCatalogBuiltins`, deduplicated and sorted from the same data,
  via `go/format.Source`, never hand-maintained). Own tests: pure-function argument-construction tests
  (no I/O), a `writeCatalogBuiltins` test against a synthetic small set, and one real end-to-end test
  (`TestGencatalog_DogfoodsRealCLI_EndToEnd`, `-short`-skippable) that drives the actual binary to
  generate one synthetic collection and device into the real repository tree, cleans up via
  `t.Cleanup`, and `go build`/`test`s the result, mirroring `cmd/pleiades/e2e_test.go`'s own release-gate
  test shape.
- **The real catalog, generated** (`internal/catalog/`, 27 packages, 71 methods, 142 files plus the
  regenerated `builtins.go`; `internal/inventory/devices/{windows,aws}/`, 2 new device packages).
  Every file is Phase 33's own unmodified template output; this phase supplied only the data
  (`catalogdata`) and drove the CLI (`gencatalog`) that produced it. `internal/inventory/builtins.go`
  gained two blank imports (`devices/aws`, `devices/windows`) by hand, the one manual step the scaffold
  itself never performs.
- **`internal/engine/import_tasks.go`** (new). `resolveImportTasks`/`resolveImportTasksInList`/
  `resolveOneImport`/`resolveImportPath`: a parse-time pre-pass, called as the first step of
  `buildFromDef` (`dag.go`, which gained a `baseDir string` parameter), that recursively rewrites any
  task with `fqcn: import_tasks` into an ordinary block task (`Block` = the referenced file's own,
  recursively-resolved task list; `FQCN`/`Params` cleared) before `synthesizeChain`/`collectSubtree`
  ever see it, requiring no change to either. `resolveImportPath` fails closed against an empty or
  absolute path and requires the resolved result to stay under the runbook's own base directory via
  `filepath.Rel` (no `..`-prefixed result accepted), the same closed-by-construction style
  `internal/forge/genutil.ValidateSegment` already uses; a `resolving` map catches an import cycle, and
  `maxImportDepth` (32) bounds a long, non-cyclic chain. `internal/engine/yaml.go` gained a shared
  `parseWorkflowYAML` helper (factored out of `BuildFromYAML` rather than duplicated) and a new
  `BuildFromYAMLFile(path)` that reads the file itself and passes `filepath.Dir(path)` as the base
  directory; `Build`/`BuildFromYAML` keep their existing signatures, passing `""`, and an `import_tasks`
  task reaching either fails with a clear, actionable error instead of silently misresolving a relative
  path. `cmd/pleiades/load.go`'s `loadWorld` now calls `BuildFromYAMLFile` directly, collapsing its own
  manual `os.ReadFile` + `BuildFromYAML` pair.
- **`internal/validate/collection_rule.go`** (new). `CollectionRule` mirrors `capability_rule.go`'s
  exact shape: skips any `task.FQCN` with no dot (every legacy built-in and engine keyword, closed by
  construction, since `pkg/collection.Register` itself refuses to register an undotted name), otherwise
  calls `collection.Lookup` and reports "not a registered collection name" or "declared but not yet
  implemented." Independent of `CapabilityRule`, which keys off the separate `engine.ActionCapability`
  map and has no notion of `pkg/collection` names.
- **`cmd/pleiades/catalog_builtins.go`** (new, one blank import of `internal/catalog`), the composition-root
  fix for defect (2) above.

**Real, end-to-end verification against the actual built binary, not just `go test`** (RULE 0): `pleiades
validate` against a runbook calling `pkg.apt.install` and a typo'd `totally.fake.name` produced
`[collection] node "tasks[0]" ...: calls "pkg.apt.install", which is declared but not yet implemented`
and `[collection] node "tasks[1]" ...: calls "totally.fake.name", which is not a registered collection
name`, exit code 1; a runbook using `import_tasks` to pull in a sibling file produced `validate: no
issues found`, exit code 0, and built the identical `*DAG` a hand-written inline `block:` would (the
actual regression test, not just a manual check).

**Fuzz/Stress:** `FuzzImportTasksPath` (`internal/engine`, seeded with `../../../etc/passwd`, an
absolute path, `..\..\windows\...`, an empty string, a NUL byte, and a planted sentinel file just
outside the runbook directory that must never appear in a successfully built DAG), 15s/~58K executions,
zero failures. `internal/validate/collection_rule_test.go`'s `TestCollectionRule_StressAllCatalogNames`
builds one task per real `catalogdata.Collections` entry (so it cannot drift out of sync with the actual
catalog) and asserts exactly one Finding each, plus a negative control over every legacy/engine-keyword
fqcn asserting zero. `internal/archtest/catalog_test.go` loads every one of the 71 manifests and both
device types through the real registries the generated files and their builtins aggregators feed.

**Adversarial Pattern Justification:** "no stub can report success" is structural (Phase 33's single,
unmodified template has exactly one `return` in every stub body, an error), proven per-package by each
generated file's own `Test<X>_NotImplemented`. "Every declared capability exists in `pkg/capability`" is
enforced three times over: `collectionscaffold.Config.Validate()`, `collection.Register()`'s own
`capability.Lookup` check, and, the strongest form, `catalogdata`'s typed `capability.Name` constants
making an unknown capability a compile error rather than a runtime one. The dispatcher question was
considered and deliberately not built: `pkg/collection.Descriptor` carries no function reference at
all, `sdk.RunbookContext` has zero implementations anywhere, and a real fqcn-keyed dispatcher
(`internal/engine`'s transport-binding executor) already exists on an incompatible raw-command shape;
building a second, incompatible dispatch mechanism next to it would itself be a new pattern, which this
phase's own Pattern Entry Gate forbids, and the Release Gate's literal wording is satisfied today for
free by the existing fallback's honest error.

**Schema/Injection Hardening:** the 71 stubs and 2 device types introduce no new boundary at all (a stub
touches no `params` before erroring; a device type is a structurally inert placeholder). The one real
new boundary, `import_tasks`' file path (Phase 39's "filesystem paths" category), audited clean: no
`FAILURE_PATTERNS.md` entry was needed for the boundary itself, closed by construction and proven by the
fuzz target and named escape/cycle regression tests above. Three unrelated, real findings were made and
fixed elsewhere during this phase's own construction and verification (see "Three real,
previously-unnoticed defects" above; `FAILURE_PATTERNS.md` #51-53).

**Release Gate:** verified against the real built binary (see above), plus `go build ./... && go vet
./...` clean, `go test ./...`/`go test -race ./...` passing (`make ci` run twice end to end, both clean;
one intervening plain `go test ./...` did hit `FAILURE_PATTERNS.md` #53's documented, accepted,
non-deterministic flake, not reproduced under `-race` or on either full `make ci` run), `make gosec`
(7 pre-existing findings, all individually waived, none new: `tools/gencatalog/main.go`'s two
subprocess-launching calls, the `go build` step and the built binary invocation, both carry an inline
`#nosec G204` justification, mirroring `cmd/pleiades/forge_scaffold_io.go`'s existing convention, rather
than a `gosec-waivers.json` entry), `make govulncheck` (0 called vulnerabilities), and `make coverage`
(78 packages
measured, none below floor; `cmd/runner` remains unrecorded, pre-existing, out of this phase's scope).
Coverage: all 27 generated catalog packages and `internal/forge/catalogdata` at 100.0%;
`internal/inventory/devices/{windows,aws}` at 80.0% (the devicescaffold template's own known
structural-skeleton gap, unchanged by this phase, not hand-patched since the output is generated and
never hand-edited); `tools/gencatalog` at 70.8%; `cmd/pleiades` rose from 43.0% to 44.2%, floor raised
to 44.0.

**Files changed:** `internal/forge/catalogdata/` (new, 11 files), `tools/gencatalog/` (new, `main.go`,
`main_test.go`), `internal/catalog/` (new, 142 generated files plus regenerated `builtins.go`),
`internal/inventory/devices/aws/` and `.../windows/` (new, 2 files each), `internal/inventory/builtins.go`
(two blank imports added), `pkg/capability/capabilities_windows.go` renamed to `capabilities_win.go`
(`git mv`, no content change), `internal/engine/import_tasks.go`, `import_tasks_test.go`,
`import_tasks_fuzz_test.go` (all new), `internal/engine/dag.go` (`buildFromDef` gained a `baseDir`
parameter), `internal/engine/yaml.go` (`parseWorkflowYAML` factored out, new `BuildFromYAMLFile`),
`cmd/pleiades/load.go` (`loadWorld` uses `BuildFromYAMLFile`), `cmd/pleiades/catalog_builtins.go` (new),
`internal/validate/collection_rule.go` and `collection_rule_test.go` (new), `internal/archtest/catalog_test.go`
(new), `.SPECIFICATION/IMPLEMENTATION.md` (Phase 34 checked off in full, two dated corrections),
`docs/hephaestus.md` (status line, workflow table, the catalog's own hedge retired, two new device types
documented, both real defects documented in place, command surface unchanged), `.SPECIFICATION/CODE_SCAFFOLD.md`
(`internal/forge/catalogdata`, `internal/catalog/`, `internal/inventory/devices/`, and a new top-level
`tools/` entry), `coverage-floor.json` (32 new/changed floors), `FAILURE_PATTERNS.md` (#51-53 new),
`LESSONS_LEARNED.md` (#55-57 new). See "The Phase 33 session" immediately below for full detail.
Everything from "The Phase 32 session" onward describes earlier sessions and is unchanged.

### The Phase 33 session

**Scope: Phase 33 in full** (`.SPECIFICATION/IMPLEMENTATION.md`), the fourth phase of Part VII (The
Forge of Hephaestus) to close. See "Current Status" above for the complete summary; this heading exists
so future sessions can find this session's detail without re-reading the whole file.

**Research and design, before any code:** three parallel Explore agents (the hand-written device-type
pattern in full, the collection registry/`pkg/sdk` pattern and what real consumers exist, and the forge
CLI dispatch pattern plus every cross-phase reference to "Phase 33" elsewhere in `IMPLEMENTATION.md`)
fed a synthesized design, which one Plan agent then pressure-tested against the real repo before any
plan file was written. The Plan agent's review caught several things worth recording: (1) the
`ItemFactory.Register` retirement is documented, not just inferred — `HANDOFF_DOCUMENT.md`'s own Phase
6 session notes say it outright; (2) `forge new-collection`'s positional name must accept two segments,
not require three, since `docs/hephaestus.md`'s own catalog table lists real two-segment names
(`exec.command`, `pkg.install`) Phase 34 must generate; (3) a segment that becomes a Go identifier needs
a leading-letter rule, not `internal/classification`'s leading-digit-permissive one; (4)
`cli_fuzz_test.go`'s pre-committed `forge new-device` fuzz seed is real but structurally can't reach
this phase's flag parsing, because the harness splices `--dir` in at a position that hits the
unknown-subcommand path first; (5) `internal/inventory/project.go`'s `Scaffold`/`writeIfAbsent` is the
right precedent for "don't clobber an existing file" even though this phase deliberately diverges from
its silent-skip behavior. All five were verified against the real files before being trusted, not taken
on the agent's word alone (`internal/classification/rule.go`'s `segmentPattern`, `cmd/pleiades/cli_fuzz_test.go`'s
literal seed, `docs/hephaestus.md`'s literal CLI example line, and `internal/inventory/project.go`'s
literal `Scaffold` function were all read directly).

**What was built:** see "Current Status" above for the complete file-by-file summary; this section adds
detail beyond it.

- `internal/forge/genutil.ValidateSegment` closes both halves of the checklist's Fuzz/Stress ask in one
  function: `^[a-z][a-z0-9_]*$` (no `.`, `/`, `\`, or leading digit is even expressible) plus
  `go/token.IsKeyword` (not a hand-maintained keyword list). `ToExportedIdent` is a plain
  underscore-split-and-titlecase helper with no dependency on the validation having already run — its
  own doc comment says so explicitly, since a caller skipping `ValidateSegment` first would get a
  PascalCase string built from invalid characters, not a panic.
- Both scaffolds' templates use a `quote` `text/template.FuncMap` entry (`strconv.Quote`) rather than
  passing `capability.Name` values (or any other named string type) directly into it: `templateData`
  converts every such value to a plain `string` in Go code before `Execute` ever runs, sidestepping
  `reflect.Value.Call`'s assignability rules entirely rather than relying on `capability.Name` happening
  to be `AssignableTo(string)` at the reflection layer (it is not, without an explicit conversion, the
  same rule that blocks it at compile time).
- The device template's `Kind()` derivation (`Config.Kind()`, `internal/inventory/devicescaffold/config.go`)
  splits `TypeKey` on its *last* underscore, not its first, and does not try to strip a vendor prefix at
  all: `docs/hephaestus.md`'s own worked example (vendor `juniper`, type key `junos_router`) has a
  vendor and a type-key prefix that don't match, so "last segment becomes the struct name" is the one
  rule that reproduces both real examples (`cisco_router`→`Router`, `linux_server`→`Server`) and the
  doc's mismatched one with no special-casing.
- The generated device starter test asserts on `Capabilities()` (the data layer), never `HasCapability()`
  (which also requires structural satisfaction): a freshly generated type has no capability-specific
  accessor methods, so `HasCapability` correctly stays `false` for every declared capability until a
  human adds them, and a starter test asserting the opposite would fail immediately out of the box,
  which would itself violate the Release Gate's "whose generated tests pass" wording. This was decided
  during design, not discovered as a test failure, but is exactly the kind of gap the design review
  exists to catch before it becomes one.
- `internal/catalog/` has no prior claimant anywhere in `PLAN.md`, `PATTERNS.md`, or Part X; the closest
  existing tree annotation (`internal/adapters/native/`, labelled "Native Go collections" in
  `CODE_SCAFFOLD.md`) turned out to describe something unrelated (Phase 16's still-stubbed
  `ExecutionAdapter`, which will eventually *dispatch to* a catalog entry, not *contain* one) once its
  real code was read directly rather than trusted from the tree comment alone.

**No defects found in the generated output itself** by the adversarial pass; the one real defect this
session found (`splitPositional`'s boolean-flag bug) was caught by the CLI's own unit test failing for
real during normal test-writing, the same "a test failing for real" discovery shape several earlier
phases' own defects were found by, not a separate adversarial review step.

**Files changed:** see "Current Status" above for the complete list.

### The Phase 32 session

**Scope: Phase 32 in full** (`.SPECIFICATION/IMPLEMENTATION.md`), the third phase of Part VII (The
Forge of Hephaestus) to close. See "Current Status" above for the complete summary; this heading
exists so future sessions can find this session's detail without re-reading the whole file.

**The reconciliation, done first:** Phase 31's own checklist text (`IMPLEMENTATION.md`'s Pattern Entry
Gate and a standalone build item) asserted "`pkg/registry` does not exist" and told the implementer to
build it, first, as `pkg/collection`'s foundation, with a two-type-parameter signature
(`Registry[K comparable, V any]`). Both claims were stale: Phase 6 already built
`pkg/registry.Registry[T]` (single type parameter, string-keyed), already consumed by `pkg/capability`
and `internal/inventory/record`. `PLAN.md` Section 25's own build-once table already carried two dated
corrections (2026-08-03, 2026-08-04) catching this exact class of drift for other primitives; this was
a third instance, just never corrected because Phase 31 hadn't been picked up yet. Building the
two-type-parameter version as literally specified would have been Section 25's own named defect: "a
second implementation is a defect, not a variation." Corrected in place, dated the same way, in
`IMPLEMENTATION.md` (Phase 31's own checklist text), `docs/hephaestus.md` ("Create a Collection"), and
`.SPECIFICATION/PATTERNS.md` (the Registry entry's consumer list) — see `LESSONS_LEARNED.md`'s new
entry for the general lesson.

**What was built:** new package `pkg/collection` (`manifest.go`, `collection.go`), consuming
`pkg/registry.Registry[Descriptor]` directly as this primitive's third consumer, not a fourth
hand-rolled map. `Manifest{SupportedTransports []string, RequiredCapabilities []capability.Name,
ExecutionContext, PlatformTargets []PlatformTarget, EngineVersion string, Status Status}`, with
`Status` = `StatusDeclared`/`StatusImplemented` and full `json` struct tags (the stable serialized form
Phase 42 later embeds as an OCI config layer). `Descriptor{Name string, Manifest Manifest}` plus
package-level `Register`/`MustRegister`/`Lookup`, mirroring `pkg/capability`'s naming exactly.
`Register` structurally enforces `PLAN.md` Section 2 (rejects a bare name, an empty namespace, or an
empty method segment, citing "Section 2" in the error) and rejects any `RequiredCapabilities` entry
`pkg/capability` doesn't recognize — safe against init-order races, since any package importing
`pkg/collection` transitively imports `pkg/capability` first, per normal Go import-init ordering.
Duplicate names are always rejected, never resolved by first-write-wins or last-write-wins (inherited
free from `pkg/registry.Registry`'s own semantics; the decision itself is recorded in `collection.go`'s
doc comment, since Part X's Phase 44 later notes namespace collision becomes a routine outcome once
Collections can arrive from outside this binary).

**A gap in Section 25's own enforcement was closed, not just documented:** `PLAN.md` Section 25 names
an architecture test proving single-Registry-implementation as something that should exist but didn't.
New `internal/archtest/registry_test.go` adds `TestKnownRegistryConsumersImportPkgRegistry` and
`TestRegistryConsumerAllowlistHasNoStaleEntries`, so a future regression back to a hand-rolled map is a
CI failure, not a silent drift — mirroring the existing `adapterAllowlist` pattern in
`internal/archtest/layering_test.go`.

**Adversarial Pattern Justification:** a grep control (`registry.New\[` across the tree, excluding
tests) found exactly 3 non-test call sites (`pkg/capability`, `internal/inventory/record`,
`pkg/collection`); `gopls references` on `registry.New` and `registry.Registry`, run after that
control, agreed exactly. This audit is honest about its own limit: import-graph analysis proves the
three known vocabularies stay wired to the shared `Registry`; it cannot structurally prove no
unrelated fourth hand-rolled map exists anywhere else, which stays a code-review-time convention.

**Schema/Injection Hardening:** not a clean "no new boundary" result, unlike Phase 30. `Manifest`
gains a real deserialization-shaped boundary (its JSON marshal/unmarshal capability) — recorded
explicitly as inert today (no code path before Part X's Phase 42 feeds it externally-sourced bytes,
only this phase's own round-trip test does) rather than silently claimed clean. `Register`'s
namespace/capability validation touches no filesystem, network, SQL, CEL, or NATS subject, the same
class of argument already made for `main.go`'s argv handling in Phase 30. No `FAILURE_PATTERNS.md`
entry; there is no live vulnerability to record, only an inert boundary honestly noted for later.

**Fuzz/Stress:** `FuzzRegister` (15s, `execs: 2305176`, ~177k/sec, zero failures) covers empty
namespaces, bare names, and duplicate registration, distinguishing a genuine cross-iteration duplicate
(the shared package-level registry persists for the life of the test binary) from a malformed-name
rejection by recomputing `Register`'s own namespace/method split inside the test.
`FuzzRegisterRequiredCapability` (15s, `execs: 1740379`, ~135k/sec, zero failures) covers capability
names absent from `pkg/capability`, registering each iteration under a fresh, atomically-counted name
so an unknown capability is always the sole possible rejection reason.

**Release Gate:** `TestManifest_RoundTrip` (a fully populated `Manifest`) and
`TestManifest_RoundTripZeroValue` (an empty one) both round-trip through JSON to a `reflect.DeepEqual`
match; `TestRegister_RejectsBareName` asserts the returned error cites "Section 2" literally. These are
ordinary in-package tests (`package collection_test`), not subprocess/e2e tests — this phase adds no
CLI behavior, so RULE 0's real-binary requirement does not apply the way it did for Phase 30.
`go test ./... -race -count=1` (whole repository) passed with zero `FAIL` lines; `gofmt -l`,
`go build ./...`, `go vet ./...`, `make gosec` (7 pre-existing findings, all individually waived, none
new), `make govulncheck` (0 called vulnerabilities), and `make coverage` (44 packages measured, none
below floor) all passed; `make ci` passed end to end. `pkg/collection` measured 100.0% coverage; floor
recorded at 100.0 in `coverage-floor.json`.

**Files changed:** `pkg/collection/manifest.go` (new), `pkg/collection/collection.go` (new),
`pkg/collection/manifest_test.go` (new), `pkg/collection/collection_test.go` (new),
`pkg/collection/collection_fuzz_test.go` (new), `internal/archtest/registry_test.go` (new),
`.SPECIFICATION/IMPLEMENTATION.md` (Phase 31 checked off in full, including the reconciliation
corrections), `docs/hephaestus.md` ("Create a Collection" corrected), `.SPECIFICATION/PATTERNS.md`
(Registry entry's consumer list extended), `.SPECIFICATION/PLAN.md` (Section 25's "Typed generic
Registry" row call-site list extended), `coverage-floor.json` (`pkg/collection` 100.0, new entry),
`LESSONS_LEARNED.md` (new entry). See "The Phase 31 session" immediately below for full detail.
Everything from "The Phase 30 session" onward describes earlier sessions and is unchanged.

### The Phase 31 session

**Scope: reconciliation, then Phase 31 in full** (`.SPECIFICATION/IMPLEMENTATION.md`), the second
phase of Part VII (The Forge of Hephaestus) to close, following directly from Phase 30's own
namespace. Part VII's own intro says Phase 31 and Phase 32 are independent of each other, so either
was a valid next step; this session took Phase 31.

A Plan-mode design review preceded any code (the plan file above, rewritten for this task from the
Phase 30 planning session): three parallel Explore-agent research passes (the real `pkg/registry`
implementation and its consumers, Phase 31/32/33's exact checklist text, and the collection-manifest
shape `pkg/capability` was named as the mirror for) fed a single written plan, approved before
implementation began.

**Why the reconciliation came first, not after:** Phase 31's own Pattern Entry Gate read
"`pkg/registry` does not exist... Build `pkg/registry` first and make `pkg/collection` its first
consumer," and a separate item asked to implement `pkg/registry/registry.go` as a generic
`Registry[K comparable, V any]`. Both were checked against the real repo, not trusted: `pkg/registry`
already existed (Phase 6), as `Registry[T any]` — one type parameter, string-keyed — with two real
consumers already wired to it. Treating the checklist's stale premise as current would have meant
building a second Registry implementation with a different signature, which `PLAN.md` Section 25
itself names as a defect the moment it exists, not a variation worth having. The correction was
written in place, dated `2026-08-04` to match the style `PLAN.md` Section 25's own table already used
twice for this identical class of drift (both times reassigning the same primitive's builder from
Phase 21 to Phase 6).

**What was built:** see "Current Status" above for the full file-by-file summary; this section adds
detail beyond it.

- `pkg/collection`'s `Register` validates `RequiredCapabilities` against `pkg/capability.Lookup`
  before delegating to the shared registry. This is safe against import-order races specifically
  because `pkg/collection` imports `pkg/capability` for its `Name` type: any package that imports
  `pkg/collection` (to call `MustRegister` from its own `init()`) transitively imports
  `pkg/capability` too, and Go guarantees a package's imports are fully initialized, `init()` included,
  before its own `init()` runs. There is no call site where the capability vocabulary could still be
  empty when this check runs.
- `PlatformTarget` and `EngineVersion` are both deliberately inert this phase: `PlatformTarget` is
  plain string data (vendor/model/version-range/deployment-context), matched against nothing yet,
  since no phase before this one builds the plan-time resolution logic to call it from; `EngineVersion`
  is an unparsed string, with no semver library added, since nothing enforces it yet either. Both
  match the checklist's own reasoning ("adding a field to a manifest that nothing has published yet is
  free") rather than gold-plating ahead of a real caller.
- `SupportedTransports` is `[]string`, not a reference to `internal/transport.Transport`. Only one
  transport (`ssh`) exists in this codebase today; binding this field to a concrete internal type
  ahead of a second transport existing would be premature structure this project avoids elsewhere.
- The two new architecture tests in `internal/archtest/registry_test.go` mirror
  `layering_test.go`'s existing `adapterAllowlist`/`TestAdapterAllowlistHasNoStaleEntries` shape
  exactly (a required-consumer list plus a stale-entry check), rather than inventing a new
  verification idiom for a very similar problem.

**No defects found** in the new code itself; the one real finding of this session was the stale
checklist premise above, caught by verifying against the actual repo state rather than trusting
`IMPLEMENTATION.md`'s own prose, the same discipline `.AGENTS/AGENTS.md` asks for before starting any
phase. `LESSONS_LEARNED.md`'s new entry generalizes this: a roadmap phase's own checklist can go stale
relative to a shared primitive an earlier-numbered phase already built, when phases execute out of
their originally-drafted order — verify the primitive's real existence in code before trusting what a
phase's own Pattern Entry Gate says about it.

**Coverage.** `pkg/collection`: 100.0%, new floor recorded at 100.0 in `coverage-floor.json`. `make
coverage` reports 44 packages measured, none below floor (`cmd/runner` remains unrecorded,
pre-existing, untouched by this phase, out of its scope).

**Verified, not assumed.** `gofmt -l`, `go build ./...`, and `go vet ./...` are clean across the
entire repository. `go test ./... -race -count=1` passes with zero `FAIL` lines, run against the full
repository. `make gosec` (7 pre-existing findings, all individually waived, none new) and
`make govulncheck` (0 called vulnerabilities) both pass. `make ci` passes end to end.

**Files changed:** see "Current Status" above for the complete list.

### The Phase 30 session

**Scope: Phase 30 in full** (`.SPECIFICATION/IMPLEMENTATION.md`), the first phase of Part VII (The
Forge of Hephaestus). This phase deliberately ships zero forge subcommands: Part VII's own ordering
note is "tooling first, catalog second," and Phases 31 through 37 (Collection Registry, Capability
Vocabulary, Scaffolds, Catalog generation, Playbook/Galaxy migration, IDE plugin) populate
`forgeCommands` later, each one file plus one map entry, never an edit to `forge.go` itself. Phase 31
and Phase 32 were explicitly kept out of scope for this session, per the prompt that began it, even
though Part VII's own notes say they are independent of each other and could theoretically start
anytime.

A Plan-mode design review preceded any code
(`/root/.claude/plans/plan-phase-30-the-delegated-treasure.md`): three Explore-equivalent research
passes (reading `.AGENTS/AGENTS.md` in full, verifying Phase 6/Phase W1 closure and `main.go`'s real
dispatch shape via `gopls`, and checking `docs/hephaestus.md` against the literal checklist wording)
preceded a single `AskUserQuestion` on the one genuine design fork the checklist left open (see
"Current Status" above), then a written plan the user approved before implementation began.

**What was built:** see "Current Status" above for the full file-by-file summary; this section adds
detail beyond it.

- `errUnknownCommand` was placed in `main.go`, not `forge.go`, deliberately: it is the generic,
  reusable half of the exit-code-parity mechanism (any future nested dispatcher can reuse it for
  free), while `forge.go` only ever *returns* it, keeping the sentinel's ownership at the same level
  as the exit-code decision that consumes it (`run()`'s own `errors.Is` check).
- `runForge`'s bare-args case (`len(args) == 0`) prints usage and returns `errUnknownCommand`, a
  deliberate difference from a namespace that might otherwise treat "no subcommand" as a silent
  no-op: `forge` is a namespace, not a runnable default action, so `pleiades forge` alone fails the
  same way `pleiades` alone does.
- `printForgeUsage`'s command list currently reads "(none registered yet; see docs/hephaestus.md...
  and .SPECIFICATION/IMPLEMENTATION.md Part VII...)" rather than an empty block or a placeholder
  subcommand invented for this phase alone. This matches the project's own established "declared is
  not implemented" convention (`docs/hephaestus.md`'s own guardrails for the catalog: a stub returns
  an explicit error, never silent success) applied to the command surface itself: the Release Gate's
  "lists its subcommands" is satisfied honestly, not by pretending Phase 31-37 work already landed.

**Fuzz/Stress, Adversarial Pattern Justification, Schema/Injection Hardening, Release Gate:** see
"Current Status" above for the full detail; all four are unusually clean for this phase specifically
because `forge.go` has zero subcommands and zero `internal/*` imports yet, a property of this phase's
narrow scope rather than evidence any of the four checks were skipped or shortened.

**No defects found.** Unlike the Phase 5 and Phase 6 sessions above, no adversarial review in this
session surfaced a real bug; the codebase is small enough (56 new lines, two edited lines beyond that
in `main.go`) that the two `gopls references` audits above serve as the adversarial check itself. One
real design decision was resolved by asking the user directly rather than by unilateral judgment (the
exit-code-parity question) since it changed the concrete file diff shape and the user was available to
decide it; that decision itself is `LESSONS_LEARNED.md` #51.

**Coverage.** `cmd/pleiades`: 33.5% (up from 29.7%), floor raised to 33.0 in `coverage-floor.json`.
`make coverage` reports 43 packages measured, none below floor (`cmd/runner` remains unrecorded,
pre-existing, untouched by this phase, out of its scope).

**Verified, not assumed.** `gofmt -l`, `go build ./...`, and `go vet ./...` are clean across the
entire repository. `go test ./... -race -count=1` passes with zero `FAIL` lines, run against the full
repository (not just `cmd/pleiades`), including every container-backed package
(`internal/lock` at 50.8s was the slowest, matching its own historical real-container cost). `make
gosec` (7 pre-existing findings, all individually waived, none new) and `make govulncheck` (0 called
vulnerabilities) both pass. `make ci` passes end to end. The Release Gate was additionally confirmed
by hand against a freshly built binary (see "Current Status" above), not solely through the automated
test suite.

**Files changed:** see "Current Status" above for the complete list.

### The Phase 6 session

**Scope: Phase 6 in full** (`.SPECIFICATION/IMPLEMENTATION.md`), done directly rather than split across
parallel background agents: the core mechanism (two new `pkg/` primitives plus their first real
consumers, spanning `pkg/registry`, `pkg/policy`, `internal/classification`, and coordinated edits across
`internal/inventory`) is one tightly-coupled surface where an inconsistency between the registry's
placement, the resolver's contract, and the classification tree's use of it would be a real bug, the same
reasoning every prior single-package-core session in this document used for its own. A Plan-agent design
review preceded any code; see the "Current Status" section above for its key findings and this session's
own adversarial-review findings summary.

**What was built:**

- **`pkg/registry`** (new): the Section 25 "typed generic Registry" shared primitive, pulled forward from
  its originally-planned Phase 21 the same way the resolver below already was in a prior session, since
  this phase's own checklist needed a real Registry before Phase 21 runs (`PLAN.md` Section 25 gets an
  identical dated correction to the resolver's own). `Registry[T]` (thread-safe, `sync.RWMutex`):
  `MustRegister` (panics on a duplicate, matching `pkg/capability`'s established init-time-programmer-error
  convention) and `Register` (non-panicking, for genuine runtime registration); `Get`; `All` (an
  independent snapshot copy, proven by `TestRegistry_AllReturnsSnapshotCopy`). `pkg/capability` was
  retrofitted onto `Registry[Descriptor]` (public `Register`/`Lookup`/`Implements` signatures unchanged),
  so Section 25's "exactly one implementation" rule holds from day one instead of a second, structurally
  identical hand-rolled map sitting next to the new shared one.
- **Device-type self-registration** (`internal/inventory` rebuilt on the registry): a new
  `internal/inventory/record/types_registry.go` hosts a `Registry[Constructor]` instance
  (`RegisterType`/`LookupType`/`AllTypes`), placed in the leaf `record` package rather than
  `internal/inventory` itself after the design review traced the real import graph: `record` is the one
  package both `factory.go` and the vendor device packages already import, so this adds zero new import
  edges, while putting it in `internal/inventory` would force `cisco`/`linux` to import it to register
  while `internal/inventory` blank-imports them to trigger that registration, the exact cycle `record.go`'s
  own doc comment says it exists to prevent. `devices/cisco/router.go` and `devices/linux/server.go` each
  gained a 3-line `init()` self-registering under `"cisco_router"`/`"linux_server"`; a new
  `internal/inventory/builtins.go` blank-imports both purely to trigger those `init()` calls, and
  `factory.go`'s `NewItemFactory` no longer imports either vendor package by name, building itself from
  `record.AllTypes()` instead. `ItemFactory.Register` (the old instance-level method) is retired entirely;
  `NewItemFactoryWithConstructors` now builds its map directly via `maps.Clone`. Net effect, proven by
  `TestBuiltinTypesSelfRegister` and `TestNewItemFactoryWithConstructors_ScopedIndependently`: a new
  in-tree device type is one new package plus one blank-import line, never a `factory.go` edit.
- **`pkg/policy`** (new): the Section 25 "hierarchical policy resolver" shared primitive.
  `Resolve[T](mode, base, layers []Layer[T], combine)` is mechanism-only: a fold over an ordered chain of
  named layers through a caller-supplied `combine`, deliberately not itself interpreting
  Override/Union/Intersection, per Section 25's own "state the mode explicitly at the call site" and its
  allowance that a single `T` can mix modes per field. Ships `Override` (plain-value replacement),
  `UnionSlices` and `IntersectSlices` (both deduplicating; `IntersectSlices` treats a nil accumulator as
  "no ceiling yet" so the first real layer establishes it, and, after this session's own adversarial
  finding above, a non-nil empty accumulator is explicitly documented as NOT the same as nil, since a
  fold step that has already narrowed to nothing must stay sticky). `PLAN.md` Section 9's
  `simulate-locked` precedence is written up as a worked doc-comment example (an Override chain whose
  `combine` refuses a later layer once a terminal value is reached), not built, since no phase consuming
  it exists yet.
- **`internal/classification`** (new): Section 6d's hierarchical classification rule tree, the
  resolver's first real consumer. `Rule{Type, ConnectionMode, Onboard *string}`, all three fields
  Override; a `Capabilities` field is deliberately absent, per a settled design decision in this
  project's own memory system assigning a data-driven capability field sourced from this same tree to
  Phase 32, once `record.Record` grows the corresponding field -- building it here first with no caller
  would be this project's own "port with no callers is a decoration" failure mode.
  `RuleSet.Classify(path)` folds every prefix of `path` with a registered rule, root to leaf, most
  specific last, skipping an unruled level rather than erroring (Section 6d's own tree has them), and
  errors if zero levels matched anywhere (Section 6g's quarantine trigger, surfaced as a plain error
  since the onboarding pipeline that owns the real lifecycle-state transition, Section 6b, is not built).
  `DefaultRuleSet` ships the Walk-tier built-in rules `PLAN.md` Section 7 promises, grounded only in the
  two device types the registry actually holds so a resolved type always hydrates. Deliberately not
  built: a filesystem loader for Section 6d's own `classification_rules/` directory tree (no config
  surface references one yet; same premature-generalization reasoning `yaml_plugin.go`'s own
  `StaticYAMLPlugin` doc comment already uses).
- **Real, wired consumer** (not just package-internal tests): `HostSpec` (`yaml_plugin.go`) gained an
  optional `Classify []string` field, an alternative to `Type`. A new `ResolveHostType`
  (`internal/inventory/host_classify.go`) is called by both independent HostSpec-to-Record conversions
  (`fileRepository.buildRecord`, the real path `cmd/pleiades validate`/`run` use; and `HydrateHosts`) so
  neither can silently drift on classification support. `Type` always wins when both are present;
  `add-host --classify a,b,c` (new flag, mutually exclusive with `--type`) resolves eagerly at write time
  and persists both fields, per Architecture Principle 5 and the ent-immutability reasoning in "Current
  Status" above. A real design-review-caught defect, fixed before it shipped: `yaml_merge.go`'s
  `applyHostSpec` (the function every write path, including `add-host`, actually goes through) hardcoded
  exactly which `HostSpec` fields it persists and had no idea `Classify` existed, so a `--classify`-added
  host would parse fine on read and then silently lose the field on the very next write.
  `TestHostsRoundTrip_PersistsClassify` is the regression test, proving the real on-disk
  `WriteHosts`/`ReadHosts` path, not just the in-memory `EncodeHosts`/`ParseHosts` path, round-trips it.
- **Documentation corrections**, matching this project's own "map verification" convention: `PATTERNS.md`'s
  Bridge entry cited a device type, `AristaSwitch`, that has never existed in this codebase (corrected to
  `cisco.Router`/`linux.Server`); its Registry entry's `Where` still pointed at the now-retired
  `inventory.ItemFactory.Register()` (corrected to `pkg/registry.Registry[T]`); a new Hierarchical Policy
  Resolver entry was added (Behavioral Patterns, immediately after Chain of Responsibility). `PLAN.md`
  Section 25's "Typed generic Registry" row gets a `Correction (2026-08-04)` note (Phase 21 -> Phase 6),
  mirroring the resolver row's own `2026-08-03` correction exactly. Phase 32's own text (which had said
  "Phase 6 is not done" about this exact resolver dependency) is corrected to state the resolved fact:
  Phase 6 landed it, so Phase 32 consumes `pkg/policy.Resolve` directly as its own intersection-mode call
  site, never a second implementation.

**Two real defects found and fixed, both by an independent adversarial review, neither by inspection
alone, and neither fixed by reflexively applying the reviewer's own literal suggestion without verifying
it first:**

1. *(Injection/architecture review)* `internal/classification.Classify` rebuilt its dotted lookup key
   from scratch on every path prefix via `strings.Join`, O(n^2) in path length with no bound on that
   length; the reviewer measured ~40s at 100,000 segments directly against the real function, reachable
   from two real new input boundaries this phase introduced. Fixed: an incremental `strings.Builder`-based
   key (O(n) total) plus an independent `maxPathSegments` (64) bound. `FAILURE_PATTERNS.md` #48,
   `LESSONS_LEARNED.md` #49.
2. *(Correctness/concurrency review)* `pkg/policy.IntersectSlices`'s own doc comment claimed a nil-or-empty
   accumulator both meant "no constraint yet," but the code only special-cased nil. The reviewer's own
   literal suggested fix (treat `len(acc) == 0` the same as nil) was checked against the fold-sequence
   invariant before being applied, and found to be a real regression: it would let a later, disjoint layer
   revive a value an earlier layer had already excluded, breaking the non-widening guarantee the function
   exists to provide. The doc comment was corrected instead of the code, pinned by the new
   `TestIntersectSlices_EmptyResultStaysStickyAcrossFold`. `LESSONS_LEARNED.md` #50.

**Fuzz/Stress:** `FuzzRegistry` (`pkg/registry`), 1.95M+ executions, clean. `FuzzResolve` (`pkg/policy`,
arbitrary layer counts/names through `Override`/`UnionSlices`/`IntersectSlices`), 2.36M+ executions,
clean. `FuzzClassify` (`internal/classification`, against the real `DefaultRuleSet`, seeded with a path
traversal attempt and pathological input), 2.5M+ executions post-fix, clean; its own invariant (a nil
error implies a non-nil resolved `Type`) is exactly the guard `ResolveHostType` depends on never being
violated. No AWX/Ansible Tower or Postgres analog exists for a generic in-memory registry or policy fold,
so real, locally measured numbers are reported directly: `BenchmarkRegistry_Get` ~24.5ns/op,
`BenchmarkRegistry_All` (100 entries) ~3.46us/op; `BenchmarkResolve_Override` (a 4-layer chain) ~5.1ns/op,
`BenchmarkResolve_Depth` confirms linear scaling with chain depth (~2.7ns at depth 1 to ~388ns at depth
64); `BenchmarkClassify` (a real 3-level `DefaultRuleSet` path) ~1.16us/op, `BenchmarkClassify_Depth`
confirms roughly linear (not quadratic) scaling up to the new `maxPathSegments` maximum, the direct
regression evidence for defect 1 above.

**Adversarial Pattern Justification:** see "What was built" and the two-defect list above. The
Registry's own duplicate-registration race is proven closed under `-race` by
`TestRegistry_ConcurrentAccess` (50 concurrent callers racing one key, exactly one winner) plus
`FuzzRegistry`. The capability binding (`Base.Declares` AND `capability.Implements`) is unchanged by this
phase and was already proven in Part 0; this phase's own addition is `TestImplementsCiscoIOS`/
`TestImplementsLinux` (`pkg/capability`), closing a real, pre-existing gap where `Implements` had never
actually been exercised against either registered descriptor's own `Assert` closure, only `Lookup` (found
while chasing a coverage regression the `pkg/capability` retrofit's smaller file exposed, not by the
adversarial review itself).

**Schema/Injection Hardening:** the one genuinely new boundary this phase introduces is a classification
path, sourced from a user-editable `inventory.yaml` or a CLI flag, used as a lookup key derived by joining
path segments with `.`. `internal/classification.validateSegments`'s `^[a-z0-9_]+$` pattern rejects any
segment containing a literal `.` before the join, which is what makes the join collision-free
(`TestClassify_DotInSegmentCannotCollide`), and now also bounds segment count (`maxPathSegments`) after
this session's own finding 1 above. `FuzzClassify` is the evidence this fails closed against arbitrary
input, including a path-traversal-shaped seed. No new SQL, CEL, command execution, or auth boundary is
introduced.

**Release Gate.** `TestFactoryHydration` (unchanged this phase) is the gate itself, run against a real
ent/SQLite row. **Corrected and closed this session**, not just re-asserted: the prior revision left this
unchecked because "the device-type discriminator lives in the mutable property bag," a premise checked
against the real code rather than inherited. `internal/ent/schema/device.go`'s `type` column has been
`.Immutable()` since Phase W4 (itself already `[x]`), and reading the real generated code confirms
`DeviceUpdate`/`DeviceUpdateOne` carry no `SetType` method at all. `record.Record.Type` is a first-class
field entirely outside `Properties`, unreachable through `AddInfo`/`RemoveInfo`. The blocking premise was
accurate when written and is stale now.

**Coverage.** `pkg/registry`: 100.0%, floor recorded at 100.0 (deterministic, no run-to-run variance,
matching `pkg/retry`'s identical precedent). `pkg/policy`: 100.0% after closing two real gaps found while
measuring (`UnionSlices`'s own internal-duplicate branch, `IntersectSlices`'s sticky-empty branch), floor
100.0. `pkg/capability`: 100.0% (up from 77.8%) after `TestImplementsCiscoIOS`/`TestImplementsLinux`
closed the pre-existing `Implements`-never-exercised-against-two-of-three-descriptors gap the retrofit's
smaller file surfaced as a real ratchet regression rather than silently accepting it; floor 100.0.
`internal/classification`: 97.5%, floor 93.0 (the small gap is `DefaultRuleSet`'s own defensive panic on
an invalid compile-time literal, unreachable by any real input path, confirmed by tracing every
production call site). `internal/inventory`: 82.2% (up from 81.6%), floor raised to 81.8.
`internal/inventory/record`: moved out of `coverage-floor.json`'s `excluded` list into `floors` at 4.0%,
since it now has real, directly-tested logic (the registry wrappers, 100% covered by
`TestBuiltinTypesSelfRegister` and siblings) rather than zero direct coverage; the low absolute number
reflects `record.Base`'s own still-transitively-tested-only majority of the file, unchanged by and out of
scope for this phase. `internal/inventory/devices/{cisco,linux}` stay excluded as before (each gained
only a one-line `init()` call). `make coverage`/`tools/coverage-check` reports 43 packages measured, none
below their recorded floor (only `cmd/runner`, pre-existing and out of this phase's scope, has no floor
yet, reported informationally).

**Verified, not assumed.** `gofmt -l`, `go build ./...`, and `go vet ./...` are clean across the entire
repository. `go test ./... -race -count=1` passes with zero `FAIL` lines -- genuinely, not merely for the
packages this phase touched: Docker was unavailable for most of this session (a WSL2/Docker Desktop
integration gap in the sandbox), so every container-dependent package (`internal/lock`, `internal/event`,
`internal/election`, `internal/topology`, `internal/transport/ssh`, `cmd/controller`, `tests/e2e`) was
individually confirmed to fail with nothing but "Docker provider unavailable" and no other error, before
the user started Docker mid-session. Once available, the required images
(`nats:2.10`/`nats:2.11`/`nats:latest`/`postgres:15-alpine`/an OpenSSH image) needed several retries each
to pull through real, transient network flakiness on the pull itself (repeated mid-transfer `EOF`
failures against the registry CDN, unrelated to this project's own code), not a permissions or
configuration problem; once cached, every one of those packages passed cleanly, and the full suite
(`go test ./... -race -count=1`, `make ci`) was then run end to end and passed with zero exceptions.
`make gosec` (7 pre-existing findings, all individually waived, none new) and `make govulncheck` (0 called
vulnerabilities) both pass.

**Files changed:** `pkg/registry/` (new: `registry.go`, `registry_test.go`, `registry_fuzz_test.go`,
`registry_bench_test.go`), `pkg/policy/` (new: `policy.go`, `policy_test.go`, `policy_fuzz_test.go`,
`policy_bench_test.go`), `internal/classification/` (new: `rule.go`, `default_ruleset.go`,
`rule_test.go`, `rule_fuzz_test.go`, `rule_bench_test.go`), `pkg/capability/capabilities.go` (retrofitted
onto `pkg/registry`) + `capabilities_test.go` (two new tests), `internal/inventory/record/` (new
`types_registry.go` + `types_registry_test.go`), `internal/inventory/devices/cisco/router.go` and
`.../linux/server.go` (each gained a 3-line `init()`), `internal/inventory/builtins.go` (new),
`internal/inventory/factory.go` (rebuilt on the registry, `Register` method retired) +
`factory_test.go` (new scoped-independence test), `internal/inventory/host_classify.go` (new),
`internal/inventory/yaml_plugin.go` (`HostSpec.Classify`, `HydrateHosts` wired) + `yaml_plugin_test.go`
(five new tests), `internal/inventory/yaml_merge.go` (`applyHostSpec` persists `classify`),
`internal/inventory/file_repository.go` (`buildRecord` wired) + `file_repository_test.go` (one new test),
`cmd/pleiades/addhost.go` (`--classify` flag) + `addhost_classify_test.go` (new, three subprocess tests),
`docs/cli_reference.md` (`add-host` section updated), `.SPECIFICATION/IMPLEMENTATION.md` (Phase 6 checked
off in full, Phase 32's own stale cross-reference corrected), `.SPECIFICATION/PATTERNS.md` (Bridge,
Registry, new Hierarchical Policy Resolver entry), `.SPECIFICATION/PLAN.md` (Section 25 Registry row
correction), `coverage-floor.json` (five entries added/changed, one moved out of `excluded`),
`FAILURE_PATTERNS.md` (#48 new), `LESSONS_LEARNED.md` (#49-50 new).

### The Phase 5 session

**Scope: Phase 5 in full** (`.SPECIFICATION/IMPLEMENTATION.md`), done directly rather than split across
parallel background agents: the core mechanism (`internal/crypto`'s new envelope/hook/rotation files) is
one tightly-coupled surface where an inconsistency between the wire format, the hook, and the
composition-root wiring would be a real bug, the same reasoning every prior single-package-core session
in this document used for its own. Three Explore agents and one Plan-agent design review preceded any
code; see `/root/.claude/plans/purrfect-wondering-zebra.md` for the full approved design and its
reasoning, and the "Current Status" section above for the adversarial review's own findings summary.

**What was built:**

- **`internal/crypto/envelope.go`** (new): `EnvelopeService`, DEK/KEK envelope encryption.
  `NewEnvelopeService(currentKey, currentVersion, previousKey, previousVersion)` (previous is optional,
  both-or-neither); `Encrypt(plaintext) (string, error)` generates a fresh 32-byte DEK per call
  (`crypto/rand`), seals it under a fresh `aesService`, wraps the DEK under the current KEK (also a
  plain `aesService`, reusing `aes.go` unmodified as a building block for both segments), zeroes the DEK
  before returning, and formats `<version>$AES256GCM$<base64 wrapped DEK>$<base64 sealed data>` (PLAN.md
  Section 17.2's own literal example format); `Decrypt(ciphertext string) ([]byte, error)` reverses it,
  resolving the KEK by matching the version tag against current then previous
  (`ErrUnknownKeyVersion` otherwise), failing closed on every malformed shape
  (`ErrMalformedEnvelope`/`ErrUnknownAlgorithm`), never a panic. `internal/crypto/aes.go` is completely
  unchanged: confirmed load-bearing for `internal/credential/file_store.go`'s already-shipped on-disk
  YAML credential file format before any code was written, not assumed.
- **`internal/crypto/key_resolve.go`** (new): `ResolveKey(dir, envVar, fileName)`, a generic extraction
  of `internal/credential/master_key.go`'s original env-var/file/generate-and-persist resolution logic;
  `internal/credential/master_key.go` is now a thin wrapper calling through it with its own existing
  constants, preserving 100% of its existing behavior (verified: its own pre-existing test suite passes
  unmodified). `generateAndSaveKey`'s commit step writes to a temp file in the target directory first,
  then links it into place with `os.Link` (not `os.Rename`, which would silently replace an existing
  destination instead of letting a loser detect and defer to a winner) -- the final shape of a real
  TOCTOU fix that went through two iterations this session, the first (`O_EXCL` alone) caught as
  insufficient by a test built specifically to probe it (FAILURE_PATTERNS.md #46).
- **`internal/crypto/device_hook.go`** (new, replaces the deleted `ent_hook.go`):
  `DeviceEnvelopePropertiesHook`/`DeviceEnvelopePropertiesInterceptor`, retargeted from `Fact.payload`
  onto `Device.properties` per the checklist's own correction ("Facts are telemetry... point encryption
  at credentials"). Registered for `OpCreate|OpUpdate|OpUpdateOne` (`Device.properties` is not
  `.Immutable()`, unlike `Fact.payload`). `isAlreadyEncryptedShape` (new) requires the exact single-key,
  string-value shape `encryptPropertiesMap` itself produces before treating a map as already-encrypted,
  closing the critical plaintext-leak bug the adversarial review found in the original mere-presence
  check (FAILURE_PATTERNS.md #43). The interceptor's read path logs and skips a row's decrypt failure
  rather than aborting the whole query (FAILURE_PATTERNS.md #44), leaving that row's `Properties` in its
  raw, still-encrypted shape.
- **`internal/crypto/rotate.go`** (new): `RotateDeviceProperties(ctx, client, svc) (int, error)`,
  PLAN.md Section 17.2's "loads a secondary key, decrypts... with the old, re-encrypts... with the new."
  Writes are conditional on the row's stored `version` column matching what was just read
  (`client.Device.Update().Where(device.IDEQ, device.VersionEQ)`, the same compare-and-swap shape
  `internal/inventory/ent_save.go`'s `entRepository.Save` uses), deliberately without setting `Version`
  itself, closing a real lost-update race the adversarial review found in the first draft's
  unconditional `UpdateOneID` write (FAILURE_PATTERNS.md #45). A row that loses the CAS, or that
  `isAlreadyEncryptedShape` recognizes as already left undecrypted by the interceptor, is skipped and
  logged, never fatal to the rest of the pass.
- **`cmd/controller/main.go`** (composition root, modified): `MASTER_ENCRYPTION_KEY` is read directly
  (base64, 32 bytes), `log.Fatal` if unset or malformed -- deliberately not through
  `crypto.ResolveKey`'s file-fallback tiers, since a redeployed/fresh-filesystem server container
  silently generating and persisting a new key would permanently orphan every previously-encrypted row.
  `client.Device.Use`/`Intercept` are installed immediately after `ent.OpenEmbedded`, before the client
  is used for anything else -- this hook's first real, running, non-test caller anywhere in this
  repository. `ROTATE_ENCRYPTION_KEYS=true` runs `RotateDeviceProperties` in its own goroutine started
  alongside (never before) the HTTP server, moved there after the adversarial review found the original
  synchronous placement could delay `ListenAndServe` -- and this binary's own Helm chart liveness/
  readiness probe, confirmed real by reading `helm/the-pleiades/values.yaml` before citing it -- past a
  default Kubernetes failure threshold on a large fleet (FAILURE_PATTERNS.md #47); a rotation failure is
  now logged, not fatal, since a partially rotated fleet is degraded but fully functional.

**Seven real defects found and fixed (six) or recorded as deliberate residual risk (one), all
independently re-verified against the actual code rather than accepted at face value:**

1. *(Adversarial review, critical)* `encryptPropertiesMap`'s "already encrypted" failsafe checked only
   whether a map contained a key named `_encrypted`, not whether that was the map's only key. A
   caller-supplied property sharing that name, alongside a real secret, bypassed encryption of the
   entire map. Fixed: `isAlreadyEncryptedShape` requires the exact single-key, string-value shape.
   `FAILURE_PATTERNS.md` #43.
2. *(Adversarial review, high)* The interceptor's batch-query branch returned a decrypt error directly
   from inside its loop, aborting the entire result set on the first bad row -- including
   `RotateDeviceProperties`'s own opening listing query, permanently deadlocking it. Fixed: log and skip
   per row. `FAILURE_PATTERNS.md` #44.
3. *(Adversarial review, high)* `RotateDeviceProperties`'s write had no compare-and-swap against the
   row's `version` column, unlike the domain's own `entRepository.Save`; a concurrent legitimate write
   was silently overwritten and the version counter left desynchronized from actual content. Fixed:
   conditional `Update().Where(IDEQ, VersionEQ)`, without setting `Version`. `FAILURE_PATTERNS.md` #45.
4. *(A test built specifically to probe the fix for finding 3's neighbor, failing for real)* An
   intermediate `O_EXCL`-based fix for a real TOCTOU race in concurrent key generation still left a
   window where a concurrent reader could observe a file that exists but is not yet written, caught by
   `TestResolveKey_ConcurrentGenerationConverges` failing with `must decode to exactly 32 bytes, got 0`.
   Fixed: write to a temp file, commit via `os.Link`. `FAILURE_PATTERNS.md` #46.
5. *(Adversarial review, high, citing the real Helm chart)* `ROTATE_ENCRYPTION_KEYS=true` ran
   synchronously before `ListenAndServe`, risking a liveness-probe-triggered crash-loop on a large
   fleet. Fixed: runs in its own goroutine alongside the server. `FAILURE_PATTERNS.md` #47.
6. *(Tests failing for real once fix 3 was attempted)* The original interceptor design assumed a
   single-result query (`Get`/`Only`) reached a separate `*ent.Device` branch that could hard-fail
   independently of the batch path. Tracing `ent`'s generated code
   (`internal/ent/device_query.go`'s own `Only`, `internal/ent/client.go`'s own `Get`) showed both are
   implemented as `Limit(2).All(ctx)`, so no bare `*ent.Device` ever reaches the interceptor; the dead
   branch was removed and two tests rewritten to match ent's real behavior rather than the original,
   false assumption.
7. *(Adversarial review, low, confirmed real but not fixed)* Envelope ciphertext carries no
   associated-data binding it to its row; a DB-level actor with write access could relocate one row's
   ciphertext onto another under a shared KEK. Recorded as deliberate residual risk in `EnvelopeService`'s
   own doc comment and this phase's Adversarial Pattern Justification evidence: closing it would require
   extending `aes.go`'s interface, ruled out by the design review as load-bearing for
   `internal/credential`. Not a Schema/Injection Hardening finding (no untrusted-input boundary is
   involved), so not recorded in `FAILURE_PATTERNS.md`.

**Fuzz/Stress:** `FuzzEnvelopeDecrypt` (new), 3.3M+ executions over a 2-minute run, clean; re-ran
pre-existing `FuzzAESDecryption`, clean. `BenchmarkEnvelopeEncrypt`/`BenchmarkEnvelopeDecrypt` against
`BenchmarkAESGCM` (single-key), same host/run: `Encrypt` ~17.4µs/op vs. `AESGCM`'s ~4.4µs/op (~3.9x,
the extra `crypto/rand` DEK generation plus a second GCM operation), `Decrypt` ~6.1µs/op (~1.4x, two GCM
operations vs. one) -- a real local comparison; the prior, uncited "Postgres raw INSERT latency" line in
`aes_bench_test.go` is removed, since it was never locally measured and compared a different system.

**Adversarial Pattern Justification:** see "What was built" and the seven-defect list above; the key
hierarchy the checklist's own item asked to defend now exists for real (DEK per call, current/previous
KEK pair, version-tagged rotation), proven by a dedicated adversarial review rather than asserted.

**Schema/Injection Hardening:** the one genuinely new boundary this phase introduces is deserializing the
`$`-delimited envelope header read back from storage; `FuzzEnvelopeDecrypt` is the evidence it fails
closed on every malformed shape. Findings 1-6 above are recorded in `FAILURE_PATTERNS.md` #43-47 (five
entries; findings 4 and part of 6 share #46's entry, the TOCTOU race).

**Release Gate.** Literal wording ("via the API... via `psql`") left unchecked with a specific reason: no
device-write API endpoint exists anywhere in this repository yet (`internal/api/router.go` only mounts
`/healthz`, Phase 11's own still-open item) and no Postgres backend exists (embedded SQLite only,
matching Phase 2's own identical, already-recorded deferral). Achieved instead: real composition-root
wiring, a real on-disk SQLite file via `ent.OpenEmbedded`, and a raw-driver query proving ciphertext-only
storage through the real domain repository write path
(`TestDeviceEnvelopeProperties_RoundTrip`/`RawStorageIsCiphertext`), plus a hands-on run of the real
built `controller` binary (`MASTER_ENCRYPTION_KEY` unset -> `log.Fatal`; set -> starts; a device created
through the real repository path -> raw `SELECT properties FROM devices` on the real on-disk file shows
only ciphertext).

**Coverage.** `internal/crypto`: 88.9% (up from the prior single-key-only implementation's 77.6%); floor
recorded at 87.5, a small safety margin below the measured figure matching this repository's own
established convention. Remaining gaps are genuinely hard-to-reach OS-level fault-injection branches
(`crypto/rand.Read` failure, `MkdirAll`/`Chmod`/`WriteFile` failure, both unreachable without real
fault injection and, in this sandbox, running as root defeats permission-based triggers anyway) and two
`RotateDeviceProperties` branches (a concurrent-write CAS loss, a mid-loop write error) that would need
either real goroutine races or an injectable test seam to trigger deterministically -- accepted, not
silently ignored, matching this repository's own established precedent for such gaps elsewhere.
`internal/credential`: 90.7%, unchanged floor (89.9), confirmed unaffected by the `ResolveKey`
extraction. `cmd/controller`: unchanged at its already-honest 0.0% floor (its only test is the
black-box, real-subprocess Release Gate; `go test -cover` cannot see coverage of code that only runs
inside a spawned child process). `make coverage` reports 40 packages measured, none below floor.

**Verified, not assumed.** `gofmt -l`, `go build ./...`, and `go vet ./...` are clean across the entire
repository. `go test ./... -race -count=1` passes with zero `FAIL` lines, run twice: once before the
adversarial review (which is when it first caught a real regression, below) and once after every fix.
`make gosec` (7 pre-existing findings, all individually waived, none new), `make govulncheck` (0 called
vulnerabilities), `make coverage`, and `make ci`'s constituent checks all pass. One real regression was
caught by the full repo-wide run and fixed before this phase closed: Phase 4's own
`TestControllerLeaderElection_ReleaseGate` (`cmd/controller`, spawns three real subprocess binaries)
started failing the moment `MASTER_ENCRYPTION_KEY` became a required startup env var, since the test's
own subprocess launcher had no reason to know about an env var that did not exist when it was written --
invisible to `internal/crypto`'s own package tests, only caught by the full suite. `FAILURE_PATTERNS.md`
#42, `LESSONS_LEARNED.md` #43. The real built `controller` binary was also exercised by hand (above,
Release Gate section), and `TestResolveKey_ConcurrentGenerationConverges` was additionally run at
`-race -count=20` (16 concurrent goroutines each run) specifically to build confidence in the TOCTOU fix
beyond a single pass.

**Files changed:** `internal/crypto/envelope.go` + `envelope_test.go` + `envelope_fuzz_test.go` +
`envelope_bench_test.go` (all new), `internal/crypto/key_resolve.go` + `key_resolve_test.go` +
`key_resolve_internal_test.go` (all new), `internal/crypto/device_hook.go` + `device_hook_test.go` (new,
replace deleted `ent_hook.go` + `ent_hook_test.go`), `internal/crypto/rotate.go` + `rotate_test.go` (new),
`internal/crypto/aes_bench_test.go` (uncited reference line removed), `internal/credential/master_key.go`
(rewritten as a thin wrapper), `cmd/controller/main.go` (envelope service wiring, rotation goroutine,
residual-risk doc comment), `cmd/controller/leader_election_release_gate_test.go`
(`MASTER_ENCRYPTION_KEY` added to the spawned subprocess environment), `coverage-floor.json`
(`internal/crypto` 77.6 -> 87.5), `.SPECIFICATION/IMPLEMENTATION.md` (Phase 5 checked off except the
Release Gate, with a specific reason), `FAILURE_PATTERNS.md` (#42-47 new), `LESSONS_LEARNED.md` (#43-48
new).

### The Phase 4 session

**Scope: Phase 4 in full** (`.SPECIFICATION/IMPLEMENTATION.md`), done directly rather than split across
parallel background agents: the extraction (a new package, `engine.Scheduler`'s own deletion, and the one
real composition-root wiring site, `cmd/controller/main.go`) is one small, tightly-coupled surface where
splitting it across agents would only add coordination overhead, the same reasoning every prior
single-package-core session in this document used for its own. A Plan-agent-reviewed design preceded any
code (cross-cutting surface: package placement, the exact `LeaderElector` API shape, whether to keep
`engine.Scheduler` as a wrapper or delete it outright, and a real TTL/interval tuning question with no
single obviously-correct answer); the review's recommendations (delete `engine.Scheduler` rather than
wrap it, a new dedicated `internal/election` package rather than adding to `internal/lock`, `WithOnAcquired`
as the only new API surface) were adopted as proposed. See `/root/.claude/plans/lovely-prancing-pudding.md`
for the full approved design and its reasoning.

**What was built:**

- **`internal/election`** (new package): `LeaderElector` (`NewLeaderElector(mgr lock.Manager, key string,
  opts ...Option) *LeaderElector`, `Run(ctx)`, `IsLeader()`, `WithOnAcquired(func())`), the reusable
  Build-Once "Leader elector" primitive PLAN.md Section 25 names, extracted from `engine.Scheduler` (which
  hardcoded both the election loop and its own lock key together). Not added to `internal/lock`: Leader
  Election is PATTERNS.md's own named, conceptually distinct application of the general-purpose Distributed
  Lock primitive, and `internal/archtest`'s layering tests confirm a package depending only on
  `lock.Manager`/`lock.Lease` (never a concrete driver) needs no adapter-allowlist change, unlike
  `internal/lock` itself. `engine.Scheduler` and its 3 test files are deleted outright (zero callers
  anywhere in the repo before this phase; the checklist's own wording is "extract... instead of
  embedding," and Phase 23's forward reference is to "the reusable `LeaderElector`," never to `Scheduler`'s
  name); their real test scenarios moved to `internal/election/election_test.go`/`election_fuzz_test.go`/
  `election_bench_test.go`, not silently dropped.
- **The five checklist fixes**, all inside `election.go`: `errors.Is(err, lock.ErrLockHeld)` replacing a
  raw `!=` comparison; `slog.Error`/`slog.Warn` structured logging (matching `internal/event/consumer.go`'s
  established field-naming convention) replacing the empty "a real app would log here" comment; a shared
  `releaseBestEffort` helper (bounded `context.WithTimeout(context.Background(), releaseTimeout)`, 2s)
  called explicitly on `KeepAlive` failure before dropping the lease reference, also retrofitted onto the
  pre-existing graceful `ctx.Done()` release call, which had the identical unbounded-`context.Background()`
  gap; and retuned defaults (`electionTTL=2s`, `electionInterval=500ms`, down from the prior
  `interval=2s`/`ttl=4s`) so worst-case failover after a hard kill targets `electionTTL+electionInterval`
  &asymp; 2.5s instead of &asymp; 6s, confirmed (not just calculated) by real, repeated `SIGKILL` measurements
  below.
- **`cmd/controller/main.go` wired for real.** Previously deliberately unwired (its own doc comment named
  this exact phase as the reason). Now constructs `lock.NewNatsLockManager(ctx, natsURL)` and
  `election.NewLeaderElector(lockMgr, "pleiades-scheduler-leader", election.WithOnAcquired(...))`, the
  first real, running, non-test caller of either the elector or (for this composition root) the lock
  manager. Shutdown sequence rewritten: `cancel()` now fires immediately on receiving `SIGINT`/`SIGTERM`
  (not only via the deferred call at `main`'s return), so the elector's own graceful release runs
  concurrently with the HTTP drain; the elector's own completion is awaited before `lockMgr.Close()` (which
  must happen after its release call, never before, or the release fails against a closed connection).
- **Two stale premises, corrected with real findings, not silently believed or silently rewritten.** (1)
  The checklist's own "the lease TTL is ignored by the only adapter" (Adversarial Pattern Justification
  prompt): false today, `internal/lock/nats.go`'s real per-key TTL (Phase 3) is genuinely honored and
  renewed; verified by reading the code, not assumed. (2) The checklist's own "election is needed again by
  Phases 22, 23, and 24" and PLAN.md Section 25's own, separately worded "Scheduler, key rotation,
  dependency manager, drift workers": direct audit of every named phase's own checklist body found only
  Phase 23 self-confirms ("hook it to the reusable `LeaderElector` from Phase 4"); Phase 22, Phase 24, "key
  rotation" (really Phase 5), and "drift workers" (no phase number exists yet) do not mention leader
  election anywhere in their own text. Corrected in both `IMPLEMENTATION.md`'s own Phase 4 evidence and a
  new dated `Correction (2026-08-03)` paragraph in PLAN.md, matching the exact precedent the resolver-row
  correction immediately above it in that same file already established.

**Three real defects found and fixed, all by tests failing for real against real infrastructure, none by
inspection alone.**

1. *(The real 3-replica container test, failing for real on its own graceful-shutdown step)* A `select`
   between `ctx.Done()` and `ticker.C` does not prioritize the former, and a call already dispatched from
   an earlier tick can still be in flight when cancellation lands concurrently; both raced independently in
   `LeaderElector.Run`, logging a misleading "renewal failed" warning on every ordinary graceful shutdown
   (`FAILURE_PATTERNS.md` #39, `LESSONS_LEARNED.md` #40). Fixed by checking `ctx.Err()` at both race points
   and routing either outcome through the same graceful path.
2. *(A benchmark whose own individually-timed calls contradicted its own aggregate result)* Container
   teardown registered via `defer` executes during the benchmark function's own return, inside the exact
   window `go test -bench` measures, inflating a real ~0.7-0.9ms `KeepAlive` call to a reported
   100ms-2s/op (`FAILURE_PATTERNS.md` #40, `LESSONS_LEARNED.md` #41). Fixed by switching to `b.Cleanup`,
   matching `internal/lock/nats_bench_test.go`'s own already-correct `startBenchNats` pattern.
3. *(Five apparently-independent Release Gate measurements coming back bit-for-bit identical to the
   microsecond)* `go test` silently serves a cached PASS result for an unchanged package/binary/flags
   combination, printing `(cached)` in its own summary line rather than genuinely re-executing
   (`LESSONS_LEARNED.md` #42). Caught before being written up as five confirmed real measurements; redone
   with `-count=1`, yielding five genuinely independent numbers (below).

**Fuzz/Stress:** `FuzzLeaderElectionCancellation` (`internal/election`, migrated from
`engine.Scheduler`'s own `FuzzSchedulerCancellation`, extended with two new fuzzed dimensions this phase's
fixes introduced: a mock lease's `KeepAlive` failing after N calls, and its `Release` call being
artificially slow), 4143 executions in 45s against a mock `lock.Manager`, clean.
`BenchmarkLeaderElectorKeepAlive` (real `nats:2.11` container, after the `b.Cleanup` fix above): ~685-703
&micro;s/op across repeated real runs, consistent with Phase 3's own `BenchmarkLockKeepAlive` reference
figure (~0.76ms) for the identical underlying call. Reference platform: Kubernetes' `client-go`
`leaderelection` package defaults (`DefaultLeaseDuration=15s`/`DefaultRenewDeadline=10s`/
`DefaultRetryPeriod=2s`, verified against the current `github.com/kubernetes/client-go` source before
citing, not assumed from memory), which tunes for a much larger worst-case window than this package's own
retuned ~2.5s target.

**Adversarial Pattern Justification:** confirmed real per-key TTL is honored today (Phase 3), correcting
this phase's own checklist premise. The real, narrower finding: at the pre-existing
`interval=2s`/`ttl=4s` defaults, worst-case failover after a hard kill was &asymp; 6s (`ttl+interval`),
already over this phase's own 3-second Release Gate budget, and not the checklist's own separately-claimed
"bucket default" (24h) fallback either, which is also false (the 24h bucket-wide TTL is only ever reached
if per-key TTL itself were broken). Retuned to `electionTTL=2s`/`electionInterval=500ms` (worst case
&asymp; 2.5s), confirmed by real, repeated `SIGKILL` measurements below, not by arithmetic alone.

**Schema/Injection Hardening.** `LeaderElector`'s own `key` parameter reaches `lock.Manager.Acquire`
unchanged; in every real caller this phase creates it is a compile-time literal, never external input, so
no new boundary is introduced. The new Release Gate test's own `exec.Command(binPath)` calls build every
argument and env var from test-internal constants. A fresh `gosec` run found zero new findings (the
pre-existing 7, all already individually waived, are unchanged); `govulncheck`: 0 called vulnerabilities.

**Release Gate.** `cmd/controller/leader_election_release_gate_test.go` (new; this binary's first test
file): `TestControllerLeaderElection_ReleaseGate` builds the real `controller` binary once, starts 3 real,
independent OS processes sharing one real `nats:2.11` container, and asserts exactly one ever prints the
literal `slog` line `"Acquired Scheduler Lease"` on its own stderr. `SIGKILL` (not `SIGTERM`, which
`cmd/controller` already handles gracefully and which the in-process 3-replica test already covers) is
sent to the winner, bypassing the Go runtime entirely so no deferred cleanup or `Release` call ever runs --
the only way to actually exercise the `electionTTL`-expiry fallback path this gate cares about. Five
genuinely independent (`-count=1`, per defect #3 above) real measured failover times: 2.0178s, 2.0015s,
2.0017s, 2.0014s, 2.0077s, all comfortably under the 3-second bound, different winners each run.
`go test ./... -race -count=1` is clean across the entire repository. `gofmt -l`, `go build ./...`, and
`go vet ./...` are clean; `make gosec`, `make govulncheck`, `make coverage`, and `make ci` all pass end to
end.

**Coverage.** `internal/election` (new): 95.0%, floor recorded at 90.0 (a small safety margin below the
measured figure, matching this repository's own established convention for a package whose real-container
tests have some run-to-run branch variance, the same reasoning `internal/lock`'s own floor already uses).
`internal/engine` improved from 91.0% to 91.9% after `scheduler.go`'s removal; floor raised to 91.5.
`cmd/controller` recorded at its real measured 0.0% (its only test is the black-box, real-subprocess
Release Gate above; `go test -cover` cannot see coverage of code that only ever runs inside a spawned
child process, the same structural reason `cmd/pleiades` already carries a low, honestly-recorded 29.7%
floor rather than a number implying more in-process testing than actually exists). `make coverage` reports
40 packages measured, none below floor; `cmd/runner` remains unrecorded (pre-existing, untouched by this
phase, out of its scope).

**Files changed:** `internal/election/` (new: `election.go`, `election_test.go`, `election_fuzz_test.go`,
`election_bench_test.go`), `internal/engine/scheduler.go` + `scheduler_test.go` + `scheduler_fuzz_test.go`
+ `scheduler_bench_test.go` (deleted), `cmd/controller/main.go` (lock manager + elector wiring, doc comment
rewrite, shutdown sequence), `cmd/controller/leader_election_release_gate_test.go` (new),
`.SPECIFICATION/IMPLEMENTATION.md` (Phase 4 checked off), `.SPECIFICATION/PLAN.md` (new dated Correction
note under Section 25's table), `.SPECIFICATION/PATTERNS.md` (Leader Election/Heartbeat/Graceful
Shutdown/Dependency Injection "Where" lines updated), `coverage-floor.json` (`internal/election` and
`cmd/controller` new, `internal/engine` raised), `FAILURE_PATTERNS.md` (#39-#40 new, prior #38 renumbered
to #41), `LESSONS_LEARNED.md` (#40-#42 new).

### The Phase 3 session

**Scope: Phase 3 in full** (`.SPECIFICATION/IMPLEMENTATION.md`), done directly rather than split across
parallel background agents: the core mechanism (`internal/lock`'s three files -- `manager.go`, `nats.go`,
`inprocess.go` -- plus the new `queue.go`) is one tightly-coupled surface where an inconsistency between
the two adapters would be a real bug, the same reasoning every prior single-package-core session used for
its own. A Plan-agent-reviewed design preceded any code (cross-cutting surface: a breaking `Manager`
interface change, a real library-API investigation, a real nats-server version requirement, and a genuine
safety question around what "priority" contention could safely mean); the review caught two real
corrections before code was written, detailed below. See `/root/.claude/plans/smooth-singing-lake.md` for
the full approved design and its reasoning. Two facts the design depended on were verified empirically in
a scratch harness before being trusted, not assumed from library documentation or prior knowledge: that
`nats.go` v1.52.0's public `KeyValue.Update` cannot refresh a per-key TTL at all (confirmed by reading the
library's own source), and that real per-key TTL requires nats-server 2.11+, with `nats:2.10` -- the
version every pre-existing container test in this repository pinned -- rejecting the required bucket
config outright.

**What was built:**

- **Real per-key TTL, honoring `ttl` for the first time.** `natsLockManager.tryAcquireOnce` passes
  `jetstream.KeyTTL(ttl)` to `kv.Create`; `KeepAlive` genuinely refreshes it via a raw `js.PublishMsg` call
  (`publishWithTTL`) against the KV bucket's own subject, bypassing `jetstream.KeyValue.Update`'s public
  signature, which hardcodes `ttl=0` and cannot renew a TTL at all in this client version. Proven
  empirically against a real container, not just read from source: a renewed key survives past its
  original deadline and dies at the new one; a stale-revision renewal fails with an error
  `errors.Is`-matching the same `jetstream.ErrKeyExists` `Create` already handles, so the existing
  CAS-mismatch translation reuses cleanly. This requires nats-server 2.11+
  (`jetstream.KeyValueConfig.LimitMarkerTTL`); the version bump was scoped to the exact four files anywhere
  in the repository that construct a real `natsLockManager` against a container (`internal/lock`'s own two,
  plus `internal/engine/scheduler_test.go`/`scheduler_bench_test.go`, found by grepping the real
  constructor call), not bumped repository-wide. The bucket-wide 24h `TTL` stays as an absolute failsafe
  ceiling. A positive `ttl` below one second is rejected with a clear domain error instead of reaching the
  server as an opaque API error -- itself a real, fuzz-caught finding, below.
- **Shared lock mode.** `AcquireOptions.Mode` (`ModeExclusive`/`ModeShared`) is new on `Manager.Acquire`,
  whose signature changed to `Acquire(ctx, itemID, ttl, AcquireOptions)`; the zero value is exactly
  today's old behavior, so both pre-existing production call sites (`Scheduler.Run`, `Executor.runOne`)
  needed only a mechanical, behavior-preserving update. The stored value became a small JSON envelope
  (`lockValue{Mode, Holders}`, NATS) or a `holders` map (in-process) instead of a single-owner value. A
  real, test-caught defect in the first draft is FAILURE_PATTERNS.md #34: 10 concurrent shared joins under
  the default reject policy, only 2 succeeded, because a shared join's own lost CAS race against a
  *different, compatible* join was surfaced as the same `ErrLockHeld` real contention uses. Fixed by
  distinguishing the two inside the CAS retry itself, not by asking callers to choose a waiting policy.
- **Three contention policies, two acquisition strategies.** `PolicyReject` (today's original behavior,
  now named explicitly), `PolicyQueue` (one shared, adapter-agnostic backoff wrapper, `queue.go`, reusing
  `pkg/retry.Backoff`), `PolicyPriority` (the same shared retry loop with priority-scaled backoff). Priority
  never preempts a live hold in any adapter, a deliberate, safety-driven narrowing from the literal PLAN.md
  Section 13 wording, caught by the Plan-agent review before code was written: `Executor.runOne` never
  calls `KeepAlive` during a device's action, so revoking a live lock mid-action would let two callers
  physically execute against the same device concurrently. Per-device-as-reached needed no new code
  (`Executor.runOne` already does this); all-at-plan-time is new (`lock.AcquireAll`, all-or-nothing,
  releases everything already acquired on first failure), wired into `Executor` via a new
  `Task.LockAcquisition` field (`AcquisitionStrategy`, yaml/json `lock_acquisition`) -- a literal per-task
  opt-in switch, deliberately not the hierarchical system/inventory/group/device policy resolver PLAN.md
  Section 25 assigns to Phase 21.
- **Monotonic time for lease renewal.** Both `natsLease` and `inProcessLease` track a locally-computed
  deadline from this process's own `time.Now()`, never a value read back from the store; `KeepAlive`
  fast-fails locally, no network call, once that deadline has passed. Expiry authority lives in exactly
  one place per adapter; a client only ever reasons about elapsed time relative to its own prior local
  reading, never by comparing timestamps across machines -- the concrete meaning of Section 16's "ignoring
  NTP" here.
- **`CapacityCounter`**, declared, not implemented (`internal/lock/manager.go`), per PLAN.md Section 25's
  own explicit "Declare Phase 3, implement Phase 24" carve-out -- the only primitive that table grants this
  shape to.
- **`lock.AcquireAll`'s own real caller**, above, closes a gap a Plan-agent review flagged mid-design: a
  built-but-uncalled primitive fails this exact phase's own Adversarial Pattern Justification bar ("a port
  with no callers is not an implemented pattern, it is a decoration"), which the plan's first draft would
  have left `AcquireAll` exposed to.

**Five real defects found and fixed, four by tests failing for real (one fuzz, three targeted), one by
manually driving the real binary -- none by inspection alone.**

1. *(A real NATS container test)* Shared mode's own join lost most of a concurrent burst under the default
   contention policy (FAILURE_PATTERNS.md #34, LESSONS_LEARNED.md #38): 10 simultaneous `ModeShared`
   Acquire calls, only 2 succeeded. Fixed by retrying a shared join's own lost CAS race internally,
   regardless of `ContentionPolicy`, since it is optimistic-concurrency noise against a *compatible*
   operation, not real contention.
2. *(A fuzz test found to be theater, then fixed for real)* `FuzzLockAcquisition` fuzzed a string and never
   called `Acquire` at all (FAILURE_PATTERNS.md #35). Rebuilt to run for real against one shared NATS
   container, 607k+ executions, which then found the next two defects on its own.
3. *(The rebuilt fuzz test, failing for real)* A sub-second positive `ttl` reached the NATS server as an
   opaque `err_code=10165` API error instead of a clear domain error (FAILURE_PATTERNS.md #36). Fixed by a
   `minPositiveTTL` check, confirmed empirically (1 second is the real minimum; 999ms, 500ms, 100ms, 1µs,
   1ns are all rejected server-side) rather than guessed.
4. *(The rebuilt fuzz test, minimized to a 3-character reproducer)* An `itemID` containing consecutive dots
   (`"..0"`) passed `nats.go`'s own key validation but produced an empty NATS subject token, hanging the
   caller for the full context timeout instead of failing cleanly (FAILURE_PATTERNS.md #37). Fixed by
   `itemIDValid`, a defense-in-depth check this package now owns since the library's own validation does
   not cover this case.
5. *(Manually driving the real `pleiades` binary, AGENTS.md's own Rule 0)* `Task.LockAcquisition` had a
   `String()` method but no `UnmarshalYAML`/`UnmarshalJSON`, so a runbook author could not actually write
   `lock_acquisition: all_at_plan_time` in a real `.yaml` file -- every test up to that point had
   constructed the field as a raw JSON int, matching the executor's own read path, never what a human would
   type (FAILURE_PATTERNS.md #38). Fixed by `ParseAcquisitionStrategy` plus full YAML/JSON marshal support,
   mirroring `pkg/inventory.LifecycleState`'s own established pattern; proven afterward against the real
   binary (`pleiades validate` and `pleiades run` both accept the human-readable string in a real runbook
   targeting a tagged, multi-device inventory today).

**Fuzz/Stress:** `FuzzInProcessManager` (extended to cover the full `AcquireOptions` surface: Mode, Policy,
Priority, ttl including negative-ttl rejection), 739k+ executions, clean. `FuzzLockAcquisition` (NATS,
rebuilt from theater to real, above), 607k+ executions against a real container, clean, after the two
defects above were found and fixed; the exact failing inputs are pinned as permanent corpus regressions.
Benchmarked for real, all on the same host in the same run, not asserted: `BenchmarkLockAcquisition` (NATS,
exclusive) ~4.2ms/op; `BenchmarkLockAcquisition_Shared` ~3.9ms/op; `BenchmarkLockKeepAlive` (previously
untimed, since `KeepAlive` had no real renewal mechanism before this phase) ~0.76ms/op;
`BenchmarkLockAcquisition_QueueContended` (real contention plus backoff) ~61ms/op; the in-process
equivalents all in the low microsecond-to-nanosecond range. `BenchmarkPostgresAdvisoryLock` (new: real
`pg_try_advisory_lock`/`pg_advisory_unlock` via a real Postgres container) ~2.5ms/op replaces the
pre-existing, unverified "Redis SETNX ~1-3ms" logged claim as this package's real reference figure (now
relabeled plainly as published, not measured), matching the "measured cost, not a guess" standard the
Phase 2 session's own event-bus benchmarks already established.

**Adversarial Pattern Justification:** confirmed `Acquire` now has two structurally different real
callers (`Scheduler.Run`'s long-lived, low-cardinality, `KeepAlive`-renewed leader election, and
`Executor.runOne`'s short-lived, high-cardinality, never-renewed per-device locking), correcting this
phase's own checklist text, which claimed only one existed. `lock.AcquireAll` gets a real, narrow
production caller this phase (`Executor`'s `AcquisitionAllAtPlanTime` opt-in) rather than shipping unused,
which a Plan-agent review flagged would otherwise fail this exact gate. `CapacityCounter` has zero callers
this phase, named as such: PLAN.md Section 25's own table is the one explicit, named carve-out for
declare-now-implement-later, granted only to this primitive. Priority's narrowed safety contract (never
preempts a live hold) is stated explicitly, not left implicit.

**Schema/Injection Hardening.** `kvSubject`'s hand-built `"$KV.<bucket>.<key>"` subject (needed because
`jetstream.KeyValue.Update` cannot renew a TTL) found a genuine gap in `nats.go`'s own upstream key
validation (defect #4 above); closed by `itemIDValid`. A fresh `gosec` run against every file this phase
touched found zero new findings; the repository's pre-existing 7 findings, all in `internal/api`, are
unchanged and outside this phase's scope. `govulncheck`: 0 called vulnerabilities.

**Release Gate.** `TestThunderingHerdLocking` (100 goroutines, real `nats:2.11` container, exactly 1
succeeds) already existed and already passed before this phase's own code changes; kept passing through
the `Acquire` signature change. `conformanceThunderingHerd` (both adapters) bumped from 50 to 100 for
exact literal parity with the checklist's own text. `TestExecutor_AcquisitionAllAtPlanTime` proves both the
all-or-nothing failure shape (zero devices run when one of three is contended) and the success shape (all
three run when none are contended), against the default strategy still running the two unblocked devices
in the identical contention scenario.

**Coverage.** `internal/lock`'s coverage has more real run-to-run variance than most packages in this
repository: several branches (shared-mode CAS-retry-then-succeed paths under genuine concurrent load) only
trigger under real network timing against a real NATS container, deliberately exercised via real
concurrent stress tests (`conformanceSharedHoldersChurn`, 24 workers) rather than mocks or fault-injection
scaffolding, per RULE 0. Measured between 88.4% and 91.1% across repeated clean runs; floor recorded
conservatively at 87.0% (`coverage-floor.json`) to avoid a flaky CI failure on ordinary variance, well
above AGENTS.md's spirit for a package whose remaining, unhit branches are either genuinely unreachable
(JSON marshal errors on internally-controlled data) or would require real fault-injection (Toxiproxy-style)
against branches that already share an identical, already-proven error-wrapping shape with sibling
branches that are covered. `internal/engine` improved from 91.4% to 92.5% closing this phase's own new
gaps (the `AcquisitionStrategy.String()` method, the all-at-plan-time success path); floor recorded at
91.0% with the same small safety margin. `make coverage` reports 39 packages measured, none below floor.

**Verified, not assumed.** `gofmt -l`, `go build ./...`, and `go vet ./...` are clean across the entire
repository. `go test ./... -race` passes with zero `FAIL` lines, including every real container test this
phase touched or added (NATS at the bumped 2.11 version, Postgres, the scheduler's own leader-election
suite). `make gosec`, `make govulncheck`, `make coverage`, and `make ci` all pass end to end. The real
built `pleiades` binary was also exercised by hand (`init` → `add-host` with a shared tag across three
devices → `validate` → `run` against both a new runbook using `lock_acquisition: all_at_plan_time` and the
pre-existing `sample.yaml`) against a fresh scratch directory, which is what found defect #5 above and
confirmed zero regression to the existing Walk-tier CLI path afterward.

**Files changed:** `internal/lock/manager.go` (`Mode`, `ContentionPolicy`, `AcquireOptions`,
`CapacityCounter`, `Manager.Acquire` signature, `AcquireAll`, shared `validateTTL`), `internal/lock/queue.go`
(new: `acquireWithContention`, `contentionBackoff`), `internal/lock/nats.go` (rewritten: `js` field,
`LimitMarkerTTL`, `jetstream.KeyTTL`, `publishWithTTL`/raw-publish renewal, `lockValue`/`holderInfo`
envelope, mode/priority `tryAcquireOnce`, `minPositiveTTL`, `itemIDValid`, local monotonic deadline),
`internal/lock/inprocess.go` (rewritten: `holders` generalization, same mode/priority/deadline logic),
every existing `internal/lock` test/bench/fuzz file (call sites updated for the new `Acquire` signature,
`ThunderingHerd` bumped to 100), new `internal/lock/{enum_test.go, nats_lease_lifecycle_test.go,
pg_advisory_bench_test.go}`, `internal/lock/conformance_test.go` (six new shared subtests),
`internal/lock/nats_fuzz_test.go` (rebuilt from theater), `internal/engine/lock_acquisition.go` (new:
`AcquisitionStrategy` plus full YAML/JSON marshal support) + `lock_acquisition_test.go` (new),
`internal/engine/dag.go` (`Task.LockAcquisition`), `internal/engine/executor.go` (`nodeExecution.Lease`,
`runNode`/`runOne` all-at-plan-time wiring) + `executor_test.go` (new
`TestExecutor_AcquisitionAllAtPlanTime`), `internal/engine/scheduler.go` (mechanical `AcquireOptions{}`
update) + `scheduler_test.go`/`scheduler_bench_test.go` (nats:2.10 → 2.11) + `scheduler_fuzz_test.go`
(mock signature), `.SPECIFICATION/IMPLEMENTATION.md` (Phase 3 checked off),
`.SPECIFICATION/PATTERNS.md` (Bulkhead entry updated), `FAILURE_PATTERNS.md` (#34-#38 new),
`LESSONS_LEARNED.md` (#36-#39 new), `coverage-floor.json` (`internal/lock` 91.7→87.0,
`internal/engine` 92.1→91.0, both with a specific, recorded reason above, not a silent lowering).

### The Phase 2 Event Bus session

**Scope: Phase 2 in full** (`.SPECIFICATION/IMPLEMENTATION.md`), done directly rather than split across
parallel background agents: the core mechanism (`internal/topology` plus `internal/event`'s interface,
envelope, consumer, DLQ, and dedup changes) is one tightly-coupled surface where an inconsistency between
files would be a real bug, not a parallelizable seam, the same reasoning Phase W5 and the Phase 1 session
both used for their own single-package cores. A Plan-agent-reviewed design preceded any code (cross-cutting
surface: a new package, a breaking interface change, several genuine design questions -- envelope-field
context propagation, idempotency-key derivation, DLQ mechanics, how far to rewire the five packages that
bypassed the port); the review caught one real correctness bug and one real API misuse before either
reached code, detailed below. See `/root/.claude/plans/validated-wandering-frog.md` for the full approved
design and its reasoning.

**What was built:**

- **`internal/topology`** (new package): the single owner of every NATS subject, stream, consumer, and
  retention/replica setting, closing the exact drift the checklist named. One stream (`"PLEIADES"`,
  `"pleiades.>"`) replaces the three independent declarations that existed before this phase: `natsBus`'s
  own `"Pleiades_Events"` stream (`"pleiades.events.>"`), `cmd/demo`'s separate `"JOBS"` stream
  (`"jobs.logs.>"`), and `internal/api/dispatcher.go`'s bare `"runbooks.dispatch"` literal, which neither
  stream's subject filter covered at all -- meaning every real dispatch silently failed in production
  before this phase, with its error swallowed into a per-device failure counter. Subject builders
  (`EventSubject`, `DispatchSubject`, `LogSubject`, `DeadLetterSubject`), `DurableName` (a
  caller-topic-string to legal-NATS-durable-name mapping that appends a content hash rather than
  sanitizing in place, so two inputs differing only in illegal characters can't collide onto the same
  consumer group -- the Schema/Injection Hardening finding for this phase), `StreamConfig`/`EnsureStream`
  (the one `CreateOrUpdateStream` call site, shared by every adapter and binary), and three
  `jetstream.ConsumerConfig` builders (durable/dispatch/log-viewer). Coverage 95.2%.
- **`internal/event`: `Bus` interface, envelope, durable consumers, panic recovery, DLQ, Idempotent
  Consumer.** `Publish(ctx, topic string, evt Event) error` (was `[]byte`, since every real caller already
  built an `Event` via `WrapPayload` immediately before marshaling by hand) and
  `Subscribe(ctx, topic string, handler func(Event) error) error` (the handler's error return is what
  makes at-least-once delivery possible at all; a handler that cannot fail forces unconditional
  acknowledgement). `Event` gained `CorrelationID`/`CausationID`/`ChainDepth`/`Actor`/`TraceID`/
  `IdempotencyKey`; six new context helpers (`WithCorrelationID` and siblings, `internal/event/context.go`)
  are what `Publish` itself reads from (`stampEnvelope`, one implementation shared by both adapters,
  Section 25's Build-Once rule) so no call site can omit them by forgetting to set them. `Chained(parent,
  received)` is the wire-level mechanism Section 15's loop prevention needs (continues an existing
  correlation chain, increments depth); enforcing the depth ceiling is stated explicitly as the
  still-unbuilt Trigger Engine's job, not preempted here. `natsBus.Subscribe` (new `consumer.go`) now uses
  a durable, named consumer per topic (`topology.SubscribeConsumerConfig`) instead of an anonymous
  ephemeral one, captures the previously-discarded `jetstream.ConsumeContext` and stops it on `ctx.Done()`,
  and recovers a handler panic via `defer recover()` rather than killing the process. `HandleDeliveryFailure`
  (new `dlq.go`) is the single Dead Letter Queue implementation Section 26.3 asks for ("lives in the bus
  adapter, never in a consumer"): below `maxDeliver`, `NakWithDelay` using the existing
  `pkg/retry.Backoff` formula; at or above it, republishes a `DeadLetterEnvelope` to
  `topology.DeadLetterSubject` and terminates the original. Both `natsBus.Subscribe` and
  `runner.Agent.handleMessage` call this same function -- `Agent` stays on its own pull-based
  `jetstream.Consumer` by design (PATTERNS.md's "Push vs Pull Execution Model" entry) and so cannot go
  through `Bus.Subscribe`, but must not hand-roll a second DLQ mechanism either. `NewIdempotentBus`
  (new `dedup.go`) is the consumer-side Idempotent Consumer decorator: check-seen, call the handler,
  mark-seen only on a nil return -- an ordering fix the design review caught before code was written (see
  below). `DefaultIdempotencyKeyDerivation` returns `evt.ID`, both stamped on the envelope and passed as
  `jetstream.WithMsgID(...)` on every publish, engaging JetStream's own producer-side dedup window; this
  went through a real, test-caught design correction, also below. `Bus` gained `Close() error`
  (Graceful Shutdown, wired into both new binaries' shutdown paths); `inProcessBus.Close` is a documented
  no-op.
- **The five direct-NATS callers, fixed.** `internal/api/dispatcher.go` (the actual fix for the silent
  production failure above: now publishes through `Bus.Publish` to `topology.DispatchSubject()`, with a
  per-device `JobID+DeviceName` idempotency key and the request's actor/trace ID bridged onto the publish
  context), `internal/adapters/native/adapter.go` and `internal/ansible/receptor.go` (migrated from raw
  `jetstream.JetStream.PublishMsg` to `Bus.Publish`, closing `FAILURE_PATTERNS.md` #17 for real, not just
  reconfirming it), `internal/runner/agent.go` (stays pull-based by design, gained the shared DLQ call and
  a real bug fix, below), and `internal/api/logs.go` (deliberately kept off `Bus.Subscribe` -- PLAN.md
  Section 26.4's own named exception for per-viewer, no-shared-ack log streaming -- but rewired onto
  `topology`, and gained a real, separately-discovered fix, below). `internal/archtest`'s own
  `TestAdapterAllowlistHasNoStaleEntries` independently confirmed the migration: it failed the moment
  `native`/`ansible` stopped importing NATS directly, and the allowlist was updated to match (`topology`
  added, since it is now the one place driver-shaped config types are declared).
- **`cmd/controller` and `cmd/runner`** (both new): the composition roots Section 25 names as Phase 2's own
  deadline ("every other primitive is advisory until something assembles them"). `cmd/controller`: embedded
  SQLite (Postgres deliberately deferred -- no ent Postgres migration path exists anywhere in this repo
  yet, and building one is not this phase's checklist item), `event.NewNatsBus` (its first real production
  caller anywhere in this codebase), the dispatch/logs routes behind `AuthMiddleware`, `ReadHeaderTimeout`
  set (a real gosec finding fixed, not waived, since this is the first real production HTTP surface this
  repository ships). `cmd/runner`: `topology.DispatchConsumerConfig()`, `native.NewAdapter(bus)` (the only
  existing type that actually satisfies `runner.ExecutionAdapter`). Both verified against the real built
  binaries, not just compiled: a manual run (real NATS container, seeded SQLite device, minted JWT)
  dispatched a runbook over HTTP, confirmed the runner picked it up with correct fields, and streamed real
  SSE log output with the full envelope back to the client.

**Five real defects found and fixed, three by tests failing for real, two by design review before code
was written -- none by inspection alone.**

1. *(Design review)* The Idempotent Consumer decorator's first draft marked a key seen *before* calling the
   handler, which would have made a first-attempt failure indistinguishable from a completed success on
   redelivery, permanently swallowing it before the DLQ could ever see it exhausted. Fixed before any code
   was written: check-seen, call handler, mark-seen only on nil. Pinned by
   `TestNewIdempotentBus_FailureIsNotSwallowedAsAlreadySeen`.
2. *(Design review)* `Nak()` was the first draft's choice for a below-threshold redelivery; `Nak()` ignores
   `AckWait`/backoff entirely and triggers instant redelivery, which is not "negative acknowledge with
   delay" Section 26.1 actually asks for. Fixed to `NakWithDelay` using the existing backoff formula before
   any code was written.
3. *(A real NATS container test, not a mock)* `DefaultIdempotencyKeyDerivation`'s first draft hashed
   `topic + evt.Data` instead of `evt.ID`, on backwards reasoning about which parts of an `Event` are
   "stable." Two distinct back-to-back publishes with identical content collapsed into one stored message
   during ordinary dev-time testing. Fixed to key on `evt.ID`. `FAILURE_PATTERNS.md` #29,
   `LESSONS_LEARNED.md` #31.
4. *(`tests/e2e`'s real Postgres-plus-NATS integration test)* Once every publish wrapped its payload in the
   `Event` envelope, `runner.Agent.handleMessage`'s own decode -- which bypasses `Bus.Subscribe` by design
   and so never got the automatic unwrap `natsBus.Subscribe` gives its own callers -- silently zeroed every
   dispatched job's `runbook_id`/`device_name` instead of erroring. `FAILURE_PATTERNS.md` #30,
   `LESSONS_LEARNED.md` #35.
5. *(A new coverage-closing test, then `-race`)* `internal/api/logs.go`'s `StreamLogs` wrote its own
   "connected" SSE line before starting the consumer, which implicitly committed the response to 200
   before a later consumer-start failure could ever be reported as a real HTTP error status
   (`FAILURE_PATTERNS.md` #32). Reordering to fix that then exposed a genuine, `-race`-caught data race
   between the handler and its own delivery callback both writing to the same `http.ResponseWriter`
   unsynchronized (`FAILURE_PATTERNS.md` #33, `LESSONS_LEARNED.md` #34), closed with a mutex serializing
   every write. A related, separately-discovered bug in the same file: `LogStreamer` used to take one
   already-built `jetstream.Consumer` at construction time, so the `{id}` URL param `StreamLogs` reads was
   never actually used to filter anything -- every viewer, whatever job they requested, saw whichever job
   the one shared consumer happened to be built against (`FAILURE_PATTERNS.md` #31). Fixed by building a
   fresh, job-scoped ephemeral consumer per request.

**Fuzz/Stress:** `FuzzDurableName` (`internal/topology`, 194k+ executions), `FuzzPublish`
(`internal/event`, 520k+ executions), `FuzzPayloadEnvelope` (pre-existing, re-run against the larger
`Event`, 111k+ executions), all clean. Benchmarks against a real NATS container, not asserted:
`BenchmarkNatsBusPublish` ~800µs-1.2ms/op; `BenchmarkNatsCorePublish` (bare core-NATS, no JetStream
durability) ~2-4µs/op, the honest floor `natsBus.Publish` is layered on top of, so the ~300-600x gap is a
measured durability cost, not a guess; `BenchmarkNatsBusPublishSubscribeRoundTrip` ~1-1.6ms/op, directly
comparable to the pre-existing `BenchmarkInProcessPublish` (~4-20µs/op).

**Adversarial Pattern Justification:** confirmed `natsBus` had never been selected by any real `main()`
before this phase (`cmd/pleiades` uses the in-process bus); `cmd/controller`/`cmd/runner` are its first
real callers, proven against the built binaries. Confirmed every publish in the codebase now routes
through `Bus.Publish` (Section 26.5's own stated precondition for a later transactional outbox), with
`internal/api/logs.go`'s and `internal/runner/agent.go`'s deliberate non-adoption of `Bus.Subscribe` named
explicitly as bounded exceptions, not oversights.

**Schema/Injection Hardening.** `topology.DurableName`'s hash-suffix design (not sanitize-in-place) is the
real finding for this phase's own new boundary, proven by `FuzzDurableName`. A fresh `gosec` run (not the
Phase 0 CI harness's prior baseline) found 10 real findings across this phase's new/touched files; two
fixed for real (the `HandleDeliveryFailure` `uint64` conversion made provably safe by a `maxDeliver <= 0`
guard, `cmd/controller`'s `ReadHeaderTimeout`), several more fixed as direct improvements while already
touching the surrounding code (`natsBus.Close`, `Agent`'s `Ack`/`Term`, `logs.go`'s `Ack` all now
propagate/log their errors instead of discarding them), the rest waived with a written, per-line reason.
`gosec-waivers.json` went from 17 entries (several stale from this phase's own file restructuring) to 7,
all re-verified against current line numbers. `govulncheck`: 0 called vulnerabilities.

**Release Gate.** `TestNatsJetStreamBus` through the real durable consumer group `Subscribe` now builds
(not the anonymous ephemeral `DeliverNew` consumer the checklist's own unchecked note complained about).
`tests/e2e`'s `TestGrandIntegration`: real Postgres, real NATS, the actual `Dispatcher`/`Agent`/
`native.Adapter` wiring, passing after defect #4 above was found and fixed.
`TestNatsBus_SurvivesConnectionSeverance` (new): a real Toxiproxy container fronting a real NATS
container, AGENTS.md's own Bulletproof Testing Matrix requirement for NATS boundaries, not previously
present anywhere in this repository for any NATS boundary -- proves a publish attempted mid-severance
genuinely fails from the caller's perspective (bounded by a timeout) and that `nats.go`'s own
reconnect-with-backoff recovers Publish/Subscribe afterward with no explicit reconnect logic in `natsBus`
itself.

**Coverage.** Every package this phase touched now meets or exceeds AGENTS.md's 90% target for new/touched
code except two, both with a specific, named, and now-recorded reason for the remainder:
`internal/adapters/native` (84.6%, down from a 92.3% floor recorded before this phase added a genuinely
unreachable `WrapPayload`-error branch to `streamLog` -- `LogEvent` has no field that can fail
`encoding/json`, so there is no way to reach it without an artificial type change) and `internal/api`
(90.4%, up from 82.1%, `HATEOASMiddleware`'s pre-existing, untouched-by-this-phase gap is the only
remainder). `internal/topology` (new) recorded at 95.2%. `coverage-floor.json` updated accordingly; `make
coverage` reports 39 packages measured, none below floor.

**Verified, not assumed.** `gofmt -l`, `go build ./...`, and `go vet ./...` are clean across the entire
repository. `go test ./... -race` passes with zero `FAIL` lines, including every real container test this
phase added (NATS, the durable-consumer-group split, the DLQ paths, the chaos test, `TestGrandIntegration`
with real Postgres). `make gosec`, `make govulncheck`, and `make coverage` all pass; `make ci` passes end
to end.

**Files changed:** `internal/topology/` (new package, 9 files including tests/fuzz), `internal/event/`
(bus.go, payload.go, nats.go rewritten; context.go, consumer.go, dlq.go, dedup.go, dedup_inprocess.go,
dedup_nats.go new; inprocess.go rewritten; every existing test file updated for the new signatures, several
new test files), `internal/api/dispatcher.go` + `logs.go` + `middleware.go` (rewritten/fixed, plus their
tests), `internal/adapters/native/adapter.go` (rewritten, plus tests), `internal/ansible/receptor.go`
(rewritten, plus tests), `internal/runner/agent.go` (fixed, plus tests, new `agent_nats_test.go`),
`internal/engine/executor.go` + its tests (mechanical signature updates), `internal/archtest/layering_test.go`
(allowlist updated), `cmd/controller/` (new binary), `cmd/runner/` (new binary), `cmd/demo/main.go` (fixed),
`tests/e2e/integration_test.go` (rewired), `gosec-waivers.json`, `coverage-floor.json`, `go.mod`/`go.sum`
(added the Toxiproxy testcontainers module and its transitive deps), `.SPECIFICATION/IMPLEMENTATION.md`
(Phase 2 checked off), `FAILURE_PATTERNS.md` (#17 updated, #29-#33 new), `LESSONS_LEARNED.md` (#31-#35 new).

### The Phase 0 CI Harness / Part 0 Readiness session

**What works, verified for real this session:**
- `go build ./...`, `go vet ./...`, `gofmt -l` (excluding stray `.claude/` worktrees left by unrelated
  prior agent runs), `go test ./...`, and `go test -race ./...` are all clean, confirmed by direct runs,
  not by reading a prior claim.
- CI now exists: `Makefile` (`build`/`vet`/`fmt`/`test`/`test-race`/`gosec`/`govulncheck`/`arch`/
  `coverage`/`ci`) plus `.github/workflows/ci.yml`, running `make ci` on push and pull request. `make ci`
  passes end to end, run twice to confirm.
- `internal/archtest` (new package) enforces the Section 25 layering rules as a real `go test`; writing it
  surfaced a real, dormant bug (`internal/ent/embedded.go` opened a SQLite connection with no production
  file registering the driver) fixed on the spot.
- `govulncheck ./...` went from 10 real, called vulnerabilities (7 in `golang.org/x/crypto/ssh`, 3 in the
  Go standard library) to 0, via `golang.org/x/crypto` -> v0.52.0 and a new `toolchain go1.26.5` directive
  in `go.mod`.
- `gosec`'s first ever real run found 33 issues, not the 8 this document already knew about. Every finding
  inside Part 0/Phase 1 scope was individually fixed or waived with a written, per-line `#nosec` reason;
  `internal/ent`'s two generated-file findings are excluded via `gosec -exclude-generated`. The 17 findings
  outside that scope (`internal/api`, `internal/runner`, `internal/ansible`, `cmd/demo`, and
  `internal/event/nats.go`) are each named individually with a reason in `gosec-waivers.json`, enforced by
  `tools/gosec-check` (`make gosec`): unlisted-or-stale findings fail the build.
- A coverage ratchet (`coverage-floor.json`, enforced by `tools/coverage-check`, `make coverage`): no
  package may drop below its recorded floor; 90% (`AGENTS.md`'s own minimum) is the target for new/touched
  code, not a day-one gate against the real 48.2% mean.
- Seven real code defects fixed, each with a test that fails without the fix: `pleiades add-host`
  destroying inventory content it does not understand (a `yaml.Node`-based merge, not a rewrite); no
  plan-time lifecycle rule (`LifecycleRule`); two independently hand-maintained fqcn-to-capability tables
  that had already drifted (`engine.ActionCapability`, one shared table, plus a startup consistency check);
  the device-type discriminator reachable through the mutable property bag (a real, immutable `type`
  column); the provenance/tags columns that existed but were never written on save (both adapters); no
  runtime lifecycle refusal in the executor (partitioned in `runNode`, before dispatch, with a named
  `SkipReason`).
- Adding `LifecycleRule` introduced a real performance regression caught only by re-running the existing
  benchmark, not by any correctness test (`FAILURE_PATTERNS.md` #28): `BenchmarkValidateFullInventory`
  doubled from ~239ms to ~480ms because two rules now independently pay `WorldView.Resolve`'s own
  documented O(n) linear scan for the same target. Fixed with a `Validate`-scoped `resolveCache` on
  `WorldView`; back to ~239-244ms.

**Explicitly not done, by deliberate scope discipline, not oversight:**
- The 17 gosec findings outside Part 0/Phase 1 (`internal/api`, `internal/runner`, `internal/ansible`,
  `cmd/demo`, `internal/event/nats.go`) were waived, not fixed: fixing them would mean editing files owned
  by later, already-implemented phases (11, 13, 14, 15, 17, 25) this session was explicitly told not to
  touch. Each waiver names its owning phase; `AGENTS.md`'s own gosec/govulncheck-before-Phase-20 rule is
  the backstop.
- The unchecked `task.Params["target"].(string)` type assertion in `capability_rule.go`/`blast_radius.go`
  (and now a third site, `lifecycle_rule.go`, added this session to match the existing convention rather
  than diverge) was not fixed: it is Phase 35's own explicit, already-filed checklist item, deliberately
  sequenced "before the translator." This session's own Schema/Injection Hardening item closed by auditing
  and confirming that framing, not by preempting it.
- Most container-backed tests (`internal/event`, `internal/lock`, `tests/e2e`, `cmd/pleiades`'s SSH release
  gate) do not use `goleak`, only `internal/engine/executor_test.go`, `scheduler_test.go`, and
  `internal/transport/ssh/ssh_container_test.go` do. `AGENTS.md` asks for `goleak` in "integration tests"
  broadly; the Phase 0 CI item's literal text ("`goleak` in integration tests") is satisfied since CI does
  run goleak-based checks for real, but the broader convention is not applied everywhere it plausibly
  could be. Not fixed this session: retrofitting it into seven more files across several owning phases was
  judged out of this session's 9-item scope, not verified safe to do quickly. Worth a dedicated pass.
- Did not individually re-audit every one of the (now 85) closed `[x]` items in scope against their
  original evidence line by line; that was infeasible within this session on top of the above. What was
  actually re-verified: the one item whose own text makes a `-race`/`goleak`/`gosec`/`govulncheck` claim
  (Phase 1's Schema/Injection Hardening item, updated with real numbers), plus everything the full test
  suite, `-race`, `gosec`, and `govulncheck` runs touch, which is most of the codebase.

**Files changed:** see "Files changed in the Phase 0 CI Harness / Part 0 Readiness session" below.

### The Secret-Marking and set_metadata session

**Scope: the design work explicitly deferred out of the Phase 1 session, plus one new, related
request raised at the start of this session** ("now we do the secret_fact work, also allow
set_metadata. dynamic metadata will allow for reporting of custom automation statistics").
Entered Plan Mode given the cross-cutting surface (the runbook YAML schema, the executor's action
vocabulary, the CEL/`WorkflowContext` mechanism, the masking model); a Plan-agent review of the
initial draft design found and closed a real hole before any code was written (see "A real hole
the plan agent found" below). See `/root/.claude/plans/polymorphic-fluttering-papert.md` for the
full approved design and its reasoning.

**What was built:**

- **`Task.SecretFields []string`** (`internal/engine/dag.go`, yaml/json `secret_fields`): marks
  named top-level keys of a task's own `ActionResult.Stats`, once computed, as secret. This is the
  project owner's first message verbatim ("facts... secret: true... set a fact from a return"),
  the Ansible-parity gap they named: Ansible itself only has a whole-task `no_log`, never
  per-value secrecy in a registered result. Evaluated in `runOne`, right where `Register`/`Merge`
  already happens, before the value is merged into `WorkflowContext` (so `when_cel` still sees the
  real value; masking is strictly an output-boundary concern, never written back into the store).
- **`Task.SecretMask *SecretMaskSpec`** (`internal/engine/secret_mask.go`, new;
  `SecretMaskSpec{Register, Fields}`, yaml/json `secret_mask`): retroactively marks named fields
  of an *earlier* task's already-registered result as secret, across every device currently
  present under that register (not just one device, matching "wherever they are" in the project
  owner's own words). This is their second message verbatim ("apply a secret_mask to an object...
  keeping returned secrets hidden too"). Deliberately a Task-level field, not a pseudo-fqcn:
  `ActionExecutor.Execute` has no `WorkflowContext` access, and `Register`/`when_cel` are already
  Executor-level, not `ActionExecutor`-level, concerns, so `SecretMask` joins them at the same
  level rather than forcing a much larger interface change. Evaluated once per node in `runNode`
  (not once per device in `runOne`): it does not depend on which device the marking task itself
  targets. `internal/engine/tasktree.go`'s `validateTask` rejects an empty `Register`/`Fields` at
  build time. `internal/validate/secret_mask_rule.go` (new) statically catches a
  `secret_mask.register` that matches no task's `Register` anywhere in the DAG, deliberately
  existence-only rather than ordering-aware (`DAG.Nodes` is an unordered map, and
  `TopologicalOrder`/`LevelIterator` explicitly exclude Rescue/Always from any total order, so
  "earlier" is undefined for a real class of legitimate runbooks): the same fidelity this codebase
  already accepts for the analogous, currently uncheckable `when_cel`/`stat.<register>` reference.
  A bad reference that passes validation anyway is a loud runtime error either way
  (`applySecretMask`'s hard error naming the register, never a value).
- **The masking mechanism itself** (`internal/engine/executor_secrets.go`, new): a run-scoped,
  mutex-guarded `stringSet` (`r.secrets`, fresh per `Run()` call, never stored on the long-lived
  `Executor`, so nothing leaks across a reused `Executor`'s separate runs), fed by
  `markSecretFields`/`applySecretMask`, both routing through a shared `secretMaskValue` guard.
  `RunResult.Secrets` exposes the complete, final set once `Run` returns (via a named return plus
  one `defer`, so even the early `ctx`-canceled/`LevelIterator`-error exit paths populate it); a
  caller (`cmd/pleiades/run.go`) masks its own printed output (the per-node `FAILED:` line and the
  new metadata report) with it, catching a secret discovered only after an earlier task's own
  event already went out. `run.publish` additionally does its own best-effort, in-flight masking
  of each event's message as it is published, documented plainly as incomplete (an event already
  published before a later task marks something secret cannot be retroactively scrubbed), the same
  "must say so, not pretend" honesty this codebase already applies elsewhere. Manually verified
  against the real binary: a value that was never itself marked secret, but merely echoed a marked
  secret as a substring inside an unrelated `set_metadata` field, still came out masked in the
  final printed report, proving the "wherever it appears" guarantee holds in practice, not just in
  the unit tests.
- **`set_metadata`** (`internal/engine/action.go`, `builtinActionExecutor`): a second recognized
  fqcn alongside `noop`, requiring a non-empty `params.data` map (mirroring Ansible's
  `set_stats: data: {...}`, deliberately without its `aggregate`/`per_host` flags: overwrite
  semantics only, nothing asked for them), reporting it as `ActionResult.Stats` with a new
  `IsMetadata` bool set. `Executor` stays fqcn-agnostic (it contained zero fqcn string literals
  before this session and still does): `runOne` only checks `actionResult.IsMetadata`, never
  `task.FQCN`, to decide whether a `Register`'d result also belongs in the new
  `RunResult.Metadata` (populated once, after `Run` completes, from one final
  `WorkflowContext.Read()` call filtered to whichever register names came from a `set_metadata`
  task). `cmd/pleiades/run.go` prints a masked `metadata:` report from it, sorted by register name,
  then device ID, then key, for deterministic output; needs no `WorkflowContext` access itself.
- **A real hole the plan agent found before any code was written**: the initial draft planned to
  blindly `fmt.Sprintf("%v", v)` any `ActionResult.Stats` value named in `secret_fields`/
  `secret_mask` and add it to the mask set. `credential.Mask` has no minimum-length guard, only an
  empty-string skip; marking a short or common value (a stringified bool `true`, a one-digit exit
  code) would have scrubbed that substring out of every later message and printed line for the
  rest of the run, corrupting unrelated output, worse than not masking at all. Closed by
  `secretMaskValue` (`executor_secrets.go`): only an actual `string` at least `minMaskableSecretLength`
  (8, tied to `credential.maskPlaceholder`'s own width) bytes long is accepted; anything else is a
  hard task-level error whose message never includes the offending value itself. `LESSONS_LEARNED.md`
  #29 records this as a general rule, not just a fix.

**Fuzz/Stress:** `internal/engine/executor_secrets_test.go`'s
`TestExecutor_SecretsConcurrentDiscoveryUnderRace` proves the accumulator under the same genuine
device-fan-out concurrency `runOne` already exercises, run under `-race`. `FuzzExecutorRun` and
`FuzzDAGBuilder` (both pre-existing, now exercising the new fields/code paths for free since they
fuzz arbitrary runbook payloads through the same real Build/Run path) ran clean for 20s each, zero
crashes.

**Adversarial Pattern Justification:** the masking-is-output-boundary-only invariant (real values
always reach `when_cel`, masked values never get written back into `WorkflowContext`) is the
single easiest thing here for a future change to accidentally violate, so it carries its own
doc-comment sentence on both `RunResult.Secrets` and `applySecretMask`, not just an implicit
convention. `run.publish`'s best-effort (not complete) masking is stated as a real, permanent
limitation, mirroring the "must say so, not pretend" precedent `action.go`'s doc comment on
unimplemented fqcns already established, rather than a gap quietly left unexplained.

**Schema/Injection Hardening:** `gosec` and `govulncheck` re-run against every touched package
(`internal/engine`, `internal/validate`, `cmd/pleiades`) found zero new findings; the one gosec
finding in that scan (`cmd/pleiades/load.go:53`, G304 on the user-supplied runbook path) is
pre-existing and in a file this session did not touch.

**Release Gate:** manually run against the real `pleiades` binary (see "What was built" above for
the cross-task substring-leak proof): `validate` statically rejects a typo'd `secret_mask.register`
before execution; a `secret_fields`-marked value survives being echoed as a bare `target` by a
later, unrelated task without leaking, both in the failed node's printed line and (best-effort) in
its published event, while a genuinely conditional task in between still branches correctly on the
real, unmasked value; a `set_metadata` task's data shows up in a final, masked `metadata:` report
on a clean run.

### The Phase 1 session

**Scope: Part I Phase 1 only** (`.SPECIFICATION/IMPLEMENTATION.md`), done directly rather than split
across parallel background agents: the surface area is one tightly-coupled schema package (every schema
edit regenerates together via one `go generate ./internal/ent`) plus one repository file, not several
disjoint packages, the same reasoning that kept the Phase W5 session single-threaded. Before writing any
code, a Plan-agent-reviewed, user-approved plan resolved every real design question up front (schema
shapes, the versioned-migration mechanism) rather than guessing mid-implementation; see
`/root/.claude/plans/polymorphic-fluttering-papert.md` for the full approved design and its reasoning.

**What was built:**

- **`DeviceID`, made real.** `Device.device_id` (`internal/ent/schema/device.go`) is a new
  `.Immutable().Unique()` UUIDv7 string column, indexed, replacing the previous
  `strconv.Itoa(dev.ID)`-stringified-integer stopgap `toRecord` used to admit. ent's own internal
  auto-increment integer primary key is untouched and stays a pure storage-layer detail used only for
  edges/FKs, deliberately not widened to a UUID type itself (`LESSONS_LEARNED.md` #26): the actual
  requirement, an opaque identifier distinct from the mutable `name`, safe on the wire, needed a new
  column, not a primary-key type change touching every edge and every existing `dev.ID`-reading call
  site. `entRepository.Save` (`internal/inventory/ent_save.go`) now keys its compare-and-swap update on
  `device.DeviceIDEQ` instead of the integer PK. The two existing real callers of `.ID()`
  (`internal/engine/executor.go`'s lock key, `internal/api/dispatcher.go`'s wire payload field) needed no
  code changes at all: both already consumed `.ID()`, only the opaque value behind it changed.
- **`Group`** (`internal/ent/schema/group.go`, new): `name`, a many-to-many `devices` edge, and a
  self-referential many-to-many `parents`/`children` edge deliberately not `.Unique()` on either side,
  because group nesting is a DAG, not a tree (a group can have more than one parent, exactly like an
  Ansible inventory group listed under more than one parent's `children:` block), mirroring the
  multi-parent generalization Phase W5's `level_iterator.go` already established for `DAG.Adjacency`.
  Schema-only substrate this phase; `Repository.GetGroup` is not wired to it, and the Adversarial Pattern
  Justification below says so plainly rather than implying otherwise.
- **`Organization`** (`internal/ent/schema/organization.go`, new): `name`, a `devices` edge, deliberately
  **optional** on the `Device` side. `PLAN.md` Section 18 says a resource belongs to exactly one
  Organization, but requiring the edge today would break every existing device-creation call site (none
  of which have any concept of organizations yet), and RBAC/tenant-filtering consumption is explicitly
  Phase 8's job. No `Team`/`Role` entities: inventing their shape now would guess at what Phase 8 needs.
- **`Device.source`/`source_synced_at`/`tags`**, wired for real. `toRecord`
  (`internal/inventory/ent_repository.go`) now builds a genuine `inventory.SourceAuthority` from stored
  columns instead of the previous hardcoded `Plugin: "ent"` literal, and calls the same `toTags` helper
  `file_repository.go` already used, so both `Repository` adapters populate `Tags()` identically for the
  first time, closing the exact gap the Phase W4 session of this document named ("`Tags()` currently
  returns a hardcoded empty slice").
- **`TimestampMixin`** (`internal/ent/schema/timestamp_mixin.go`, new), applied to all six schemas.
  Hand written rather than ent's builtin `mixin.Time`, whose `create_time`/`update_time` field names
  would break this codebase's own established `_at` convention (`Revision.changed_at`).
- **`Fact.payload`/`Fact.hash`**, now genuinely `.Immutable()`, not just documented as such. Verified
  non-breaking by grep (nothing called `Fact.Update()...SetPayload`/`SetHash` anywhere). Follow-on
  cleanup: `internal/crypto.EnvelopeEncryptionHook`'s op mask narrowed from
  `OpCreate|OpUpdate|OpUpdateOne` to `OpCreate`, matching what the schema now makes structurally
  possible.
- **`storage.UnitOfWork`** (`internal/storage`, new package): a port plus an ent-backed adapter following
  ent's own documented `WithTx` idiom (`ent.NewContext`/`ent.FromContext` thread the transaction-scoped
  client through `context.Context`). Real caller from day one: `entRepository.Save` runs its Device
  update and Revision inserts through it, replacing its previous hand-rolled `client.Tx`/manual-rollback
  code, with `entRepository.entClient(ctx)` making the port reusable by any future repository method
  reached from inside a `WithTx` callback, not a `Save`-only special case.
- **Versioned migrations, replacing ent's automatic `Schema.Create`, with zero new dependencies.**
  `internal/ent/migrate/apply.go` (new, hand written, coexisting with generated `migrate.go`/`schema.go`
  the same way `embedded.go` already coexists with generated files in `internal/ent`) embeds committed,
  numbered SQL migration files and applies pending ones in filename order, each in its own transaction,
  recording every applied one in a `schema_migrations` table that doubles as the startup schema-version
  gate: `Apply` refuses to start if the database already has a migration this binary's embedded set does
  not recognize, or a gap in an already-applied prefix. The initial migration
  (`migrate/migrations/sqlite/0001_initial.sql`) was captured by diffing a completely empty SQLite
  database against the desired schema via ent's own already-generated `Schema.WriteTo`, sidestepping
  ent's documented `sql/versioned-migration` feature-flag path entirely, which wants a live Atlas "dev
  database" (Docker, for anything beyond SQLite) that has no place in a dependency-free embedded CLI
  (`LESSONS_LEARNED.md` #27). `internal/ent/embedded.go`'s `OpenEmbedded` now opens the raw driver,
  migrates it, and wraps the same connection in the ent client. Only SQLite has an embedded migration
  today; no Postgres composition root exists anywhere in this repository yet (`cmd/controller` is Phase
  2's job), so no Postgres DDL was fabricated against nothing. An optional
  `internal/ent/migrate/gen/main.go` (`//go:build ignore`) gives whoever adds the next schema change a
  documented, repeatable way to produce the next migration file.

**A real, pre-existing-pattern bug was found and fixed while writing this phase's own Fuzz/Stress Test
scaffolding, not deferred.** Three `CreateBulk` call sites in `internal/inventory` (a 50,000-row memory
test, a fuzz target, a 10,000-row benchmark) started failing with `too many SQL variables` the moment
`Device` gained this phase's new columns, because a batch size tuned against the old, narrower row width
silently stopped being safe once `rows * columns` crossed SQLite's fixed placeholder ceiling. Fixed by a
shared, deliberately headroom-padded chunking helper (`bulkCreateDevices`,
`internal/inventory/ent_bulk_testutil_test.go`) used by all three call sites. `FAILURE_PATTERNS.md` #25.

**Fuzz/Stress Test, per the phase checklist's own item.** `FuzzGroupCreation`, `FuzzOrganizationCreation`,
`FuzzDeviceIDLookup` (`internal/ent`), each run for real (`-fuzztime=15s`, 27k-48k executions apiece,
zero crashes); table-driven adversarial coverage of the migration gate itself
(`internal/ent/migrate`'s `TestApply_FailsClosedOnUnrecognizedAppliedVersion`,
`TestApply_FailsWhenMigrationScriptConflictsWithExistingSchema`, `TestCheckGate`'s gap scenarios).
Benchmarked for real: `BenchmarkDeviceIDLookup_Ent` vs `BenchmarkDeviceIDLookup_RawSQL`
(`internal/ent/device_id_bench_test.go`), the honest "industry alternative" baseline for a data-layer
phase specifically (raw `database/sql` is the floor ent's own generated code is layered on top of).
`BenchmarkEntRepositorySave` (`internal/inventory/ent_save_bench_test.go`) now exists where none did
before, directly comparable to the pre-existing `BenchmarkFileRepositorySave`'s identical shape: ~358µs
(ent) vs ~961µs (file) per full `GetByName`/`AddInfo`/`Save` round trip, measured, not asserted.

**Adversarial Pattern Justification, on the four patterns the Pattern Entry Gate named.** The load-bearing
proof for Unit of Work is a genuine, unsimulated mid-transaction failure, not a mock (RULE 0):
`TestSave_RollsBackDeviceUpdateWhenALaterRevisionInsertFails`
(`internal/inventory/ent_save_test.go`) records a pending revision whose value is a Go channel, which
`encoding/json` cannot marshal, so the transaction's second `Revision.Create().Save` genuinely fails
after the Device update and the first Revision insert already succeeded; the test then asserts the
device's version, properties, and history are all exactly as they were before `Save` was called. For
Interceptor, the honest finding is stated plainly rather than stretched: `EnvelopeEncryptionHook`/
`EnvelopeDecryptionInterceptor` still have zero production callers, and installing them is Phase 5's own
explicit checklist item, whose own text already flags today's target (`Fact.payload`) as likely wrong, so
this phase records the gap rather than preempting that rework.

**Schema/Injection Hardening.** Every new boundary this phase introduced (new SQL predicates, the new
`tags` JSON column, the new `//go:embed`'d migration files) was audited against Phase 39's categories and
came back clean: SQL stays entirely behind ent's parameterized builder (`FuzzDeviceIDLookup` exercises
injection-shaped strings for real and finds nothing), the embedded migration files are compiler-fixed
content with no runtime path to traverse, and `Organization`'s schema-only status this phase is stated
explicitly so no tenant-isolation claim is implied from the table's existence alone. `gosec` and
`govulncheck` were both run against every new and touched file in this phase specifically: zero new
findings from either; the pre-existing findings both tools report (file-permission/int64-to-uint64
gosec warnings, `golang.org/x/crypto/ssh` CVEs from the Phase W6 transport work) all predate this phase
and sit entirely outside the files it touches.

**Verified, not assumed.** `gofmt -l`, `go build ./...`, and `go vet ./...` are clean across the entire
repository. `go test ./... -race -cover` passes with zero `FAIL` lines, including every real container
test (the Phase W6 SSH container suite ran for real, ~33s, not skipped). The Release Gate
(`internal/ent/client_test.go`'s `TestGraphTraversal`) was repointed from `enttest.Open`'s auto-migration
shortcut to the real production path this phase built (`LESSONS_LEARNED.md` #28 explains why only this
one test needed repointing, not every `enttest`-based test in the repository), and passes through it. The
real built `pleiades` binary was also exercised by hand (`init` → `add-host` → `validate` → `run`
against a fresh scratch directory) to confirm zero regression to the Walk-tier CLI path, which this phase
did not touch.

**Deliberately deferred: secret-marked facts/registered values.** Mid-session, the project owner flagged
a real gap Ansible itself does not solve either: a task's registered/returned output can carry a value
that is only discovered to be a secret at runtime (a generated password, a Cisco DevNet dynamic AAA
token), and there is currently no way to mark such a value, or an already-registered object's specific
fields, as secret so that every later surface it could reach (other facts, stat lookups, logs, event
payloads, API output) shows it masked. This is explicitly not the same problem
`internal/credential.Mask()` already solves (that masks *known, pre-registered* credentials out of
arbitrary text); it is closer to, and the project owner and this session agreed genuinely sharpens, the
still-open question in Phase 5's own checklist about whether `Fact.payload` or `Device.properties` is
even the right place for a secret to live, and the named-but-unbuilt "transparent sealing of Cisco DevNet
dynamic AAA logins" item in that same phase. Asked directly whether to fold this into Phase 1, pause
Phase 1 to design it now, or finish Phase 1 first and design it next: **the project owner chose finish
Phase 1 first, design this next.** Not designed or scoped further this session beyond recording it here
precisely, on purpose, so the next session does not have to reconstruct the requirement from a vague
memory of the conversation. See "Current blocker / next step."

**Resolved in "The Secret-Marking and set_metadata session" above** (`Task.SecretFields`/
`Task.SecretMask`, scoped deliberately to the ephemeral `WorkflowContext`/`register` mechanism,
not to `Fact.payload`/`Device.properties`; Phase 5's own still-open question is untouched by that
resolution and remains open).

### The Phase W6 session

**Scope: Part 0 Phase W6 only** (`.SPECIFICATION/IMPLEMENTATION.md`), the phase the prior handoff named
as the next structural step. Two genuinely disjoint new packages were built as two parallel background
agents in isolated git worktrees (mirroring the Phase W4 four-parallel-agent precedent, scaled to this
phase's actual two disjoint slices), each briefed against a precise, pre-agreed type contract so they
would integrate without drift; this session's own role was the same as every prior multi-agent session's:
assemble the briefs, independently re-verify every claim (rebuild, re-vet, re-run `-race`, read the actual
code) before trusting either agent's self-report, then do the cross-package integration and Release Gate
work no disjoint slice could do alone. A design decision genuinely open at the start (how Walk-tier SSH
credentials should be supplied, since no `CredentialStore` of any kind existed anywhere in this codebase
and PLAN.md Section 17's own version is explicitly Crawl/Run-tier, Postgres/Vault-backed, behind unbuilt
Phase 22) was put to the project owner directly rather than guessed; the answer (a new minimal
`credential.Store` port now, a Walk-tier local-file adapter, Phase 22 adds a database/Vault adapter behind
the same port later) shaped the whole session.

**What was built:**

- **`internal/credential`** (new package, Agent A): the Walk-tier `CredentialStore` port. `Credential`
  (Username/Password/PrivateKeyPEM/Passphrase) is redaction-safe through every serialization mechanism
  this codebase's own audit could find a caller for: `fmt` (via `String`/`GoString`), `encoding/json` (via
  `MarshalJSON`), and `log/slog` (via `LogValue`) all render the same `<redacted, set>`/`<not set>` shape,
  never the real bytes. `Mask(secrets, text)` (`mask.go`) scrubs known secret substrings out of arbitrary
  text, longest-secret-first so one secret can't carve into another's placeholder; a real fuzz-found edge
  case (a secret starting or ending in `*` can, at a placeholder's boundary only, reconstruct itself from
  adjacent leftover text) is proven, documented precisely, and pinned with a named regression test rather
  than papered over. `NewFileStore`/`SaveFileStore` (`file_store.go`, `file_store_save.go`) persist secrets
  under `<dir>/.pleiades/credentials.yaml`, each secret field independently AES-256-GCM encrypted by
  **reusing `internal/crypto.Service`** (built for PLAN.md Section 17's Postgres-backed store, previously
  called only by the `ent.Fact` hook; this is its second production caller and first reuse across a
  storage medium, proving the DRY reuse this codebase's own rules ask for actually pays off). The AES
  master key resolves from `PLEIADES_MASTER_KEY` or a generated, `0600`-permission local file
  (`master_key.go`), written atomically (`file_store_save.go`'s `atomicWriteCredentialsFile`, mirroring
  `internal/inventory/file_repository_save.go`'s own temp-file-plus-rename idiom). Coverage 89.9%
  (package-wide; the shortfall from the 90% bar is entirely `os.Chmod`/`os.CreateTemp`/`os.Rename` failure
  branches unreachable while running as root in this sandbox, the same documented, unavoidable limitation
  `internal/inventory`'s own `atomicWriteFile` already carries).
- **`pkg/retry`** (new leaf package, bundled into Agent A as a small, unrelated, low-risk DRY extraction):
  `Backoff(base, max, attempt)`, the exact jittered-exponential-backoff formula that used to live
  hand-rolled inside `internal/runner/agent.go`'s unexported `calculateBackoff`, extracted to a pure
  function with zero behavior change (proven, not just claimed: `internal/runner`'s own test suite passes
  unchanged after the refactor) so `internal/transport/ssh` and `internal/runner` share one implementation
  instead of two copies of the same math.
- **`internal/transport`** (new package, Agent B): a deliberately tiny, protocol-agnostic port:
  `Target{Host, Port}`, `Result{Stdout, Stderr, ExitCode}`, `Transport.Exec`, with a precisely documented
  contract distinguishing a non-zero `ExitCode` (not a Go error; the command ran and reported failure) from
  a non-nil error (the outcome could not be determined at all). It imports nothing from `internal/engine`,
  so Phase 16's future runner mesh can hold the exact same transport behind its own seam later, per this
  phase's own doc note, without this package changing.
- **`internal/transport/ssh`** (new package, Agent B): the first real Adapter anywhere in this repository
  to genuinely contact a device, over `golang.org/x/crypto/ssh`. Retry-with-backoff-and-jitter and a
  new, minimal, per-target circuit breaker (`circuit_breaker.go`, the first real implementation anywhere
  in this repo of `.SPECIFICATION/PATTERNS.md`'s own "Circuit Breaker" entry, previously marked only
  "POTENTIALLY") both guard the dial phase exclusively; once a command is actually sent to a session, it
  is never retried, since a network failure mid-command leaves its real-world outcome unknown and blind
  retry would violate this codebase's own Convergence principle (`LESSONS_LEARNED.md` #24). Host key
  verification (`known_hosts.go`) fails closed on every branch (missing file, unset `$HOME`, unknown host,
  mismatched key), with the only bypass an explicit, loudly-named `InsecureSkipHostKeyVerify` opt-in
  (`LESSONS_LEARNED.md` #25), proven against a real forged host key, not just argued. A real bug was
  found and fixed while writing the real-container tests: `realDial`'s original draft only bounded the TCP
  connect step with `DialTimeout`, so a dial against a severed connection with a bare `context.Background()`
  could hang roughly 77 seconds (the OS's own TCP timeout) instead of respecting the documented fallback
  bound; fixed by deriving a `context.WithTimeout` from `config.Timeout` and using it for the whole dial
  sequence, confirmed by a real container stop/reconnect test dropping from ~77s to ~6.8s. Coverage 95.7%.
- **`internal/engine/action_ssh.go`** (new file, this session's own integration work): `TransportBinding`
  and `NewTransportActionExecutor`, the literal "Strategy keyed by capability" the Pattern Entry Gate
  names, mirroring `validate.actionCapability`'s own established "a new action is a new map entry, never a
  change to the executor itself" precedent. `"ssh_exec"` (already the example FQCN `dag.go`'s own doc
  comment and `capability_rule.go`'s `actionCapability` map used before any code implemented it) dispatches
  through a real `transport.Transport`; every other FQCN, `"noop"` included, falls through unchanged to
  the existing `builtinActionExecutor`, so Phase W5's own Release Gate keeps passing with zero
  modification to it. `Changed` defaults `true` for `ssh_exec` (the opposite of `noop`'s default, since a
  raw remote command is not provably idempotent, mirroring Ansible's own `command`/`shell` module),
  overridable via the same `params.changed` convention `noop` already established. Coverage 100%.
- **`cmd/pleiades/addcredential.go`** (new CLI subcommand): `pleiades add-credential <device> --username
  <user> [--password <p> | --key <path> [--passphrase]]`, prompting interactively with no terminal echo
  (`golang.org/x/term.ReadPassword`) when no `--password`/`--key` is given, so a secret does not have to
  land in shell history or a process listing by default. This is not optional scope: unlike
  `inventory.yaml`, an AES-GCM-ciphertext credentials file cannot be hand-edited, so without this command
  the credential store would be unusable and untestable through the real CLI at all.
- **`cmd/pleiades/lazy_credential_store.go`** (new file): defers resolving the master key and constructing
  the file-backed store until the first actual `Lookup`, so a `"noop"`-only runbook still runs with zero
  credential setup and `pleiades run` never creates `.pleiades/master.key` on disk unless something
  actually needs a credential. Proven, not assumed: a dedicated test asserts no file exists after mere
  construction.
- **`cmd/pleiades/run.go`** (composition root rewiring): constructs the lazy credential store and
  `sshtransport.New(sshtransport.Options{})` (its own conservative defaults: fail-closed known_hosts,
  bounded retry, a circuit breaker) and wires both into `engine.NewTransportActionExecutor`, replacing the
  bare `engine.NewBuiltinActionExecutor()` previously passed to `engine.NewExecutor`.

**A real, user-facing bug was found and fixed while building this phase's own Release Gate, not
deferred.** `add-host --set port=<n>` silently produced a `port` property stored as a YAML **string**,
which `SSHPort()`'s `Properties.Int` accessor cannot read (it only recognizes `int`/`float64`), so
`SSHPort()` silently fell back to its own default of 22 with no error at all; the identical gap applied
to any `--set` bool property (`netconf_enabled`) too, since `Properties.Bool` has the same strictness.
Nothing had ever exercised `--set` for a non-string property through the real CLI before this phase needed
a real container's real non-default mapped port. Fixed at the root cause: `addhost.go`'s `keyValueList.Set`
now infers a value's type (`true`/`false` become `bool`, a base-10-integer-parseable value becomes `int`,
everything else, including a dotted version string, stays a `string`) the same way `inventory.yaml`'s own
YAML decoder would. `FAILURE_PATTERNS.md` #21.

**Fuzz/Stress Test, per the phase checklist's own item.** `FuzzHostKeyCallbackConstruction`,
`FuzzBuildAuthMethod` (`internal/transport/ssh`), `FuzzNewFileStoreParsesArbitraryYAML`, `FuzzMask`
(`internal/credential`): all clean. Real stress, not simulated: `TestSSHContainer_StoppedContainerRetriesThenBreakerOpens`
severs a real container's TCP connection mid-test and proves retry-then-breaker-opens against the genuine
failure. Benchmarked for real against `ansible-playbook` on the identical task against the identical real
container (`sshpass` installed this session specifically so the comparison would run for real rather than
honestly skip): `BenchmarkSSHExec` ~43.1ms/op versus `BenchmarkAnsiblePlaybookComparableSSH` ~1.01s/op,
roughly 23x, recorded exactly as measured.

**Adversarial Pattern Justification, on two fronts.** First, the Strategy seam: `TestTransportActionExecutor_DispatchesToASecondUnrelatedProtocol`
registers a second, wholly independent, in-test-only protocol (its own capability, its own fake
`transport.Transport`, its own `Target` accessor) in the same `bindings` map as `ssh_exec` and proves both
dispatch correctly with zero change to `transportActionExecutor` itself: the seam genuinely generalizes
past SSH, not just in argument. Second, the MITM defense: a real container's real host key was captured
via a bootstrap dial, proven to be accepted when correctly recorded in `known_hosts`, and proven to be
**rejected** when a different, forged key was substituted for the same host
(`TestSSHContainer_HostKeyVerification`, plus a faster synthetic version in `known_hosts_test.go`).

**Schema/Injection Hardening, run as a background workflow, mirroring Phase 39's own methodology at
Phase W6's own scale.** Three independent finder agents, one per new boundary this phase introduced
(credential storage; command-execution/injection; auth/host-key handling plus secret masking), each
required to write and run real Go test code against the real packages, not read structurally. A second,
independent pass then adversarially re-checked every claim (attempted its own reproduction, tried to
refute it) before anything counted as real. Three findings survived that second pass and were fixed by
this session directly:

- **`credential.Credential` leaked every secret through `encoding/json` and `log/slog`'s JSON handler.**
  Neither consults `fmt.Stringer`, so the type's own doc comment claiming a leak was "structurally
  impossible" was true for exactly one family of Go serialization, not all three the audit actually
  tried. Fixed: `MarshalJSON` and `LogValue` (`credential.go`), both returning the same redacted shape
  `String` already provided. `FAILURE_PATTERNS.md` #22.
- **An `ssh_exec` transport-level error (dial/auth failure) was not masked, only a command's captured
  stdout/stderr was.** No real `golang.org/x/crypto/ssh` error embeds credential material today, but
  nothing enforced that as an invariant. Fixed: the secret list is computed once, before `Transport.Exec`
  is called, and both failure shapes (`Exec` returning an error, and a non-zero exit code) are masked
  through it; the transport-error path deliberately breaks `%w` wrapping (masks via `%s` instead) so a
  masked error can never be unwrapped back to its unmasked original by a caller further up the stack.
  `FAILURE_PATTERNS.md` #23.
- **`.pleiades`'s directory permissions were not tightened when the directory already existed**
  (`os.MkdirAll`'s mode argument is create-only, a no-op on an existing directory), rated low severity by
  the audit since the actual secret files are always written `0600` regardless. Fixed anyway, cheaply, for
  defense in depth: an explicit `os.Chmod(dir, 0700)` follows every `MkdirAll` in this package now.
  `FAILURE_PATTERNS.md` #24.

Everything else audited (command-verbatim-passing under adversarial device `Name`/`Properties` content, no
local shell invocation anywhere in `internal/transport/ssh`, YAML deserialization robustness, host-key
fail-closed behavior) survived the same adversarial re-check with no real finding.

**Verified, not assumed.** `gofmt -l`, `go build ./...`, and `go vet ./...` are clean across the entire
repository. `go test ./... -race -cover` passes with zero `FAIL` lines, container tests included (not
skipped): `internal/credential` 89.9%, `internal/transport/ssh` 95.7%, `internal/engine`'s new
`action_ssh.go` 100%. The Release Gate itself (`TestCLI_RunExecutesSSHTransport`) was run for real against
Docker, not merely written: the real built binary configured a real, independently-implemented `sshd`
container end to end (`init` → `add-host` → `add-credential` → `run`), with real fail-closed host key
verification (a real known_hosts file populated with the container's own captured key, exactly as an
operator's would be), and the result was confirmed by a second, independent SSH connection this test
opened itself and used to read back the file the runbook's task actually wrote on the container, never by
trusting `pleiades`'s own printed output.

### The Phase W5 session

**Scope: Part 0 Phase W5 only** (`.SPECIFICATION/IMPLEMENTATION.md`), the next step the prior handoff
named, plus a mid-session addition from the project owner: a new final phase, Schema / Injection Testing,
added to `IMPLEMENTATION.md` and then run for real against previously completed phases (see below). This
session's own work, not delegated to background agents: the surface area is one tightly-coupled package
(`internal/engine`) plus its one composition-root caller, not several disjoint packages the way Phase W4's
four adapters were, so it was built directly rather than split across parallel agents.

**What was built:**

- **`internal/engine/level_iterator.go`:** `LevelIterator`, a generic topological-level walker built on a
  new shared helper, `reachableWithInDegree` (extracted from `TopologicalOrder` itself, `topology.go`,
  with zero behavior change verified by the existing `topology_test.go` passing unchanged). It is written
  against `DAG.Adjacency`'s real general shape, a node can have more than one parent, per PATTERNS.md's
  own Composite entry, not against the linked-list-only shape `synthesizeChain` happens to be the only
  current producer of. `FuzzLevelIterator` builds synthetic diamond and long-chain graphs directly
  (bypassing the tree-walk builder, which cannot produce a diamond) and asserts the topological ordering
  property holds; 250k+ executions, zero failures in a 15s local run. `LESSONS_LEARNED.md` #22.
- **`internal/engine/workflow_context.go`:** `NewInProcessWorkflowContext`, the Walk-tier local adapter
  behind the `WorkflowContext` port (`trigger.go`), which had zero implementations before this session,
  the same "adapter behind an existing port" shape Phase W4 established for `lock.Manager`/`event.Bus`/
  `inventory.Repository`. A plain nested map (`nodeID` then `deviceID`) guarded by one mutex; `Read`
  returns a deep copy so a caller can never observe or corrupt a later `Merge`.
- **`internal/engine/action.go`:** `TargetResolver` (satisfied for free by `validate.WorldView`, which
  already has the identical `Resolve` method, so no logic is duplicated across the two packages) and
  `ActionExecutor` (the Strategy seam Phase W6 replaces with a real transport). The Walk-tier default,
  `NewBuiltinActionExecutor`, knows exactly one action, `"noop"`, which echoes its own `Params` into
  `ActionResult.Stats` and reads an optional `Params["changed"]` bool, the only way to prove conditional
  branching end to end before a real transport exists. Every other `fqcn` fails with an explicit
  "not implemented" error, never a fake success (the same honesty rule the Forge catalog's stub decision
  already established).
- **`internal/engine/executor.go`:** `Executor.Run` walks a `*DAG` one `LevelIterator` level at a time,
  running every node in a level concurrently (Fan-Out/Fan-In via a shared `runConcurrently` helper),
  bounded by one shared semaphore per `Run` call (the Worker Pool) sized to match `ansible-playbook`'s own
  default `forks` value (5) for a fair benchmark comparison. `nodeExecution` is the Command object. A
  node failure stops the walk after its own level finishes; a skipped (condition false) node never does.
  Publishes events through the real `event.Bus.Publish` port, wrapped in a genuine `event.Event` envelope
  (`event.WrapPayload`), not a raw payload; the first draft skipped the envelope and every test passed
  except the one actually decoding a subscriber's received field, `LESSONS_LEARNED.md` #23. Coverage
  91.4% package-wide (all new code 82-100% per function).
- **`cmd/pleiades/run.go`:** rewired to construct a real `Executor` (in-process lock manager, in-process
  bus, in-process workflow context, the builtin action executor) after validation passes, print each
  node's real outcome, and return a non-zero exit on any node failure. `run.go`'s own doc comment, which
  explicitly named this as the file that would gain a real execution step once Phase W5 landed, was
  rewritten to match.
- **`cmd/pleiades/e2e_test.go`:** new `TestCLI_RunExecutesConditionalBranch`, Phase W5's Release Gate
  itself, run against the real built binary: a `precheck` task registers a stat, a `reboot` task's
  `when_cel` reads it and is true (runs, reports `changed`), a `skip-me` task's `when_cel` reads the same
  stat and is false (never runs). Manually exercised against the built binary before being locked in as a
  test, exactly the "verified against the real binary, not asserted from a plan" standard this document's
  earlier sessions already hold themselves to.

**Fuzz/Stress Test, per the phase checklist's own item.** `FuzzLevelIterator` and `FuzzExecutorRun` (the
latter running the full `Executor.Run` path, not just the ordering primitive) both fuzz synthetic diamond
and long-chain DAGs; both ran clean locally (250k+ and 160k+ executions, zero failures). Benchmarked
against a real, present-on-this-machine `ansible-playbook` binary (`BenchmarkAnsiblePlaybookComparable`),
not skipped: an equivalent five-task, `connection: local`, fact-gathering-disabled playbook took roughly
755ms per run against `BenchmarkExecutorRun_FiveTaskChain`'s roughly 178µs, run for real, not asserted.

**Adversarial Pattern Justification, attacked on two fronts.** First, whether the in-process Command
(`nodeExecution`) and the distributed one (`runner.DispatchPayload`) are genuinely the same shape at
different granularity: they are, the only difference is what consumes them. Second, whether the event
contract in-process execution reports actually matches what the distributed native Adapter reports: it
does by design (`nodeEvent` mirrors `adapters/native.LogEvent` field for field), but attacking this
surfaced a real, pre-existing, out-of-scope defect: `adapters/native.Adapter.streamLog` publishes to a
subject (`jobs.logs.<job-id>`) that does not match its own bus's configured stream subjects
(`pleiades.events.>`) at all, and discards the resulting publish error unconditionally. Recorded, not
fixed (Phase 14's own file): `FAILURE_PATTERNS.md` #17.

**Verified, not assumed.** `gofmt -l`, `go build ./...`, and `go vet ./...` are clean across the entire
repository. `go test ./... -race -cover` passes with zero `FAIL` lines; `internal/engine` alone is at
91.4% coverage. The Release Gate scenario was run by hand against the real built binary before being
locked into a test (shown in this session's own transcript, not just claimed).

### The Phase 39 session

**Scope: a new phase, added mid-session at the project owner's request, not named in any prior handoff.**
`.SPECIFICATION/IMPLEMENTATION.md` gained a new final phase, Part VIII, Phase 39: Schema & Injection
Hardening, deliberately cross-cutting rather than one new package, re-examining every boundary already
built by the phases before it (deserialization, CEL expressions, SQL, command execution, NATS subjects,
filesystem paths, JWT validation) under real adversarial input. This session's own role: write the phase's
checklist, then run it, not just define it and stop.

**How it was run:** five independent background agents, one per audit category (deserialization + CEL;
SQL; command/argument injection; NATS subject injection + path traversal; JWT forgery), each instructed
that a claim only counts if backed by a real test run against the real code, not a structural read-through.
Each wrote and ran throwaway Go tests against the real packages (deleted afterward, confirmed via `git
status`), and none were permitted to edit production source; this session read every report, independently
judged which findings were real, and applied every fix itself.

**Two real, confirmed, fixed findings:**

- **A single `when_cel` string can hang a task's execution for minutes.** cel-go bounds an expression's
  parsed size (100,000 code points) but not its evaluation cost; a nested comprehension well within that
  size limit measured multiple real seconds of CPU time and scales quadratically, with no timeout anywhere
  `Executor.runNode` calls `Eval`. Fixed: `engine.NewCELEvaluator`'s `Compile` (`cel.go`) now builds every
  `cel.Program` with `cel.CostLimit(100_000)`, a value chosen from a real measurement (it rejects the
  pathological case in well under a second; any realistic condition costs orders of magnitude less).
  `TestCELEngine_RejectsExpensiveComprehension` (`cel_test.go`). `FAILURE_PATTERNS.md` #19.
- **An unvalidated runbook `id:` can widen or misroute a NATS subject.** `WorkflowDef.ID` flowed
  unvalidated into `Executor.publish`'s subject string; a crafted `id: "billing.exfil"` was proven,
  against the real in-process `Bus`, to make a subscriber scoped to `pleiades.events.workflow.billing.>`
  also receive an unrelated workflow's events. Latent (no scoped subscriber exists in production code
  yet), not exploited, but real. Fixed: `buildFromDef` (`dag.go`) now rejects any `id` outside
  `[A-Za-z0-9_-]*` via a new `validRunbookID` regexp, checked once at the one compilation path every
  surface format shares. `TestDAGBuilder_RejectsUnsafeRunbookID`/`_AllowsSafeRunbookID` (`dag_test.go`).
  `FAILURE_PATTERNS.md` #18.

**One real finding, deliberately deferred, not fixed:** JWT validation (`internal/auth/jwt.go`) pins its
signing algorithm correctly (proven against real forged `alg: none` and RS256-confusion tokens, both
rejected) and correctly rejects expired/not-yet-valid/tampered/stripped tokens, all proven with real forged
tokens run through the real `ValidateToken`, not just read. But it pins no issuer or audience, so a token
signed with the same secret by an unrelated issuer would be accepted. Not fixed because no token-issuing
code exists anywhere in this repository yet to define a real issuer/audience to pin against; recorded as
`FAILURE_PATTERNS.md` #20, to be revisited when PLAN.md Section 17 or 32 defines one.

**Everything else audited came back clean, each verified empirically, not assumed:** YAML alias/anchor
bombs are already rejected by the parser library itself (closed the one real gap here too: no named
regression test existed before this session; added `TestBuildFromYAML_RejectsAliasBomb` and a matching
curated `FuzzBuildFromYAML` seed). Deep JSON/YAML nesting is bounded by each library's own guard to
100,000 levels tried. No SQL injection anywhere: every database call site uses ent's parameterized
builder, proven with a real malicious device name round-tripped through a real database. No command
injection: all seven `os/exec` call sites in the repository are hardcoded test/benchmark scaffolding, none
reachable from runbook or device data (concrete guidance recorded for Phase W6/25, which will be the real
surface once built). No path traversal risk: the Walk-tier CLI reads exactly the path its own user names,
the same trust model as `cat`, and `internal/api` does no filesystem access at all today.

**Living documents updated:** `FAILURE_PATTERNS.md` #18-20, `.SPECIFICATION/IMPLEMENTATION.md` (all of
Phase 39, `[x]` on every item with the evidence above).

**Verified, not assumed.** Every fix was re-run against the full test suite after being applied:
`gofmt -l`, `go build ./...`, and `go vet ./...` stayed clean; `go test ./... -race -cover` passed with
zero `FAIL` lines both before and after the fixes landed.

### The Phase W4 session

**Scope: Part 0 Phase W4 only** (`.SPECIFICATION/IMPLEMENTATION.md`), the next step the prior handoff
named. Four genuinely disjoint adapter builds (one package each: `internal/lock`, `internal/event`,
`internal/inventory`, `internal/ent`) were run as four parallel background agents, mirroring the
four-parallel-agent pattern the prior restructure session established (`LESSONS_LEARNED.md` #11); this
session's own role was assembling the task briefs, then independently re-verifying every claim (rebuilding,
re-vetting, re-running tests myself rather than trusting each agent's self-report) before treating any of
it as done, and doing the cross-package integration work no single agent's disjoint slice could do alone.

**What was built, one per port:**

- **`internal/lock/inprocess.go`:** `NewInProcessManager`, stdlib-only, genuinely honors `Acquire`'s
  per-call `ttl` (unlike `natsLockManager`, which ignores it in favor of one fixed 24-hour bucket TTL,
  now recorded as `FAILURE_PATTERNS.md` #15). A shared conformance suite
  (`internal/lock/conformance_test.go`) runs identically against this adapter and, in a new
  `TestNatsManagerConformance`, against a real ephemeral NATS container. Coverage 91.7%.
- **`internal/event/inprocess.go`:** `NewInProcessBus`, matching `natsBus`'s fire-and-forget publish,
  decode-at-delivery-time, silent-drop-on-malformed-payload, and trailing `>` wildcard subject contract
  exactly. This session added the cross-adapter half the building agent had explicitly deferred:
  `internal/event/conformance_test.go`'s `runBusConformance`, run against both the in-process bus and a
  real NATS container (`TestNatsBusConformance`, including a real 3-subscriber fan-out), all passing for
  real. Coverage 85.3%.
- **`internal/inventory/file_repository*.go`:** a file-backed `Repository` keeping `hosts.yaml` exactly
  as-is and adding a sidecar file (`.inventory-state.generated.yaml`) for version, lifecycle state, and
  history, the same current-state/audit-trail split the ent schema already uses. `Save` matches
  `entRepository.Save`'s exact optimistic-concurrency contract and writes both files atomically
  (temp file plus `os.Rename`). A real defect was found and fixed while writing its tests:
  `yaml.Marshal` panics rather than errors on a handful of unencodable Go types, and `AddInfo` accepts
  any value, so both encode paths now recover that panic into a normal error
  (`FAILURE_PATTERNS.md` #16). This session added `internal/inventory/repository_conformance_test.go`,
  four scenarios run identically against `entRepository` (over in-memory SQLite, the same `ent.Client`
  code a real Postgres deployment runs) and `fileRepository`, all passing identically on both. Coverage
  79.9% package-wide.
- **`internal/ent/embedded.go`:** `OpenEmbedded(ctx, path)`, a hand-written (non-generated) file opening
  a real on-disk SQLite file with a DSN whose every parameter was verified against
  `mattn/go-sqlite3` v1.14.49's own source, migrating idempotently via ent's Atlas-backed
  `Schema.Create`. Tested for real durability across a close-and-reopen of the same file, the one thing
  the existing in-memory SQLite test pattern cannot prove.

**The Release Gate is honestly split, not uniformly closed, and this session recorded exactly why**
(`LESSONS_LEARNED.md` #21): substitutability (a conformance suite proving two adapters behave identically)
and composition-root selection (something in the real call graph actually choosing between them) are
different claims. `inventory.Repository` had a real production caller already:
`cmd/pleiades/load.go` was calling `StaticYAMLPlugin` directly, bypassing the port entirely. This session
rewired it to construct `inventory.NewFileRepository` and read through `GetGroup`, with zero changes to
`run.go` or `validate.go`, and reverified against the real built binary
(`cmd/pleiades/e2e_test.go`'s `TestCLI_EndToEnd`, `TestCLI_ValidateRejectsMissingCapability`,
`TestCLI_NoInfrastructure` all still pass). `lock.Manager` and `event.Bus` have no production caller
anywhere in this codebase yet, because nothing executes a runbook yet: their conformance suites prove real
substitutability against genuine containerized NATS, which is the strongest claim available today, but the
composition-root-selection half of their Release Gate stays open until Phase W5/W6 give them a first real
caller.

**Also fixed, found while closing this phase's own explicit checklist item:** `internal/lock/manager_test.go`'s
`mockManager` was missing `Close` (Phase W4's own listed item). Fixing it and re-running `go vet ./...`
surfaced two further, independent, pre-existing compile failures already sitting in `internal/runner`'s
test package (a duplicate `MockAdapter`, one of the two duplicates referencing `DispatchPayload` without
its package qualifier, and three `NewAgent` call sites still using an old two-argument signature after
`ExecutionAdapter` had already been added to the real constructor). All three were mechanical fixes using
an interface that already existed, so they were fixed rather than deferred a second time
(`FAILURE_PATTERNS.md` #14). `go vet ./...` is clean across the entire repository as of this session, with
no remaining exceptions anywhere.

**Verified, not assumed.** Every one of the four background agents' self-reports was independently
re-checked in this session, not taken on faith: `gofmt -l`, `go build ./...`, `go vet ./...`, and
`go test -race -cover` were re-run directly against each package after each agent finished, and the code
itself was read before being trusted. A full `go test ./... -race -cover` run across the entire repository,
including every real-Docker integration test, passed clean at the end of the session with zero `FAIL`
lines.

### The Forge of Hephaestus session (documentation only)

Named and specified the tooling a user touches before a runbook runs: authoring, playbook migration,
linting, the IDE plugin, the Collection scaffold, the device-type scaffold, and Galaxy collection
migration. Written up in `docs/hephaestus.md` and scheduled as `IMPLEMENTATION.md` Part VII, Phases 30
through 38.

Four decisions were made with the project owner and are load-bearing for anyone continuing this work:

1. **AWX server import is removed, not deferred.** Inventory belongs to a sync plugin pointed at the
   real upstream source (`PLAN.md` Section 6a), playbook and role source belongs to the Forge, and
   secrets are re-entered by a human. Nothing was left for an importer to do. Phase 29 was deleted and
   the Parity Conformance Suite was renumbered from Phase 30 to Phase 29.
2. **Native collection names are capability-oriented and three-part:** `<domain>[.<impl>].<method>`,
   as in `pkg.apt.install`, `svc.systemd.restart`, `net.ios.config`. The capability hierarchy is
   visible in the name, so `pkg.install` resolves down to `pkg.apt.install` at plan time. This renames
   every module relative to Ansible, which is acceptable only because the translator does the renaming
   mechanically and the IDE serves the names by autocomplete.
3. **The Forge is built before the catalog it generates.** The thirty six committed Ansible modules
   become roughly twenty seven collections *generated by* `forge new-collection`, not hand written.
4. **The catalog ships as manifests plus stubs first, implementations later.** A stub must return an
   explicit `not implemented` error, the manifest carries a `status` field, and a validation rule
   flags any runbook calling an unimplemented name.

**Not done, and deliberately so:** no Go code exists for any of Part VII yet. The Collection registry
(`pkg/collection`), the generic `pkg/registry` primitive, the ~23 new capability interfaces, both
scaffolds, the translator, and the language server are all unbuilt. Phase 30 is the entry point.

**The write path is now BUILT.** It was identified in `docs/desired_state_design.md` as the blocking
prerequisite for drift, journaling, rollback, baselines, and the CVE census, and it landed in this
session. What changed:

- **`internal/ent/schema/device.go`** gained a `version` column (the optimistic-concurrency token) and
  a `state` column (lifecycle, stored as its string form so a new state needs no migration).
- **`internal/ent/schema/revision.go`** is new: the persisted form of `inventory.Revision`, a table
  rather than a JSON column because the questions it answers are cross-device and cross-time and a
  blob cannot be indexed for those. Indexed on `(version, device)` and on `field_name`.
- **`pkg/inventory.ParseLifecycleState`** is new, the inverse of `String()`. It returns an error on an
  unrecognized value rather than defaulting, because `StateActive` is the only state permitting
  execution and silently promoting an unknown value into it would let a quarantined or archived device
  accept work.
- **`record.Record`** gained `Version` and `History`; **`NewBase`** now restores both, plus a new
  `BaseVersion()` reporting the version the item was hydrated at. Previously every hydrated item
  restarted at v0, which made the whole Section 1 versioning contract unenforceable.
- **`Repository`** gained `Save` and `GetByName`, plus an exported `ErrVersionConflict`.
  `internal/inventory/ent_save.go` implements Save as a real compare-and-swap in one transaction: the
  update matches on `id AND version`, so a racing writer matches zero rows and is rejected rather than
  silently winning. Only revisions above the loaded version are written, so nothing is duplicated.

**Verification, per RULE 0.** Tests run against a real in-memory SQLite database through `enttest`,
never a mock, because the entire claim is that a value survives a round trip through storage. Both
central assertions were **mutation tested**: reverting the version restore in `NewBase` fails the
round-trip test, and removing the `device.VersionEQ(baseVersion)` predicate fails the conflict test.
Coverage of the new code is 77 to 100 percent per function; `pkg/inventory` is now at 97.7 percent.
All touched packages pass `-race`. `go generate ./internal/ent` is idempotent.

**Watch out:** widening the `Repository` port broke `MockRepository` in `internal/api`'s tests, which
`go build ./...` does not catch because it does not build tests. `go vet ./...` does. Run vet, not just
build, after any port change.

That note also records two rejected designs, with reasons, so they are not re-derived: a declarative
resource model (reverses four separate prior decisions) and a general plan mode over runbooks (unknown
*actions* propagate through `when`, unlike Terraform's unknown *values*, and a hybrid plan cannot be
rendered honestly). What survives is a read-only baseline comparison, which `PLAN.md` had already
gestured at under the name "golden configs."

**The highest-value change available, and it is blocked by nothing: the DAG is a linked list.**
`synthesizeChain` (`internal/engine/tasktree.go`) chains each task's exit to the next task's entry in
list order, so every node has out-degree 1 and in-degree 1. Meanwhile the repo already contains a real
`Adjacency` graph map, Kahn's topological sort (`internal/engine/topology.go`), a DFS cycle detector
whose own comment concedes cycles are "structurally unreachable" in what it is given, and per-edge
compiled CEL conditions. `Task.Register` is parsed off the wire and has **zero readers outside tests**.
So the full cost of a dependency-graph engine has been paid and none of the benefit collected. Reading
`Register` and inferring an edge where a later task references an earlier task's result turns the
existing machinery on, and needs nothing from the state work above. Keep it additive: authored list
order stays the default.

**Two real defects were found while specifying the Forge, and both are scheduled rather than fixed.** They
are recorded as `FAILURE_PATTERNS.md` entries 10 and 11, and both are required items in Phase 35
ahead of the translator, because both would silently defeat its no-silent-drop guarantee:

1. **Unknown runbook keys are silently dropped.** Neither decode path sets `KnownFields(true)` or
   `DisallowUnknownFields`, so a runbook containing `become:`, `loop:`, `tags:`, or `notify:` builds
   and validates as though those keys were never written.
2. **A non-string `target` disables two validation rules.** `capability_rule.go` and
   `blast_radius.go` both read it through an unchecked `.(string)` assertion that yields `""` and then
   skips, so `target: [web1, web2]` validates clean and checks nothing.

**Known pre-existing issue found but not fixed (out of scope):** `.SPECIFICATION/PATTERNS.md` contains
many em-dashes, which `.AGENTS/AGENTS.md` forbids outright. It was left alone rather than bundled into
an unrelated change.

### Earlier sessions

Prior session's scope: the Phase 1 blocking prerequisite (`*ent.Device` removed from domain signatures)
plus Part 0 Walk phases W1 through W3. Phases W4 through W6 remain deliberately deferred to a follow-up
batch, per an explicit checkpoint agreed before that work.

This session's scope, on top of the above: (1) standardized terminology on "runbook" for the native
Pleiades workflow artifact, reserving "playbook" exclusively for real Ansible artifacts; (2) added a
`type` discriminator field to the native runbook format, plus a pre-parse sniff that rejects a real
Ansible-shaped file with an actionable error; (3) restructured the concrete device types out of a flat
`internal/inventory/devices.go` into a per-vendor package hierarchy. All three were done via four parallel
implementation agents against disjoint file sets, followed by an independent integration and verification
pass (this file's author did not just trust the agents' own reports).

### What works, verified for real (not by reading logs)

- **The blocking prerequisite is closed** (prior session). `InventoryItem` is the full `PLAN.md` Section 1
  contract; the capability vocabulary is consolidated in `pkg/capability`; `ItemFactory.Register`/`Build`
  take a storage-agnostic `Record`, never `*ent.Device`.
- **`cmd/pleiades` (`init`, `add-host`, `validate`, `run`) works against the real built binary with zero
  infrastructure** (prior session, re-verified this session after the rename and restructure landed).
- **Terminology is now consistent: "runbook" for native, "playbook" only for Ansible.** Verified by a final
  whole-repository grep: every remaining `.go` file match for "playbook" is genuinely about the real
  Ansible artifact or the real `ansible-playbook` binary (the type-kvp's own Ansible-detection code and
  tests, `internal/ansible/receptor.go`'s fixture event name, `cmd/pleiades/cli_bench_test.go`'s benchmark
  comparison). The scaffolded project directory is now `runbooks/`, not `playbooks/`.
  `internal/api/dispatcher.go`'s `DispatchPayload.RunbookID` and `internal/runner/agent.go`'s mirrored copy
  were kept in sync (same field name, same JSON tag). One inconsistency the rename agent missed was caught
  by this session's own independent audit and fixed by hand:
  `internal/auth/evaluator.go`/`jwt_test.go` still said `"playbook:execute"` after `dispatcher.go` had
  already moved to `"runbook:execute"` (see `FAILURE_PATTERNS.md` #7).
- **The native runbook format has a `type` field, verified against the real binary in both directions.**
  A runbook with no `type` (the default, `native`) builds normally; `type: native` explicit works
  identically; `type: ansible` is rejected with a specific, actionable error rather than a confusing
  parse failure; and a real Ansible-shaped file (a top-level YAML list) is independently detected and
  rejected with its own specific error, before the typed parse is even attempted. Both rejection paths
  were exercised by hand against the built binary, not just asserted in a unit test.
- **The device type catalog is now a per-vendor package hierarchy with no import cycle.** `Record` and the
  shared embeddable device state (`Base`, was the unexported `baseDevice`) live in the leaf package
  `internal/inventory/record`, which imports nothing back up the tree.
  `internal/inventory/devices/cisco` (type `Router`) and `internal/inventory/devices/linux` (type `Server`)
  each import that leaf plus `pkg/inventory`/`pkg/capability`, never `internal/inventory` itself.
  `internal/inventory/factory.go`'s `NewItemFactory()` kept its exact zero-argument signature; no external
  caller needed a single edit (confirmed by grep and by a full `go build ./...`). Verified end to end
  against the real binary: a `cisco_router` host correctly declares `CiscoIOSCapable` and a validation rule
  requiring it against a `linux_server` host correctly rejects it.
- All new and touched code passes `go build ./...`, `gofmt -l` (whole repo, zero output), `go vet` (only
  the pre-existing, out-of-scope `internal/lock`/`internal/runner` failures remain, confirmed unchanged),
  and `go test -race` for every touched package, re-run independently after the parallel agents' own runs
  (not just trusting their self-reported results). New fuzz targets ran with zero crashes.

### Resolved later in this session

- **The `type: native` versus `PLAN.md` Section 23's `type: auto-roboto` reconciliation is now closed.**
  The user confirmed `native` as the correct value (product-name-agnostic by design, so it survives a
  product rename), and separately confirmed the product itself is renamed: "Auto-Roboto" is retired, "The
  Pleiades" (module and binary name `pleiades`) is the name going forward. Section 23 now reads
  `type: native` too; both conventions agree.
- **Every `auto-roboto` occurrence across the repo was replaced with `pleiades`/`Pleiades`/`PLEIADES`
  (case-preserving)**: both spec documents, `AWX_PARITY.md`, `.AGENTS/AGENTS.md`'s title, `docs/*.md`'s
  illustrative binary/path names (`auto-roboto-controller`, `auto-roboto-config`,
  `/etc/auto-roboto/runner.yaml`, `auto-roboto/cisco-ios-utils`), and the web UI's header text
  (`web/src/components/Layout.tsx`, `web/src/views/JobDetails.tsx`). The Go module path
  (`github.com/SubjectVoidLLC/the-pleiades`) and the CLI binary name (`pleiades`) already used the new name
  before this rename; only prose and illustrative names lagged behind.

### Explicitly not resolved, by deliberate choice

- **Genuine out-of-module third-party device-type extensibility is still unsolved.** The per-vendor package
  restructure improves modularity and lets the codebase's own vendor catalog grow without one file becoming
  unreviewable, but `ItemFactory` still lives in `internal/inventory`, which Go's own visibility rules make
  unimportable from outside this module regardless of directory layout. `PLAN.md` Section 1's "anyone can
  add new concrete types" promise is not fully delivered by this restructure; it would need `ItemFactory`
  and `Record` to move to `pkg/`, a separate and larger decision this session did not make.
- **`PLAN.md` Section 23 retains some genuinely dual-purpose "playbook" prose** (a few sentences describing
  GitOps auto-discovery behavior that plausibly applies to both native and Ansible content, without saying
  which) that were deliberately left alone rather than force a reading either way.

### Current blocker / next step

No blocker. **Phase 3 (Distributed Mutual Exclusion / Locking) is now fully closed**, every checklist item
`[x]` including its Pattern Entry Gate, Fuzz/Stress Test, Adversarial Pattern Justification,
Schema/Injection Hardening, and Release Gate (see "The Phase 3 session" at the top of this document).

**A structural correction for whoever reads this next, found and fixed by this session, not by a prior
one:** this document's own "Current Status" used to describe Phase 1/2-era state while `git log` already
showed commits through "Phase 14 The Dispatcher," and neither matched the actual working tree, which holds
a much larger amount of real, uncommitted implementation than either source reflected (whole packages --
`internal/engine/executor.go`, `action_ssh.go`, `tasktree.go`, `workflow_context.go`, `internal/api`'s
dispatcher/HATEOAS/middleware work, and many more -- exist only as untracked or modified-but-uncommitted
files; `git status --short` shows the full extent). **The one reliable source of truth for "what is
actually done" is `.SPECIFICATION/IMPLEMENTATION.md`'s own checklist marks, read directly, not this
document's prose summary and not `git log`'s commit messages.** This document is still worth reading for
the *reasoning* behind what was built and the real defects each session found, but treat any phase-status
claim in it (including this one) as provisional until cross-checked against the checklist itself; do not
assume a phase is unstarted just because no session summary here describes it, and do not assume it is
complete just because a commit message names it.

Cross-checking `IMPLEMENTATION.md` directly (not assumed) as of this session's own close: **Phase 4 (Cron
Leader Election) is the next phase with real, unchecked work**, and it is partially built already, the
same shape Phase 3 was in at the start of this session -- worth reading its checklist closely before
assuming what "next" means. Checked already: a background scheduler lease loop exists and uses the
(now-completed) NATS Lock Manager to arbitrate it. Unchecked: extracting leader election into a reusable
`LeaderElector` taking a key (PLAN.md Section 25 names this exact primitive, "Build by Phase 4," needed
again by Phases 22/23/24); using `errors.Is` for the lock-held comparison instead of `==`
(`scheduler.go:64`'s `err != lock.ErrLockHeld` -- verified this session, after the `Manager.Acquire`
signature change, that `ErrLockHeld` is still always returned unwrapped in every real contention path in
both adapters, so the existing `==` check is not a live bug today, only a latent fragility the checklist's
own text already names); replacing an empty error-logging branch with real structured logging; releasing the lease
explicitly when renewal fails rather than dropping the reference and waiting out a TTL; the Fuzz/Stress
Test, Adversarial Pattern Justification, Schema/Injection Hardening, and Release Gate items, none audited
in depth by this or any prior session. This session's own `TestSchedulerLeaderElection` (real NATS
container, real graceful handover, now proven against `nats:2.11`) is the closest thing to a Release Gate
proof that currently exists for this phase, but the checklist's own literal three-controller-instance
scenario was not built or run.

Phase 39 (Schema & Injection Hardening) remains cross-cutting and explicitly meant to be re-run, not run
once: this session's own re-run of it, scoped to Phase 3's new boundaries (real per-key TTL, shared lock
mode, the raw NATS subject construction it needed), found and fixed one real finding
(`itemIDValid`/FAILURE_PATTERNS.md #37) before this checklist item closed. One real, deliberately deferred
gap from the original Phase 39 run is still open and unaffected by this session: `FAILURE_PATTERNS.md` #20,
JWT issuer/audience pinning, pending a token-issuing path that still does not exist anywhere in this
repository.

## Files changed in the Phase 0 CI Harness / Part 0 Readiness session

**New CI infrastructure:** `Makefile`; `.github/workflows/ci.yml`; `gosec-waivers.json`;
`coverage-floor.json`; `tools/gosec-check/main.go`; `tools/coverage-check/main.go`;
`internal/archtest/layering_test.go`.

**New:** `internal/inventory/yaml_merge.go` (the `add-host` content-preserving merge);
`internal/validate/lifecycle_rule.go` + `lifecycle_rule_test.go`; `internal/validate/validate_test.go`;
`internal/engine/action_capability.go` + `action_capability_test.go`;
`cmd/pleiades/addhost_preserves_content_test.go`.

**Rewritten:** `internal/inventory/yaml_plugin.go` (`EncodeHosts`/`WriteHosts` now merge into prior content
instead of replacing it); `internal/inventory/file_repository_save.go` (threads `original` bytes through
to the merge); `internal/inventory/ent_repository.go`/`ent_save.go` (real `type` column, plus
`Tags`/`Source`/`SourceSyncedAt` now written on save); `internal/inventory/file_repository.go`/
`file_repository_state.go` (sidecar now carries `Source`/`SourceSyncedAt`); `internal/engine/executor.go`
(runtime lifecycle guard in `runNode`, new `NodeResult.SkipReason`); `internal/validate/capability_rule.go`
(reads the shared `engine.ActionCapability` table instead of its own private copy);
`internal/validate/validate.go` (`WorldView` gained an unexported `resolveCache`, `Validate` initializes
it; fixes the `LifecycleRule`-caused benchmark regression, `FAILURE_PATTERNS.md` #28);
`cmd/pleiades/run.go` (builds `TransportBinding` from the shared table, calls the new consistency check);
`internal/ent/schema/device.go` (new immutable `type` column, regenerated, plus
`internal/ent/migrate/migrations/sqlite/0002_add_device_type.sql`); `internal/ent/embedded.go` (directory
permission `0o755` -> `0o750`, added the `mattn/go-sqlite3` blank import); `go.mod`/`go.sum`
(`golang.org/x/crypto` -> v0.52.0, `toolchain go1.26.5`).

**Eighteen test files updated for the new required `Device.type` column** (mechanical: moved a `"type"`
key out of `SetProperties` into `.SetType(...)`, or added `.SetType(...)` where none existed):
`internal/inventory/{iterator_test.go,ent_save_bench_test.go,iterator_fuzz_test.go,factory_test.go,
iterator_bench_test.go,ent_save_test.go,repository_conformance_test.go}`,
`internal/ent/{device_id_bench_test.go,client_fuzz_test.go,client_bench_test.go,client_test.go,
group_organization_test.go,embedded_bench_test.go,embedded_test.go}`,
`internal/ent/migrate/apply_test.go`, `internal/crypto/ent_hook_test.go`,
`internal/storage/ent_unitofwork_test.go`, `tests/e2e/integration_test.go`.

**Test fixtures updated for the new lifecycle rule/guard** (added explicit `StubState:
inventory.StateActive`, since `inventorytest.Stub`'s zero value is `StateDiscovered`, not `StateActive`;
see `LESSONS_LEARNED.md` #30): `internal/validate/{capability_rule_test.go,validate_bench_test.go}`,
`internal/engine/{executor_test.go,executor_secrets_test.go,executor_bench_test.go}`.

**Inline `#nosec` waivers added, each with its own reason** (see `gosec-waivers.json`'s header for the
policy): `internal/transport/ssh/known_hosts.go`, `internal/inventory/{yaml_plugin.go,
file_repository_state.go,file_repository_save.go,project.go}`, `internal/credential/{master_key.go,
file_store.go,file_store_save.go}`, `cmd/pleiades/load.go`, `pkg/retry/backoff.go`.

## Files changed in the Forge of Hephaestus / rename session (on top of the prior session's changes)

**New leaf package:** `internal/inventory/record` (`record.go`: `Record`, `Base`, `NewBase`, `Declares`).

**New vendor packages:** `internal/inventory/devices/cisco` (`router.go`: `Router`, `NewRouter`),
`internal/inventory/devices/linux` (`server.go`: `Server`, `NewServer`).

**Deleted (moved):** `internal/inventory/record.go`, `internal/inventory/devices.go`.

**Rewritten:** `internal/inventory/factory.go` (explicit vendor-package registration inside
`NewItemFactory()`, plus new `NewItemFactoryWithConstructors`).

**Renamed field/API surface (playbook to runbook):** `internal/api/dispatcher.go`
(`DispatchPayload.RunbookID`, `DispatchRunbook`, query param, NATS subject, auth scope string),
`internal/runner/agent.go` (mirrored `DispatchPayload`), `internal/adapters/native/adapter.go`,
`internal/inventory/project.go` (`runbooks/` directory), `cmd/pleiades/{run,validate,load,init}.go`,
`internal/auth/evaluator.go` (caught by this session's own audit, not the rename agent).

**New format feature:** `internal/engine/dag.go` (`WorkflowDef.Type`, validation in `buildFromDef`),
`internal/engine/yaml.go` (pre-parse Ansible-shape sniff in `BuildFromYAML`).

**Spec docs updated for terminology consistency:** `.SPECIFICATION/PLAN.md` (Sections 7, 9, 14, 20, 22.2,
23, 27), `.SPECIFICATION/IMPLEMENTATION.md` (Phase W1 gate text, Phase 14 items, blocking-prerequisite
note expanded with this session's follow-up work).

**Test files updated to match** across `internal/inventory`, `internal/api`, `internal/engine`,
`internal/adapters/native`, `internal/auth`, `internal/runner` (the last one's pre-existing,
out-of-scope test-compile failure is unchanged; only its JSON fixture string literals were updated for
consistency, since that costs nothing and removes a trap for whoever eventually fixes Phase 15).

Note: this session did not touch `internal/lock`, `web/`, `helm/`, the Dockerfiles, or `docker-compose.yml`.

## Files changed in the Phase W4 session

**New adapter files:** `internal/lock/inprocess.go`, `internal/event/inprocess.go`,
`internal/inventory/file_repository.go` + `file_repository_save.go` + `file_repository_state.go` +
`file_repository_iterator.go`, `internal/ent/embedded.go`.

**New test files (unit, fuzz, bench, per adapter):** `internal/lock/inprocess_test.go` +
`inprocess_fuzz_test.go` + `inprocess_bench_test.go`; `internal/event/inprocess_test.go` +
`inprocess_fuzz_test.go` + `inprocess_bench_test.go`; `internal/inventory/file_repository_test.go` +
`file_repository_concurrency_test.go` + `file_repository_errors_test.go` + `file_repository_fuzz_test.go`
+ `file_repository_bench_test.go`; `internal/ent/embedded_test.go` + `embedded_fuzz_test.go` +
`embedded_bench_test.go`.

**New cross-adapter conformance suites (this session's own integration work, not any single background
agent's slice):** `internal/lock/conformance_test.go` (`runManagerConformance`, run against the in-process
adapter and, via a new `TestNatsManagerConformance` in `nats_test.go`, a real NATS container);
`internal/event/conformance_test.go` (`runBusConformance`, run against the in-process adapter and, via a
new `TestNatsBusConformance` in `nats_test.go`, a real NATS container);
`internal/inventory/repository_conformance_test.go` (four scenarios run against both `entRepository` and
`fileRepository`).

**Modified:** `internal/lock/manager_test.go` (added `mockManager.Close`), `internal/lock/manager.go`
(whitespace only), `internal/lock/nats_test.go` (added `TestNatsManagerConformance`),
`internal/event/nats_test.go` (added `TestNatsBusConformance`), `internal/runner/agent_test.go` +
`agent_bench_test.go` + `agent_fuzz_test.go` (fixed the three-layer pre-existing compile failure,
`FAILURE_PATTERNS.md` #14), `cmd/pleiades/load.go` (composition-root rewiring: constructs
`inventory.NewFileRepository` and reads through `GetGroup` instead of calling `StaticYAMLPlugin` directly,
the real Release Gate closure for the `inventory.Repository` port).

**Living documents updated:** `FAILURE_PATTERNS.md` #14-16, `LESSONS_LEARNED.md` #21,
`.SPECIFICATION/IMPLEMENTATION.md` (Phase 0's compile-failure item, all of Phase W4).

Note: this session did not touch `web/`, `helm/`, the Dockerfiles, `docker-compose.yml`, or any
ent-generated file.

## Files changed in the Phase W5 session

**New files, `internal/engine`:** `level_iterator.go` (`LevelIterator`, `NewLevelIterator`) +
`level_iterator_test.go` + `level_iterator_fuzz_test.go` (`synthesizeAcyclicDAG`, `FuzzLevelIterator`);
`workflow_context.go` (`inProcessWorkflowContext`, `NewInProcessWorkflowContext`) +
`workflow_context_test.go` + `workflow_context_fuzz_test.go` + `workflow_context_bench_test.go`;
`action.go` (`TargetResolver`, `ActionResult`, `ActionExecutor`, `NewBuiltinActionExecutor`) +
`action_test.go`; `executor.go` (`Executor`, `NewExecutor`, `NodeResult`, `RunResult`, `nodeExecution`,
`nodeEvent`, `runConcurrently`) + `executor_test.go` + `executor_fuzz_test.go` (`FuzzExecutorRun`) +
`executor_bench_test.go` (`BenchmarkExecutorRun_FiveTaskChain`, `BenchmarkAnsiblePlaybookComparable`,
`BenchmarkExecutorRun_DeviceFanOut`).

**Modified:** `internal/engine/topology.go` (extracted `reachableWithInDegree`, shared by
`TopologicalOrder` and `LevelIterator`, zero behavior change); `cmd/pleiades/run.go` (composition-root
rewiring: constructs a real `engine.Executor` and executes after validation passes, instead of only
printing a plan); `cmd/pleiades/e2e_test.go` (new `TestCLI_RunExecutesConditionalBranch`, Phase W5's
Release Gate).

**Living documents updated:** `FAILURE_PATTERNS.md` #17, `LESSONS_LEARNED.md` #22-23,
`.SPECIFICATION/IMPLEMENTATION.md` (all of Phase W5; Phase W1, W2, and W4's Release Gate notes updated to
reflect what Phase W5 actually closed).

Note: this session did not touch `web/`, `helm/`, the Dockerfiles, `docker-compose.yml`, any ent-generated
file, or any package outside `internal/engine` and `cmd/pleiades`.

## Files changed in the Phase 39 session

**Modified, fixes:** `internal/engine/cel.go` (`defaultCELCostLimit`, wired into `Compile` via
`cel.CostLimit`); `internal/engine/dag.go` (`validRunbookID`, checked in `buildFromDef`).

**Modified, new regression tests:** `internal/engine/cel_test.go`
(`TestCELEngine_RejectsExpensiveComprehension`); `internal/engine/dag_test.go`
(`TestDAGBuilder_RejectsUnsafeRunbookID`, `TestDAGBuilder_AllowsSafeRunbookID`);
`internal/engine/yaml_test.go` (`TestBuildFromYAML_RejectsAliasBomb`); `internal/engine/yaml_fuzz_test.go`
(one curated alias-bomb seed added to `FuzzBuildFromYAML`).

**Not modified, by design:** no production source outside `internal/engine/cel.go` and
`internal/engine/dag.go`. `internal/auth`, `internal/ent`, `internal/api`, `internal/inventory`, and
`internal/adapters/native` were all audited (five parallel background agents, one per category) but
needed no code change; each agent's own throwaway verification test was deleted before this session
treated its report as evidence, confirmed via `git status` showing no residue.

**Living documents updated:** `FAILURE_PATTERNS.md` #18-20, `.SPECIFICATION/IMPLEMENTATION.md` (all of
Phase 39, every item `[x]` with the audit evidence inline).

## Files changed in the Phase W6 session

**New packages:** `internal/credential` (`credential.go`, `mask.go`, `master_key.go`, `file_store.go`,
`file_store_save.go` + `credential_test.go`, `mask_test.go` + `mask_fuzz_test.go`, `master_key_test.go`,
`file_store_test.go` + `file_store_fuzz_test.go`, `file_store_save_internal_test.go`); `pkg/retry`
(`backoff.go` + `backoff_test.go`); `internal/transport` (`transport.go`); `internal/transport/ssh`
(`ssh.go`, `auth.go`, `backoff.go`, `circuit_breaker.go`, `known_hosts.go` + `circuit_breaker_test.go`,
`known_hosts_test.go`, `ssh_test.go`, `ssh_realdial_test.go`, `ssh_fuzz_test.go`, `ssh_container_test.go`,
`ssh_bench_test.go`).

**New files, `internal/engine`:** `action_ssh.go` (`TransportBinding`, `NewTransportActionExecutor`,
`SSHTarget`) + `action_ssh_test.go`.

**New files, `cmd/pleiades`:** `addcredential.go` (`runAddCredential`, `promptSecret`) +
`lazy_credential_store.go` (`lazyCredentialStore`, `newLazyCredentialStore`) +
`lazy_credential_store_test.go`; `addhost_test.go` (`TestParsePropertyValue`);
`ssh_release_gate_test.go` (`TestCLI_RunExecutesSSHTransport`, Phase W6's own Release Gate).

**Modified:** `internal/runner/agent.go` (`calculateBackoff` now delegates to `pkg/retry.Backoff`, zero
behavior change); `cmd/pleiades/main.go` (registered `add-credential`); `cmd/pleiades/addhost.go`
(`parsePropertyValue`, fixing `FAILURE_PATTERNS.md` #21); `cmd/pleiades/run.go` (composition-root
rewiring: constructs the lazy credential store and a real `ssh.Transport`, wires both into
`engine.NewTransportActionExecutor` in place of the bare builtin executor); `cmd/pleiades/e2e_test.go`
(new `TestCLI_AddCredential`); `.gitignore` (`.pleiades/`); `internal/inventory/project.go`
(`starterReadme` mentions `add-credential`); `go.mod`/`go.sum` (`golang.org/x/crypto` and
`golang.org/x/term` promoted from indirect to direct; `go mod tidy` also reconciled unrelated pre-existing
drift already present in this session's starting uncommitted tree).

**Modified after the Schema/Injection Hardening audit (this session's own fixes, not the building
agents'):** `internal/credential/credential.go` (`MarshalJSON`, `LogValue`, fixing `FAILURE_PATTERNS.md`
#22) + `credential_test.go` (`TestCredential_MarshalJSONRedactsSecrets`,
`TestCredential_LogValueRedactsSecrets`); `internal/engine/action_ssh.go` (mask a transport-level error
too, not just a command's captured output, fixing `FAILURE_PATTERNS.md` #23) + `action_ssh_test.go`
(`TestTransportActionExecutor_MasksSecretsInTransportError`); `internal/credential/file_store_save.go` and
`master_key.go` (explicit `os.Chmod` after `MkdirAll`, fixing `FAILURE_PATTERNS.md` #24) +
`file_store_test.go`/`master_key_test.go` (`TestSaveFileStore_TightensPreexistingDirPermissions`,
`TestResolveMasterKey_TightensPreexistingDirPermissions`).

**Living documents updated:** `FAILURE_PATTERNS.md` #21-24; `LESSONS_LEARNED.md` #24-25;
`.SPECIFICATION/IMPLEMENTATION.md` (all of Phase W6, every item `[x]` with evidence inline; Phase W1's and
Phase W2's Release Gates flipped to `[x]` with a Phase W6 update note; Phase W4's Schema/Injection
Hardening checkbox corrected to match its own already-closed body text).

**Not modified, by design:** `web/`, `helm/`, the Dockerfiles, `docker-compose.yml`, any ent-generated
file, and every production file outside the packages named above. `pkg/capability` and
`internal/inventory/devices/{cisco,linux}` were read but not touched: `SSHTransportCapable`'s existing
`SSHHost()`/`SSHPort()` shape was already exactly what this phase needed.

Note: this session did not touch `web/`, `helm/`, the Dockerfiles, `docker-compose.yml`, any ent-generated
file, or any production file outside `internal/engine/cel.go` and `internal/engine/dag.go`.

## Files changed in the Phase 1 session

**New hand-written schema files, `internal/ent/schema`:** `timestamp_mixin.go` (`TimestampMixin`),
`group.go` (`Group`), `organization.go` (`Organization`).

**Modified schema files:** `device.go` (`device_id`, `source`, `source_synced_at`, `tags` fields;
`groups`/`organization` edges; `device_id` index; `TimestampMixin`), `fact.go` (`.Immutable()` on
`payload`/`hash`; `TimestampMixin`), `user.go` and `revision.go` (`TimestampMixin` only).

**Regenerated by `go generate ./internal/ent`** (not hand edited): `client.go`, `mutation.go`,
`runtime.go`, `ent.go`, `hook/hook.go`, `predicate/predicate.go`, `migrate/schema.go`, every
`device*.go`/`fact*.go`/`user*.go`/`revision*.go` top-level file, and the new `group*.go`/
`organization*.go` top-level files plus `internal/ent/{group,organization}/` subpackages.

**New hand-written files, `internal/ent/migrate`** (coexisting with generated `migrate.go`/`schema.go`):
`apply.go` (`Apply`, `checkGate`, the versioned-migration runtime) + `apply_test.go` +
`apply_internal_test.go` (`TestCheckGate`, `TestMigrationNames_SkipsSubdirectories`) +
`testdata/mixed/` (fixture for the latter); `migrations/sqlite/0001_initial.sql` (the committed initial
migration, captured via `Schema.WriteTo`, not hand written); `gen/main.go` (`//go:build ignore`, the
migration-file generator for future schema changes).

**Modified:** `internal/ent/embedded.go` (`OpenEmbedded` rewired to the raw-driver-then-migrate-then-wrap
sequence, replacing `client.Schema.Create`); `internal/crypto/ent_hook.go`
(`EnvelopeEncryptionHook`'s op mask narrowed to `ent.OpCreate`).

**New test files, `internal/ent`:** `group_organization_test.go`
(`TestGroupDeviceMembership`, `TestGroupNestingAllowsMultipleParents`,
`TestOrganizationDeviceEdgeIsOptional`) + `group_organization_fuzz_test.go` (`FuzzGroupCreation`,
`FuzzOrganizationCreation`); `device_id_fuzz_test.go` (`FuzzDeviceIDLookup`) +
`device_id_bench_test.go` (`BenchmarkDeviceIDLookup_Ent`, `BenchmarkDeviceIDLookup_RawSQL`).

**Modified test file:** `client_test.go` (`openMigratedTestClient` helper, new; `TestGraphTraversal`
repointed from `enttest.Open` to the real versioned-migration path, this phase's Release Gate).

**New package `internal/storage`:** `unitofwork.go` (`UnitOfWork` port) + `ent_unitofwork.go`
(`entUnitOfWork`, `NewEntUnitOfWork`) + `ent_unitofwork_test.go` (commit, rollback-on-error,
rollback-on-panic, transaction-scoped-client-isolation, and closed-client-start-failure, all against a
real database).

**Modified, `internal/inventory`:** `ent_repository.go` (`entClient(ctx)` helper; `toRecord` populates
real `Source`/`Tags`/`DeviceID`; `GetGroup`/`GetByName` route through `entClient`); `ent_save.go`
(`Save` rewritten around `storage.UnitOfWork.WithTx`; CAS predicate moved to `device.DeviceIDEQ`);
`repository_conformance_test.go` (new `TestRepositoryConformance_TagsAndSourceRoundTrip` scenario, plus
its own dedicated seed helpers); `ent_save_test.go` (new
`TestSave_RollsBackDeviceUpdateWhenALaterRevisionInsertFails`, the Adversarial Pattern Justification's
load-bearing Unit of Work proof); `iterator_test.go`/`iterator_bench_test.go`/`iterator_fuzz_test.go`
(repointed through the new shared `bulkCreateDevices` helper, fixing the `too many SQL variables`
regression `FAILURE_PATTERNS.md` #25 records); `file_repository_bench_test.go` (stale comment corrected
now that `BenchmarkEntRepositorySave` exists to compare against).

**New files, `internal/inventory`:** `ent_bulk_testutil_test.go` (`bulkCreateDevices`,
`sqliteBulkInsertBatch`); `ent_save_bench_test.go` (`BenchmarkEntRepositorySave`).

**Living documents updated:** `FAILURE_PATTERNS.md` #25; `LESSONS_LEARNED.md` #26-28;
`.SPECIFICATION/IMPLEMENTATION.md` (all of Phase 1, every item `[x]` with evidence inline).

**Not modified, by design:** `web/`, `helm/`, the Dockerfiles, `docker-compose.yml`, `cmd/pleiades` (the
Walk-tier CLI does not call `OpenEmbedded`/`NewEntRepository` in production yet, confirmed by grep; this
phase's changes are exercised by tests and by the real built binary's unaffected file-backed path, both
verified this session), and every production file outside the packages named above. `PLAN.md` and
`.AGENTS/AGENTS.md` were read but not edited.

Note: this session did not touch `internal/api`, `internal/auth`, `internal/engine`, `internal/event`,
`internal/lock`, `internal/runner`, `internal/adapters`, `internal/transport`, or `internal/validate`,
beyond reading call sites to confirm `DeviceID`'s value change needed no edits there.

## Files changed in the Secret-Marking and set_metadata session

**New files, `internal/engine`:** `secret_mask.go` (`SecretMaskSpec`); `executor_secrets.go`
(`stringSet`, `secretMaskValue`, `minMaskableSecretLength`, `(*run).markSecretFields`,
`(*run).applySecretMask`); `executor_secrets_test.go` (six tests: masked-in-later-event,
mask-across-devices, unknown-register-error, short/non-string-value rejection (table-driven),
concurrent-discovery-under-race, `set_metadata`-in-`RunResult.Metadata`); `secret_mask_test.go`
(YAML/JSON round-trip plus two build-time empty-`Register`/`Fields` rejection tests).

**Modified, `internal/engine`:** `dag.go` (`Task.SecretFields`, `Task.SecretMask`); `tasktree.go`
(`validateTask` rejects an empty `SecretMask.Register`/`Fields`); `action.go`
(`ActionResult.IsMetadata`; `builtinActionExecutor.Execute` is now a two-case `switch`, `"noop"`
unchanged plus `"set_metadata"`); `action_test.go` (three new `set_metadata` tests); `executor.go`
(`RunResult.Secrets`/`Metadata`; `Run` switched to a named return plus one `defer` so every exit
path populates them; `run` struct gains `secrets`/`metadataRegisters`; `runNode` gains the
`applySecretMask` call; `runOne` gains the `markSecretFields` call and the `IsMetadata` check
alongside the existing `Register`/`Merge` block; `publish` masks its `message` argument, best
effort, before publishing).

**New file, `internal/validate`:** `secret_mask_rule.go` (`SecretMaskRule`, existence-only
`secret_mask.register` reference check) + `secret_mask_rule_test.go`.

**Modified, `internal/validate`:** `capability_rule.go` (comment naming `set_metadata` alongside
`noop` as requiring no capability).

**Modified, `cmd/pleiades`:** `run.go` (mask `node.Err`'s text and the new `metadata:` report
through `result.Secrets`; new `printMetadata` helper, sorted by register/device/key); `e2e_test.go`
(`TestCLI_RunReportsSetMetadata`, `TestCLI_RunMasksSecretFields`, both real-binary-subprocess RULE
0 tests).

**Living documents updated:** `LESSONS_LEARNED.md` #29 (a value's secrecy is only known at
runtime; the string/minimum-length safety check has to live at the exact call site that adds a
value to the mask set, not deferred to print time). No `FAILURE_PATTERNS.md` entry: the one real
defect class this design was exposed to (blind stringify-and-mask corrupting unrelated output on a
short/common value) was caught and closed during planning, before it was ever committed as code,
so there is no bug to catalogue, only the rule in `LESSONS_LEARNED.md`. No
`.SPECIFICATION/IMPLEMENTATION.md` change: this work is a retrofit onto the already-closed Part 0
Phase W5 DAG executor, not a numbered phase.

**Not modified, by design:** `internal/ent` (no schema change; `ent.Fact` is deliberately untouched,
see "Current blocker / next step"), `internal/credential` (reused as-is via `Mask`; no change to
its own "known, pre-registered secret" scope), `internal/event` (the event envelope shape is
unchanged; only the `message` payload's *content* is masked before publishing), and every
production file outside the packages named above.
