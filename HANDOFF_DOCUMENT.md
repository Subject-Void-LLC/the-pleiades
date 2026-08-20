# Handoff Document

Rewrite the "Current Status" section when stopping mid-task or handing off, per `.AGENTS/AGENTS.md`.

## Current Status (this session)

**Branch `feature/Filter-Infrastructure-n-CEL-Wiring`, off `main`. HEAD is `2e705ea`, the forge
structural-type support and Phase 52 (Structured Data Filters), committed since a prior session's
handoff (not by this session; no live go-ahead was given this session, so this session never ran `git
commit`). Everything below is implemented, tested, and verified on top of that commit, but uncommitted:
no such word has been given yet this session.**

This session opened with a direct request to decide whether the forge needed tuning before Phase 54,
then build Phase 54 itself. Two deliverables, in order: a real, load-bearing `internal/forge/filterscaffold`
upgrade (not a "no changes needed" like Phase 53), and Phase 54 (`.SPECIFICATION/IMPLEMENTATION.md`'s
Part XII) built through it, end to end.

### What landed

**A real forge gap found and fixed before writing any filter code.** Phase 54's checklist names
`FilterListByKV`/`ExcludeListByKV`, both three-argument filters (`list`, `key`, `value`) -- the second and
third three-argument filter this codebase has ever needed, after Phase 53's `RegexExtract`. Reading
`internal/forge/filterscaffold/generate.go`'s `bindingFunc` before writing the two Phase 54 functions by
hand a second time (the way `RegexExtract` was) surfaced a real, previously undiscovered bug: for arity
three and above, the generator emitted a `*Binding` function signature with individual named
`argN ref.Val` parameters (`func fooBinding(arg0 ref.Val, arg1 ref.Val, arg2 ref.Val) ref.Val`), but
`cel.FunctionBinding`'s real Go type is `func(...ref.Val) ref.Val`, a variadic slice -- the two signatures
do not satisfy each other, so the generated stub would not even compile as the value `cel.FunctionBinding`
requires, which is exactly why `RegexExtract`'s binding needed to be hand-rewritten from scratch rather
than filled in from the scaffold, undocumented as a real gap until this session read the generator's own
source looking for it. `bindingFuncFor`/`bindingFunc` were fixed to generate the real, proven-in-Phase-53
shape for arity three and above: a real `args ...ref.Val` parameter, a generated arity check
(`if len(args) != N { return types.NewErr(...) }`), and indexed access (`args[0]`, `args[1]`, ...) instead
of individual names. Verified with `Reminder()`'s own output for a three-argument config before writing
either real Phase 54 function, byte-for-byte matching the shape `RegexExtract`'s own hand-written binding
already used.

**A second, smaller forge gap: no well-known type for "an arbitrary CEL value used as a plain value,"
not a structured document.** `FilterListByKV`/`ExcludeListByKV`/`ListContains` all take one argument
compared for equality against a `dyn`-typed map/list element (a device fact's own value, of unknown Go
type at compile time) -- not a string being safely cast (Phase 50's cast filters' own `dyn`-in/`string`-
in-Go pattern doesn't fit; the value here isn't being parsed *from* a string) and not a document being
walked (Phase 52's `celToMap`/`celToDynList`/`celToMapList`). `wellKnownCELTypes` gained `"any"` ->
`cel.DynType`, `conversionFor("any")` reuses `internal/engine/cel_filters.go`'s own `celToAny` (already
built for Phase 52's internals, never exposed as a top-level well-known type before), and `exampleArg`
gained a case for it. No `wrapperFor("any")` was added: nothing in Phase 54 returns a bare `dyn` value,
and building an unused wrapper would be exactly the unrequested-abstraction Phase 53's own single-occurrence
`RegexExtract` decision already argues against.

**Phase 54: 17 validation and business-logic filters, all scaffolded through the newly-tuned forge, then
hand-implemented and fully tested**, across four category files matching the phase's own natural
groupings: `pkg/filters/validate.go` (`IsValidFQDN` -- a small, hand-rolled RFC 1035/1123 label-rule
parser, since there is no single stdlib primitive for this; `IsValidEmail` -- reuses the real
`net/mail.ParseAddress`, additionally requiring no display name and an exact address-string round-trip
to reject a decorated RFC 5322 mailbox that isn't UPN-shaped; `IsValidUUID` via `github.com/google/uuid`
(already a dependency, from `GenerateUUIDv4`); `IsValidBase64` via `encoding/base64.StdEncoding`,
deliberately treated as document-shaped (`MaxStructuredInputBytes`, 1 MiB) since a real base64 payload
routinely exceeds the flat-scalar cap; `IsValidJSON` via the stdlib's own `encoding/json.Valid`;
`IsValidYAML` via the same `go.yaml.in/yaml/v3.Unmarshal` call `YAMLToJSON` already uses; `IsValidPort`
taking a Go `int` directly, matching `ValidateVLAN`/`ValidateASN`'s own established numeric-predicate
convention rather than a string), `pkg/filters/collection.go` (`DropEmptyValues`; `FilterListByKV`/
`ExcludeListByKV`, an exact partition of a list with no overlap and no gap, proven as a real test
invariant, not just asserted in prose; `ListContains`; `HasMandatoryTags`, returning the missing keys, not
just a bool; `ListIntersect`/`ListDiff`, deliberately non-deduplicating -- `DedupeByKey` is the checklist's
own separate, explicit operation for that; `DedupeByKey`; and a shared `valuesEqual`/`toFloat64` pair so a
device fact decoded through JSON as `float64` still compares equal to a runbook literal typed as a CEL
`int`, which `reflect.DeepEqual` alone would silently never do), `pkg/filters/semver.go` (`CompareSemVer`,
lenient by design since it has no fallback argument to return on malformed input the way the cast filters
do; a real, tested asymmetry: a non-numeric byte before the version core ends truncates everything from
that point on, not just the one component containing it, so `"1.x.3"` compares as `"1"`, not as `"1.0.3"`
with only its middle component zeroed), and `pkg/filters/cron.go` (`IsValidCronExpr`, backed by a small,
hand-rolled 5-field cron parser -- `parseCronExpr`/`parseCronField`/`splitCronStep`/`parseCronRange` --
built to be reused unchanged by Phase 55's still-unbuilt `CronNextRun`/`CronPreviousRun`, per the
checklist's own instruction; explicitly does not accept the named shorthands some cron implementations add
(`@daily`), staying deliberately small).

**A real dead-code finding during coverage work, fixed by deletion rather than by fabricating a test for
it.** `parseCronField`'s own `if len(set) == 0 { return nil, error }` check, after its main loop, can never
actually fire: by the time that line is reached, `lo <= hi` is already guaranteed (`parseCronRange` itself
refuses `lo > hi`) and `step >= 1` is already guaranteed (checked immediately above), so the loop
`for v := lo; v <= hi; v += step` always executes at least once. Coverage measurement caught it as the
one line the phase's otherwise-100%-per-function tests couldn't reach; rather than inventing an input to
exercise unreachable code (the project's own stated principle against handling scenarios that can't
happen), the dead branch was deleted.

**Tests**: table-driven tests per function, including the checklist's own Adversarial Pattern
Justification requirement made concrete as four real tests (`TestIsValidJSON_AgreesWithRealParser`,
`_YAML`, `_UUID`, `_Base64`), each running a shared corpus of well-formed and malformed input through both
the filter and the real stdlib/library parser it delegates to and asserting they agree on every input, not
only the cases the table above happens to cover. Two `Fuzz` targets (`FuzzIsValidCronExpr`, plus six more
across `IsValidFQDN`/`Email`/`UUID`/`Base64`/`JSON`/`YAML`), zero panics. A benchmark file
(`BenchmarkIsValidCronExpr`, `BenchmarkIsValidJSON`, `BenchmarkIsValidYAML`, `BenchmarkFilterListByKV`
against a 500-element realistic device-inventory slice), the Release Gate's own required-benchmark item.
`internal/engine/cel_filters_validation_test.go`: every one of the 17 functions proven callable through
the real, unmodified `engine.NewCELEvaluator()`/`Program.Eval` via a compiled `when_cel` expression, plus
a combined condition chaining five functions against a realistic `stat` payload with a negative control.
`internal/engine/cel_filters_internal_test.go` (whitebox) gained the same "argument not convertible"
defensive-branch proof for this phase's bindings, including both three-argument filters' own wrong-arity
cases -- documented explicitly where it does *not* apply: `celToAny`'s own default branch (`v.Value(),
true`) succeeds for every real `ref.Val` cel-go can produce, so `FilterListByKV`/`ExcludeListByKV`/
`ListContains`'s own `value`-parameter "not convertible" branch has no constructible failing input, the
same category of structurally-present-but-unreachable-in-practice gap `TestCelToXHelpers` already
documents for `celToInt`/`celToBool`'s `ConvertToType` path.

`pkg/filters` measures 99.3%; every one of this phase's 17 functions (and their own unexported helpers)
reached 100% on its own, so `coverage-floor.json` was **raised** from 98.9 to 99.2. `internal/engine`
measures 94.4%, up from Phase 51's and Phase 53's own identical 94.1% reading (both left unraised at the
time because the number hadn't moved) -- this time it genuinely had, so `coverage-floor.json` was
**raised** here too, from 93.2 to 93.8, both with written justifications in the file's own `_comment`.

**Documentation Gate closed with zero hand-written doc changes**, the same design bet four sessions
running now: `docs/reference/filters/index.md` picked up all 17 new `filters.*` entries automatically, and
a second `gendocs` run produces byte-identical output.

### Read this first

**No commit without the user's own live word in the current conversation.** Unchanged. Nothing has been
asked for yet this session.

**Never use the Agent or Workflow tool to delegate without being asked, even with Ultracode on.**
Unchanged. Held again this session despite a system-level "Ultracode is on" reminder: every forge change,
filter, test, and doc change was written directly.

**Reading a scaffolder's own generator source before hand-writing a second occurrence of the same shape
is what surfaces a real, previously-undiscovered bug -- narrating "the forge doesn't support this yet" a
second time without checking why is a missed generalization opportunity.** This session's actual finding:
Phase 53 treated `RegexExtract`'s hand-written three-argument binding as a one-off exactly because nothing
forced a second look at *why* the generator couldn't produce it; Phase 54 needing two more three-argument
filters was the second data point that made generalizing worth it, and reading `bindingFunc` directly (not
just re-deriving the shape by hand a second time) is what found that the generator's arity-three-plus
output would not even have compiled as `cel.FunctionBinding`, not merely that it needed a `TODO` filled in.

**Before building a filter the checklist names, check whether an existing internal helper already covers
the "arbitrary dyn value" case before assuming a new well-known type is needed.** `celToAny` already
existed (Phase 52, for `celToMap`/`celToDynList`/`celToMapList`'s own internals) before this session
exposed it as `wellKnownCELTypes["any"]`; the check that mattered was "does the conversion already exist
and is it correct," not "invent one."

**`LOCALSTACK_AUTH_TOKEN` must be exported before a full `coverage-check`/`-race` run**, or two unrelated
AWS packages read as a false regression. Exported correctly from the start this session.

**The `examples/webserver_lab` `plain` SSH-container RULE 0 pattern reused cleanly a third time.** Same
shape as the last two sessions: `docker compose up -d plain`, a scratch `pleiades init` project,
`add-host`/`add-credential`, a scratch runbook. No surprises.

### The remainder, in order

Phase 54 is done. Every phase in Part XII from here still depends only on Phase 50's `filtersLib()`
aggregation point and, from this phase on, its own `parseCronExpr`/`cronSchedule` (unexported, ready for
Phase 55 to consume unchanged):

1. **Phase 55: Time, Date & Scheduling Filters.** Consumes Phase 54's cron parser (`cronSchedule`'s own
   doc comment already states the intent: `CronNextRun`/`CronPreviousRun` walk it one candidate minute at
   a time); distinct from Phase 23's own RRULE scheduler (a value transform over a string, never this
   platform's own scheduling mechanism).
2. **Phase 56: Security & Cryptography Filters.** Expected rejection named in its own Pattern Entry
   Gate: no function may silently also verify a signature, or be mistakable for verification, when it
   only parses.
3. **Phase 57: Cloud Provider Data Filters.**
4. **Phase 58: File, Text & Log Filters.**

Skim each phase's own header before starting it rather than assuming a one-line summary is the whole
scope, per this branch's own repeated discipline -- and, per this session's own finding, read a
scaffolder's own generator source (not just its output) before assuming "no forge change needed" a second
time in a row.

### Verification state

`go build ./...`, `go vet ./...`, `make fmt` all pass with no output. `make gosec`: 9 pre-existing
individually-waived findings, zero new. `make govulncheck`: 0 vulnerabilities in this module's own code or
imported packages (3 unrelated vulnerabilities in required-but-unused modules, unaffected). `go test
./internal/archtest/...` passes clean. `go run ./tools/gendocs` is idempotent; `go run ./tools/docs-lint`
passes clean.

RULE 0: built the real `pleiades` binary fresh, brought up `examples/webserver_lab`'s `plain` SSH
container for real, ran `pleiades init`/`add-host`/`add-credential` into a scratch project, wrote a
runbook with one task gated on a five-filter combined `when_cel` condition (`isValidFQDN`, `isValidPort`,
`hasMandatoryTags`, `compareSemVer`, `isValidCronExpr`) and a second gated on a deliberately false one
(`isValidPort(99999)`); `pleiades validate` passed clean, `pleiades run` executed the real task over real
SSH ("changed") and skipped the second with the real expression named in the skip reason. Container torn
down afterward; the example's own committed files were never touched.

**Full-repo `go test -race ./...` ran to completion with zero failures.**

`go run ./tools/coverage-check` reports **175 packages measured, none below their recorded floor**, with
the token exported. Two `coverage-floor.json` changes, each recorded with a written reason in the file's
own `_comment`: `pkg/filters` **raised** from 98.9 to 99.2 (measured 99.3); `internal/engine` **raised**
from 93.2 to 93.8 (measured 94.4).

### Commit message

Drafted, not run; nothing beyond `2e705ea` is committed.

```
feat(engine,filters): Phase 54's forge tuning and 17 validation & business-logic filters

Two deliverables, per this session's own opening request: decide
whether the forge needed tuning before Phase 54, then build Phase 54
(PLAN.md Section 36's Part XII, Validation & Business-Logic
Predicates) through it, end to end.

The forge tuning was real, not a "no changes needed" like Phase 53.
Phase 54's checklist names FilterListByKV/ExcludeListByKV, this
codebase's second and third three-argument filters after Phase 53's
RegexExtract. Reading internal/forge/filterscaffold/generate.go's
bindingFunc before hand-writing a three-argument binding a second
time surfaced a real bug: for arity three and up, the generator wrote
individual named "argN ref.Val" parameters, but cel.FunctionBinding's
real Go type is func(...ref.Val) ref.Val, a variadic slice -- the two
signatures do not satisfy each other, so the generated stub would not
even have compiled as a cel.FunctionBinding value. This is exactly
why RegexExtract needed hand-rewriting from scratch rather than
filling in a TODO, previously undocumented as a real gap.
bindingFuncFor/bindingFunc now generate the real, arity-three-plus
shape directly: "args ...ref.Val", a generated arity check, and
indexed access, verified against Reminder()'s own output before
either real Phase 54 function was written, matching RegexExtract's
own hand-written shape byte for byte. A second, smaller gap:
wellKnownCELTypes gained "any" -> cel.DynType (for an arbitrary dyn
value compared for equality against a device fact, not a string being
cast and not a document being walked), reusing cel_filters.go's own
celToAny, which Phase 52 had already built but never exposed as a
top-level well-known type.

The 17 functions, across four category files matching this phase's
own natural groupings: pkg/filters/validate.go (IsValidFQDN -- a
small, hand-rolled RFC 1035/1123 label-rule parser, no stdlib
primitive exists for this; IsValidEmail -- the real
net/mail.ParseAddress, additionally requiring no display name and an
exact address round-trip, to reject a decorated RFC 5322 mailbox that
is not UPN-shaped; IsValidUUID via github.com/google/uuid;
IsValidBase64 via encoding/base64.StdEncoding, treated as
document-shaped since a real payload routinely exceeds the flat-scalar
cap; IsValidJSON via encoding/json.Valid; IsValidYAML via the same
go.yaml.in/yaml/v3.Unmarshal call YAMLToJSON already uses; IsValidPort
taking a Go int directly, matching ValidateVLAN/ValidateASN's own
convention), pkg/filters/collection.go (DropEmptyValues;
FilterListByKV/ExcludeListByKV, proven to exactly partition a list
with no overlap and no gap; ListContains; HasMandatoryTags, returning
the missing keys, not just a bool; ListIntersect/ListDiff,
deliberately non-deduplicating since DedupeByKey is the checklist's
own separate operation for that; DedupeByKey; and a shared
valuesEqual/toFloat64 pair so a JSON-decoded float64 device fact still
compares equal to a CEL int literal), pkg/filters/semver.go
(CompareSemVer, lenient by design with no fallback argument; a real,
tested asymmetry where a non-numeric byte truncates everything after
it, not just its own component), and pkg/filters/cron.go
(IsValidCronExpr, backed by a small hand-rolled 5-field cron parser
built to be reused unchanged by Phase 55's still-unbuilt
CronNextRun/CronPreviousRun).

A real dead-code finding during coverage work: parseCronField's own
"no values matched" check after its main loop can never fire, since
lo<=hi and step>=1 are both already guaranteed by that point in the
function. Fixed by deleting the unreachable branch rather than
fabricating a test for it.

Tests: table-driven tests per function. The checklist's own
Adversarial Pattern Justification made concrete as four real tests
proving IsValidJSON/YAML/UUID/Base64 agree with the real stdlib/
library parser each delegates to, across a shared corpus, not just
the cases in each function's own table. Eight Fuzz targets, zero
panics. A benchmark file, including FilterListByKV against a
500-element realistic device-inventory slice. Every function proven
callable through the real, unmodified engine.NewCELEvaluator()/
Program.Eval via a compiled when_cel expression, plus a five-function
combined condition against a realistic stat payload with a negative
control. A whitebox test file exercises every new binding's "argument
not convertible" defensive branch, including both three-argument
filters' own wrong-arity cases, and documents where the branch is
provably unreachable (celToAny's own default branch succeeds for
every real ref.Val, so the three dyn-value-parameter branches have no
constructible failing input).

docs/reference/filters/index.md picked up all 17 new entries with
zero hand-written doc changes.

coverage-floor.json: pkg/filters RAISED from 98.9 to 99.2 (measured
99.3; every one of this phase's 17 functions reached 100% on its
own). internal/engine RAISED from 93.2 to 93.8 (measured 94.4, up
from Phase 51's and Phase 53's own identical 94.1 reading that had
been left unraised because it had not moved).

go test -race ./... ran clean across the whole repository. go run
./tools/coverage-check reports 175 packages measured, none below
floor, with LOCALSTACK_AUTH_TOKEN exported. make gosec: 9
pre-existing waived findings, zero new. make govulncheck: clean.
RULE 0: the real pleiades binary, built fresh, ran a scratch runbook
against a real, running examples/webserver_lab SSH container, gating
one real ssh_exec task on a five-filter combined when_cel condition
(true, ran) and a second on a deliberately false one (skipped, named
in the skip reason), via real pleiades validate and pleiades run.
```
