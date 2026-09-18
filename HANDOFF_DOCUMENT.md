# Handoff Document

Rewrite the "Current Status" section when stopping mid-task or handing off, per `.AGENTS/AGENTS.md`.

## Current Status (this session)

**Branch `feature/Phase-83-Setup-Command`, off `origin/main` at `c762297` (PR #32). Phase 83, the
setup command, is built in full and NOTHING IS COMMITTED, by instruction: the work is in the tree,
and the commit messages below are what to commit it with, in order.** The branch has no upstream
on purpose (`git branch --unset-upstream`): it was created tracking `origin/main`, and a stray
`git push` would have targeted main.

### What it is

`controller setup` generates the master encryption key and the JWT secret through
`crypto.GenerateKey` (the resolver's own generator, exported), writes them where the deployment
reads them (`.env` for compose; a 0600 Secret plus a secret-free values file for Helm), and chains
`bootstrap-admin` in-process under the new key, which never touches an environment variable.
`docker-compose.yml` no longer carries a usable key, and `make up` runs setup the first time.

- **The guard scales with the field.** `internal/setup/class.go` classifies every field. The key is
  replaced only with `--destroy-existing-encryption-key`, plus a typed `destroy <fp>` at a
  terminal, and is refused outright while any stored row opens under a key the file holds.
- **It counts before it writes.** `crypto.TakeCensus` over `ent.OpenExisting` (raw SQL: no
  interceptor, no migration, creates nothing) decides which key holds each row from the key
  material, ignoring version tags, and a read error is a refusal, never zero. A first write over a
  surviving volume is refused and names both ways out, including rows under the key earlier
  compose files published.
- **Where it runs for compose.** A profile-gated `setup` service on the stack's own network, so it
  counts the database the controller opens, with `logging: driver: none` because at a terminal it
  shows the key.
- **The possession check is described as what it is:** an exact copy held a moment ago, nothing
  about where. Non-interactive runs skip it and say so.
- **The activity trail** records the key by fingerprint: `encryption_keys` registry (both
  dialects), written by setup as generated and by the controller at startup as first used.
- **Helm.** The chart composes `DB_DSN` from `$(POSTGRES_PASSWORD)` under `existingSecret`, and two
  new values carry the guards an operator-managed Secret used to switch off (FAILURE_PATTERNS #234).

### Found and fixed on the way (report these to the user as findings)

- **#233, data loss:** `ROTATE_ENCRYPTION_KEYS` rotated devices only; the guide's procedure lost
  every credential and saved survey answer. `crypto.RotateAll` covers every column.
- **#234:** the chart's recommended `existingSecret` path disabled its reinstall refusal and pod roll.
- **#235, security:** compose published PostgreSQL (password in the file) and an unauthenticated
  NATS on every interface. Both are loopback-only now.
- `JWT_SECRET` never signed browser sessions; docs/10, values.yaml and the chart's refusal said it
  did. Corrected everywhere it was said.

### Verified, and how

- Every touched package's tests with `-race`, including real SQLite and PostgreSQL, a Toxiproxy
  severance, real pty sessions against the built binary (interrupt restores echo; mutation-checked),
  and the archtest proof that setup cannot generate a key any other way (mutation-checked).
- `FuzzParseEnvFile`, 17.9M execs clean, plus a corpus through real `docker compose config`.
- Release gates against real infrastructure, all green: `tests/e2e/packaging_setup_test.go`
  (compose from nothing to a sign-in, the three refusals, the setup service on a real terminal with
  logging none), the Phase 20 compose gate (now given its secrets as process env), and the kind
  gate with a third release installed from setup's output (checksum roll, existingSecret reinstall
  refusal).
- `make gosec` (clean), `make govulncheck` (nothing reachable), `make helm-lint` (setup profile
  plus two mutation checks), `make docs-lint`, `make docs-gen-check` (unchanged, as designed),
  `tidy-check`, vet and build for Linux, macOS and Windows.
- The full gate: see the last section of this status.

### Known and deliberately not done

- **No startup refusal on a mismatched key.** A Helm reinstall with a new Secret onto a surviving
  volume is caught by the credential fingerprint only for the postgres password, not for the master
  key: the controller records the new key as first used and starts. Declined for this phase; it is
  the natural next step and the registry exists for it.
- The controller provisions its TLS certificate before its missing-key refusal. Harmless (a
  certificate regenerates freely) and noted because a test had to point it away from the tree.
- `make up` itself is not run by a test, because it writes `.env` into the checkout; the gate runs
  the exact service it runs, and `make -n up` was read by hand.
- The LESSONS index lists #190 and #191 twice with #192 between them (pre-existing); not
  renumbered, per the rule.

### Commit messages, in order (each must be staged by file, and by hunk where noted)

1. `fix(crypto): rotate every encrypted column, and say when the old key can go`
   internal/crypto/{rotate.go, rotate_all.go, columns.go, columns_internal_test.go,
   rotate_all_test.go, every_column_fixture_test.go, aad_migration_test.go, rotate_test.go,
   rotate_credential_test.go, rotate_launch_test.go}; cmd/controller/{rotation.go,
   rotation_test.go}; cmd/controller/main.go (the rotation goroutine and the stale unbound-form
   comment hunks); docs/10 ("Rotating the master key" hunk); changelog/rotation-every-pass.fixed.md
2. `refactor(crypto): export the key resolver's generator, decoder and fingerprint`
   internal/crypto/{key_resolve.go, key_export_test.go}; cmd/controller/main.go (decodeEnvelopeKey
   and the encoding/base64 import hunks)
3. `feat(ent): read an existing database without migrating or creating anything`
   internal/ent/{open_existing.go, open_existing_test.go, open_existing_chaos_test.go}
4. `feat(crypto): count what a master key protects, by key, from raw storage`
   internal/crypto/{census.go, census_test.go}
5. `feat(keyregistry): record which master keys a database has been told about`
   internal/ent/schema/encryption_key.go and the regenerated internal/ent files (client.go, ent.go,
   hook/hook.go, migrate/schema.go, mutation.go, predicate/predicate.go, runtime.go, tx.go,
   encryptionkey*.go, encryptionkey/); both 00xx_add_encryption_keys.sql migrations;
   internal/keyregistry/; internal/activity/activity.go
5b. `test(flakegate): fail at unit speed when a container package is missing from the Docker list`
   Makefile (the internal/keyregistry entry in DOCKER_DEPENDENT_PACKAGES, the count, and the comment
   hunks); tools/internal/flakegate/repeatgate_test.go (TestEveryContainerPackageIsListed)
6. `feat(prompt): read an echoed line and a hidden one from one terminal without losing input`
   internal/prompt/{terminal.go, terminal_test.go}; go.mod (creack/pty and golang.org/x/sys to
   direct)
7. `feat(setup): classify every field it touches, and read a compose env file back without guessing`
   internal/setup/{class.go, envfile.go, envfile_test.go, envfile_fuzz_test.go,
   envfile_compose_test.go, testdata/}
8. `feat(setup): write the key where the target reads it, and refuse to replace one that protects data`
   the rest of internal/setup/ except compose_file_test.go; internal/archtest/setup_test.go
9. `feat(controller): add the setup command, and record the key a controller first runs with`
   cmd/controller/{setup.go, setup_release_gate_test.go, keyrecord.go, keyrecord_test.go,
   admindeps.go, healthcheck.go, healthcheck_test.go, admin.go}; cmd/controller/main.go (the
   remaining hunks: key before database, errMissingMasterKey, recordKeyFirstUse, routeSetup)
10. `feat(helm): keep the chart's guards for a Secret it did not create`
    helm/the-pleiades/ (templates/_helpers.tpl, controller-deployment.yaml, _validations.tpl,
    values.yaml, values.schema.json); tools/helm-lint/{setupprofile.go, setupprofile_test.go,
    objects.go, main.go, profiles.go}; docs/10 (install intro, "Installing the chart", the
    reinstall paragraph and the air-gapped block); changelog/helm-existing-secret-guards.changed.md
11. `feat(compose): stop shipping a key, and bring the stack up with make up`
    docker-compose.yml (all but the two ports hunks); Makefile (up, setup, setup-env-check and the
    dev-cert comment hunks); .gitignore;
    internal/setup/compose_file_test.go (all but the loopback test); docs/02, docs/12;
    changelog/compose-no-published-key.security.md
12. `fix(compose): publish the database and the broker on loopback only`
    docker-compose.yml (the two ports hunks); internal/setup/compose_file_test.go
    (TestComposePublishesTheDatabaseAndBrokerOnLoopbackOnly); changelog/compose-loopback-ports.security.md
13. `test(e2e): gate setup from nothing to a sign-in, on compose and on Kubernetes`
    tests/e2e/{packaging_setup_test.go, packaging_kind_setup_test.go, packaging_kind_test.go,
    packaging_compose_test.go, packaging_support_test.go}
14. `docs: say what setup's key means later, and record what this phase found`
    docs/10 ("What setup tells you about later"); changelog/setup-command.added.md;
    FAILURE_PATTERNS{,_ARCHIVE}.md (#233 to #239); LESSONS_LEARNED{,_ARCHIVE}.md (#193 to #195);
    HANDOFF_DOCUMENT.md, HANDOFF_ARCHIVE.md

No message carries a model trailer. Push only after `make push-gate` writes its receipt on the
committed tree, and confirm the push with `git ls-remote`.

### After the gate: a Ctrl+C on the key screen

A real `make up` ended with "setup was interrupted, and wrote nothing" (exit 130, nothing written,
postgres left running). The key screen said "copy the key", and in most terminals Ctrl+C is stop,
not copy. The key screen now reads keys in raw mode (`prompt.Terminal.WaitForEnter`): the first
Ctrl+C explains and keeps waiting, a second one returns `setup.ErrInterrupted`, exit 130. Tested at
the primitive, the Screen and the built binary on real pseudo terminals. These hunks join commits
6 (internal/prompt), 8 (internal/setup interact.go, words.go and their tests) and 9
(cmd/controller/setup.go and its gate test), plus the docs/10 possession paragraph in 14. The
compose-on-a-terminal e2e gate has not been re-run since.

### Next step

Commit in the order above, run `make push-gate`, push the branch, and open the PR. Then the
startup refusal on a mismatched key, which the registry now makes a small change.

### The full gate on this tree

- **`make push-gate`: every check passed.** `testgate` tolerated four failures, all in packages
  listed in `flaky-packages.json`, all passing when re-run alone: `cmd/controller`'s leader
  election gate, `internal/event`, and two in `internal/runner`. Coverage: no package below its
  floor; the new `internal/keyregistry` is at 89.2% and `internal/setup` at 81.0% (no floors yet;
  setup's interactive paths are exercised through the built binary, which coverage does not count).
  Its last step, the receipt, refused because the tree is uncommitted, which is the expected
  result under the no-commit instruction: MAKE_EXIT 2 at that step and nowhere else.
- **Strict `make ci` did NOT go green in a single run.** Three attempts failed, each only in
  `test-integration` or `coverage` and each on container provisioning in a flaky-listed package: an
  sshd readiness timeout (`cmd/pleiades`), NATS containers refusing connections (`cmd/runner`,
  `internal/event`, `internal/runner`, `internal/topology`), a LocalStack readiness timeout
  (`internal/inventory/plugins`), and a certificate the JWKS gate waited 17 seconds for under load
  (`cmd/controller`). Every one passed when run alone; the JWKS gate three times in a row, in about
  a second each. Every other target passed: build, devtools, vet, fmt, tidy, test-race,
  test-repeat, gosec, govulncheck, docs-lint, docs-gen-check, helm-lint, templ-gen-check.
- **The push gate found one real integration gap, now fixed:** `internal/keyregistry` starts a
  PostgreSQL container and was not in `DOCKER_DEPENDENT_PACKAGES`, so `test-repeat` ran it three
  times over under full load (FAILURE_PATTERNS #239). It is listed, and a unit test now catches the
  omission.
- After the push gate: a docstring was added above the package clause of 39 new files, which the
  commit gate requires. `go run ./tools/commitgate` on the whole staged tree then passed (the index
  was reset afterwards), and build, vet under both tag sets and every touched package's tests were
  re-run clean.
