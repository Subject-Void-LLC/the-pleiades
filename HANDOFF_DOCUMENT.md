# Handoff Document

Rewrite the "Current Status" section when stopping mid-task or handing off, per `.AGENTS/AGENTS.md`.

## Current Status (this session)

**Branch `feature/Capability-Reachability-And-Plugin-Wiring`, cut at the start of this session off
`feature/aws-collection`'s HEAD (`505c203`, "cleanup chore for swapped crawl/walk phases"). HEAD is
now `afb6569`, a commit **the user made themselves, mid-session**, fixing CI on all three matrix
legs; it is unrelated to the work below except that it swept this session's already-written
`FAILURE_PATTERNS` entries into itself alongside the user's own. `origin/main` was then merged in,
which collided on numbering in both living documents (see the note directly below); the merge
resolution is part of this branch. A drafted
commit message is in this session's final message.**

This session fixed one class of defect and the guard gap that let it through: **things that are
built, tested, documented, and reached by nothing that ships.** Three live instances, three new
`internal/archtest` sweeps, and the lower-priority audit items that were re-verified before being
acted on.

### The three live defects

**1. `pleiades inventory sync --plugin aws` was broken unconditionally, from the day the plugin
landed.** `aws.WithRegion` and `aws.WithCredentialStore` had test callers and nothing else
(`gopls references`), while the registry's own `Descriptor.New` took no arguments, so the CLI built
the plugin with `region == ""` and `creds == nil` and `Connect` refused every time. Three suites
were green throughout, because each constructed the plugin its own way.
`cmd/pleiades/inventory.go`'s `buildSyncPlugin` type switch wired exactly one plugin and its own
comment had predicted the failure ("when a third plugin needs it, this becomes an optional
interface..."). Fixed by making the dependency a constructor parameter rather than an option:
`syncplugin.Constructor` is now `func(Deps) Plugin`, `Deps` carries the credential store, a
per-deployment value is declared data (`Descriptor.Settings []SettingSpec`) supplied as
`--set key=value`, and `syncplugin.Open` is the one construction path both `cmd/pleiades` and the
conformance suite go through. `buildSyncPlugin` is gone. `static_yaml` was checked and had no such
gap; it is now the control case proving `Deps` and `Settings` are genuinely optional.

**2. Phase 73's serial/console/telnet transports were unreachable.** No production device type
implemented `SerialCapable`, `RawPassthroughCapable`, `RFC2217Capable` or `TelnetCapable`; the only
implementers were stubs in `internal/engine/action_ssh_test.go`. Fixed with a real
`console_device` type (`internal/inventory/devices/console`), scaffolded through the actual
`pleiades forge new-device` CLI and hand-completed, hydrating from `pkg/inventory.Properties` the
way `linux.Server.SSHPort` does. It declares each capability **per record** rather than all four
unconditionally, because they are alternative ways to reach one device rather than four facts about
it; the package doc argues that at length, and the deviation from `container.Host`'s unconditional
baseline is deliberate and disclosed (including in the generated `docs/reference/devices.md`, whose
marker is derived by hydrating each type rather than hardcoded).

**3. The guard gap, which mattered most.** `TestImplementedCollectionCapabilitiesAreSatisfiable`
walks `catalogdata.Collections` only, so transport fqcns were entirely outside its coverage; Phase
73 shipped that guard and this defect in the same commit. Three new sweeps in
`internal/archtest`, each with a permanent negative control rather than a one-off manual
un-wiring:

- `TestDispatchableTransportCapabilitiesAreSatisfiable` / `TestBoundTransportCapabilitiesAreSatisfiable`
  (`transport_reachability_test.go`) cover `engine.ActionCapability` and the real
  `NewDefaultTransportBindings` registry. Running them for the first time reproduced all three
  failures verbatim.
- `TestRegisteredCapabilitiesAreReachable` covers the class neither of the other two can see: a
  capability nothing requires **and** nothing satisfies. It found `FileTransferCapable`, which
  `docs/03-migrating-from-ansible.md` was telling users `archive.extract` requires (it requires
  `POSIXFileSystemCapable`).
- `TestEveryRegisteredPluginOpensFromTheSharedPath` and
  `TestCompositionRootsBuildPluginsThroughTheRegistry` (`plugin_reachability_test.go`) are the
  same shape one level up. The second is the one that would have caught #1 outright: it forbids
  any `cmd/` package from importing an individual plugin package, which forbids the type switch
  that made a per-plugin arrangement expressible at all.

### The forge, which the user asked about first

Yes, it needed updating, and the update is what stops #1 recurring one generation later.
`pluginscaffold.Config` gained `RequiresCredentials` and `Settings`; the template emits a
constructor taking `syncplugin.Deps`, a descriptor declaring both, and a `Connect` whose TODOs name
`p.creds` and `cfg.Setting("...")`. `pleiades forge new-plugin` gained `--requires-credentials` and
`--settings-json` (following `--doc-json`'s established `@file` convention), `tools/gencatalog`
threads both through the real CLI, and `catalogdata.Plugins` now declares AWS's region and
credential requirement so a regenerated skeleton comes out wired.

### Lower-priority audit items: re-verified first, then acted on selectively

A six-agent workflow re-derived each claim from source with `gopls` (the user authorized workflows
mid-session). Two claims came back materially corrected, and both corrections are recorded rather
than quietly absorbed:

- **NATS KV bucket: acted on, but the reported diagnosis was wrong.** There was one config literal
  reached through one constructor, not two independent declarations, so the "multi-declaration
  drift" framing would send a reader hunting for a second literal that does not exist. The real
  exposure is version skew across a rolling upgrade of two separately-built images. Fixed by moving
  the shape to `topology.LockBucketConfig()` and adding
  `TestOnlyTopologyDeclaresJetStreamShapes`, an AST rule that makes topology's own doc claim (and
  `layering_test.go`'s repetition of it, both false when written) true.
- **`credstore.ReconcileManaged`: NOT acted on, deliberately.** The equality guard the audit asked
  for is the wrong fix: it saves six no-op UPDATEs nothing observes and leaves the same TOCTOU
  window. The real (minor) issue is a lost-race warning on a concurrent cold start against an empty
  shared database, and the honest fix is in `EnsureManagedType`'s constraint-error branch, which
  changes error semantics and deserves its own change with its own tests. Left undone and reported.
- **`internal/pki` and `lock.CapacityCounter`: not dead, do not delete.** Both are deliberate
  Build-Once declarations already documented elsewhere; `internal/pki` gained the package-level
  disclosure it was missing.
- **`transport.DockerExecEndpoint`: disclosed rather than deleted.** Deleting shipped API surface
  was beyond what was asked; the doc comment now states why nothing constructs it and why the
  variant is kept. The delete option is reported.
- **`catalystcenter.WithClientOption`: deleted** (zero callers, and its "tests use it" claim was
  false). **`pkg/catalystcenter.WithHTTPClient`: comment fixed, not deleted** (it is on `pkg/`, the
  surface a Collection may import, and a TLS/proxy escape hatch is a normal thing to offer); its
  ordering hazard against `WithInsecureSkipVerify` is now documented on both.
- **`go mod tidy`: run.** 8 modules promoted indirect to direct, **6 removed** (aws-sdk-v2
  config/sso/ssooidc/sts/signin/imds), 3 stale go.sum pairs dropped. The removals are the part a
  reviewer needs to see: anything later wanting `config.LoadDefaultConfig` re-adds them.
  `make ci` and `make push-gate` gained a `tidy-check` target so this cannot drift again.

### One real flake found and fixed, in ten places, after the first sweep for it missed five

A full sweep failed once on `internal/transport/serialtcp`'s bastion test: `FAILURE_PATTERNS.md`
#177 verbatim, in a file #177's own fix did not touch. A grep on the expression shape found four
sites, which were fixed; the next full sweep then failed on a fifth, and re-searching on the
code's *intent* instead ("nothing is listening", `deadListener`, "listening now") found all ten in
one pass. Eight now use literal port 0 and cite #123/#177/#181 by number. Two cannot: `pkg/tftpxfer`'s test
asserts a timeout budget, which a closed UDP port or an invalid address would both short-circuit,
so it holds a real socket open and silent instead; and
`internal/catalog/pleiades/builtin/wait`'s `portClosedPort` needs a port that is closed now and
bindable later, which port 0 cannot express. That second one was first written up as a considered
exception and left alone, and the very next full sweep failed on it ("a closed port was reported as
open by the bash prober", against a prober that was working correctly), so it now closes the race
by checking rather than by construction: it confirms the released port really refuses a connection
before handing it back, and retries if not. Three direct-dial assertions were also strengthened
from "an error occurred" to "the error names the address", which is what their own doc comments
already claimed. Verified `-count=8 -race` across all seven affected packages and `-count=6 -race`
on the wait package.

### Documentation

`FAILURE_PATTERNS.md`/`_ARCHIVE.md` #180-#183 (this session) and #184-#186 (the user's own CI-fixing commit), `LESSONS_LEARNED.md`/`_ARCHIVE.md` #154-#157. Numbers as resolved against `main`; see the merge note above.
`docs/10-running-in-production.md` gained the `console_device` configuration section its serial
transport docs were describing without ever saying how to declare one.
`docs/03-migrating-from-ansible.md`'s stale "only catalyst_center" claim and its wrong
`archive.extract` capability row are fixed. `docs/reference/{devices,plugins,cli}.md` regenerate
clean, and `plugins.md` gained a **Needs** column derived from each descriptor.

### Read this first

**The audit's own claims needed re-verification, and two were materially wrong.** The user said so
up front and was right. Do not carry an audit finding into a fix without re-deriving it; the NATS
KV item in particular would have produced a commit message describing a defect that does not exist.

**A negative control belongs in the test file, not in a session transcript.** Every sweep added
here carries a permanent synthetic control, because Phase 73's own guards were controlled by
temporarily un-wiring a device type, which is real evidence that leaves no trace for the next
reader. Two of the new rules were additionally controlled live against the real tree (a probe file
in `internal/lock`, and removing an allowlist entry with a scratchpad backup rather than
`git checkout --`, per the prior session's own lesson).

**`console_device` deliberately breaks the "baseline capabilities" invariant every other device
type follows.** That is the one design decision here a reviewer should push back on if they
disagree. The reasoning is Architecture Principle 5 (type safety moves left): declaring all four
unconditionally would make `validate.CapabilityRule` answer "yes, serial_exec is fine" for a
Telnet-only device.

**No commit without the user's own live word.** Held throughout.

### Verification state

`go build ./...`, `go vet ./...`, `go vet -tags integration ./...`, `make fmt` and `make tidy-check`
all clean. `go test ./... -race`: **144 packages, zero failures**, run to completion three times
(the first two each surfaced one more instance of the port-reuse flake, which is how the count went
from four to ten). `make gosec` clean (9 findings, all pre-existing and individually waived; this
session introduced none). `make govulncheck` clean. `make docs-lint` clean. `make arch` clean.
`tools/gendocs` is idempotent, proven by diffing a second run's output byte for byte rather than by
assertion.

Coverage: `make coverage` is **clean, with no regressions**. `internal/inventory/devices/console`
100%, `internal/inventory/syncplugin` 90.5 to 95.6, `internal/topology` 95.4 to 95.8,
`internal/forge/pluginscaffold` 83.5 to 85.7, `internal/launch` 87.4 to 91.1; all five floors
recorded.

`internal/launch` needs a note, because its regression was not this branch's and was fixed anyway
at the user's request. It measured 87.0% against a floor of 87.4%, verified as pre-existing by
stashing this session's entire diff (untracked files included) and measuring 87.0% on a clean
`afb6569`, deterministically, with zero skipped tests. The floor dates to Phase 22b on 2026-08-13.
The gap was real rather than cosmetic: `KindCatalogFuncs.Verify` and `staticCatalog.List` were at
0.0%, meaning the catalog port's own happy path (its entire reason for existing, answering "yes,
this is launchable here" at template create) had never once run; `Fields.Int` and `Fields.List` were
covered only for the Go shape, while their doc comments name the JSON and HTML-form shapes and state
outright that a reader handling only the first "would work in tests and fail on the wire"; and
`ResolveKind`, the single place the default-kind rule lives after being consolidated from two, had
no test at all. Those are the gaps that were filled, not padding: the package is at 91.1%.

One thing found there and deliberately left: `internal/launch` cannot be run with `-count>1` in one
process. `unknownkind_test.go` registers process-global kinds named after the test with no cleanup,
so a second iteration collides on a duplicate registration. Confirmed pre-existing (four identical
failures on a clean tree). `make ci` runs `-count=1`, so no gate is affected.

The real-binary AWS sync gate (`tests/e2e/inventory_sync_cli_test.go`, integration-tagged) passes
against real LocalStack, as does the whole plugin conformance suite through the new shared
construction path.

### Next steps

### The merge with `main`, and what it changed beyond numbers

`origin/main` gained `FAILURE_PATTERNS` #178-#179 and `LESSONS_LEARNED` #152 from PR #24 (Phase
96-101) while this branch was open, and this branch had independently used the same numbers. Four
files conflicted. The trunk's numbers were kept and this branch's entries shifted: failure patterns
#178-#184 became **#180-#186**, lessons #152-#156 became **#153-#157**. Every cross-reference was
updated with them.

**One conflict was not a numbering conflict, and a naive resolution would have shipped it broken.**
This branch's JetStream entry (now #182) argued that several composition roots reshaping one
JetStream object at startup is "this codebase's deliberate pattern," citing `topology.EnsureStream`
doing exactly that for the main stream from three roots, and concluded the only real problem was
that the lock bucket's shape was written down in two places. `main`'s #178 reaches the opposite and
correct judgement about the same unchanged code: last-writer-wins over shared infrastructure with no
owner is a latent defect, and the Runner is the process whose opinion should carry the least weight
precisely because it is the one most likely to be an older build. Phase 96 is planning at the time
of this merge, so no code moved under either entry. #182 now records that it was half wrong and
points at #178; its `LockBucketConfig` move is described as a prerequisite for #178's single-owner
fix rather than a substitute for it.

`main`'s #179 is the same class this whole branch is about, seen from the other end: two fully-built
shared primitives with zero production callers, and the observation that "a port with no callers is
invisible to every automated gate this repository runs." #181 now cross-references it and states
honestly that these sweeps give three registries such a gate rather than closing the general case.
#179's own subjects (`event.NewIdempotentBus`, `pkg/policy`) remain unreached.

**`LESSONS_LEARNED.md` auto-merged into two `152.` entries in different regions, and git did not
flag it.** Only the archive conflicted. Anyone resolving these four files by accepting the flagged
hunks alone would have committed a duplicate-numbered index; it was found by grepping the merged
index for duplicate numbers rather than by the merge tool.

The user reviews and commits. Three things are deliberately left undone and are the natural
follow-ups: `internal/launch`'s inability to run under `-count>1` (above),
`credstore.EnsureManagedType`'s lost-race branch, and a decision on whether
`transport.DockerExecEndpoint` should be deleted rather than disclosed.

## Previous session (Phase 72: Transport Foundation)

**Branch `feature/Transport-Foundation-the-Circuit-Breaker`, off `main`. HEAD is `dc8e2df`. All of
Phase 72's actual code is committed, across two commits the user made themselves (no live go-ahead
was ever given to the assistant this session, so the assistant itself never ran `git commit`,
matching the standing rule): `835431b` (Workstream A, the `pkg/retry.Do` consolidation) and
`dc8e2df` (Workstreams B-F: the hop chain, hierarchical bastion config, engine wiring, the CI
matrix, and chaos/fuzz/adversarial/hardening/docs). The only thing left uncommitted at the moment
this section was written is this session's own documentation bookkeeping: two new
`FAILURE_PATTERNS.md`/`FAILURE_PATTERNS_ARCHIVE.md` entries (#168, #169, below) and this
handoff rotation itself. No commit message is needed for the code — it is already in history: see
`git show dc8e2df` for the full message.**

This session implemented **Phase 72: Transport Foundation (the Circuit Breaker, `retry.Do`, and the
Hop Chain)** end to end — the foundational phase of Part XV (the Transport Layer) that Phases 73-77
depend on.

### What landed

**Workstream A — `pkg/retry.Do[T]`/`Sleep`.** One generic, context-aware retry loop replacing three
independent hand-rolled ones (`internal/lock/queue.go`'s `acquireWithContention`,
`internal/lock/nats.go`'s timer half, `pkg/remoteexec/dial.go`'s `dialWithRetry`). Circuit-breaker
`Allow`/`RecordFailure` calls stayed at the SSH-dial call site, deliberately, since they are
dial-specific, not generic retry behavior. All three migrations were behavior-preserving — existing
tests passed unmodified, which is the actual proof. A new `internal/archtest` rule now asserts no
second retry loop or second breaker exists anywhere in the module.

**Workstream B — the N-hop SSH tunnel (`pkg/remoteexec`, `internal/transport`).**
`transport.Target` gained `Route []Hop` (`Host`, `Port`, `DeviceName`, a resolved
`credential.Credential`); an empty `Route` is exactly today's behavior, so every existing caller is
unedited. `pkg/remoteexec` gained the matching hop-chain shape and does the real tunneling: hop 1
dials with the existing `dialWithRetry`, each subsequent hop opens a `direct-tcpip` channel through
the previous hop's already-authenticated `*ssh.Client` via `DialContext`, then a fresh
`ssh.NewClientConn` runs a genuinely independent SSH handshake over that channel. Host key
verification and `InsecureSkipHostKeyVerify` are both per-hop, not global. `internal/transport/ssh`
stayed a thin translator, looping the same single-target conversion it already did.

**Workstream C — hierarchical bastion configuration (new storage + resolver).** `Group` and
`Inventory` both gained a `properties` field (`field.JSON`, matching `Device`'s existing shape),
with real migrations for both dialects
(`internal/ent/migrate/migrations/{sqlite/0017,postgres/0014}_add_group_inventory_properties.sql`).
`internal/inventory.Repository.GroupAncestry` (`internal/inventory/ent_group_ancestry.go`) is the
first real BFS walk of Group's parent/child DAG this codebase has ever needed — group nesting is a
graph, not a tree, so it needed a deterministic tiebreak (ascending name) for siblings at equal
distance. `engine.ResolveRoute` (`internal/engine/hop_resolve.go`) feeds the ancestry chain through
the existing `policy.Resolve`, most-specific-wins, capped at 16 hops
(`maxRouteHops`), rejected at resolve time before any per-hop lookup happens.

**Workstream D — engine wiring.** `NewTransportActionExecutor` gained a fourth constructor
parameter, an inventory-lookup dependency (`hopChainInventory`), needed because resolving hop *N*
requires looking up hop *N*'s own device (for `SSHHost()`/`SSHPort()`) and hop *N*'s own credential
independently of the primary target's. Every composition root that builds one was updated
(`cmd/pleiades/run.go` and others). The secret-masking union at
`internal/engine/action_ssh.go` was extended to cover every hop's `Password`/`PrivateKeyPEM`/
`Passphrase`, not just the target's — the same class of gap `FAILURE_PATTERNS.md` #22 already
recorded for `MarshalJSON`, recurring in a new shape. `internal/adapters/native`'s per-task
subprocess path deliberately skips route resolution rather than half-implementing it (no live
inventory connection there, a credential store scoped to one device) — recorded as a named gap, not
silently worked around.

**Workstream E — the CI matrix.** `.github/workflows/ci.yml` went from one `ubuntu-latest` job to
three: ubuntu (blocking, unchanged, still the only leg with Docker and therefore the only one
running the container-backed conformance tests) and macOS (blocking: build, vet, and every
non-Docker-dependent unit test, verified concretely — `pkg/remoteexec`'s own suite uses an
in-process fake SSH server, no Docker, so it genuinely runs for real on macOS) both required;
Windows is advisory/non-blocking (`remoteexectest` shells out to `/bin/sh`, which doesn't exist
there). A new Makefile target names the non-Docker package set explicitly rather than by a fragile
glob, so a future container-backed test doesn't silently join or leave a leg it shouldn't. A code
comment next to the matrix records the `_windows`/`_linux`/`_darwin` implicit-build-constraint trap
(`FAILURE_PATTERNS.md` #51) for whoever reaches for a platform-suffixed file in Phase 73/75.

**Workstream F — chaos, fuzz, stress, adversarial, hardening, docs.** A real Toxiproxy-fronted SSH
container severed at three moments (mid-dial: retry then breaker trip; after session establishment:
error, no retry, per the existing no-retry-after-send rule; mid-tunneled-command on the bastion hop:
error naming the failed hop, never a silent zero-value `Result`) — all under `-race` and `goleak`.
Route parsing is fuzzed against deeply nested/self-referential/absurdly long chains: never panics,
fails closed naming the offending hop, and fails at parse time rather than dial time. 300 concurrent
hop-chained sessions ran clean under `-race`, with a per-hop cost benchmark. An adversarial pair
proves per-hop key confusion (bastion's key known, tunneled endpoint's deliberately absent from
`known_hosts`) fails closed and is attributable to the right hop, then proves the same chain
succeeds once the real key is added. `PATTERNS.md`'s Circuit Breaker entry moved from `POTENTIALLY`
to **YES**, correctly naming `pkg/remoteexec` (not the stale spec's assumed
`internal/transport/resilience`) as where it actually lives — see the "stale spec" note below.
`docs/10-running-in-production.md` and a changelog fragment
(`changelog/ssh-bastion-hop-chains.added.md`) cover the bastion/hop-chain configuration, the
per-hop credential rule, and the per-hop host-key requirement.

### Two real bugs these gates found, both fixed and recorded

See `FAILURE_PATTERNS.md` #168 and #169 (full detail in `FAILURE_PATTERNS_ARCHIVE.md`):

1. **#168 — `Connect`'s per-hop loop only kept the last `*ssh.Client`, leaking every earlier hop's
   connection.** Found by a real `goleak`-based container test failure, not by inspection. `Conn`
   gained a `chain []*ssh.Client`; `Close()` walks it in reverse; `Connect` also gained a `defer`
   cleanup for the partial-failure case (a later hop fails after earlier ones succeeded).
2. **#169 — a `goleak` check in the new Toxiproxy chaos test depended on a `sync.Once`-populated
   package-level baseline an unrelated test happened to populate first**, so the check passed only
   when run as part of the full suite and failed when run in isolation. Fixed with a local
   `goleak.IgnoreCurrent()` snapshot taken after the test's own setup completed, independent of
   execution order.

### Read this first

**A stale spec section is a starting hypothesis, not an instruction to follow literally — verify
against the real code first.** Phase 72's own checklist in `.SPECIFICATION/IMPLEMENTATION.md` said
to extract the circuit breaker out of `internal/transport/ssh` into a new
`internal/transport/resilience` package. Reading the actual code first showed the breaker already
lived in `pkg/remoteexec` (built after the checklist was written) and was already unexported/
encapsulated, so the concern the checklist was guarding against was already handled. Relocating
working, already-encapsulated code to satisfy a stale doc's literal file path would have been pure
churn. Decision, made explicit in the plan before any code was written: leave it where it is. This
matches the Architecture Mismatch/Map Verification protocol in `.AGENTS/AGENTS.md` — it existed for
exactly this situation.

**`AllowTcpForwarding no` is the default in the SSH test image** (`lscr.io/linuxserver/openssh-server`),
which silently breaks any hop-chain test relying on `direct-tcpip` tunneling until it's overridden.
Found by starting a real probe container and reading its `sshd_config` directly, not by guessing.
Fixed by mounting a `.conf` snippet at `/config/sshd/sshd_config.d/allow-tcp-forwarding.conf` via
testcontainers' `Files` field — verify this with a real throwaway `ssh -L` test before trusting it,
the same way this session did, if a future phase (73/75/77) touches this container setup again.

**`command | tee file` reports `tee`'s exit code, not the piped command's.** Cost real time this
session: a `make ci 2>&1 | tee log` was assumed to have succeeded because the harness reported exit
code 0, when the real failure was buried in the log body (a known-flaky `cmd/runner` container
test — confirmed via `flaky-packages.json` and five clean isolated reruns, not a regression). Always
read the log tail directly, or use `PIPESTATUS`/`set -o pipefail`, never trust a piped command's
reported exit code.

**`LOCALSTACK_AUTH_TOKEN` must be exported before a full `coverage-check`/`-race` run, or unrelated
AWS-backed packages report false regressions.** Unchanged gotcha from prior sessions:
`.IGNORE/.localstack.env`'s key is `token=`, not `LOCALSTACK_AUTH_TOKEN=` — it must be manually
translated before export, or LocalStack-backed tests silently skip rather than fail, and coverage
reads as a regression that isn't one.

**No commit without the user's own live word in the current conversation.** Held throughout — both
of this session's commits (`835431b`, `dc8e2df`) were made by the user, not the assistant, even
after the assistant offered drafted commit message text both times.

**Never use the Agent or Workflow tool to delegate without being asked, even with Ultracode on.**
Held throughout this session.

### Verification state

`go build ./...`, `go vet ./...`, `gofmt -l` all clean. `go test ./internal/transport/...
./pkg/remoteexec/... ./pkg/retry/... ./internal/lock/... ./internal/engine/... -race` clean —
proves the `retry.Do` migration is behavior-preserving. `internal/ent/migrate/parity_test.go`
clean (both dialects have the new `properties` field). `go test ./internal/archtest/...` clean,
including the new no-second-breaker/no-second-retry-loop rule. `make ci` and `make push-gate` both
run to completion; the only failure seen anywhere was the pre-documented `cmd/runner`
container-port-mapping flake (`flaky-packages.json`), confirmed not a regression via five clean
isolated reruns. `make gosec`/`make govulncheck` clean. Coverage: all touched packages at or above
their recorded floor; new `coverage-floor.json` entries added for packages crossing a boundary for
the first time.

### Next steps

Phase 72 is done; Phases 73-77 of Part XV (non-network endpoints + the full hostile-bastion proof;
NETCONF/RESTCONF/gNMI; WinRM; `internal/psdiag`; SFTP) are unstarted and out of scope for this
session. Each reuses Phase 72's breaker, retry loop, and hop chain rather than building its own —
read this section before assuming any of their own scope from `.SPECIFICATION/IMPLEMENTATION.md`
alone, per the "stale spec" lesson above; each deserves its own planning pass against the real
current code first.
