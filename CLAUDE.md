# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

**Always read and use `.AGENTS/AGENTS.md` first.** It is the canonical, authoritative rules
file for this repository (writing style, licensing, doc-comment requirements, testing
policy, Go conventions, the LSP-over-grep tooling mandate, and the architecture
mismatch/map verification protocol). Everything below is a supplement for orientation,
not a replacement for it.

**`HANDOFF_DOCUMENT.md`, `LESSONS_LEARNED.md`, and `FAILURE_PATTERNS.md` are each a short index; a
`*_ARCHIVE.md` sibling holds the full history.** Read the tracked file, not the archive, by default.
Open an archive only when you need a specific past entry's full detail (a prior session's writeup, a
rule's full reasoning, a bug's full symptom/fix). See `.AGENTS/AGENTS.md`'s Mandatory Documentation
Rules for how to append to both correctly.

## What this is

Pleiades (module `github.com/Subject-Void-LLC/the-pleiades`) is an object-oriented,
strongly typed automation mesh that also runs Ansible. Ansible support is a migration
on-ramp: a playbook lands unchanged, then converts to native typed collections over time.
It targets the same problem AWX/Ansible Automation Platform does (RBAC, audit trail,
scheduler, multi-user), not a single-laptop CLI replacement.

Read `docs/01-start-here.md` before assuming any feature works end to end. This is a
pre-1.0 project and the honest state is not what the docs' introductions might imply:

- **Crawl tier (the `pleiades` CLI) is real.** Single static binary, no server/DB/broker,
  connects over real SSH, genuinely executes `ssh_exec` against real devices.
- **Walk tier's control plane is real and tested**: data layer, event bus, distributed
  locking, leader election, envelope encryption, inventory factory, RBAC, the CEL
  conditional engine, the workflow DAG builder, the HATEOAS API gateway, job dispatcher.
- **Walk tier's distributed execution plane reaches real devices as of Phase 16.**
  `internal/adapters/native/adapter.go`'s `Execute` resolves the dispatched runbook to a real
  compiled DAG and runs it through the same `engine.Executor` stack the Crawl-tier CLI uses,
  scoped to the one device the dispatch names, over the same real SSH transport. A Collection
  method runs inside a per-task subprocess (`ipc_parent.go`/`ipc_child.go`) so a secret crosses
  a real process boundary on stdin, never argv or the environment (PLAN.md Section 17.5). Proven
  end to end against real NATS and real `sshd` containers by
  `cmd/runner/ssh_mesh_release_gate_test.go`. Two honest caveats: the Controller resolves a
  device's credential and attaches it to the dispatch payload, so a secret rides the one
  JetStream stream and can persist there for up to its retention window; and PLAN.md Section
  17.4's full `CredentialStore` (rotation, Vault, PFX) is still unbuilt, with the Controller
  using the same file-backed store the Crawl tier does for per-device credentials.
- **Credential types and the injector engine are real (Phase 22).** A type is data
  (`internal/credtype`), an AWX export decodes into it with no translation layer, and its
  injectors are rendered by the one shared template engine (`internal/render`) and injected
  at fan-out into env vars, extra vars and generated files. Bound credentials reach a real
  `ansible-playbook` in a real container, proven by
  `cmd/runner/ansible_injection_release_gate_test.go` and by
  `tests/e2e/credential_injection_test.go` through the real binaries. Four caveats, each
  enforced rather than merely stated: `env` and `file` injectors are refused on the native
  Go path (Section 29.4 keeps the stricter rule there) at bind time and again at run time;
  one external secret source is implemented (`file`) and eight are declared-not-implemented;
  a prompted credential input is never stored, so such a job cannot be relaunched; and the
  one-per-kind binding rule is application-enforced, not a database constraint. The
  JetStream caveat above gets **larger in volume and identical in kind**: a template
  binding a cloud credential plus two file-generating ones puts several more secrets on
  the same message, including whole PEM bodies.
- **Module catalog: 78 registered FQCNs; 71 implemented, 7 declared-not-implemented.** These counts
  and every per-method status come from the generated
  `docs/reference/schemas/module-catalog.json`, which `tools/gendocs` builds from the real registry
  and which is authoritative over any hand-written tally in this file — read it rather than trusting
  this paragraph, which has gone stale before. By namespace, implemented: `svc` 16, `file` 10,
  `pkg` 9, `identity` 6, `net` 5, `cloud` 4, `container` 4, `exec` 3, `fw` 3, `archive` 2, `fs` 2,
  `wait` 2, `win` 2, `facts` 1, `http` 1, `pleiades` 1. The `svc` group is the 6 `svc.systemd.*`
  methods, the 5 `svc.windows.*` ones, and the 5 generic `svc.*` ones that resolve a device's
  service manager and dispatch to whichever applies, all built on `pkg/remotesvc`.
  **The short and decision-relevant list is what is NOT implemented, all seven of them:**
  `file.template` — deliberate and not a gap to close casually, since the render engine lives in
  `internal/render` and a Collection may not import `internal/`; `net.cli.command`,
  `net.cli.config` and `net.ios.config`, blocked on Phase 86.5's interactive network CLI
  transport; and `net.netconf.config`, `net.junos.config` and `net.eos.config`, blocked on
  Phase 74. Each returns an explicit "declared but not implemented" error rather than a silent
  no-op, though that error is a backstop rather than the mechanism: the dispatcher refuses any
  method whose `Status` is not `StatusImplemented` before its body is ever called
  (`internal/engine/collection_action.go`).
  `exec.command` was the first write-capable method and the first built on `pkg/remoteexec`,
  the shared SSH execution primitive a Collection may import (a Collection may import only
  `pkg/`, so `internal/transport/ssh` is unreachable from one and is now a thin adapter over
  the same primitive). Every implemented method declares `collection.Reversibility` (a bool plus
  a required reason when false, enforced at registration), and a run that changes something emits
  the concrete reversing instruction via `sdk.RecordInverse` as an `inverse` stat holding an FQCN
  and resolved params. Nothing performs a rollback yet; the recording exists because only the
  forward run can capture the values an undo needs.
- **The scheduler is real (Phase 23).** A schedule is an RFC 5545 recurrence attached to a
  Template, so one mechanism covers every `Launchable` kind. `internal/schedule/rrule` is a
  hand-rolled, deliberately bounded engine (no new dependency, following `pkg/filters/cron.go`),
  and its AWX parity is *earned rather than claimed*: `tools/genrrulefixtures` generates golden
  occurrence vectors from python-dateutil, the library AWX itself schedules on, and Python is
  never a build or CI dependency. `internal/schedule.Scanner` gates on the
  `pleiades-scheduler-leader` lease `cmd/controller` had elected and ignored since Phase 4, taking
  `isLeader func() bool` exactly as `dispatch.Reaper` does, so the package imports neither
  `internal/election` nor `internal/lock` (asserted by `internal/archtest`). Firing goes through
  `api.Dispatcher.LaunchScheduled`, the same path a manual launch takes. Four things are worth
  knowing before describing it: the recurrence grammar is a bounded subset refused at *save* time,
  not run time; missed runs are **coalesced** to one, with a durable `skipped` row for each that
  did not happen; a schedule fires once because of a unique index on
  `(schedule, occurrence_at)` claimed before launching, **not** because of leader election, whose
  two-second fencing-token-less lease cannot promise it; and a template bound to a prompted
  credential, or a saved configuration answering a survey password, is refused outright, because
  neither value is stored and replaying one unattended forever is worse than doing it once.
- **Plan-time capability checking is a two-entry table** (`internal/engine/action_capability.go`,
  covering only `ssh_exec` and `ios_backup`). `pleiades validate` will pass a runbook whose
  capability mismatch only surfaces at run time.
- **The web UI (`web/`) is a mockup.** Five of six routes render hardcoded content; the
  sixth (SSE log viewer) has three defects that stop it reaching a real Controller. This is a
  different thing from `internal/ui`, the server-rendered view registry the Controller actually
  serves, where most views are real; do not conflate the two when describing UI status.

When touching any of the above, do not describe it as more finished than it is — see
`docs/01-start-here.md#implementation-status` for the generated, current matrix.

## Common commands

```bash
make ci              # the whole gate, run LOCALLY: build vet fmt test-race test-repeat test-integration gosec govulncheck coverage docs-lint docs-gen-check helm-lint templ-gen-check
make ci-remote       # what GitHub Actions runs: `ci` minus test-race, test-repeat, test-integration and coverage — no tests at all
make build            # go build ./...
make test             # go test ./...
make test-race        # go test -race ./...   (required before calling anything "verified" per RULE 0)
make fmt               # gofmt -l check, hard failure on any unformatted file (excludes .claude/)
make fmt-fix           # gofmt -w, actually fixes it
make vet
make gosec             # go run ./tools/gosec-check — wraps gosec with gosec-waivers.json's per-finding waivers
make govulncheck
make coverage           # go run ./tools/coverage-check — ratchet against coverage-floor.json, not a flat 90% gate
make arch               # go test ./internal/archtest/...  — Section 25 layering rules as a real test
make docs-lint          # go run ./tools/docs-lint — fails if a gitignored internal doc is cited anywhere a user could see it
make docs-gen-check     # regenerates docs/reference and internal/api/wellknown, fails on any diff or untracked file
make tools              # installs gosec/govulncheck at the Makefile's pinned versions; no-op when already correct
make hooks              # once per clone: point core.hooksPath at .githooks so `git push` runs `make push-gate` first
make push-gate           # everything `ci` runs, with test-race/test-integration/coverage swapped for tolerant equivalents; warns instead of failing on flaky-packages.json packages
```

**The test suite runs locally and only locally. GitHub Actions runs no tests.**
`.github/workflows/ci.yml` checks out, sets up Go from `go.mod`, installs the pinned
tools and Helm, and runs `make ci-remote` — `ci` minus `test-race`, `test-repeat`,
`test-integration` and `coverage`, i.e. compilation on three operating systems, `vet`
under both tag sets, `gofmt`, `go mod tidy -diff`, `gosec`, `govulncheck`, the
docs/`templ` regeneration checks and the Helm chart lint. Nothing there proves a single
test passes. The reason is that the full job never once went green on a hosted runner:
around twenty packages provision real ephemeral containers through `testcontainers-go`,
`make ci` runs the suite three times over plus a fourth pass inside
`tools/coverage-check`, and several of those packages are deliberately
timing-sensitive (`internal/event`'s Phase 96a gate severs a real broker for 150
seconds). A permanently red gate gates nothing. So `make ci` is now a gate a human runs,
and `.githooks/pre-push` (`make hooks`, once per clone) is what makes that automatic.

There is still no CI-only step and no CI-only tool version — `gosec` and `govulncheck`
are pinned once in the `Makefile` (`GOSEC_VERSION`, `GOVULNCHECK_VERSION`) and installed
by `make tools` on both sides, so a local run and the CI job run byte-identical scanners.
Never `go install` either tool by hand at `@latest`: a newer scanner than the pin reports
findings CI will not, and an older one misses findings CI will. The one thing a local run
still cannot predict is `govulncheck`'s live advisory database.

`.githooks/pre-push` runs `make push-gate`, not `make ci`, deliberately: it is every
check `ci` runs, with `test-race`/`test-integration` swapped for `tools/testgate`'s own
invocations and `coverage` swapped for `go run ./tools/coverage-check -tolerant` (that
tool runs its own separate full `go test ./... -cover` internally, so it needed the
identical tolerance applied a second time, not just once at the test-race/
test-integration layer). Both print a warning instead of failing the push when a test
failure is confined to a package listed in `flaky-packages.json` (each entry with a
written reason, mirroring `gosec-waivers.json`'s per-finding convention), classified by
the shared `tools/internal/flakegate` package both tools use so they cannot disagree.
This exists because packages that provision real ephemeral Docker containers or real
multi-replica timing races (`tests/e2e`, `internal/lock`, `internal/event`,
`internal/election`, `cmd/controller`, and others `flaky-packages.json` names) reliably
flake under this kind of sandboxed environment's full parallel `-race` load —
`FAILURE_PATTERNS.md` #61 — and pass individually every time. `make ci` itself is
completely unaffected by any of this and stays exactly as strict. A build failure, or a
test failure in any package not listed, still fails `push-gate` exactly like `ci`.

Read that tolerance more carefully now than you would have before: there is no stricter
run waiting downstream of a push any more. `push-gate` used to be a preview of a gate
GitHub would apply again in full; it is now the last automatic check anything gets. A
package listed in `flaky-packages.json` without a real, written, observed reason is a
package nothing checks anywhere, so run `make ci` itself — not just `push-gate` — before
calling work verified.

Single test / single package:

```bash
go test ./internal/engine/...
go test ./internal/engine/... -run TestName -v
go test -race ./internal/lock/...   # internal/lock, internal/event, internal/transport/ssh dial real ephemeral Docker containers (NATS, sshd)
```

Required one-time tool setup (`.AGENTS/AGENTS.md`'s IDE & LSP Tooling section):

```bash
go install golang.org/x/tools/gopls@latest
# ensure $(go env GOPATH)/bin is on PATH persistently (not just this shell) — see AGENTS.md

make hooks   # once per clone: run `make push-gate` before every push, so CI failures land here first
```

Prefer `gopls references` / `gopls definition` over `grep` for any claim about Go call
graphs or symbol usage — AGENTS.md treats a grep-derived claim about Go semantics as a
guess, not evidence. Grep is fine for prose/YAML/markdown.

### ent code generation

`internal/ent` is generated from `internal/ent/schema`. Never hand-edit generated files:

```bash
go generate ./internal/ent
```

A schema edit without regenerating is a silent no-op that still compiles — the worst
failure shape available.

Regenerating is only half of it. The runtime applies **versioned migration files**, not
`Schema.Create`, so a new entity also needs one per dialect or its tables never exist in a real
deployment (the generated Go client compiles and every unit test using `enttest` passes anyway,
because `enttest` does run `Schema.Create`):

```bash
go run internal/ent/migrate/gen/main.go sqlite   <name>
go run internal/ent/migrate/gen/main.go postgres <name>   # starts an ephemeral container
```

`internal/ent/migrate/parity_test.go` is what catches a dialect left behind.

### Catalog code generation

`internal/catalog/` and `internal/inventory/devices/{windows,aws}/` are generated from
`internal/forge/catalogdata`, driven through the real `pleiades forge` CLI (`tools/gencatalog`),
never hand-edited:

```bash
go generate ./internal/forge/catalogdata
```

Fix the data in `internal/forge/catalogdata` or the scaffold templates
(`internal/forge/collectionscaffold` / `devicescaffold`), never the generated output directly.

The command is idempotent and safe to re-run: every subcommand is invoked with
`--skip-existing`, so an entry already on disk is left exactly as it is (implementation,
hand-written tests and all) and only a genuinely new entry is written. It reports how many
files it wrote, which on an unchanged table is legitimately zero. Skipping is per ENTRY, not
per file: writing only the missing half of an already-implemented method would drop a
generated starter test asserting "declared, not implemented" underneath a real
implementation.

A method's `Doc` travels to the scaffold as JSON on `forge new-collection --doc-json`
(or `--doc-json @file.json`), so a scaffolded method comes out carrying the full reference
documentation `internal/forge/catalogdata` declares, rather than needing it transcribed by
hand before `internal/archtest`'s `TestCatalogDataDocsMatchTheRegistry` will pass.

`docs/11-extending-pleiades.md` has the full worked example, using `exec.winrm.shell`.

## Architecture

### The three tiers, and their composition roots

| Tier | Composition root | Adds |
|---|---|---|
| Crawl | `cmd/pleiades` | Offline CLI: static inventory, credentials, validate, run. Links inventory/engine/validate packages directly, no Controller dial. |
| Walk | `cmd/controller` + `cmd/runner` | API Gateway (embedded SQLite + NATS JetStream) and a stateless worker pulling jobs off a durable NATS consumer group. |
| Run | (not built) | GitOps-synced config, promotion gates, Ansible interop for unconverted playbooks. |

**These two labels were swapped on 2026-08-22.** Until then Walk named the offline CLI and
Crawl named the Controller/Runner tier, inverting "crawl, walk, run"; the ladder itself never
changed. Docs and archives were rewritten to the corrected names, but **git commit messages
were not** — a commit dated before 2026-08-22 saying "Walk tier" means what this table now
calls Crawl. The `W` in phase identifiers `W1`-`W6` is a leftover of the old name, not a
mnemonic. See `LESSONS_LEARNED.md` #153.

`cmd/demo` wires a minimal controller-adjacent stack for exercising the web UI's SSE log
stream in isolation.

Every `cmd/*` binary is a *composition root*: the one place concrete drivers get wired
into interfaces. Business logic never lives in `cmd/`; it parses args/config and delegates
into `internal/`.

### Layering rule (enforced by `internal/archtest`, not just convention)

- `pkg/` never imports `internal/`.
- `internal/engine` imports no concrete driver.
- Only a small allowlisted set of adapter packages may import a concrete driver directly
  (NATS, the ent SQL driver): `internal/api`, `internal/ent`, `internal/event`,
  `internal/lock`, `internal/runner`, `internal/topology`. Adding to this allowlist is a
  real design decision — `go test ./internal/archtest/...` fails immediately if it drifts.
- `cmd/` composition roots are exempt (wiring concrete implementations is their job).

### Core domain vocabulary

- **Runbook**: native YAML automation format (`id`, `hosts`, `tasks`, ...). Never call it
  a "playbook" (reserved for a real Ansible file).
- **Task / FQCN**: a runbook step names an action by fully-qualified collection name,
  `<namespace>.<method>` (e.g. `net.catalyst.device_facts`), and passes it `params`.
- **Collection**: a namespaced Go package implementing one FQCN as a
  `pkg/collection.Descriptor` (a `Manifest` of required capabilities/transports/status,
  plus an `Invoke` function). Lives under `internal/catalog/<namespace>/...`. Registers
  itself via package `init()`, made reachable only by a blank import from
  `internal/catalog/builtins.go`.
- **Capability**: what a device *can do* (e.g. `AptCapable`), matched structurally against
  a Go interface the device type implements — not what the device *is*.
- **Inventory item**: a managed device — name, type, properties, lifecycle state, version,
  history. Concrete device types live under `internal/inventory/devices/<vendor>/`.
- **Transport**: how a task's command reaches a device, auto-selected from device
  capabilities (`internal/transport`, with the real implementation in
  `internal/transport/ssh`).
- **Sync plugin**: implements `internal/inventory/syncplugin.Plugin`'s four-stage contract
  (`Connect` → `Discover` → `Classify` → `Sync`), verified by a shared conformance suite
  (`internal/inventory/plugins/conformance_test.go`) that every real plugin is driven
  through identically. Lives under `internal/inventory/plugins/<name>/`.
- **`when` / `when_or` / `when_cel`**: three ways to gate a task — Ansible-compatible ANDed
  list, ORed list, or a raw CEL escape hatch, compiled before execution.

### Extending the catalog (`pleiades forge`)

`forge new-collection` / `new-device` / `new-plugin` scaffold a new Collection method,
device type, or sync plugin (two gofmt-clean files each: implementation + test). None of
the three self-registers into the running binary — that requires a deliberate, one-line
blank import into the relevant `builtins.go` (`internal/catalog/builtins.go`,
`internal/inventory/builtins.go`, `internal/inventory/plugins/builtins.go`). This is
intentional: a generated-but-unwired file compiles and its tests pass, but stays invisible
to `pleiades doc --list`, `validate`, and the dispatcher until wired in.

There is no out-of-tree extension mechanism today: every extension point lives under
`internal/`, reachable only from inside this module or a fork of it (see
`docs/11-extending-pleiades.md`).

### Control plane API (`cmd/controller`, `internal/api`)

Front Controller pattern: every route passes through tracing, metrics, structured
logging, rate limiting, authentication, then scope authorization, before its handler
runs. The route table (`internal/apispec`) is the single source both the real router and
the generated OpenAPI doc build from — but nothing enforces that `cmd/controller`'s
hand-written route registrations cover every `apispec.Endpoints` entry; a route added to
the spec but never mounted fails silently (404) rather than at build time.

Two independent authorization mechanisms, easy to conflate:
1. **Scope check**: a route's required scope (e.g. `runbook:execute`) against the JWT's
   `Scopes` claim. `admin` bypasses unconditionally.
2. **RBAC** (`internal/auth.ScopeResolver`): hierarchical RoleBindings (`viewer` <
   `operator` < `admin`) at a target (system/org/group/device), with explicit Deny always
   winning over Allow at the same or broader level. This decides who *can be granted*
   which scope; the scope check decides what a granted token can *call*.

A successful response's `_links` hypermedia array and an `OPTIONS` request's `Allow`
header are computed by one shared builder, so they cannot disagree.

### Documentation generation

`docs/reference/` and `internal/api/wellknown/` are generated by `tools/gendocs` from
source (catalog manifests, apispec, JSON schemas, CLI spec). `make docs-gen-check` proves
the committed tree matches a fresh run; regenerate and commit together, never hand-edit
generated reference pages.

### Internal-only documents (gitignored, never cite from anything a user can see)

`.SPECIFICATION/`, `.AGENTS/`, and any `.[A-Z]*`-prefixed path are gitignored (pattern
`.[A-Z]*` in `.gitignore`) and never ship. `.SPECIFICATION/PLAN.md` is the main spec.
`tools/docs-lint` (wired into `make ci`) fails the build if a citation into one of these
leaks into `docs/`, CLI `--help` text, a scaffolded project file, root-level Markdown, or
generated reference pages — there is no waiver mechanism for this check, unlike
`gosec-waivers.json`.

## Testing policy highlights (full detail in `.AGENTS/AGENTS.md`)

- **RULE 0 (Representative-or-nothing)**: a test only counts as verification if it runs
  the same config path the platform actually runs. A test that mocks the transport layer
  while testing transport behavior proves nothing.
- `internal/lock`, `internal/event`, and `internal/transport/ssh` run real conformance
  tests against ephemeral Docker containers (NATS, sshd) — Docker must be available.
- Coverage is a ratchet (`coverage-floor.json`), not a flat threshold: no package may drop
  below its recorded floor; a package with no floor yet is reported, not failed.
- Every `gosec` finding accepted into `gosec-waivers.json` needs an individually written
  reason — no blanket rule-ID or directory suppression.
