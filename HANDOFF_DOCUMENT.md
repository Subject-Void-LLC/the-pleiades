# Handoff Document

Rewrite the "Current Status" section when stopping mid-task or handing off, per `.AGENTS/AGENTS.md`.

## Current Status (this session)

**Branch `feature/Phase-46-Simulation-Modes`, on top of the 19 committed check-mode and external
Collection commits (whose status is now the top entry of `HANDOFF_ARCHIVE.md`). This session's work is
UNCOMMITTED in the working tree, and `make ci` has not run on it.** The user asked for Phase 21's last
open item, "rebind schedules, workflow nodes and notification policies to `Launchable`", and chose to
build the seam rather than close the item on paper.

### What it turned out to be

The item's two earlier notes were both wrong, and correcting them is the work. The second note (made
earlier the same day) had called schedules done because a schedule attaches to a `Template` and "a
Template carries its kind, so one mechanism covers every Launchable kind". That conflated the two axes
`.SPECIFICATION/AWX_PARITY_ROADMAP.md` section 1.1 had separated a year earlier: a `launch.Kind` says
which ENGINE runs a definition, while a schedule has to name which OBJECT to run, and a project sync is
not a `Template` of any kind. So a project could not be scheduled at all, which is exactly the
roadmap's C1 gate. LESSONS 208.

### What was built

- **`internal/launchable`**, the axis-A abstraction (AWX's UnifiedJobTemplate): an open registry of
  launchable TYPES keyed by AWX's own names (`job_template`, `project`), each declaring a label, what
  one of its runs is called, the scope needed to launch it and whether it takes a saved configuration.
  `Reach.Admits` is the one predicate the UI picker filters with and the store checks on submit.
  `Router` launches by map lookup, never a type switch, and refuses to be built with a gap in either
  direction (a registered type with no launcher, or a launcher for an unregistered type).
- **A `Launchable` ent entity** as the stable reference: one row per launchable thing, a nullable
  unique pointer per target type, a CHECK requiring exactly one, cascading from `Template` and
  `Project`. A schedule's key into it is uncascaded, so deleting a scheduled template or project is
  refused as one statement (409). Migrations `sqlite/0032` and `postgres/0029`, both hand-backfilled.
- **Schedules rebound**: `Schedule.LaunchableID` replaces `TemplateID`, `Dispatcher.LaunchScheduled` is
  gone (the Dispatcher is now a `launchable.Launcher` with a `Preflight`), `internal/project`'s Runner
  is the other launcher, and `Scanner.fire` makes one `Launch` call. A target already running is a skip
  carrying `already_running` rather than a failure that would be retried for as long as the run lasts.
- **A sync run now exists from the moment it starts and records who asked** (status `running`, nullable
  finish, `actor`), which is what lets a fired occurrence name the attempt it started. The Sync history
  tab gained a STARTED BY column.
- **API**: AWX's `unified_job_template`, with `template` kept as a deprecated write alias (both
  disagreeing is a 400). Responses carry `unified_job_template{,_name,_type}`; an occurrence carries
  `unified_job_type`. Reference regenerated.
- **UI**: the RUNS picker offers both sorts, grouped in native `<optgroup>`s, filtered by what the
  viewer may launch.
- **Spec**: Phase 21's item ticked with the correction; workflow nodes re-homed to Phase 27 (which had
  been assuming a workflow graph existed, and which nothing owned building) and notification policies to
  Phase 28, each with the reference's rules written out; Phase 23's and Phase 24's notes corrected;
  roadmap C1 marked done; parity's `project_updates` moved from 0/9 to 4/9.

### Findings: report each to the user as its own item

1. **Data loss and a failed upgrade, MEASURED, fixed (FAILURE_PATTERNS 266, LESSONS 207).** SQLite
   migrations never really turned foreign keys off: `applyOne` ran each script inside a transaction,
   where that pragma is a no-op, and SQLite declines it silently. Migration 0022 therefore deleted every
   authored survey question and saved launch configuration on a populated database, and 0030 could not
   be applied at all to one holding a job task, so the Controller would not start after upgrading. No
   test had ever migrated a database with rows in it. Fixed by pinning a connection, setting the pragma
   before the transaction, reading it back, and running `PRAGMA foreign_key_check` before commit.
2. **Security, fixed (FAILURE_PATTERNS 268).** `schedule:write` alone could arrange for any template in
   any organization to run for real, repeatedly, unattended. Now the write path requires the scope the
   target's own type declares; proven over real HTTP, and the proof fails with the check removed.
3. **Correctness, fixed (FAILURE_PATTERNS 267).** A project that had ever synced could not be deleted:
   the history's key was uncascaded and the delete answered 500, while the schema's own comment said the
   history goes with the project.
4. **Correctness, fixed (FAILURE_PATTERNS 269).** Editing a schedule in the UI silently dropped the
   saved configuration its runs used, because the form renders no control for it and the binder wrote
   the zero value.
5. **Minor, fixed (FAILURE_PATTERNS 270).** The template-delete 409 advised disabling the schedule,
   which does not release the reference. The same advice was in docs/09.
6. **Security, FIXED the next day (FAILURE_PATTERNS 272), and reported to the user first.** `GitSyncer`
   had no URL-scheme allowlist, so a `project:write` holder could aim a sync at any repository the
   Controller could reach. Two things I said when reporting it were wrong and are corrected in the
   allowlist section below: the local-path half is not exploitable in the shipped image (go-git's file
   transport needs a git binary the image does not carry), and an allowlist does not stop internal reach
   (anything shaped like `host:path` is a valid ssh address). The live half was the network transports
   with no integrity, since a clone's content is code that runs on managed devices.
7. **Stated, pre-existing.** No request carries a tenant, so the tenancy half of the launch check can
   only compare a target's organization to the schedule's, not to the caller's. Enforced at the store for
   a narrowed caller; unenforceable at the API until requests carry a tenant.

### Verified, and how

- **The C1 gate** (`internal/schedule/launchable_gate_test.go`): a real file database through the
  production opener and the real migrations, the real template and project stores, a real git repository
  and the real `GitSyncer`, the real Dispatcher over a real in-process bus, the real router and Scanner.
  Two due schedules, ONE `Sweep`: a job with the schedule's actor, and a sync attempt whose id the
  occurrence records, whose actor is `scheduler:<id>` and whose revision is the repository's own HEAD.
  Plus the busy-target skip (and that a second sweep adds no row), and a launchable type this build has
  never heard of firing through the same store and scanner with no code change.
- **The migration backfill** on a populated SQLite database: every template and project gains its row,
  every schedule is repointed with its saved configuration kept, the old column is gone, occurrences are
  typed, the scheduled-template delete is refused and an unscheduled one cascades. Removing the project
  backfill line fails it. The upgrade tests for 0022 and 0030 fail with finding 1's fix reverted.
- **Mutations checked** (each turned a named test red): the launch-scope check removed; every type
  routed to the template launcher; `ErrBusy` treated as a launch failure; the project backfill dropped;
  finding 1's fix reverted; a branch on launchable type planted in the scanner (caught by the archtest);
  the actor cell blanked in the UI history.
- Full suites of `./internal/...` green, including `internal/backup` (see below), `tests/parity`,
  `internal/archtest`. `-race` on launchable, schedule, api, project, ent/migrate. The three-replica
  `TestControllerScheduler_FiresExactlyOnce_ReleaseGate` passes against real NATS on the new schema
  (69s). `make vet` (both tag sets), `make fmt`, `make gosec` (20 findings, all waived; one waiver
  renumbered with a written re-review), `make docs-lint`, migration parity, generated reference
  idempotent, no em dash in any added line.
- **A fixture regenerated rather than edited.** Adding a table broke
  `internal/backup`'s `TestParseTOC_ReadsARealBackupOfThisSchema`, which counts the tables a real
  `pg_restore --list` holds. Both listings were recaptured the way their provenance describes: postgres
  at the pinned 15.19 image, the real migrations, the real `controller bootstrap-admin`, then `pg_dump`
  and `pg_restore --list` from inside the container so the server and the tool both read 15.19. The
  procedure is now written down in that test.

### Known and not done

- **`make ci` and `make push-gate` have not run** on this work (they saturate this machine for about
  twenty minutes; ask first). Nothing is committed yet either.
- **The release gate over three real controller processes covers a job template only.** Extending it
  with a project-sync schedule is the one item from this session's plan left undone; the seam itself is
  proven in-process by the C1 gate above.
- **An inventory source is not a launchable type**, because there is no Controller-side entity for one:
  inventory sync runs from the `pleiades` CLI. A workflow is not one either, and Phase 27 now owns
  building it.
- **A project sync is still not a row in the Jobs list.** Its record is its own `SyncRun`. Unifying the
  two lists (AWX's UnifiedJob) is a separate decision nobody has taken, and `tests/parity` says so.
- **`GET /unified_job_templates`** as a listing route is deferred to the roadmap's D3, which needs
  per-type read scopes. Discovery today is the `unified_job_template` field on a template or a project.

### Then the project source allowlist (2026-09-20, also uncommitted)

You asked for the allowlist half of finding 6 above, as a plan; it was approved and built.
`internal/project/source.go` is the predicate: the protocols go-git will dial, allowlisted, defaulting
to https and ssh, with `PLEIADES_PROJECT_ALLOW_INSECURE_SOURCE` (http, the git daemon) and
`PLEIADES_PROJECT_ALLOW_LOCAL_SOURCE` (file and bare paths) as separate opt-ins because they are
separate threats. Enforced at `project.entStore` Create and Update, which is the only writer of the
column, and again in `GitSyncer.Sync` before anything touches disk. A password in the URL is refused at
the write only, since refusing it at the sync would make an existing row permanently unsyncable with no
migration. The protocol is decided by `transport.NewEndpoint`, the same call the transport layer makes,
rather than by a second parser (LESSONS 209).

**Two corrections to what I told you when I reported the finding, both from review:**

1. The local-path half is not exploitable in the shipped image. go-git's file transport shells out to
   `git-upload-pack` and the image carries no git binary, so a local source fails there anyway. It
   works on a developer's machine, which is why the tests that use one now say so. The package comment
   claiming go-git needs no git binary was wrong and is corrected.
2. An allowlist does not stop internal reach: `srv:secrets-repo` is a valid ssh address, so an allowed
   protocol still reaches any resolvable host. Stated in the code, in docs/10 and in the roadmap rather
   than implied.

**A second defect, fixed here (FAILURE_PATTERNS 271).** `fetch` passed go-git no remote, so it used the
URL the FIRST clone configured: editing a project's address changed nothing about what was fetched,
forever, while the sync reported success. That also made any check on the column bypassable by a
checkout that already existed. Fixed by passing `RemoteURL` AND by replacing a checkout whose remote
differs, because an unrelated history cannot be fast-forwarded into and the project would otherwise
fail with "non-fast-forward update" with nothing an operator could clear. The replacement clones into a
sibling and swaps on success, so a wrong new address leaves the last good checkout serving.

**What the tests found that the plan had wrong.** The plan called `RemoteURL` a two-line fix and
deferred the re-clone. The repoint test showed that leaves a repointed project permanently unsyncable,
so the sibling-swap went in. The plan's own mutation list is therefore out of date in one entry:
dropping `RemoteURL` no longer fails the repoint test, because the mismatch check subsumes it. The
comment there says so rather than claiming a guarantee the test does not check.

**Verified:** `internal/project` green including four new tests (the classification table, the
representative refusal asserting the checkout directory was never created, the store-then-sync control
that proves the two checks are not redundant, and the failed-repoint test that proves the old tree
survives); every mutation caught (admit everything at sync, allow file by default, key the secret check
on the user instead of the password, and the two above); `-race` on project; full suites of api,
ui/..., schedule/..., internal/... and tests/parity; vet both tag sets, fmt, gosec (20, all waived),
docs-lint, reference regenerated and idempotent. One infrastructure failure in `internal/ent`
(a Postgres container not ready within 60s under load) which passes alone.

**Docs and records:** docs/10 gains "Where a project's source may come from" with the two toggles and
the three limits (no host allowlist, redirects followed, and git-over-ssh not using this platform's
known_hosts, which makes https the only source that works in the image as shipped); the apispec
description and the 400; FAILURE_PATTERNS 271 and 272; LESSONS 209; two changelog fragments; and an A1b
addendum in `.SPECIFICATION/AWX_PARITY_ROADMAP.md` recording both open items.

### Next step

Run `make ci` (or `make push-gate`) with nothing else running, then commit. A commit series is not yet
drafted; the work splits along the same lines the plans' steps did (the migration-runner fix, the
interface retirement, the sync-run change, the launchable package, the schedule rebind, the UI, the
docs, and then the source allowlist with its fetch fix), and each step's own tests pass on their own.

Two items this work names and does not do, both in `internal/project`: known hosts for git over ssh
(which is what stops an ssh project working in the shipped image at all), and a host allowlist, which
belongs with whatever settings mechanism lands first.
