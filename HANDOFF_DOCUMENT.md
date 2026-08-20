# Handoff Document

Rewrite the "Current Status" section when stopping mid-task or handing off, per `.AGENTS/AGENTS.md`.

## Current Status (this session)

**Branch `feature/Filter-Infrastructure-n-CEL-Wiring`, off `main`. HEAD is `1c6549a`, the forge upgrade
and Phase 51 (Network & Addressing Filters), committed since the prior session's handoff (not by this
session; no live go-ahead was given this session, so this session never ran `git commit`). Everything
below is implemented, tested, and verified on top of that commit, but uncommitted: no such word has been
given yet this session.**

This session opened with a direct request: "update the forge if needed and build Phase 52: Structured
Data Filters." Two deliverables, in order: extending `internal/forge/filterscaffold` to handle
structured (map/list) types, which it could not before, and Phase 52
(`.SPECIFICATION/IMPLEMENTATION.md`'s Part XII) built through it, end to end.

### What landed

**`internal/forge/filterscaffold` upgraded to know four structural Go types**, not just
`string`/`int`/`bool`: `[]string`, `map[string]any`, `[]any`, `[]map[string]any`. `wellKnownCELTypes`,
`conversionFor`, `wrapperFor` and `exampleArg` all gained the four entries; `[]string` reuses Phase 51's
existing `celToStringList`/`wrapStringList` (which existed but had never been wired into this table,
since `Supernet`/`SubnetSplit` needed an explicit `CELType` at the time); the other three needed new
shared helpers in `internal/engine/cel_filters.go` (`celToMap`/`wrapMap`, `celToDynList`/`wrapDynList`,
`celToMapList`/`wrapMapList`). `bindingFuncFor`/`bindingFunc` also gained real, non-TODO support for a
zero-`Param` filter (`cel.FunctionBinding` with a `_ ...ref.Val` signature, since that constructor's
real type is `func(...ref.Val) ref.Val`, not the arity-zero function a naive template might guess), first
needed by `GenerateUUIDv4`.

**A real correctness bug in the naive approach, caught by testing through the real CEL environment, not
assumed from the native-Go-map case that worked fine.** `celToMap`/`celToDynList`/`celToMapList`'s first
implementation called `ref.Val.ConvertToNative` directly, mirroring Phase 51's own
`celToStringList`. That is correct for a *flat* list of scalars, but wrong for a *nested* map: verified
directly with a scratch program, `ConvertToNative(map[string]any{})` converts a top-level CEL map's
values correctly, but for a *nested* map's value it takes cel-go's internal `ConvertToNative(any)` path,
which substitutes `map[any]any` instead of `map[string]any` at that level -- and only when the source is
a CEL map *literal* (`filters.flatten({"a": {"b": 1}})`), not a map wrapped from a native Go value via
`types.NewDynamicMap` (which was the only shape the first round of scratch verification tested, before
Phase 52 needed nested literals at all). `Flatten`'s own Go code type-switches on `map[string]any`
specifically, so a silently different nested shape made it treat a legitimate nested map as an opaque
leaf instead of recursing into it -- caught by `TestCELFilters_Phase52StructuredFilters`'s `flatten` and
`deep_merge` cases failing (not `unflatten`/`shallow_merge`, since those cases' test data never handed
CEL a nested map for the outer call, an asymmetry that made the bug's shape informative rather than just
"some tests fail"). Fixed by replacing the `ConvertToNative` calls with `celToAny`, a walker over the
`traits.Mapper`/`traits.Lister` interfaces every cel-go map/list representation implements (a literal, a
wrapped native Go value, a proto struct field), falling back to `ref.Val.Value()` only for an actual
scalar leaf -- correct regardless of which internal representation the source happens to use, rather
than depending on one specific one.

**Phase 52: 11 structured-data filters, all scaffolded through the real upgraded CLI, then
hand-implemented and fully tested**, in one consolidated `pkg/filters/structured.go` (the phase's own
checklist groups these as one category, unlike Phase 51's five natural sub-categories): `Flatten`/
`Unflatten` (dot-notation keys, a list element's index as a numeric segment), `DeepMerge` (nested maps
merge key by key, lists append, anything else in `b` overwrites `a`, never drops a key from either
input), `ShallowMerge` (top-level overwrite only), `CSVToList`/`ListToCSV` (one CSV line via stdlib
`encoding/csv`, correct quote handling), `Pluck` (skips a map missing the key rather than padding the
result with a placeholder), `YAMLToJSON`/`JSONToYAML` (`go.yaml.in/yaml/v3`, round-tripping through
`interface{}`), `GenerateUUIDv4` (`github.com/google/uuid`, crypto/rand-backed -- this package's one
deliberate exception to "pure, deterministic transform of its arguments," documented as such rather than
left looking like an oversight), and `XMLToJSON` (one documented, opinionated element/attribute
convention: `@name` for an attribute, `#text` for non-empty text alongside one, a single value or an
array in document order for a repeated child tag; a namespace declaration, `xmlns=`/`xmlns:ns=`, is
dropped everywhere rather than leaking through as a meaningless `@xmlns`/`@ns` key, verified directly
against `encoding/xml`'s own `Attr` shape after a first version leaked it).

Two new package-level constants carry this phase's own bounds, both named and justified rather than
reusing Phase 50's: `MaxStructuredInputBytes` (1 MiB, every raw JSON/YAML/XML document or CSV line this
phase parses, deliberately larger than Phase 50's flat-scalar `MaxInputBytes`) and `maxStructuredDepth`
(32, every function that walks a nested structure -- `Flatten`, `Unflatten`, `DeepMerge`, and the tree
`YAMLToJSON`/`JSONToYAML`/`XMLToJSON` decode before re-encoding -- verified against a real 50-level-deep
input for each rather than assumed). A related finding verified directly rather than assumed: a
self-referential YAML anchor (`a: &x\n  b: *x`) is rejected by `yaml.Unmarshal` itself with a real error,
not silently decoded into a cyclic Go value that would hang the depth-walker's own recursion.

**Tests**: table-driven tests per function including the malformed/boundary cases the checklist's own
Adversarial Pattern Justification names by value (`Flatten`/`Unflatten` round-trip losslessly for a
representative structure, with the one real, inherent ambiguity this scheme carries -- a map whose own
keys genuinely are `"0"`, `"1"` is indistinguishable from a two-element list once flattened -- documented
with a passing test rather than hidden; `DeepMerge` never drops a key from either input, proven by
iterating both inputs directly, not by example). Five `Fuzz` targets (`FuzzCSVToList`, `FuzzListToCSV`,
`FuzzYAMLToJSON`, `FuzzJSONToYAML`, `FuzzXMLToJSON`), 8-9s each, zero panics across tens to hundreds of
thousands of executions. `internal/engine/cel_filters_structured_test.go`: every one of the 11 functions
proven callable through the real, unmodified `engine.NewCELEvaluator()`/`Program.Eval` via a compiled
`when_cel` expression, plus a combined condition chaining five functions against a realistic device
`stat` payload with a negative control. `internal/engine/cel_filters_internal_test.go` (whitebox) gained
direct tests for `celToMap`/`celToDynList`/`celToMapList`'s own branches and every one of the 11 new
bindings' "argument not convertible" defensive branch, the same unreachable-through-real-CEL class Phase
51's own whitebox tests already established.

`pkg/filters` measures 98.6% (down from Phase 51's 99.6%, `coverage-floor.json` moved from 99.5 to 98.5
with a written reason: five branches across four functions, all provably unreachable for the same class
of reason `URLPort`'s own existing gap is -- `ListToCSV`'s `csv.Writer.Write`/`Error` checks against a
`strings.Builder` sink that never errors, and the `json.Marshal`/`yaml.Marshal` calls ending
`YAMLToJSON`/`JSONToYAML`/`XMLToJSON`, each of which only ever receives a value shape its own target
encoder already knows how to encode without error). `internal/engine` measures 93.8%, above its existing
93.2 floor; no change needed there.

**Documentation Gate closed with zero hand-written doc changes**, the same design bet Phase 50 made and
Phase 51 already validated once: `docs/reference/filters/index.md` picked up all 11 new `filters.*`
entries automatically (39 entries total: 3 from Phase 50, 25 from Phase 51, these 11), and a second
`gendocs` run produces byte-identical output.

**A stale CLI-level test broke, found only by running the full `-race` suite, not by this session's own
narrower `pkg/filters`/`internal/engine` runs.** `cmd/pleiades/forge_new_filter_test.go`'s "unknown param
type with no explicit CELType rejected" case asserted `--param cidrs:[]string` was rejected -- true before
this session's forge upgrade, false after. Fixed to use a genuinely-still-unknown type (`[]int`), with a
new case added proving `[]string` now needs no explicit `CELType`. A reminder that a scaffolder's own
unit tests and its CLI wrapper's tests can drift out of sync silently when the scaffolder's accepted-type
set grows, since nothing enforces they stay in lockstep.

### Read this first

**No commit without the user's own live word in the current conversation.** Unchanged. Nothing has been
asked for yet this session.

**Never use the Agent or Workflow tool to delegate without being asked, even with Ultracode on.**
Unchanged. Held again this session: every filter, test, and forge change was written directly.

**`LOCALSTACK_AUTH_TOKEN` must be exported before a full `coverage-check`/`-race` run, or two unrelated
AWS packages read as a false regression.** New finding this session, worth carrying forward explicitly:
the first `go run ./tools/coverage-check` attempt (run without the token) reported
`internal/catalog/cloud/aws/ec2` and `internal/catalog/cloud/aws/s3` dropping from their 96.7%/98.0%
floors to 49.2%/48.0% -- a large, alarming-looking drop from a session that touched neither package.
Confirmed as purely environmental, not a regression: re-running just those two packages with
`export LOCALSTACK_AUTH_TOKEN=$(cut -d= -f2 .IGNORE/.localstack.env)` set reproduced their exact floor
values (96.7%/98.0%) precisely. `host_system_crashes.md` already documented this gotcha for LocalStack
readiness generally; this is the first time in this branch's own session history it was actually hit and
worked through rather than just known about in the abstract.

**A real, working pattern for RULE 0 against a live SSH target, not just a compiled/unit-tested
proof.** `examples/webserver_lab/docker/docker-compose.yml`'s `plain` service (a bare Ubuntu container,
`svc-netauto`/`pleiades-lab-2026`, port 2221) is a ready-made, already-committed fixture for exactly this:
`docker compose ... up -d --build plain`, a scratch `pleiades init` project in a scratchpad directory
(never touching the committed example files), `add-host`/`add-credential`, a scratch runbook with a real
`ssh_exec` task gated on a `when_cel` filter condition. Worth reusing directly for a future phase's own
RULE 0 check rather than reinventing a fixture each time.

**Using a newly-upgraded code generator against its own first real workload is worth doing before
trusting it, again.** Same lesson Phase 51's own session already drew about `filterscaffold`, reconfirmed
here in a different shape: the forge's new structural-type support was scaffolded and used for all 11 of
this phase's real functions before any were hand-implemented, and while the scaffolder itself produced
correct code this time, the CEL-level *helpers it referenced* (`celToMap` and friends) had a real bug
that only running the scaffolded-and-implemented functions through the real, compiled CEL environment
(not just `pkg/filters`' own Go-level unit tests) surfaced.

### The remainder, in order

Phase 52 is done. Every phase in Part XII from here still depends only on Phase 50's `filtersLib()`
aggregation point and, from this session on, `internal/forge/filterscaffold`'s now-broader
`wellKnownCELTypes` table:

1. **Phase 53: String, Encoding & Path Filters.** Expected rejection named in its own Pattern Entry
   Gate: decide whether a separate `filters.` base64 pair is needed at all, given Phase 50 already
   exposes `base64.encode`/`decode` via `ext.Encoders()`, before building a second.
2. **Phase 54: Validation & Business-Logic Predicate Filters.** Introduces a hand-rolled 5-field cron
   parser shared with Phase 55.
3. **Phase 55: Time, Date & Scheduling Filters.** Consumes Phase 54's cron parser; distinct from Phase
   23's own RRULE scheduler (a value transform over a string, never this platform's own scheduling
   mechanism).
4. **Phase 56: Security & Cryptography Filters.** Expected rejection named in its own Pattern Entry
   Gate: no function may silently also verify a signature, or be mistakable for verification, when it
   only parses.
5. **Phase 57: Cloud Provider Data Filters.**
6. **Phase 58: File, Text & Log Filters.**

Skim each phase's own header before starting it rather than assuming a one-line summary is the whole
scope, per this branch's own repeated discipline.

### Verification state

`go build ./...`, `go vet ./...`, `make fmt` all pass with no output. `make gosec`: 9 pre-existing
individually-waived findings, zero new. `make govulncheck`: 0 vulnerabilities in this module's own code
or imported packages (3 unrelated vulnerabilities in required-but-unused modules, unaffected). `go test
./internal/archtest/...` passes clean (no new `text/template`-importing package this session, so no
allowlist change needed). `go run ./tools/gendocs` is idempotent; `go run ./tools/docs-lint` passes at
182 files.

RULE 0: built the real `pleiades` binary fresh, brought up `examples/webserver_lab`'s `plain` SSH
container for real, ran `pleiades init`/`add-host`/`add-credential` into a scratch project, wrote a
runbook with one task gated on a five-filter combined `when_cel` condition (`flatten`, `deepMerge`,
`pluck`, `csvToList`, `generateUUIDv4`) and a second gated on a deliberately false one; `pleiades
validate` passed clean, `pleiades run` executed the real task over real SSH ("changed") and skipped the
second with the real expression named in the skip reason. Container torn down afterward; the example's
own committed files were never touched.

**Full-repo `go test -race ./...` ran to completion with zero failures across 128 packages**, real
containers included (`LOCALSTACK_AUTH_TOKEN` sourced as described above).

`go run ./tools/coverage-check` reports **175 packages measured, none below their recorded floor**, with
the token exported (see above for why the first, token-less attempt looked like a regression and was
not). One `coverage-floor.json` change, recorded with a written reason in the file's own `_comment`:
`pkg/filters` moves from 99.5 to 98.5 (measured 98.6, five branches provably unreachable, detailed
above). `internal/engine` measures 93.8%, above its existing 93.2 floor; no change needed there.

### Commit message

Drafted, not run; nothing beyond `1c6549a` is committed.

```
feat(forge,engine,filters): forge structural-type support, and Phase 52's 11 structured-data filters

Two deliverables: upgrading internal/forge/filterscaffold to handle
map/list types, which it could not before, and Phase 52 (Structured
Data Filters, PLAN.md Section 36's Part XII) built through it end to
end.

internal/forge/filterscaffold's wellKnownCELTypes table gained four
structural entries: []string, map[string]any, []any, []map[string]any.
[]string reuses Phase 51's own celToStringList/wrapStringList, which
existed in internal/engine/cel_filters.go but had never been wired into
this table (Supernet/SubnetSplit needed an explicit CELType at the
time). The other three needed new shared conversion helpers
(celToMap/wrapMap, celToDynList/wrapDynList, celToMapList/wrapMapList).
bindingFuncFor/bindingFunc also gained real support for a zero-Param
filter (cel.FunctionBinding's real signature is func(...ref.Val)
ref.Val, spelled "_ ...ref.Val", not the arity-zero function a naive
template might guess), first needed by GenerateUUIDv4.

Running the upgraded forge against all 11 of this phase's own
functions before hand-implementing any of them surfaced a real
correctness bug, not in the scaffolder's generated code but in the
shared CEL conversion helpers it referenced. celToMap/celToDynList/
celToMapList's first implementation called ref.Val.ConvertToNative
directly, mirroring celToStringList's own established pattern -- correct
for a flat list of scalars, wrong for a nested map. Verified directly
with a scratch program: ConvertToNative(map[string]any{}) converts a
top-level CEL map correctly, but a nested map's value takes cel-go's
internal ConvertToNative(any) path, which substitutes map[any]any
instead of map[string]any at that level, and only when the source is a
CEL map literal (filters.flatten({"a": {"b": 1}})), not a map wrapped
from a native Go value. Flatten's own Go code type-switches on
map[string]any specifically, so the wrong nested shape made it treat a
legitimate nested map as an opaque leaf instead of recursing into it --
caught by the CEL-level integration tests, not the Go-level unit tests,
since those construct their own native Go maps directly and never
exercise a CEL literal's own map representation. Fixed by replacing
ConvertToNative with celToAny, a walker over the traits.Mapper/
traits.Lister interfaces every cel-go map/list representation
implements (a literal, a wrapped native Go value, a proto struct
field), falling back to ref.Val.Value() only for an actual scalar leaf.

Phase 52 itself, one consolidated pkg/filters/structured.go: Flatten/
Unflatten (dot-notation keys), DeepMerge (nested maps merge key by
key, lists append, never drops a key from either input) and
ShallowMerge (top-level overwrite only), CSVToList/ListToCSV (one CSV
line via encoding/csv, correct quote handling), Pluck (skips a map
missing the key rather than padding the result), YAMLToJSON/
JSONToYAML (go.yaml.in/yaml/v3, round-tripping through interface{}),
GenerateUUIDv4 (github.com/google/uuid, crypto/rand-backed -- this
package's one deliberate exception to "pure, deterministic transform
of its arguments"), and XMLToJSON (one documented, opinionated
element/attribute convention: @name for an attribute, #text for
non-empty text, a single value or a document-order array for a
repeated child tag; a namespace declaration is dropped everywhere
rather than leaking through as a meaningless @xmlns/@ns key). Two new
bounds: MaxStructuredInputBytes (1 MiB, every raw document/CSV line
this phase parses) and maxStructuredDepth (32, every function that
walks a nested structure), both verified against real oversized/deep
inputs rather than assumed, including that a self-referential YAML
anchor is rejected by yaml.Unmarshal itself rather than decoding into
a cyclic value that would hang the depth walker.

Tests: table-driven tests per function including this phase's own
named adversarial cases (Flatten/Unflatten round-trip losslessly for a
representative structure, with the one real ambiguity this scheme
carries documented by a passing test rather than hidden; DeepMerge
never drops a key from either input, proven directly). Five Fuzz
targets, zero panics. Every function proven callable through the real,
unmodified engine.NewCELEvaluator()/Program.Eval via a compiled
when_cel expression, plus a five-function combined condition with a
negative control. A whitebox test file exercises every one of the 11
new bindings' "argument not convertible" defensive branch, unreachable
through the real compiled CEL path and provable only by calling the
unexported binding function directly. docs/reference/filters/index.md
picked up all 11 new entries with zero hand-written doc changes (39
total). A stale CLI-level test in cmd/pleiades/forge_new_filter_test.go
asserted []string was rejected as unknown; fixed to use a
genuinely-still-unknown type, with a new case proving []string now
needs no explicit CELType.

coverage-floor.json: pkg/filters moves from 99.5 to 98.5 (measured
98.6, five branches provably unreachable: ListToCSV's csv.Writer
error checks against a strings.Builder sink that never errors, and
the json.Marshal/yaml.Marshal calls ending YAMLToJSON/JSONToYAML/
XMLToJSON, each of which only ever receives a value shape its target
encoder already knows how to encode). internal/engine measures 93.8%,
above its existing 93.2 floor.

go test -race ./... ran clean across all 128 packages. go run
./tools/coverage-check reports 175 packages measured, none below
floor, with LOCALSTACK_AUTH_TOKEN exported (a token-less first attempt
misreported two unrelated AWS packages as regressed; confirmed
environmental, not a regression, by reproducing their exact floor
values once the token was set). make gosec: 9 pre-existing waived
findings, zero new. make govulncheck: clean. RULE 0: the real
pleiades binary, built fresh, ran a scratch runbook against a real,
running examples/webserver_lab SSH container, gating one real
ssh_exec task on a five-filter combined when_cel condition (true, ran)
and a second on a deliberately false one (skipped, named in the skip
reason), via real pleiades validate and pleiades run.
```
