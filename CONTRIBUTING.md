# Contributing to Pleiades

## Before you start

Read [`README.md`](README.md) for what is real today and what is not. This is a
pre-1.0 project: large parts of the roadmap are declared but not yet implemented, and
contributions land against a moving DSL and catalog.

## Development workflow

Once per clone:

```bash
go install golang.org/x/tools/gopls@latest   # editor/LSP tooling; not run by CI
make lsp                                     # check that language server works on this tree
make hooks                                   # run make ci automatically before each push
```

`make lsp` is worth running even if you never open an editor here. The repository's
`.mcp.json` exposes `gopls mcp`, the language server's headless mode, to AI coding
assistants, which is how one answers a question about Go code with a typed query instead
of a text search. `make lsp` does a real handshake against that server and fails if gopls
is missing from `PATH`, is not speaking the protocol, or is too old to type-check the Go
version `go.mod` asks for. It takes about a second once the build cache is warm. Note that
`$(go env GOPATH)/bin` is not on the default `PATH`, and that adding it with a bare
`export` lasts only for the current shell, so put it in your shell profile.

Then, to check a change:

```bash
make ci
```

`make ci` runs everything a pull request must pass: build, `go vet`, `gofmt` (a hard
failure, not an auto-fix, so a PR is expected to already be formatted), `go test -race
./...`, `gosec`, `govulncheck`, the coverage ratchet, `docs-lint`, and `docs-gen-check`.

Do not install `gosec` or `govulncheck` by hand. `make ci` installs them itself, at the
versions pinned in the `Makefile` (`GOSEC_VERSION`, `GOVULNCHECK_VERSION`), and the CI
workflow installs them by calling the same `make tools` target. That is what makes a
local `make ci` and the CI job run byte-identical scanners, so a pass here means
something about what will happen there. Installing your own `@latest` copy defeats it:
a newer scanner than the pin will report findings CI does not, and an older one will
miss findings CI does. Bumping a pinned version is a deliberate commit against the
`Makefile`.

`make hooks` points `core.hooksPath` at the tracked [`.githooks/`](.githooks/)
directory. It is opt-in per clone because Git will not run a hook that arrived with a
fetch until you ask it to, which also means a fresh clone pushes with nothing checking
anything until you run it.

The `pre-push` hook does **not** run the gate. Git opens its connection to the remote
before calling the hook, so a suite that takes minutes in there kills the push with
SIGPIPE and no output at all while printing "all checks passed". So the gate is a
separate step you run on your own schedule, and it leaves a receipt naming the commit it
verified:

```bash
make push-gate     # or make ci, which is stricter; minutes, and needs Docker up
git push           # the hook reads the receipt back, in about a second
```

A receipt is only written from a clean tree, and only if HEAD did not move while the gate
ran, so what was verified and what you are pushing are the same commit. The hook checks
each ref's tip: the gated commit, an annotated tag pointing at it, a ref created or
fast-forwarded onto a commit already inside its history, or a push that only deletes refs.
Anything else is refused, naming the reason and the command that fixes it, and a receipt
expires after a day because `govulncheck` reads a live advisory database. Skip a single
push with `git push --no-verify`.

Do not read a local pass as a CI pass, in either direction. `govulncheck` queries a live
vulnerability database, so a newly published advisory can turn a commit red hours after it
passed here; the version pin closes the gap under this project's control and does not
pretend to eliminate it. In the other direction, the hosted job runs `make ci-remote`,
which runs no tests at all, so every test result this project has comes from a run like
the one above.

### Coverage

`coverage-floor.json` is a ratchet, not a flat threshold: no package may drop below
its recorded floor, and 90% is the target for new and touched code, but the whole
repository is not held to 90% on day one. A package with no recorded floor is reported
as new, not failed. See `coverage-floor.json`'s own header for the full policy.

### Security findings

`gosec-waivers.json` accepts a finding only with an individually written reason, never
a blanket rule or directory exclusion. A genuinely new finding anywhere in the module
still fails the build.

### Documentation

A handful of directories in this repository hold internal specification and agent
material and are gitignored on purpose: they never ship in a checkout a real user
receives. `docs-lint` fails the build if a citation into one of them appears anywhere
a user could see it: `docs/`, the CLI's own `--help` text, a scaffolded project file,
root-level Markdown, or the packages the documentation generator reads. A citation
into a file that never ships sends a real reader somewhere they cannot follow; see
`tools/docs-lint`'s own doc comment for the exact list.

If your change adds or changes a Collection method, a runbook key, a CLI flag, a
config variable, an API route, a capability, a device type, or a sync plugin, update
the relevant page under `docs/` in the same PR. This project's internal roadmap tracks
a documentation update as a standing, per-change obligation, not an afterthought.

### Generated code

Everything under `internal/catalog/` and `internal/inventory/devices/{windows,aws}/`
is generated by `tools/gencatalog` from `internal/forge/catalogdata`, driven through
the real `pleiades forge` CLI, never hand-edited. If generated output looks wrong, fix
the data in `internal/forge/catalogdata` or the template in
`internal/forge/collectionscaffold`/`devicescaffold`, not the generated file itself.

### Schema changes

The controller's schema comes from `internal/ent/schema`. After changing it, run
`go generate ./internal/ent`, then generate a migration for EACH dialect:

```bash
go run internal/ent/migrate/gen/main.go sqlite   <name>
go run internal/ent/migrate/gen/main.go postgres <name>   # starts a throwaway container
```

A migration must *expand* the schema: add tables, columns that may be empty or have a
default, and indexes that are not unique. The build before it then keeps working
against the schema it leaves, which is what lets old controllers keep serving during
a rolling upgrade and lets a controller be rolled back.

A migration that removes or narrows something (drops a table or column, changes a
type, adds or removes NOT NULL, changes a foreign key's delete action, adds a unique
index over existing columns, adds a trigger or function) is a *contract*. Declare it
in `internal/ent/migrate/compat.go`, for both dialects, with the oldest migration
whose build can still serve after it and a sentence saying what the build before it
would do wrong. Better still, split it: expand in one release, stop using the old
shape, and contract in a later one.

Three tests hold you to this:

- `TestEveryMigrationIsClassifiedAsDeclared` migrates each dialect one migration at a
  time and fails when a migration contracts without a declaration, or is declared a
  contract and changes nothing.
- `TestANewContractNeedsTheGuard` fails the first time a new contract is declared: a
  contract also needs the apply-time guard that refuses to run it while an older
  controller is still alive, and that guard is built with the first real contract.
- The upgrade gate in `tests/e2e/upgrade_binary_gate_test.go` runs the previous
  build's controller against your migrated database and makes it do its ordinary
  work.

What none of them can see is reviewed by hand: a column whose meaning changes while its
shape does not (declare it with `semantic` set), the shape of messages on the broker,
and a new value in a field stored as text. A new table's foreign key onto an existing
table should be `ON DELETE CASCADE` or `SET NULL`, or an older build deleting the
parent can be refused.

### Code style

- No em-dashes, in code, comments, or documentation.
- American English spelling.
- Every exported (and most unexported) function, type, and package gets a Google-style
  doc comment explaining the non-obvious "why," not just restating the name.
- Error messages are lowercase, no trailing punctuation.
- Table-driven tests for anything with more than one input/output pair.

## Licensing

All new source files must be compatible with GPLv3. Apache 2.0 dependencies are fine;
proprietary or more restrictive licenses are not.

## Reporting a security issue

See [`SECURITY.md`](SECURITY.md). Do not open a public issue for a vulnerability.
