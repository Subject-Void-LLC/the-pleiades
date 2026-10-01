# Contributing to The Pleiades

## Before you start

Read [`README.md`](README.md) for what is real today and what is not. This is a
pre-1.0 project: large parts of the roadmap are declared but not yet implemented, and
contributions land against a moving DSL and catalog.

## Development workflow

Once per clone:

```bash
make doctor                                  # what this machine can verify, and how to fix each gap
make tools                                   # the pinned gosec, govulncheck and actionlint
make hooks                                   # the commit and push hooks
go install golang.org/x/tools/gopls@latest   # editor/LSP tooling; not run by CI
make lsp                                     # check that language server works on this tree
```

`make doctor` takes seconds and answers the questions that used to surface an hour into a
gate run: whether your Go toolchain is the one `go.mod` asks for, whether a Docker daemon
answers, whether the pinned scanners and hooks are installed, and which optional gates
(a real Windows host, the VirtualBox lab, pywinrm) this machine will skip. Each line that
needs attention names the command that fixes it.

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
make ci-fast   # every package that starts no container: minutes, and needs no Docker
make ci        # everything, strictly: needs Docker, and takes a while
```

`make ci-fast` is the loop to run while you work. It is also exactly what the `fast` CI
job runs, so a pass here is a pass there. `make ci` runs everything a pull request must
pass: build, `go vet`, `gofmt` (a hard failure, not an auto-fix, so a PR is expected to
already be formatted), the whole test suite once (`make test-full`: race detector,
integration tag, coverage recorded), `gosec`, `govulncheck`, the coverage ratchet from
the numbers that run recorded, `docs-lint`, `docs-gen-check`, and a lint of the CI
workflow itself.

Every test run ends with a list of the tests it skipped and why. A test that needs Docker,
a real Windows host or a tool you do not have skips rather than fails, so read that list
before reading a green run as "everything passed".

`make test-clean-room` runs the fast tier inside a pinned Go container, as an
unprivileged user with an empty home directory and no git identity, from a fresh clone of
your last commit. It is the closest this repository gets to "does it work on someone
else's machine", and it is how a test that quietly depends on your environment is found.

Do not install `gosec`, `govulncheck` or `actionlint` by hand. `make ci` installs them itself, at the
versions pinned in the `Makefile` (`GOSEC_VERSION`, `GOVULNCHECK_VERSION`, `ACTIONLINT_VERSION`), and the CI
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
make push-gate     # or make ci, which is stricter; about an hour, and needs Docker up
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
pretend to eliminate it. In the other direction, the pull-request jobs tolerate a failure
that passes when re-run alone, as `make push-gate` does, while `make ci` and the nightly
job do not.

### What CI runs

Every job calls a `make` target, so you can run exactly what a job ran:

| Job | Target | What it proves |
| --- | --- | --- |
| `ci` | `make ci-remote` | build, vet, format, scanners, generated files, on Linux, macOS and Windows |
| `fast` | `make ci-fast` | every container-free package, and each three times over, on Linux |
| `fast-macos` | `make ci-fast` | the same on macOS; advisory until it has passed once |
| `containers` | `make ci-containers SHARD=k/4` | the container packages, in four shards |
| `coverage` | `make coverage-measured` | the coverage ratchet, from the numbers the jobs above recorded |
| `nightly` | `make test-full` | the whole suite, strictly, with nothing re-run |

The `containers` job restores its images from a cache and only pulls on a miss. A
maintainer can set a read-only `DOCKERHUB_TOKEN` (and `DOCKERHUB_USERNAME`) secret to
raise Docker Hub's pull limit; a pull request from a fork runs without it. No job runs
LocalStack: it needs an auth token the hosted jobs do not carry, so the AWS tests that
need it skip there as `needs localstack`, the `coverage` job names their floors as
unchecked rather than failing them, and a developer with a token runs them with `make ci`.

### Coverage

`coverage-floor.json` is a ratchet, not a flat threshold: no package may drop below
its recorded floor, and 90% is the target for new and touched code, but the whole
repository is not held to 90% on day one. A package with no recorded floor is reported
as new, not failed. See `coverage-floor.json`'s own header for the full policy.

A test that needs something a machine may lack should stop through
`internal/testsupport`'s `Require` (or a helper built on it, such as `LocalStackToken`),
not a bare `t.Skip`. Its skip then names the need (`needs localstack: ...`), and when that
package's coverage falls below its floor in a run that lacked the need, the floor check
names the floor as unchecked instead of failing a change that never touched it. A floor
recorded on a machine with LocalStack cannot be checked on one without it, and saying so
is the honest answer; `PLEIADES_TEST_REQUIRE` makes a need mandatory for a run.

A test container is ready when it answers through its mapped host port, not when it logs
that it started: wait for the log line and then `testsupport.ForGreeting` (`SSHGreeting` for
an SSH server), or `wait.ForHTTP` for an HTTP one. A log line is said inside the container,
before Docker may be forwarding the port, and a test whose first call does not retry fails in
that gap.

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
