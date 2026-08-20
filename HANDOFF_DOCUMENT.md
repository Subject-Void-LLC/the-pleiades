# Handoff Document

Rewrite the "Current Status" section when stopping mid-task or handing off, per `.AGENTS/AGENTS.md`.

## Current Status (this session)

**Branch `feature/Filter-Infrastructure-n-CEL-Wiring`, off `main`. HEAD is `bc4ab37`, Phase 50 (Filter
Infrastructure & CEL Wiring), committed since the prior session's handoff (not by this session; no live
go-ahead was given this session, so this session never ran `git commit`). Everything below is
implemented, tested, and verified on top of that commit, but uncommitted: no such word has been given
yet this session.**

This session opened with a direct request: rate the prior session's own delivery, then "upgrade the
forge and use it to build phase 51." Two deliverables, in order: a fourth `pleiades forge` scaffolder
for `pkg/filters` functions, and Phase 51 (Network & Addressing Filters,
`.SPECIFICATION/IMPLEMENTATION.md`'s Part XII) built through it, end to end.

### What landed

**`internal/forge/filterscaffold`** (new), the Forge's fourth scaffolder after `collectionscaffold`/
`pluginscaffold`/`viewscaffold`. `Config` carries two independent names (`GoName`, e.g.
`"CIDRToNetmask"`; `CELName`, e.g. `"cidrToNetmask"`) rather than deriving one from the other, since Go
and CEL naming diverge on acronym casing in a way no mechanical rule can safely reverse. `Generate`
writes one new file per filter (`pkg/filters/<snake_case CELName>.go` plus `_test.go`), not a shared
per-category file: every other scaffolder in this family writes a brand-new file and refuses to
overwrite, and `pkg/filters` has no per-entity directory the way a Collection method or plugin does to
make an append-to-existing-file story safe. `Reminder` renders the CEL registration block (a
`cel.Function`/`cel.Overload`/`FunctionDocs`/`OverloadExamples` block plus a `*Binding` function) a
human pastes into `internal/engine/cel_filters.go`, mirroring `viewscaffold.Reminder`'s own "the one
step no generator can perform" pattern: `cel_filters.go` is one hand-maintained file, not a directory a
blank import can wire in. Wired into the CLI as `pleiades forge new-filter` (`cmd/pleiades/
forge_new_filter.go`), reusing the shared `forge_scaffold_io.go` helpers (`writeGeneratedFile`,
`firstExistingFile`) every other subcommand already uses, and into `internal/clispec/clispec.go` so
`docs/reference/cli.md` documents it too.

**Tests**: `internal/forge/filterscaffold/generate_test.go` (table-driven, every accepted case's output
proven to parse as real Go via `go/parser`), `generate_fuzz_test.go` (`FuzzGenerate`/`FuzzReminder`, 15s
each, zero panics), `release_gate_test.go` (`TestGenerate_ReleaseGate`: writes generated output as a
real, process-unique temp file directly into `pkg/filters/`, itself an unavoidable divergence from
`collectionscaffold`'s own scratch-subpackage release gate, since every filter shares one flat package
rather than getting its own; runs the real `go build`/`go test` toolchain against it, cleans up via
`t.Cleanup`). `cmd/pleiades/forge_new_filter_test.go` mirrors `forge_new_plugin_test.go`'s shape
(missing-flag errors, a successful generation, `--skip-existing`, refuses-to-overwrite).
`internal/archtest/render_test.go`'s `templateEngineAllowlist` gained this package's entry, the same
allowlist the other three scaffolders already carry (`text/template` for code generation is not the
Section 25 template-renderer primitive `internal/render` owns).

**A real defect the tool's own first real use caught, before it ever reached Phase 52.** The first
`camelToSnake` implementation inserted an underscore before every uppercase letter, so an
acronym-heavy CEL name split letter by letter: `"classifyIP"` became `"classify_i_p"`,
`"macOUI"` became `"mac_o_u_i"`, mangling both the generated file name and the CEL overload ID. Caught
by literally running `pleiades forge new-filter` for all 25 of this phase's functions and reading the
output, not by a unit test written in advance. Fixed to treat a run of uppercase runes as one acronym
(an underscore only at a lowercase-to-uppercase transition, or at the last letter of an uppercase run
immediately followed by a lowercase one), re-verified against every one of this phase's own names, with
a regression test (`TestGenerate_FileNamesForAcronymHeavyNames`) pinning the fix. One known, documented,
accepted residual: a name mixing an acronym directly against a version-style suffix
(`ToIPv4MappedIPv6`) still splits awkwardly (`to_i_pv4_mapped_i_pv6` before a manual rename to
`to_ipv4_mapped_ipv6.go`), because no purely mechanical rule can tell "IPv4" (one token) from
"IPServer" (two) without a dictionary. Documented in the function's own doc comment as a known
limitation, not silently worked around.

**Phase 51: 25 network and addressing filters, all scaffolded through the real CLI, then hand-implemented
and fully tested.** `pkg/filters/network.go` (CIDR/netmask/wildcard-mask conversion, broadcast address,
subnet split, supernet, IP-to-int and back, IPv4-mapped-IPv6 conversion, IP classification, 11
functions), `mac.go` (Cisco/colon/Windows MAC normalization, OUI extraction, 4), `vlanasn.go` (VLAN/ASN
validators, 4), `interfacename.go` (Cisco IOS short/long form, 2), `dns.go` (FQDN/hostname, URL
domain/port, 4). Every function is IPv4-scoped and returns a documented sentinel on malformed input
(`""` or `-1`, since none of `pkg/filters`' functions carry an error return) rather than throwing.
`internal/engine/cel_filters.go` gained `celToInt`/`celToBool` (mirroring the existing `celToString`)
plus `celToStringList`/`wrapStringList` (for `Supernet`'s `[]string` parameter and `SubnetSplit`'s
`[]string` result, using `ref.Val.ConvertToNative` and `types.NewStringList`/`types.DefaultTypeAdapter`,
cel-go's own generic native-conversion path and exported default adapter), and all 25 `cel.Function`
registrations.

**Tests**: table-driven tests per function (including a MAC/VLAN/ASN/CIDR-shaped adversarial case for
every function this phase's own checklist names by value: a malformed MAC, an out-of-range CIDR prefix,
`ValidateVLAN(-1)`/`ValidateVLAN(99999)`, `ValidateASN(0)`, an interface name with an embedded NUL
byte), round-trip tests (`IPToInt`/`IntToIP`, `ToIPv4MappedIPv6`/`FromIPv4MappedIPv6`,
`InterfaceShortForm`/`InterfaceLongForm`, `HostnameToFQDN`/`FQDNToHostname`), one `Fuzz` target per
parsing-shaped function family (`network_fuzz_test.go`, `mac_fuzz_test.go`,
`interfacename_fuzz_test.go`, 8-15s each, zero panics). `internal/engine/cel_filters_network_test.go`:
every one of the 25 functions proven callable through the real, unmodified
`engine.NewCELEvaluator()`/`Program.Eval` via a compiled `when_cel` expression, plus the checklist's own
explicit combined-condition requirement (`TestCELFilters_Phase51CombinedCondition`, chaining five
functions against a realistic device `stat` payload with a negative control). `internal/engine/
cel_filters_internal_test.go` (whitebox, `package engine`): direct tests for `celToString`/`celToInt`/
`celToBool`/`celToStringList`'s own branches and every one of the 25 bindings' "argument not
convertible" defensive branch, which is unreachable through the real compiled CEL path (cel-go's own
type checker already guarantees convertibility for a statically-typed overload before any binding
runs) and so only provable by calling the unexported binding function directly. `pkg/filters` measures
99.6% coverage (one provably unreachable branch: `URLPort`'s `strconv.Atoi` error path, since
`net/url.Parse` itself only ever accepts an all-digit port); `internal/engine` measures 94.1%, above its
recorded floor.

**Documentation Gate closed with zero hand-written doc changes**, exactly validating Phase 50's own
design bet: `docs/reference/filters/index.md` picked up all 25 new `filters.*` entries automatically
from `go run ./tools/gendocs` (which diffs the live CEL environment, not a hand-maintained table), and
`docs/reference/cli.md` picked up `forge new-filter` from the `internal/clispec` addition. `go generate
./... && git diff --exit-code` regenerates clean.

### Read this first

**A real gosec finding, fixed rather than waived.** `uint32ToIP4`'s original
`[4]byte{byte(n >> 24), byte(n >> 16), byte(n >> 8), byte(n)}` construction tripped G115 (integer
overflow conversion `uint32 -> byte`) three times on one line. Rewritten to use
`encoding/binary.BigEndian.PutUint32`, which performs the identical byte extraction without an explicit
narrowing conversion gosec's heuristic flags, closing the finding rather than adding a ninth
`gosec-waivers.json` entry for what was correct-by-construction code in the first place. `make gosec`
still reports the same 9 pre-existing waived findings, zero new.

**A real, acknowledged-flaky test, not a regression.** The first full `go run ./tools/coverage-check`
attempt failed on `internal/runner`'s `TestAgent_ReportResult_SuccessfulExecutionFlushesToWAL`
(10-second timeout under the full-suite's parallel load). `internal/runner` is listed in
`flaky-packages.json` with a prior, dated, directly-observed flake under this exact sandboxed
environment's parallel load (`FAILURE_PATTERNS.md` #61's class). Confirmed, not assumed: the same test
passed in 0.02s run in isolation. A second full `coverage-check` run passed clean. Nothing in this
session's own changes touches `internal/runner`.

**Module names are `xxx.xxx.xxx`.** `FAILURE_PATTERNS.md` #158; unaffected, `filters.*` stays a
different, expression-engine-function namespace.

**No commit without the user's own live word in the current conversation.** Unchanged. This session was
asked, mid-turn, to show the drafted Phase 50 commit message rather than run it; Phase 50 was committed
by the user's own separate action afterward, not by this session. Nothing below has been asked for yet.

**Never use the Agent or Workflow tool to delegate without being asked, even with Ultracode on.**
Unchanged. Held again this session: every filter, test, and scaffolder file was written directly, not
delegated, despite the large surface (25 functions plus a new scaffolder package).

**Using a newly-built code generator against its own first real workload is worth doing before trusting
it.** New finding this session, worth carrying forward explicitly: `filterscaffold`'s `camelToSnake` bug
was invisible in isolated unit tests written alongside the generator itself (which used simple,
non-acronym names as fixtures) and was only caught by actually running `pleiades forge new-filter`
against all 25 of Phase 51's real, acronym-heavy names and reading the output. A future session adding a
fifth scaffolder, or extending this one, should run it against a realistic batch of real names before
trusting its output, not just its own narrower unit tests.

### The remainder, in order

Phase 51 is done. Every phase in Part XII from here still depends only on Phase 50's `filtersLib()`
aggregation point, unchanged by this phase:

1. **Phase 52: Structured Data Filters.** JSON flatten/unflatten, deep/shallow merge, CSV via
   `encoding/csv`, `Pluck`, YAML/JSON conversion, one opinionated XML-to-JSON mapping, `GenerateUUIDv4`.
   `filterscaffold` is available and proven for this phase's own scaffolding, the same way it was used
   here; expect its `wellKnownCELTypes` set (currently `string`/`int`/`bool`) to need a fourth entry
   (`cel.ListType(cel.DynType)` or similar) for JSON-shaped inputs, which will fall to the same
   explicit-`CELType`-required path `Supernet`/`SubnetSplit` already exercise, not a scaffolder change.
2. **Phase 53: String, Encoding & Path Filters.**
3. **Phase 54: Validation & Business-Logic Predicate Filters.**
4. **Phase 55: Time, Date & Scheduling Filters.**
5. **Phase 56: Security & Cryptography Filters.**
6. **Phase 57: Cloud Provider Data Filters.**
7. **Phase 58: File, Text & Log Filters.**

Skim each phase's own header before starting it rather than assuming a one-line summary is the whole
scope, per this branch's own repeated discipline.

### Verification state

`go build ./...`, `go vet ./...`, `make fmt` all pass with no output. `make gosec`: 9 pre-existing
individually-waived findings, zero new (one real finding fixed at the source, see above). `make
govulncheck`: 0 vulnerabilities in this module's own code or imported packages (3 unrelated
vulnerabilities in required-but-unused modules, unaffected). `go test ./internal/archtest/...` passes
clean. `go run ./tools/gendocs` is idempotent; `go run ./tools/docs-lint` passes at 181 files. RULE 0:
built the real `pleiades` binary fresh, ran `pleiades init` into a scratch project, wrote a runbook with
four `when_cel` tasks exercising `classifyIP`, `validateVLAN`, `interfaceShortForm`, and
`subnetSplit(...).size()`; `pleiades validate` passed clean, `pleiades run` executed it for real with
the expected task skipped and named in the skip reason.

**Full-repo `go test -race ./...` ran to completion with zero failures across 128 packages**, real
containers included (`LOCALSTACK_AUTH_TOKEN` sourced via `export
LOCALSTACK_AUTH_TOKEN=$(cut -d= -f2 .IGNORE/.localstack.env)`, per `host_system_crashes.md`'s own
documented gotcha).

`go run ./tools/coverage-check` reports **175 packages measured, none below their recorded floor**, on
the second attempt (the first hit the acknowledged `internal/runner` flake described above). Two
`coverage-floor.json` changes, both recorded with a written reason in the file's own `_comment`:
`pkg/filters` moves from 100.0 to 99.5 (measured 99.6, the one uncovered branch provably unreachable);
`internal/forge/filterscaffold` enters as a new package at 88.0 (measured 89.3). `internal/engine`
measures 94.1%, above its existing 93.2 floor; no change needed there.

### Commit message

Drafted, not run; nothing beyond `bc4ab37` is committed.

```
feat(forge,catalog): pleiades forge new-filter, and Phase 51's 25 network/addressing filters

Two deliverables: a fourth Forge scaffolder for pkg/filters functions,
and Phase 51 (Network & Addressing Filters, PLAN.md Section 36's Part
XII) built through it end to end, proving the tool against a real,
acronym-heavy 25-function workload rather than only its own narrower
unit tests.

internal/forge/filterscaffold generates one new pkg/filters function's
stub, starter test, and a paste-ready CEL registration block for
internal/engine/cel_filters.go, mirroring collectionscaffold/
pluginscaffold/viewscaffold's shape (text/template + go/format.Source,
zero filesystem I/O, paths relative to the repo root) where it fits and
diverging where PLAN.md Section 36 forces it to: every filter shares
one flat pkg/filters package, so Generate writes one new file per
filter rather than a shared per-category file, and cel_filters.go is a
hand-maintained file a Reminder() block is pasted into, not a directory
a blank import can wire in. Config carries GoName and CELName as two
independent, explicit fields rather than deriving one from the other,
since Go and CEL naming diverge on acronym casing (CIDRToNetmask vs.
cidrToNetmask) in a way no mechanical rule can safely reverse. Wired in
as `pleiades forge new-filter`, reusing the shared forge_scaffold_io.go
helpers every other subcommand already uses.

Running the new tool against all 25 of Phase 51's own real,
acronym-heavy filter names (ClassifyIP, MACOUI, ValidateVLAN,
ValidateASN, and so on) caught a real defect before it reached Phase
52: camelToSnake inserted an underscore before every uppercase letter,
mangling an acronym into "classify_i_p" instead of "classify_ip". Fixed
to treat a run of uppercase runes as one acronym, with a regression
test pinning every one of this phase's own names. One residual,
documented rather than silently worked around: a name mixing an
acronym directly against a version-style suffix (ToIPv4MappedIPv6)
still splits awkwardly, since no purely mechanical rule can tell "IPv4"
from "IPServer" without a dictionary.

Phase 51 itself: pkg/filters/network.go (CIDR/netmask/wildcard-mask
conversion, subnet split, supernet, IP-to-int, IPv4-mapped-IPv6
conversion, IP classification), mac.go (Cisco/colon/Windows MAC
normalization, OUI extraction), vlanasn.go (VLAN/ASN validators),
interfacename.go (Cisco IOS short/long form), dns.go (FQDN/hostname,
URL domain/port). Every function is IPv4-scoped and returns a
documented sentinel on malformed input rather than throwing, since none
of pkg/filters' functions carry an error return; URLDomain/URLPort
additionally refuse a schemeless input rather than guessing one, since
net/url.Parse itself silently misparses a bare "host:port/path" string
into an empty host. cel_filters.go gained celToInt/celToBool
(mirroring the existing celToString) and celToStringList/
wrapStringList for the two list-shaped signatures (Supernet's
[]string parameter, SubnetSplit's []string result), using
ref.Val.ConvertToNative and types.NewStringList/DefaultTypeAdapter.

Tests: table-driven and fuzz coverage per function (one Fuzz target per
parsing-shaped function family, matching this phase's own checklist
item), round-trip tests, and every function proven callable through the
real, unmodified engine.NewCELEvaluator()/Program.Eval via a compiled
when_cel expression, including the checklist's own explicit
combined-condition requirement chaining five functions with a negative
control. A whitebox test file directly exercises every one of the 25
bindings' "argument not convertible" defensive branch, unreachable
through the real compiled CEL path (cel-go's own type checker already
guarantees convertibility for a statically-typed overload) and provable
only by calling the unexported binding function directly.
docs/reference/filters/index.md picked up all 25 new entries with zero
hand-written doc changes, validating Phase 50's own
diff-the-live-environment generator design.

A real gosec G115 finding (uint32 -> byte narrowing in uint32ToIP4) was
fixed at the source with encoding/binary.BigEndian.PutUint32 rather
than waived. coverage-floor.json: pkg/filters moves from 100.0 to 99.5
(measured 99.6, the one gap provably unreachable: net/url.Parse only
ever accepts an all-digit port); internal/forge/filterscaffold enters
as a new package at 88.0 (measured 89.3).

go test -race ./... ran clean across all 128 packages. go run
./tools/coverage-check reports 175 packages measured, none below
floor, on the second attempt (the first hit internal/runner's
documented, acknowledged flaky WAL test under full-suite parallel
load, confirmed by an isolated pass in 0.02s, unrelated to this
change). make gosec: 9 pre-existing waived findings, zero new. make
govulncheck: clean. RULE 0: the real pleiades binary, built fresh, ran
a scratch runbook exercising four of this phase's filters through
pleiades validate and pleiades run.
```
