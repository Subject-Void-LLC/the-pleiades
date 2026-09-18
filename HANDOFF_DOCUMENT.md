# Handoff Document

Rewrite the "Current Status" section when stopping mid-task or handing off, per `.AGENTS/AGENTS.md`.

## Current Status (this session)

**Branch `feature/Phase-83-Setup-Command`, off `origin/main` at `c762297`. Two bodies of work sit in
the tree, and NOTHING IS COMMITTED, by instruction:** Phase 83 (the setup command, whose full status
is now the top entry of `HANDOFF_ARCHIVE.md`) and, on top of it, the backup half of Phase 84:
`make backup`, `make restore`, `make down` and `make decom`. The branch has no upstream on purpose.
The commit messages below cover both, in order.

### What this session added

- **`make backup`** runs `controller backup` in a new profile-gated `backup` compose service, whose
  image is the `backup` stage of `Dockerfile.controller`: the same controller binary on
  `postgres:15.19-bookworm`, so `pg_dump`/`pg_restore` match the server (the binary links glibc, so
  not alpine). The file is written 0600 under a temporary name, read back through `pg_restore
  --list` and the restore's own table-of-contents check, then linked to
  `backups/pleiades-<UTC>-<fp8>.dump`. **The key is never in the backup** (decided with the user);
  the name carries its fingerprint, and the output says the file and the key must be kept apart.
- **`make restore BACKUP=<file>`** stops the controller and runner, then `controller restore`:
  listing allowlist (seven entry kinds, all measured), load into `pleiades_restore` as a role that
  owns only that database, clear every setting the file could leave (and count what is left),
  census under `.env`'s keys requiring the key AND its version tag, migrate as that role, compare
  the whole schema with a fresh reference database, fail jobs the backup caught running, end every
  session, record `controller-restore restored database pleiades from <file>`, back the live
  database up as `...-before-restore.dump`, and swap names in one transaction. Then make resets
  the broker volume (`docker compose down --volumes nats`) and runs `make up`. On a machine with no
  `.env` it asks for the key (echo off, checked against the name's fingerprint) or reads
  `--key-stdin`, and writes it with the rows' tag via `setup.ImportKey`.
- **`make down`** keeps everything. **`make decom`** shows what goes and stays (the newest backup
  and which key it needs), takes the typed phrase `decommission <fp>` or `DECOM_FLAGS=--destroy-deployment`,
  removes the volumes, and only then deletes `.env` (decided with the user). Backups are kept.

### Findings: report each to the user as its own item

1. **Data loss, fixed:** every compose release gate ran as the checkout's own project `pleiades`
   and began with `docker compose down -v`, so `make ci`/`make push-gate` deleted the database of a
   stack started with `make up` from the same checkout (FAILURE_PATTERNS 240). Gates now run as
   `pleiades-release-gate` and refuse while the ports are held. **Consequence: run `make down`
   before `make ci` if a stack is up**, or the compose gates fail on the port check.
2. **Security, fixed for compose:** compose pulls an `image:` name before building it, even with
   `build:` (measured on v5.5.1), and the Docker Hub namespace `pleiades` belongs to a third party
   (measured; no `controller`/`runner`/`backup` image there today). `pull_policy: never` on every
   built service, with a test. **Not fixed: the Helm chart's default image repository points at the
   same namespace**; which registry it should name is the user's decision (FAILURE_PATTERNS 241).
3. **Security, designed against:** `pg_restore` runs whatever SQL an archive holds, and the compose
   app login is a superuser; objects a file leaves (a `pg_read_file` default, a trigger) run later
   as that superuser. Contained by the scratch role, the settings reset and the positive schema
   comparison (LESSONS 196, FAILURE_PATTERNS 242 to 244), each proven by an adversarial test.
4. **Security, not fixed (pre-existing):** the compose stack's database login is the PostgreSQL
   superuser (`POSTGRES_USER`), which widens anything that reaches SQL. Reasoned.
5. **Low:** the scratch role's random password appears in a `CREATE ROLE` statement, which a server
   logging DDL would record; the role exists only during the restore. Reasoned.
6. **Correctness, fixed:** the documented `echo "$PASSWORD" | make up SETUP_FLAGS=...--password-stdin`
   never worked: `make up`'s `--check` run drained stdin (FAILURE_PATTERNS 245). Found by the new
   gate, the first test to run `make up`.

### Verified, and how

- `internal/backup` against real PostgreSQL 15.19 with real `pg_dump`/`pg_restore`: the round trip
  read back through the controller's hooks, every refusal leaving the live database byte-identical
  (by oid and row counts) with nothing left behind, a clean-machine import with a non-default tag,
  a newer-schema refusal, the adversarial tests, two Toxiproxy cuts (mid `pg_dump`, mid
  `pg_restore`), goleak, `-race`. Three mutation checks (comparison off, tags ignored, running jobs
  left) each fail their test. Coverage 88.4% (no floor yet). `BenchmarkTake`: 123 ms against 90 ms
  for `pg_dump` alone (1.37x). `FuzzParseTOC` about 13M executions, clean.
- The built binary on real pseudo terminals: restore with no `.env` (prompt names the fingerprint,
  echo off, key never shown), `--key-stdin`, and decommission's exact phrase.
- **Compose gates, all four green together:** the new
  `TestBackupReleaseGate_ComposeFromBackupToACleanMachine` (make up from nothing, backup, restore,
  down, decom refused then done, a fresh install refusing the old backup, a clean-machine restore
  with the key on stdin ending in a sign-in with the original password), Phase 20's compose gate
  (warm 2.15 s), and both Phase 83 setup gates, including the terminal one not re-run since the
  Ctrl+C change. The user's own stack was stopped with `make down` for the run and brought back
  with `make up`, data intact.
- gosec, govulncheck, docs-lint, docs-gen-check, helm-lint, templ-gen-check, tidy-check, arch, vet
  under both tag sets, the commit gate on the whole staged tree (then unstaged).
- **Not run this session:** the full `make ci`/`make push-gate` (the user's stack holds the ports),
  and the kind gate (nothing it covers changed).

### Known and not done

- Helm and external databases have no backup or restore wiring; docs/10 says so.
- The rest of Phase 84: concurrent migration, the compatibility policy, upgrade and rollback.
- Data retention and purge are not built; docs/10 now says "not built yet".
- Keeping a deployment's unsealed data after its key is lost has no command.
- Phase 83's startup refusal on a mismatched key is still open.

### Commit messages, in order

Phase 83's fourteen (1 to 14, and 5b) are in the archive entry below, unchanged, with two
amendments: commit 11 also takes the `</dev/null` hunks of the Makefile's `up` target (its fix
belongs with the target it fixes), and the files listed there now also hold this session's hunks,
which belong to the commits below and have to be left out of Phase 83's with `git add -p`.

15. `fix(e2e): run the compose gates as their own project, so make ci cannot delete a make up stack`
    tests/e2e/{packaging_support_test.go (gateComposeProject, freshComposeStack,
    requireComposePortsFree, the packagingCommand env hunk), packaging_compose_test.go (header
    paragraph, freshComposeStack, composeDown profiles), packaging_setup_test.go (freshComposeStack,
    the project filter)}; changelog/release-gates-own-project.fixed.md
16. `fix(compose): never pull an image the stack builds`
    docker-compose.yml (pull_policy on controller, runner and setup, and the controller comment);
    internal/testsupport/images_test.go (PullPolicy, the pinnedImages comment, the header bullet,
    TestComposeNeverPullsWhatItBuilds); changelog/compose-never-pulls-built-images.security.md
17. `refactor: name the default key version, and export setup's count phrasing`
    internal/crypto/envelope.go; cmd/controller/{keyrecord.go, main.go (the DefaultKeyVersion hunk)};
    internal/setup/{run.go, run_helm.go, keyrules.go}
18. `feat(crypto): report the version tag of every value the census counts`
    internal/crypto/{census.go, census_test.go (the TagsUnder hunk)}
19. `feat(ent): read an existing database's migration history`
    internal/ent/{open_existing.go, open_existing_test.go} (the MigrationHistory hunks)
20. `feat(setup): write a restored key into an env file that holds none`
    internal/setup/{import.go, import_test.go}
21. `feat(activity): record a database restored from a backup`
    internal/activity/activity.go (ActionRestored, KindDatabase); internal/ui/resources/activity/activity.go
22. `feat(backup): back up and restore the compose stack's database, never with the key in the file`
    internal/backup/ (all); Makefile (the internal/backup entry in DOCKER_DEPENDENT_PACKAGES)
23. `feat(controller): add backup, restore and decommission, and the image they run in`
    cmd/controller/{backup.go, backup_release_gate_test.go, healthcheck.go (routeBackup),
    healthcheck_test.go (three cases), main.go (the routeBackup case), admin.go (usage),
    setup_release_gate_test.go (startPTYWith)}; Dockerfile.controller;
    internal/testsupport/images_test.go (TestBackupImageMatchesTheServer)
24. `feat(compose): add make backup, restore, down and decom`
    docker-compose.yml (the backup service and the header paragraph); Makefile (the four targets,
    .PHONY, BACKUP_DIR, BACKUP, RESTORE_FLAGS, DECOM_FLAGS); .gitignore (/backups/);
    internal/setup/compose_file_test.go (Volumes, TestComposeBackupServiceIsConfinedToWhatItNeeds);
    tests/e2e/packaging_backup_test.go; changelog/backup-restore-commands.added.md
25. `docs: document backup and restore, and record what this work found`
    docs/10 (the install paragraph, "The database" paragraph, the backup section);
    FAILURE_PATTERNS{,_ARCHIVE}.md (240 to 246); LESSONS_LEARNED{,_ARCHIVE}.md (196 to 198);
    HANDOFF_DOCUMENT.md, HANDOFF_ARCHIVE.md

No message carries a model trailer. Push only after `make push-gate` writes its receipt on the
committed tree (with any `make up` stack stopped first), and confirm with `git ls-remote`.

### Next step

Commit in the order above, stop any local stack with `make down`, run `make push-gate`, push, and
open the PR. Then decide the Helm chart's default image repository (finding 2), and the rest of
Phase 84.
