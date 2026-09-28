# Changelog

All notable user-facing changes to The Pleiades are recorded here. Format loosely follows
[Keep a Changelog](https://keepachangelog.com/); entries are assembled from
`changelog/*.md` fragments (see `changelog/README.md`).

This project does not yet use semantic version tags; entries below are grouped by
date until a first tagged release. The numbering and compatibility rules those tags
will follow are in
[Releases and stability](docs/13-releases-and-stability.md#versioning-and-deprecation-policy).

## Unreleased

### Added

- Root `README.md`, `CONTRIBUTING.md`, `SECURITY.md`, `CODE_OF_CONDUCT.md`, and this
  file. The repository previously had none of them.
- `tools/docs-lint`, wired into `make ci`: fails the build if a citation into a
  gitignored internal specification document appears anywhere a real user could see
  it.
- The `changelog/` fragment convention this file is assembled from.
- `examples/webserver_lab/`: three real, SSH-reachable Ubuntu targets (a baseline
  server, an nginx bootstrap pattern, and a WordPress LAMP stack) used to capture
  genuine `pleiades` output for documentation.
- A `design/` directory holding internal design notes and aspirational scenario
  narratives previously mixed into `docs/`, so `docs/` now contains only real user
  documentation.
- `tools/gendocs`, which generates `docs/reference/` from the live `pkg/collection`,
  `pkg/capability`, and `internal/forge/catalogdata` registries: one page per Collection
  method (75 total), namespace/capability/device/plugin indexes, the task-key
  reference, the implementation-status matrix, and the CLI reference. Proven
  byte-for-byte idempotent by a test that runs it twice and diffs the output tree.
- `runbook.schema.json`, `inventory.schema.json`, and `module-catalog.json`, generated,
  committed under `docs/reference/schemas/`, and served from the running binary at
  `/.well-known/pleiades/*.json`.
- `pleiades doc`: `--list [namespace]`, `<fqcn>`, `--snippet <fqcn>`, and `--json
  [fqcn]`, reading the same `pkg/collection` registry `tools/gendocs` and the real
  dispatcher both read. Works offline, with no virtualenv or collection install.
- `pleiades version` and a declarative CLI command tree (`internal/clispec`), shared by
  `cmd/pleiades`'s own `--help` text at every level and `tools/gendocs`'s generated
  `docs/reference/cli.md`, replacing the hand-maintained (and stale)
  `docs/cli_reference.md`.
- A `make docs-gen-check` CI gate: regenerates `docs/reference/` and
  `internal/api/wellknown/`, and fails the build on any diff or untracked file.
- A completeness test (`tools/gendocs`) failing the build on any `implemented`
  Collection method with an empty summary, an undescribed or untyped parameter, or
  zero examples.
- `docs/03-migrating-from-ansible.md`: the playbook-to-runbook keyword map (with
  explicit unsupported rows), the module-to-FQCN map, the AWX/AAP object map, and the
  borrowed-vocabulary section, absorbing and trimming duplicated content out of
  `examples/upgrade_ios/README.md`.
- `docs/10-running-in-production.md`: failure semantics (the SSH transport's
  dial-only retry policy), safety (blast radius, lifecycle gating, locking), security
  and credential storage (the AES-256-GCM master-key resolution order), and a data
  handling disclosure covering what is and is not encrypted at rest and the precise,
  best-effort scope of `register_mask`/`secret_mask` live-event masking.
- `internal/apispec`: the control-plane API route table as data (method, pattern,
  scope, link relation, summary, parameters, and per-status responses), shared by
  `cmd/controller`'s real router and a new generated OpenAPI 3.1 document
  (`docs/reference/schemas/openapi.json`, also served live at
  `/api/v1/openapi.json`), so neither can describe a route the other does not.
- `docs/09-control-plane-and-api.md`, `docs/11-extending-pleiades.md`,
  `docs/13-releases-and-stability.md`, and `docs/14-project.md`, completing the
  fourteen-book documentation scaffold.

### Fixed

- A missing database migration for the `Job`/`JobTask` schema
  (`internal/ent/migrate/migrations/sqlite/0004_add_jobs.sql`). Without it, every
  `POST /api/v1/jobs/dispatch` call failed with `"no such table: jobs"` on any
  deployment, not just a fresh one: the tables were never created. Found while
  capturing real output for the Walk-tier quickstart.
- `pleiades --help`, `pleiades forge --help`, and `pleiades inventory --help` no
  longer cite an internal specification document that is gitignored and never ships.
  Same fix applied to the `inventory.yaml` header a scaffolded project's
  `pleiades init`/`add-host` writes to disk.
- The doc comment on all 71 declared-but-unimplemented Collection method packages
  under `internal/catalog/` no longer claims "no dispatcher anywhere in this codebase
  consumes this," which stopped being true once `engine.NewCollectionActionExecutor`
  landed. It now correctly explains that a runbook task calling the FQCN reaches the
  real dispatcher and is refused there, by design, until the method is really
  implemented.
- Regenerated those same 71 packages' source and test files from the current
  `collectionscaffold` templates, which also fixed a latent function-signature drift
  (the on-disk stubs no longer matched `collection.Method`'s real signature). The four
  real, hand-implemented `net.catalyst.*` methods were left untouched.
- `docs/cli_reference.md`'s printed usage block was missing the `inventory` and
  `forge` commands, which had shipped since the reference was last updated. The file
  itself is now deleted in favor of the generated `docs/reference/cli.md`.
- `net.catalyst.site_facts` and `net.catalyst.tag_facts` were `implemented` but carried
  no `Doc.Examples`, caught by the new completeness test the moment it was written.
  Both now document a real, minimal example.
- Generated JSON (`module-catalog.json`, `runbook.schema.json`,
  `inventory.schema.json`, and `pleiades doc --json`'s output) no longer HTML-escapes
  `>` inside a version constraint like `>=1.0.0`.
- `examples/upgrade_ios/README.md` no longer claims "no dispatcher wires a declared
  collection method to a real SSH transport yet": the real dispatcher
  (`engine.NewCollectionActionExecutor`) has reached every Collection FQCN since it
  landed, and correctly refuses each one that is not yet implemented. Also fixed two
  stale citations into files `design/` now holds instead of `docs/`.

## Pre-history

Everything before this entry: the Crawl-tier CLI, the control plane (data layer, event
bus, distributed locking, leader election, envelope encryption, inventory factory,
RBAC, the CEL engine, the workflow DAG builder, the HATEOAS API gateway, the
dispatcher), the Forge scaffolding tooling, and the 75-method module catalog (4
implemented). Recorded as one entry rather than reconstructed after the fact; see
`git log` for the real history.
