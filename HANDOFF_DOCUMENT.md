# Handoff Document

Rewrite the "Current Status" section when stopping mid-task or handing off, per `.AGENTS/AGENTS.md`.

## Current Status (this session)

**This session implemented Phase 33: The Scaffolds in full**, the fourth phase of Part VII (The Forge
of Hephaestus) to close, after a Plan-mode design review (three parallel Explore-agent research passes
plus one Plan-agent design validation, both driven from this session directly rather than a saved plan
file reused from an earlier phase). All nine checklist items are now `[x]`. This phase builds the two
generators Phase 34 will invoke ~27+ times to dogfood-generate the actual module catalog:
`internal/inventory/devicescaffold` and `internal/forge/collectionscaffold`, plus their `pleiades forge
new-device`/`new-collection` CLI wiring.

**Two real, verified gaps in the checklist's own premises, corrected in place rather than built
around:** the checklist named `ItemFactory.Register` as the device registration mechanism in three
places; that method was retired during the Phase 6 registry retrofit and does not exist. The real
mechanism, unchanged by this phase, is `record.RegisterType`, reachable only via
`internal/inventory/builtins.go`'s blank-import list — `NewItemFactory()` itself hardcodes nothing, it
builds from `record.AllTypes()`. Separately, `pkg/collection` (Phase 31) turned out to be pure
planning-time metadata with no execution-side consumer anywhere in this codebase: no dispatcher calls a
registered Collection method's real implementation, so a generated collection stub, while real,
buildable, and testable Go code, is not reachable from any execution path yet. Both gaps are corrected
with dated notes in `IMPLEMENTATION.md`, `docs/hephaestus.md`, and `CODE_SCAFFOLD.md`, and the second
one is additionally documented in every generated collection package's own header, the same "must say
so, not pretend" duty the checklist's device-scaffold bullet already named.

**What was built:** `internal/forge/genutil` (new, shared by both scaffolds): `ValidateSegment`
(`^[a-z][a-z0-9_]*$`, stricter than `internal/classification`'s own `^[a-z0-9_]+$` because a segment
here becomes a Go identifier, not just a map key — a leading digit is legal for one and not the other),
`go/token.IsKeyword`-based keyword rejection, a segment-count and per-segment-length bound, and
`ToExportedIdent` (snake_case to PascalCase). `internal/inventory/devicescaffold` and
`internal/forge/collectionscaffold` (new, mirrored shape): a `Config` type with a `Validate()` using
`genutil` plus `capability.Lookup`, `text/template` sources reproducing the hand-written
`devices/cisco/router.go` pattern and the `pkg/collection`/`pkg/sdk.RunbookContext` pattern respectively
(this is the first in-repo code generator; no `text/template` usage existed anywhere before this
phase), and a pure `Generate(cfg) ([]GeneratedFile, error)` doing no filesystem I/O, formatting output
via `go/format.Source` rather than a shelled-out `gofmt` binary. The device template adds the `var _
inventory.InventoryItem = (*T)(nil)` compile-time assertion neither hand-written package has today. New
directory `internal/catalog/`, added to `CODE_SCAFFOLD.md`'s own tree with a justifying comment (per
`.AGENTS/AGENTS.md`'s Map Verification protocol), is where `collectionscaffold` writes, nested by
namespace segment (`pkg.apt.install` → `internal/catalog/pkg/apt/install.go`).

**A real, previously-unnoticed CLI bug was found and fixed while wiring `forge new-collection`, not
worked around:** `cmd/pleiades/addhost.go`'s shared `splitPositional` (used by `add-host`,
`add-credential`, and now both new `forge new-*` subcommands) assumed every flag takes a following
value, which silently swallowed a real flag as a bare boolean flag's "value" the moment a caller had one
followed by another flag — `forge new-collection --requires-elevation --engine-version ">=1.0.0"` was
the case that surfaced it. Fixed with a `boolFlags map[string]bool` parameter; `add-credential`'s
pre-existing `--passphrase` bool flag had the identical latent bug, closed as a byproduct rather than
left in place once the general fix was made. See `FAILURE_PATTERNS.md` #50, `LESSONS_LEARNED.md` #54.

**Adversarial Pattern Justification:** both scaffolds' `Generate` functions only ever return paths under
their own fixed base directory (`internal/inventory/devices/`, `internal/catalog/`), proven for
arbitrary input by fuzzing, never touching `internal/inventory/factory.go`, `builtins.go`, or any
existing `pkg/collection` entry. `internal/inventory/devicescaffold/release_gate_test.go` proves this
positively: it generates into a real temporary sibling package inside the actual checkout, then builds
and runs a *second*, independent temporary harness package that blank-imports only the generated
package (the one real composition-root edit) and resolves it through
`inventory.NewItemFactoryWithConstructors(record.AllTypes())`.

**Schema/Injection Hardening:** the one new boundary class is generated-source injection (user strings
embedded into real `.go` files this phase's generators write). Closed at two layers: every segment that
becomes a path component or Go identifier is `genutil`-validated before any template executes (so `..`,
`/`, quote, and backtick characters are unreachable, not merely escaped), and every string embedded as a
Go string literal rather than an identifier goes through `strconv.Quote` as defense in depth. No
deserialization, CEL, SQL, NATS subject, or auth/token boundary is introduced. `make gosec` found no new
finding (7 pre-existing, all individually waived, matching every prior phase).

**Fuzz/Stress:** `FuzzValidateSegment` (`internal/forge/genutil`, 1.5M+ execs/15s, zero failures);
`FuzzGenerate` in both `devicescaffold` (710K+ execs/15s) and `collectionscaffold` (748K+ execs/15s),
each proving accepted input never escapes its base directory and always parses as valid Go via
`go/parser`; `FuzzRunForgeNewDevice`/`FuzzRunForgeNewCollection` (new, `cmd/pleiades`) calling the
subcommand functions directly rather than through `run()`/`runForge`, since `cli_fuzz_test.go`'s
existing generic harness splices `--dir <tmp>` in at a position that makes any `forge`-prefixed seed hit
the unknown-subcommand path before reaching real flag parsing — both ran clean (11.7K and 5.1K execs
respectively; slower per-exec since each iteration performs real filesystem writes into a `t.TempDir()`).

**Release Gate:** proven at two independent layers, both against the real `go` toolchain and the real
repository tree, never in-process alone. Library layer: both scaffold packages' own
`release_gate_test.go` generate into a real temporary package, `go build`/`go test` it as a subprocess,
and clean up via `t.Cleanup` (confirmed via `git status` showing no leftover paths after a run). CLI
layer: `cmd/pleiades/e2e_test.go`'s new `TestCLI_ForgeNewDevice_EndToEnd` and
`TestCLI_ForgeNewCollection_EndToEnd` drive the actual built `pleiades` binary against the real
repository root, then `go build`/`go test` the generated package as a subprocess. `make ci` (build,
vet, fmt, `test-race`, `gosec`, `govulncheck`, `coverage`) passed clean end to end, run twice to rule
out a transient failure seen once from a standalone `make coverage` invocation (not reproduced inside
`make ci` itself, and not related to any file this phase touched). Coverage: `internal/forge/genutil`
96.0%, `internal/forge/collectionscaffold` 90.2%, `internal/inventory/devicescaffold` 88.6% (both
scaffold packages' only uncovered lines are `format.Source`/`template.Execute` error-wrapping branches,
unreachable through the public `Generate` API once `Config.Validate()` already guarantees safe,
template-compatible input — an honestly recorded gap, not forced to 90% with a contrived test, matching
this project's own established precedent for a defensively unreachable branch). `cmd/pleiades` rose
from 33.5% to 43.9%, floor raised to 43.0.

**Files changed:** `internal/forge/genutil/` (new: `identifiers.go`, `identifiers_test.go`,
`identifiers_fuzz_test.go`), `internal/inventory/devicescaffold/` (new: `devicescaffold.go`,
`config.go`, `templates.go`, `generate.go`, `generate_test.go`, `generate_fuzz_test.go`,
`release_gate_test.go`), `internal/forge/collectionscaffold/` (new: same seven-file shape),
`cmd/pleiades/forge_new_device.go`, `forge_new_device_test.go`, `forge_new_device_fuzz_test.go` (all
new), `cmd/pleiades/forge_new_collection.go`, `forge_new_collection_test.go`,
`forge_new_collection_fuzz_test.go` (all new), `cmd/pleiades/forge_scaffold_io.go` (new, shared
file-write/`--capabilities`-parsing helpers), `cmd/pleiades/forge.go` (two `forgeCommands` entries,
`printForgeUsage` updated), `cmd/pleiades/addhost.go` (`splitPositional` gained `boolFlags`, doc comment
generalized), `cmd/pleiades/addcredential.go` (`splitPositional` call site updated, closing its own
latent bug), `cmd/pleiades/addhost_test.go` (new `TestSplitPositional_BoolFlagTakesNoFollowingValue`),
`cmd/pleiades/e2e_test.go` (`TestCLI_ForgeHelp` updated for real subcommands, two new release-gate
tests), `.SPECIFICATION/IMPLEMENTATION.md` (Phase 33 checked off in full, including the
`ItemFactory.Register` correction), `docs/hephaestus.md` (status line, workflow table, command surface,
"Create a Collection"/"Create an inventory device type" sections all updated), `.SPECIFICATION/CODE_SCAFFOLD.md`
(`internal/forge/`/`internal/catalog/` tree entries, `internal/adapters/native/` annotation corrected,
`RunbookContext` Section G corrected), `coverage-floor.json` (four new/changed floors),
`FAILURE_PATTERNS.md` (#50 new), `LESSONS_LEARNED.md` (#54 new). See "The Phase 33 session" immediately
below for full detail. Everything from "The Phase 32 session" onward describes earlier sessions and is
unchanged.

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
