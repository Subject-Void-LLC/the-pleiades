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
  connects over real SSH, genuinely executes `ssh_exec` against real devices. `pleiades adhoc
  <hosts> <method> key=value ...` runs one method with no runbook, through the same pipeline
  as `run` (`cmd/pleiades/run_pipeline.go`), and `run --json`/`adhoc --json` print that
  pipeline's report model as one JSON document (Phase 113). For an agent, `adhoc ... --json` is
  the way to call a method once; do not write a throwaway runbook for it.
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
- **Module catalog: 104 registered FQCNs; 101 implemented, 3 declared-not-implemented.** These counts
  and every per-method status come from the generated
  `docs/reference/schemas/module-catalog.json`, which `tools/gendocs` builds from the real registry
  and which is authoritative over any hand-written tally in this file - read it rather than trusting
  this paragraph, which has gone stale before. By namespace, implemented: `virt` 19, `svc` 16,
  `net` 12, `file` 10, `pkg` 9, `identity` 6, `cloud` 4, `container` 4, `win` 4, `exec` 3, `fw` 3, `wait` 3, `archive` 2, `fs` 2,
  `pleiades` 2, `facts` 1, `http` 1. The `svc` group is the 6 `svc.systemd.*`
  methods, the 5 `svc.windows.*` ones, and the 5 generic `svc.*` ones that resolve a device's
  service manager and dispatch to whichever applies, all built on `pkg/remotesvc`. The `net` group
  is `net.ssh.ping`, the 4 `net.catalyst.*` methods, and, as of Phase 86.5, `net.cli.command` and
  `net.cli.config` (generic, built on `pkg/netcli.FromPrompt` against a device's own declared
  `cli_prompt` property) plus 4 Cisco-specific ones on `pkg/netcli.IOS`'s real paging,
  configuration-mode and error conventions, every one verified directly against a real Cisco IOS XE
  device: `net.ios.config`, and the follow-on `net.ios.facts` (parses `show version`/`show
  inventory`/`show ip interface brief`, and is the ONLY fact gatherer a Cisco device has, since
  `facts.gather` requires `FactGathererCapable` which no Cisco device type declares),
  `net.ios.ping` (pings FROM the device, a different question from `net.ssh.ping`'s "can this
  platform reach the device"), and `net.ios.save` (`write memory`). The twelfth is
  `net.netconf.config`, Phase 74a's, and it is the only one in the group that is not a terminal
  session at all: it speaks RFC 6241 NETCONF over an SSH subsystem channel (`pkg/netconf`,
  implementing the shared `pkg/datastore` port), so a rejected element comes back as a structured
  error carrying the device's own error-tag and the XPath it objected to, rather than as a line of
  vendor text. Its parameter names are `ansible.netcommon.netconf_config`'s.
  **The short and decision-relevant list is what is NOT implemented, all three of them:**
  `file.template` - deliberate and not a gap to close casually, since the render engine lives in
  `internal/render` and a Collection may not import `internal/`; and
  `net.junos.config` and `net.eos.config`, which need a `JunosCapable`/`AristaEOSCapable` device
  type that does not exist yet (Phase 74d).
  Each returns an explicit "declared but not implemented" error rather than a silent
  no-op, though that error is a backstop rather than the mechanism: the dispatcher refuses any
  method whose `Status` is not `StatusImplemented` before its body is ever called
  (`internal/engine/collection_action.go`).
  `exec.command` was the first write-capable method and the first built on `pkg/remoteexec`,
  the shared SSH execution primitive a Collection may import (a Collection may import only
  `pkg/`, so `internal/transport/ssh` is unreachable from one and is now a thin adapter over
  the same primitive). Every implemented method declares `collection.Reversibility` (a bool plus
  a required reason when false, enforced at registration), and a run that changes something emits
  the concrete reversing instruction via `sdk.RecordInverse` as an `inverse` stat holding an FQCN
  and resolved params. The method declares on `Reversibility.Inverses` (`sdk.InverseSpec`) which
  of its undo's params are identifiers the journal may keep (`Record`); only those reach the
  journal's `inverse_params`, and everything else is withheld. **Rollback is real (Phase 40) on
  both tiers**, planned by one pure planner (`internal/rollback`): `pleiades rollback <run-id>`
  (plus `pleiades journal list|show`, a strict journal reader, and `.pleiades/run.lock`) and
  `POST /jobs/{id}/rollback`, whose job dispatches on its own subject
  (`topology.RollbackSubject`, durable `runner-rollback`) so a Runner that predates it never
  runs the undone runbook in its place. It refuses as a whole before contact unless each gap is
  accepted by name (`--leave`, `--allow-partial`, `--allow-unknown`, `--despite-run`), resumes a
  rollback that stopped partway, and holds each recorded undo to the method's declaration (and,
  on the Walk tier, to the runbook the job ran, on the Controller and again on the Runner). A
  task's `rollback:` list (not hashed into `DAG.Version`, so it can be written after a failure)
  wins over a recorded undo. External Collections' recorded undos are never replayed.
- **The scheduler is real (Phase 23), and since Phase 21's C1 seam it schedules more than
  templates.** A schedule is an RFC 5545 recurrence attached to a `launchable.Target`: one row
  in the `launchables` table standing for a job template or for a project whose run is a sync,
  with `internal/launchable` holding the open registry of those TYPES. That is a different axis
  from `launch.Kind`, which says which ENGINE runs a definition, and conflating the two is what
  left a project unschedulable for three phases (LESSONS 208,
  `.SPECIFICATION/AWX_PARITY_ROADMAP.md` section 1.1). Both sorts fire through one
  `Scanner.fire` and one `launchable.Router` (a map lookup, never a type switch; asserted by
  `internal/archtest`), each type's launcher being composed in `cmd/controller`: the Dispatcher
  for a template, `internal/project`'s Runner for a sync. Writing a schedule requires the scope
  the target's own type declares (`runbook:execute`, `project:write`), not merely
  `schedule:write`, which was a real hole (FAILURE_PATTERNS 268), and every refusal a type can
  make is made at the write through `Preflight` rather than discovered unattended. The API field
  is AWX's `unified_job_template`, with `template` kept as a deprecated write alias.
  `internal/schedule/rrule` is a
  hand-rolled, deliberately bounded engine (no new dependency, following `pkg/filters/cron.go`),
  and its AWX parity is *earned rather than claimed*: `tools/genrrulefixtures` generates golden
  occurrence vectors from python-dateutil, the library AWX itself schedules on, and Python is
  never a build or CI dependency. `internal/schedule.Scanner` gates on the
  `pleiades-scheduler-leader` lease `cmd/controller` had elected and ignored since Phase 4, taking
  `isLeader func() bool` exactly as `dispatch.Reaper` does, so the package imports neither
  `internal/election` nor `internal/lock` (asserted by `internal/archtest`). Firing goes through
  each type's own launcher, which for a template is the same `LaunchTemplate` a manual launch
  takes. Five things are worth knowing before describing it: the recurrence grammar is a bounded
  subset refused at *save* time, not run time; missed runs are **coalesced** to one, with a
  durable `skipped` row for each that did not happen; a schedule fires once because of a unique
  index on `(schedule, occurrence_at)` claimed before launching, **not** because of leader
  election, whose two-second fencing-token-less lease cannot promise it; a template bound to a
  prompted credential, or a saved configuration answering a survey password, is refused outright,
  because neither value is stored and replaying one unattended forever is worse than doing it
  once; and a target already running (a project mid-sync) is a **skip** carrying
  `already_running`, never a failure, since a failure would be retried for as long as the first
  run lasts.
- **Check mode is real for 91 of the 101 implemented methods (Phase 46).** `pleiades run --mode
  check` runs each task's declared `Descriptor.Check` (`collection.ModeCheck`; the
  manifest's `SupportsCheck` must agree, enforced by `Register`) and names every other
  task as unchecked, ending non-zero. The engine refuses a check result carrying an
  `inverse` stat, never journals a check, and admits simulate-locked devices only in check
  mode (`engine.LifecycleAdmitsIn`, shared with `validate.LifecycleRule`). Most methods
  predict through one body shared with `Invoke` that branches on the mode after the same
  reads and refusals; read-only ones set `Check` to their own `Invoke`. A method that
  checks only some calls answers the rest with `collection.CannotCheck`, and when the
  params alone decide (`exec.command`/`exec.shell` guards, `http.request`'s method) also
  sets `Descriptor.CheckCall`, which validation calls to refuse `check_mode` on such a
  call. The ten methods with no check (`exec.winrm.shell`, `container.docker.exec`,
  `net.cli.*`, `net.ios.config`, `net.netconf.config`, the four waits) each carry
  `Manifest.NoCheckReason`, which `internal/archtest` requires of every built-in without
  check support. The generated module catalog's `supportsCheck` is the authority. The
  Walk tier runs checks too: the runbook launch kind's `mode` field (`launch.ModeField`, a
  `TypeChoice`) is resolved by its own narrowing rule (`launch.resolveMode`: a check at any
  layer wins without the template opening the field, execute beneath a check is refused),
  `POST /templates/{id}/check` forces it, and a check is published on its own subject
  (`topology.CheckSubject`, durable `runner-check`) that only a check-aware Runner reads,
  through `routing.CheckOnly`, so an old Runner never runs one for real. The job shows its
  mode (`dispatch.Job.Mode`) and, once finished, whether the check was complete
  (`dispatch.Job.CheckCoverage`, from each device's unchecked count, which the Runner
  reports through `wire.Outcome`, the second return of every execution adapter). The check
  route needs `runbook:check` (`auth.ScopeRunbookCheck`, implied by `runbook:execute` in
  `Identity.HasScope`); a check launched without `runbook:execute` never runs an external
  program's Check (`dispatch.Job.ExternalChecks` to `wire.DispatchPayload.ExternalChecks`
  to `engine.WithExternalChecks`, off by default). Phase 35's conversion classes (observe, asserted,
  imperative) are cross-checked against each method's check support, disagreements listed with a
  reason in `internal/forge/playbook`'s `reviewedCheckClass`. A runbook, block or task
  can ask for a check with Ansible's `check_mode: true` (`engine.CheckModeFlag`, copied
  down by the builder, resolved per task by the one shared `engine.TaskMode`); `false`
  is refused at parse, and validation refuses it on an uncheckable action or when a real
  task's condition reads a checked task's result. An unknown top-level runbook key is
  refused (`engine.RunbookKeys`, FAILURE_PATTERNS 252). A check from an external program is
  never run against a simulate-locked device (`collection.Descriptor.Provider`, set only by
  the loader; FAILURE_PATTERNS 253): it is reported unchecked there. The engine stamps
  `predicted: true` (`sdk.StatPredicted`, and `sdk.DiffPredicted` in the diff) on every
  check result and refuses it on a real one; an incomplete check exits 3 and
  `--allow-unchecked <method>` accepts named gaps.
- **SSH connections persist between a device's tasks by default.** `pkg/remoteexec.Pool`
  lends connections through `sdk.Connect` whenever the method's `RunbookContext` implements
  `sdk.ConnectionPooler`, so no Collection changes to take part. Crawl: `pleiades run
  --persist-connections` (default on). Walk: the runbook launch field `persist_connections`
  (`on`/`off`) and `wire.DispatchPayload.PersistConnections` (absent means off), honored by one
  session child per dispatch (`--internal-collection-session`,
  `internal/adapters/native/ipc_session.go`; `pkg/external.ServeChild` is unchanged). The device
  ladder is the `persist_connections` property through `engine.PersistFor`, and off at either
  ladder wins. Reuse is refused on a key mismatch (address, credential identity, host key mode,
  known_hosts path), any change to the known_hosts content, a failed keepalive, or a tainted
  connection (a shell, subsystem, streamed process or cut-off command). A method whose manifest
  sets `EndsLoginSession` (the six identity methods, `pleiades.builtin.connection.reset`) closes
  it after a real run. `net.ssh.ping` and the transport actions never pool.
- **Generic device types are real (Phase 111).** `generic_ssh`, `generic_netconf`, `generic_http` and
  `generic_grpc` (`internal/inventory/devices/generic`) take their capabilities beyond a small baseline
  from the device: `internal/inventory/onboard` probes it over its protocol (one prober per type on a
  `pkg/registry`) and records the result in the reserved `discovered` property
  (`pkg/inventory.DiscoveredProperty`), which only `record.Base.RecordDiscovery` writes and every other
  write path refuses (`add-host --set`, `hosts.yaml`, sync create and update, `AddInfo`/`RemoveInfo`).
  A generic device starts `discovered` (`record.InitialState`), which runs nothing. CLI `pleiades onboard`;
  Controller `POST /inventory/devices/{name}/onboard`, scope `inventory:onboard`, which `inventory:write`
  does not imply. The SSH probe grants the Linux capabilities only for a Linux kernel, and a FreeBSD
  kernel POSIX file access alone (proven by `TestCLI_FreeBSDFileChecksMatchTheirRealRuns`).
  `http.request` with a path `url` calls a device's own API with the device's credential
  (`pkg/httpapi`). A device's TLS is its own (`pkg/devicetls`): a pinned `tls_ca_pem`, `tls_server_name`,
  mutual TLS from the stored certificate, and for an old device TLS 1.0/1.1, legacy cipher suites or a
  credential over plain HTTP, each behind its own explicit per-device flag and warned about on every
  onboard and run (`sdk.StatWarnings`); `internal/archtest` keeps every weak version or suite name inside
  `pkg/devicetls`. **Walk tier:** a Runner rebuilds each dispatched device as its real type
  (`record.LookupType`) from `wire.DispatchPayload.DeviceType` and `DeviceProperties`, which carry only
  the keys that type declares its accessors read (`record.RegisterDispatchProperties`, held equal to the
  code by `internal/archtest` and refused if a key names a secret) plus its discovery; an older
  Controller's payload falls back to `pkg/external.Device`.
- **Task params render, targets can come from data, and every method says where it runs (Phase
  117a).** A string in `params` holding `{{ }}` renders when its node dispatches
  (`internal/engine/render_params.go`, through the one `internal/render` engine each composition root
  hands in with `engine.WithRenderer`), reading `vars` (run/launch variables, never an injected secret),
  `nodes` and `result.<register>` (a register one device wrote; conditions have the same `result` root);
  a one-expression value keeps its type. A
  rendered `params.target` needs a literal `within:` and resolves to device names inside it only
  (`resolveBounded`). `collection.ExecutionContext` has `Site` (target, controller, hybrid) and `Device`
  (required, optional with `Descriptor.DeviceCall`, none); a call needing no device skips `hosts:` and
  runs once with no credential (`engine.TaskTarget`). `collection.Param.Format` (`command`, `url`) holds a
  rendered value to `| quote`/`| cli_token` or a fixed host, checked by `validate`'s `TemplateRule` and
  again at render. `pleiades run --extra-vars`/`-e`. **Walk caveat:** a Runner still runs every task
  against its one dispatched device (Phase 117b builds segmented dispatch), and the Runner's per-task child
  refuses a call with no device.
- **Plan-time checking covers transports; capabilities only for a two-entry table.** Since Phase 75
  `pleiades validate` refuses a task whose device reaches none of its method's `SupportedTransports`
  (`internal/validate`'s `TransportRule` and the engine's run-time gate share
  `collection.CheckTransports`; the vocabulary and the capability reaching each transport are
  `pkg/capability`'s `ReachedBy`, and `collection.Register` refuses an unknown name). The generic
  `pkg.*`/`svc.*` dispatchers declare the union of their concrete methods' transports and re-check
  the concrete one. Required capabilities are still compared at plan time only for `ssh_exec` and
  `ios_backup` (`internal/engine/action_capability.go`), so a capability mismatch on a reachable
  device surfaces at run time, refused by the engine before the method runs. `WindowsShellCapable`
  is a child of `CommandExecCapable`; the transport check is what keeps SSH-only `exec.command` off
  a Windows server.
- **One circuit breaker, `pkg/breaker` (Phase 75).** `pkg/remoteexec` (SSH, telnet, serial-over-TCP)
  and `internal/transport/winrm` share it; every platform instance is an unexported field, and
  `internal/archtest`'s `TestNoPlatformCircuitIsReachable` refuses one anywhere a Collection could
  reach. Only a network failure counts: a refused credential (SSH's "unable to authenticate", a
  WinRM 401 or TLS alert) is neither retried nor counted. The half-open probe is leased, so a lost
  one is reissued after a cooldown (FAILURE_PATTERNS 146, 398). WinRM Collections that call
  `pkg/winrmexec` directly (`exec.winrm.shell`, `pkg/winrmsvc`, the waits) have no breaker yet.
- **The web UI (`web/`) is a mockup.** Five of six routes render hardcoded content; the
  sixth (SSE log viewer) has three defects that stop it reaching a real Controller. This is a
  different thing from `internal/ui`, the server-rendered view registry the Controller actually
  serves, where most views are real; do not conflate the two when describing UI status.

When touching any of the above, do not describe it as more finished than it is - see
`docs/01-start-here.md#implementation-status` for the generated, current matrix.

## Common commands

```bash
make ci              # the whole gate, run LOCALLY: build vet fmt test-race test-repeat test-integration gosec govulncheck coverage docs-lint docs-gen-check helm-lint templ-gen-check
make ci-remote       # what GitHub Actions runs: `ci` minus test-race, test-repeat, test-integration and coverage - no tests at all
make build            # go build ./...
make test             # go test ./...
make test-race        # go test -race ./...   (required before calling anything "verified" per RULE 0)
make fmt               # gofmt -l check, hard failure on any unformatted file (excludes .claude/)
make fmt-fix           # gofmt -w, actually fixes it
make vet
make gosec             # go run ./tools/gosec-check - wraps gosec with gosec-waivers.json's per-finding waivers
make govulncheck
make coverage           # go run ./tools/coverage-check - ratchet against coverage-floor.json, not a flat 90% gate
make arch               # go test ./internal/archtest/...  - Section 25 layering rules as a real test
make docs-lint          # go run ./tools/docs-lint - fails if a gitignored internal doc is cited anywhere a user could see it
make docs-gen-check     # regenerates docs/reference and internal/api/wellknown, fails on any diff or untracked file
make tools              # installs gosec/govulncheck at the Makefile's pinned versions; no-op when already correct
make hooks              # once per clone: point core.hooksPath at .githooks, enabling all three hooks below
make lsp                # verify gopls answers over MCP for this module (the agent's LSP tooling)
make commitgate         # go run ./tools/commitgate: the commit-time gate, against whatever is staged right now
make push-gate           # everything `ci` runs, with test-race/test-integration/coverage swapped for tolerant equivalents; warns instead of failing on flaky-packages.json packages
```

**The test suite runs locally and only locally. GitHub Actions runs no tests.**
`.github/workflows/ci.yml` checks out, sets up Go from `go.mod`, installs the pinned
tools and Helm, and runs `make ci-remote` - `ci` minus `test-race`, `test-repeat`,
`test-integration` and `coverage`, i.e. compilation on three operating systems, `vet`
under both tag sets, `gofmt`, `go mod tidy -diff`, `gosec`, `govulncheck`, the
docs/`templ` regeneration checks and the Helm chart lint. Nothing there proves a single
test passes. The reason is that the full job never once went green on a hosted runner:
around twenty packages provision real ephemeral containers through `testcontainers-go`,
`make ci` runs the suite three times over plus a fourth pass inside
`tools/coverage-check`, and several of those packages are deliberately
timing-sensitive (`internal/event`'s Phase 96a gate severs a real broker for 150
seconds). A permanently red gate gates nothing. So `make ci` is now a gate a human runs,
and `.githooks/pre-push` (`make hooks`, once per clone) is what stops an ungated commit
being pushed: it verifies the receipt that run left behind, rather than running the gate
itself. See below for why that distinction is load bearing.

`make hooks` now enables three hooks, not one. `.githooks/pre-commit` and `.githooks/commit-msg`
run `tools/commitgate`, which takes well under a second because it builds nothing, runs no test,
and reads the index rather than the working tree. It refuses what `.AGENTS/AGENTS.md` states
absolutely and a machine can settle: an em dash in any added line, a staged Go file gofmt would
rewrite, a Go file the commit adds with no docstring, an ent schema edit with no regenerated code
beside it or a new entity missing either dialect's migration, a subject that is not a conventional
commit, and a trailer crediting a model as an author. Rules that file states softly (the 300-line
cap) and judgements a static check cannot settle (an error message opening with a capital) print
as warnings and do not block. It deliberately does not build, vet, test or scan, and it cannot tell
whether a doc comment is true or a test is representative under RULE 0, so a green run is not
evidence of having followed that file. `git commit --no-verify` skips it.

There is still no CI-only step and no CI-only tool version - `gosec` and `govulncheck`
are pinned once in the `Makefile` (`GOSEC_VERSION`, `GOVULNCHECK_VERSION`) and installed
by `make tools` on both sides, so a local run and the CI job run byte-identical scanners.
Never `go install` either tool by hand at `@latest`: a newer scanner than the pin reports
findings CI will not, and an older one misses findings CI will. The one thing a local run
still cannot predict is `govulncheck`'s live advisory database.

**`.githooks/pre-push` does not run the gate. It checks a receipt.** The gate is a
process you run on its own schedule, and the push is a separate action:

```bash
make push-gate     # or make ci, which is stricter; ~20 minutes
git push           # the hook verifies in about a second
```

The reason is mechanical rather than stylistic. Git opens its connection to the remote
*before* running `pre-push`, because it needs the remote's ref advertisement to build the
hook's stdin. A hook that then runs a twenty minute suite hands git back a connection the
remote dropped long ago, and the push dies writing to it: SIGPIPE, exit 141, no output at
all, while the gate prints "all checks passed". That was reproduced five times here before
the cause was found, and nothing about it is discoverable from the symptom.

`make push-gate` and `make ci` each end by writing `.git/pleiades-gate.json` through
`tools/gatereceipt`, naming the commit they verified, which gate ran, and when. The hook
pipes git's own pre-push lines straight into `gatereceipt verify --push-stdin`, which
decides **each ref's tip**, and every decision lives in that command so it is unit tested
against real repositories rather than in shell that splits its own input.

What it lets through, and what each allowance is worth knowing for:

- the receipt's own commit, which is the ordinary push;
- an object that **peels** to it, because git hands a hook an annotated tag's *tag object*,
  not its commit, so a tag cut at the commit that just passed used to be refused at the one
  moment a developer is most certain they did everything right;
- a ref **created or fast-forwarded** onto a commit already inside the gated commit's
  history, since those commits travel under the gated tip anyway. Moving an existing ref
  *backwards* is refused, because the remote would then serve a tree no gate examined;
- a push that only deletes refs, which sends no code.

Everything else is refused, each with its own message and the command that fixes it: no
receipt at all, an unreadable one, one that does not say which gate ran or names a gate
this repository does not have, one dated ahead of the clock (an age is a subtraction, so a
future date used to mean a receipt that never expired), one past `MaxReceiptAge` (a day,
which exists for `govulncheck` alone, since that is the one check whose answer moves
without the tree moving), a ref outside the gated history, a rewind, and an object that is
not a commit at all. `git push --no-verify` still skips the lot.

Two things this does **not** prove, both worth saying plainly. Only the tip is gated: a
push sends every commit its tip can reach, and an intermediate commit that does not compile
travels under a green receipt, exactly as it did under the hook that ran the suite inline.
And hooks are opt-in per clone (`make hooks`), so a fresh clone pushes with nothing checking
anything; the gate now warns at the end of a run when this clone is in that state.

This is **stricter** than running the suite in the hook, which is worth stating because it
looks like a loosening. That arrangement proved something about the *working tree* and then
pushed *commits*; with uncommitted edits those are different code, and nothing noticed. A
receipt is only issued from a clean tree, so the thing verified and the thing pushed are the
same object by construction. It is also issued only when HEAD did not move while the gate
ran, and the Makefile reads HEAD *while it parses itself* to make that checkable: the
receipt used to name HEAD as it was when the gate's last line ran, twenty minutes after its
first, so a commit made in that window collected a receipt for a tree nothing had examined
and nothing could notice, since committing leaves the tree clean.

`push-gate` itself is every check `ci` runs, with `test-race`/`test-integration` swapped for `tools/testgate`'s own
invocations and `coverage` swapped for `go run ./tools/coverage-check -tolerant` (that
tool runs its own separate full `go test ./... -cover` internally, so it needed the
identical tolerance applied a second time, not just once at the test-race/
test-integration layer). Both re-run every failure **alone** and decide on that: a test
that passes by itself lost a race and is printed as a warning, a test that fails again
fails the push, and more failures than `flakegate.MaxIsolationRetries` distinct tests fails
without re-running anything, because that many at once is a change that broke something
rather than a busy machine. `flaky-packages.json` (each entry with a written reason,
mirroring `gosec-waivers.json`'s per-finding convention) no longer decides anything: a
listed package gets **no protection from a real defect**, and an unlisted one is tolerated
anyway when the re-run says contention. Its entries supply the reason printed beside a
tolerated failure. Both tools share `tools/internal/flakegate` so they cannot disagree.
This exists because packages that provision real ephemeral Docker containers or real
multi-replica timing races (`tests/e2e`, `internal/lock`, `internal/event`,
`internal/election`, `cmd/controller`, and others `flaky-packages.json` names) reliably
flake under this kind of sandboxed environment's full parallel `-race` load -
`FAILURE_PATTERNS.md` #61 - and pass individually every time. `make ci` itself is
completely unaffected by any of this and stays exactly as strict. A build failure still
fails `push-gate` exactly like `ci`, and so does any test that fails a second time on its
own.

Read that tolerance more carefully now than you would have before: there is no stricter
run waiting downstream of a push any more. `push-gate` used to be a preview of a gate
GitHub would apply again in full; it is now the last automatic check anything gets. A
package listed in `flaky-packages.json` without a real, written, observed reason is a
package nothing checks anywhere, so run `make ci` itself - not just `push-gate` - before
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
# ensure $(go env GOPATH)/bin is on PATH persistently, not just in this shell:
# a bare `export` lasts one shell, and an agent's shell is never that shell

make lsp     # verify the language server works here before relying on it
make hooks   # once per clone: enable the pre-commit, commit-msg and pre-push hooks
```

### Use the language server, not grep, for Go

`.mcp.json` wires `gopls mcp`, the language server's headless MCP mode, in as a server named
`gopls`, and `.claude/settings.json` pre-approves it. A session started in this repository
therefore has eight typed Go tools: `go_search`, `go_symbol_references`, `go_package_api`,
`go_file_context`, `go_diagnostics`, `go_workspace`, `go_vulncheck` and `go_rename_symbol`.
Use them for any claim about Go call graphs, symbol usage or interface compliance.
AGENTS.md treats a grep-derived claim about Go semantics as a guess rather than evidence,
and the typed query is also much cheaper: `go_package_api` returns a package's exported
surface in a screen or two, where reading that package's files to learn the same thing costs
thousands of lines. Call `go_diagnostics` after every Go edit; it reports in about a second
the compile error a `go test` run needs a minute to reach, though it proves only that the
code compiles and never that the behavior is real (RULE 0 is unchanged).

`make lsp` is the check that the server really answers for this module, since a version
string proves nothing: it does a real MCP handshake, calls `go_workspace`, and looks for this
module's path in the reply. If the `gopls` tools are missing from a session then the server
did not connect, which is a broken setup and not a suspended rule: run `make lsp` to see
whether gopls or the wiring is at fault, use `gopls references` and `gopls definition` on the
command line for that session, and say so in the writeup. Grep stays correct for prose, YAML
and markdown.

### ent code generation

`internal/ent` is generated from `internal/ent/schema`. Never hand-edit generated files:

```bash
go generate ./internal/ent
```

A schema edit without regenerating is a silent no-op that still compiles - the worst
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
were not** - a commit dated before 2026-08-22 saying "Walk tier" means what this table now
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
  real design decision - `go test ./internal/archtest/...` fails immediately if it drifts.
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
  a Go interface the device type implements - not what the device *is*.
- **Inventory item**: a managed device - name, type, properties, lifecycle state, version,
  history. Concrete device types live under `internal/inventory/devices/<vendor>/`.
- **Transport**: how a task's command reaches a device, auto-selected from device
  capabilities (`internal/transport`, with the real implementation in
  `internal/transport/ssh`).
- **Sync plugin**: implements `internal/inventory/syncplugin.Plugin`'s four-stage contract
  (`Connect` → `Discover` → `Classify` → `Sync`), verified by a shared conformance suite
  (`internal/inventory/plugins/conformance_test.go`) that every real plugin is driven
  through identically. Lives under `internal/inventory/plugins/<name>/`.
- **`when` / `when_or` / `when_cel`**: three ways to gate a task - Ansible-compatible ANDed
  list, ORed list, or a raw CEL escape hatch, compiled before execution.

### Extending the catalog (`pleiades forge`)

`forge new-collection` / `new-device` / `new-plugin` scaffold a new Collection method,
device type, or sync plugin (two gofmt-clean files each: implementation + test). None of
the three self-registers into the running binary - that requires a deliberate, one-line
blank import into the relevant `builtins.go` (`internal/catalog/builtins.go`,
`internal/inventory/builtins.go`, `internal/inventory/plugins/builtins.go`). This is
intentional: a generated-but-unwired file compiles and its tests pass, but stays invisible
to `pleiades doc --list`, `validate`, and the dispatcher until wired in.

Collection methods have one out-of-tree mechanism, **external Collections** (Phases 42
and 45, built 2026-09-18): a separate program built with the public `pkg/external` SDK,
loaded by `internal/loader` from the directory `PLEIADES_COLLECTIONS_DIR` names, in
`cmd/pleiades` (run, validate, doc) and `cmd/runner` only (`internal/archtest` enforces
both that and that `internal/engine` never reaches the loader). The program runs as a
child process BESIDE The Pleiades, never copied onto a device (the user's decision), speaking
the same `pkg/wire` ChildRequest/ChildResponse the Runner's own per-task child speaks,
through the one shared `external.ServeChild`. Its methods register through the ordinary
`collection.Register`, so validate, doc and dispatch treat them as built-in, and it gets
credentials exactly as a built-in method does (`InjectSecrets`: the template's bound
machine credential resolved at fan-out, or the device's own); the loader adds no
credential path, by the user's explicit rule. The trust
model is the directory's ownership and permissions, an approval list of exact builds
(`.pleiades-approvals.json`, written by `pleiades collection approve|revoke|list`, re-read
before every call), and a SHA-256 re-checked before every run, with the run executing the
very file that was checked (`/proc/self/fd`, never the path); there is NO signature
verification and NO registry (Phases 43 and 44 are unbuilt). Every namespace a built-in
method uses, plus `pleiades` and `ansible`, is reserved (`collection.BuiltinNamespaces`),
and each external method's `NodeResult` carries its `Provider` (program and digest). The
Runner loads external Collections before telemetry or any broker connection, so a bad
directory stops it at once, and it routes a method in process exactly when its descriptor
has a `Provider`. A method's engine version constraint is enforced only by a release
build: every binary reports one version from `internal/buildinfo` (stamped through
`-ldflags -X`, honored only when it reads as a release, else `0.0.0-dev+<commit>`; `pleiades
version` and `runner version` print it), the Runner and the CLI both hand it to the loader,
and a development build loads a constrained method with one warning per program. Text from a program (descriptions, error messages, stat values) is
third-party input: `internal/termsafe` refuses or escapes whatever a terminal acts on,
and an archtest keeps Go source free of invisible control characters (FAILURE_PATTERNS
256, 257).
Every run is confined with Linux Landlock (`internal/loader/confine*.go`) to its own
directory, system files, known_hosts, a private TMPDIR and `PLEIADES_COLLECTIONS_READ_PATHS`
grants (never one reaching `.pleiades/` or containing the home directory), and the parent
marks itself non-dumpable first (FAILURE_PATTERNS 251, 254); loading is refused wherever
Landlock is absent, which is every platform but Linux. `forge new-external` scaffolds one. Device types and
sync plugins remain `internal/`-only (see `docs/11-extending-pleiades.md`).

### Control plane API (`cmd/controller`, `internal/api`)

Front Controller pattern: every route passes through tracing, metrics, structured
logging, rate limiting, authentication, then scope authorization, before its handler
runs. The route table (`internal/apispec`) is the single source both the real router and
the generated OpenAPI doc build from - but nothing enforces that `cmd/controller`'s
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
generated reference pages - there is no waiver mechanism for this check, unlike
`gosec-waivers.json`.

## Testing policy highlights (full detail in `.AGENTS/AGENTS.md`)

- **RULE 0 (Representative-or-nothing)**: a test only counts as verification if it runs
  the same config path the platform actually runs. A test that mocks the transport layer
  while testing transport behavior proves nothing.
- `internal/lock`, `internal/event`, and `internal/transport/ssh` run real conformance
  tests against ephemeral Docker containers (NATS, sshd) - Docker must be available.
- Coverage is a ratchet (`coverage-floor.json`), not a flat threshold: no package may drop
  below its recorded floor; a package with no floor yet is reported, not failed.
- Every `gosec` finding accepted into `gosec-waivers.json` needs an individually written
  reason - no blanket rule-ID or directory suppression.
