# Handoff Document

Rewrite the "Current Status" section when stopping mid-task or handing off, per `.AGENTS/AGENTS.md`.

## Current Status (this session)

**Branch `feature/Filter-Infrastructure-n-CEL-Wiring`, off `main`. HEAD is `2e705ea`, the forge
structural-type support and Phase 52 (Structured Data Filters), committed since the prior session's
handoff (not by this session; no live go-ahead was given this session, so this session never ran `git
commit`). Everything below is implemented, tested, and verified on top of that commit, but uncommitted:
no such word has been given yet this session.**

This session opened with a direct request: "Phase 53: String, Encoding & Path Filters next." One
deliverable: `.SPECIFICATION/IMPLEMENTATION.md`'s Part XII, Phase 53, built end to end. No forge change
was needed this time (every one of this phase's 16 functions uses only `string`/`int`/`bool`, all
already well-known since Phase 50), so the forge was used as-is, without modification, to scaffold every
function before it was hand-implemented -- the same discipline the last two sessions established, now
running against a phase that needed nothing new from the tool.

### What landed

**A real Pattern Entry Gate finding, caught before writing any filter code.** The checklist's own next
item, after the expected base64 rejection, lists "string condition predicates: starts-with, ends-with,
contains, regex-match, is-absolute-path, is-empty-or-whitespace" as something to build. Verified directly
against a bare `cel.NewEnv()` with no extensions and no `filtersLib()` at all: `"x".startsWith("y")`,
`"x".endsWith("y")`, `"x".contains("y")`, and `"x".matches("y")` all compile and evaluate today, with
zero Phase 50-53 work of any kind -- these four are part of CEL's own core standard library, not an
extension. Building `filters.startsWith`/`endsWith`/`contains`/`regexMatch` as thin wrappers around
methods already reachable with no `filters.` prefix at all would have been exactly the kind of redundant
work the checklist's own base64 rejection is warning against, just unnamed. Only `IsAbsolutePath` and
`IsEmptyOrWhitespace` -- genuinely absent from core CEL -- were built from that item;
`TestCELFilters_Phase53StringEncodingPathFilters` includes a case proving the four native ones work with
no filter at all, so the finding is asserted, not just narrated.

**Phase 53: 16 string, encoding, and path filters, all scaffolded through the real, unmodified forge,
then hand-implemented and fully tested**, across three category files matching the phase's own natural
groupings: `pkg/filters/encoding.go` (`URLEncode`/`URLDecode` via `net/url.QueryEscape`/`QueryUnescape`,
documented as encoding a space to `+` rather than `%20` since that specific choice is easy to get wrong
silently; `StringToHex`/`HexToString`; `BytesToHuman`/`HumanToBytes`, binary base-1024, lossy above 1024
by the same design `ls -lh`/`du -h` already are), `pkg/filters/stringutil.go` (`CamelToSnake` -- reusing
`internal/forge/filterscaffold`'s own acronym-run algorithm verbatim, duplicated rather than imported
since `pkg/` may not import `internal/` -- `SnakeToCamel`, `MaskSecret`, `RegexExtract` -- named capture
group extraction, RE2 syntax throughout this codebase so a pathological pattern cannot become a
resource-exhaustion vector the way a backtracking engine's could -- and `IsEmptyOrWhitespace`), and
`pkg/filters/path.go` (`WindowsPathToPOSIX`/`POSIXPathToWindows` -- a bare separator swap, deliberately
never touching a drive letter since no single POSIX convention for one exists --
`OctalToSymbolicPerms`/`SymbolicToOctalPerms`, and `IsAbsolutePath`, which checks POSIX, Windows
drive-absolute, and Windows UNC conventions all at once since a device fact this filter gates on may
report either OS's path shape).

`RegexExtract` needed a hand-written binding: it is this codebase's first three-argument filter, and
`internal/forge/filterscaffold`'s `bindingFuncFor` has no typed `OverloadOpt` past arity two (documented
in its own comment as an accepted gap, the same way arity zero was before `GenerateUUIDv4` needed it last
session). The forge scaffolded the real `cel.Function`/`cel.Overload` block with a
`cel.FunctionBinding /* TODO: arity */` placeholder exactly as designed; filled in by hand as a real
`func(...ref.Val) ref.Val` taking a length-3 slice, not invested in as a new generator capability for a
single occurrence.

**Two round-trip pairs, each with one real, documented asymmetry rather than a claimed-perfect
inverse.** `OctalToSymbolicPerms("0755")` comes back from `SymbolicToOctalPerms` as `"755"`, not
`"0755"`: the symbolic form alone cannot distinguish "no special bit, written with a redundant leading
zero" from "no special bit, written the canonical way," so the reverse direction always emits the
shorter, canonical form (the same one `chmod(1)` itself prints). `BytesToHuman`/`HumanToBytes` round-trip
exactly only for a value whose scaled form needs two decimal digits or fewer (every power of 1024, plus
a clean fraction like 1536 bytes = "1.5KiB"); `BytesToHuman(1500)` formats as `"1.46KiB"`, and
`HumanToBytes` of that reconstructs 1495, not 1500 -- the same lossy rounding every human-readable size
formatter in wide use already has. Both asymmetries are stated in the functions' own doc comments and
proven with representative inputs chosen to be exact, not hidden by only testing the exact cases.

**Tests**: table-driven tests per function including the malformed/boundary cases the checklist's own
Adversarial Pattern Justification names (`OctalToSymbolicPerms`/`SymbolicToOctalPerms` and
`BytesToHuman`/`HumanToBytes` round-trip for ten and eight representative inputs respectively). Four
`Fuzz` targets (`FuzzRegexExtract`, `FuzzWindowsPathToPOSIX` -- exercising both path functions and
`IsAbsolutePath` together -- `FuzzOctalToSymbolicPerms`, `FuzzSymbolicToOctalPerms`), 8-9s each, zero
panics across hundreds of thousands of executions.
`internal/engine/cel_filters_stringencoding_test.go`: every one of the 16 functions proven callable
through the real, unmodified `engine.NewCELEvaluator()`/`Program.Eval` via a compiled `when_cel`
expression, plus a combined condition chaining five functions with a negative control.
`internal/engine/cel_filters_internal_test.go` (whitebox) gained the same "argument not convertible"
defensive-branch proof for all 16 new bindings, including `RegexExtract`'s own wrong-arity case.

`pkg/filters` measures 99.0%; every one of this phase's 16 functions reached 100% on its own, so
`coverage-floor.json` was **raised** (not just left alone) from 98.5 to 98.9 -- the direction this file's
own ratchet is supposed to move, and the first time this branch's own sessions have done it rather than
only holding steady or lowering with a justified reason. `internal/engine` measures 94.1%, the same
number Phase 51 measured; left at its existing 93.2 floor unchanged, matching that phase's own decision
not to bump it for an identical reading.

**Documentation Gate closed with zero hand-written doc changes**, the same design bet three sessions
running now: `docs/reference/filters/index.md` picked up all 16 new `filters.*` entries automatically
(55 entries total: 3 from Phase 50, 25 from Phase 51, 11 from Phase 52, these 16), and a second `gendocs`
run produces byte-identical output.

### Read this first

**No commit without the user's own live word in the current conversation.** Unchanged. Nothing has been
asked for yet this session.

**Never use the Agent or Workflow tool to delegate without being asked, even with Ultracode on.**
Unchanged. Held again this session: every filter, test, and doc change was written directly.

**Before building a filter the checklist names, check whether CEL's own core standard library (not just
`ext.Encoders`/`ext.Network`/`cel.OptionalTypes`, which Phase 50 already wired in) already provides it.**
New finding this session, generalizing the base64 lesson Phase 50's own checklist named explicitly:
`startsWith`/`endsWith`/`contains`/`matches` needed no `filtersLib()` work at all, and the checklist's own
wording did not flag that the way it flagged base64. The Pattern Entry Gate's own discipline -- verify
before building, the same way a duplicate-collision test needs a real negative control -- applies to
every item a phase's checklist names, not only the one item the checklist happens to call out by name.
Worth checking again for Phase 54 onward: `ext.Strings()` (`lowerAscii`/`upperAscii`/`trim`/`split`/
`replace`) is real and unwired, in case a later phase's checklist names something it would also make
redundant.

**`LOCALSTACK_AUTH_TOKEN` must be exported before a full `coverage-check`/`-race` run, or two unrelated
AWS packages read as a false regression.** Unchanged from last session's own finding; exported correctly
from the start this session, so it was not re-hit, only re-applied.

**The `examples/webserver_lab` `plain` SSH-container RULE 0 pattern reused cleanly a second time.**
Confirms last session's own note that it was worth carrying forward: `docker compose ... up -d --build
plain`, a scratch `pleiades init` project, `add-host`/`add-credential`, a scratch runbook. No surprises
the second time through; this is now the established RULE 0 fixture for this branch's own filter phases.

### The remainder, in order

Phase 53 is done. Every phase in Part XII from here still depends only on Phase 50's `filtersLib()`
aggregation point, unchanged by this phase (no forge change and no `cel_filters.go` helper change beyond
the new bindings themselves):

1. **Phase 54: Validation & Business-Logic Predicate Filters.** Introduces a hand-rolled 5-field cron
   parser shared with Phase 55.
2. **Phase 55: Time, Date & Scheduling Filters.** Consumes Phase 54's cron parser; distinct from Phase
   23's own RRULE scheduler (a value transform over a string, never this platform's own scheduling
   mechanism).
3. **Phase 56: Security & Cryptography Filters.** Expected rejection named in its own Pattern Entry
   Gate: no function may silently also verify a signature, or be mistakable for verification, when it
   only parses.
4. **Phase 57: Cloud Provider Data Filters.**
5. **Phase 58: File, Text & Log Filters.**

Skim each phase's own header before starting it rather than assuming a one-line summary is the whole
scope, per this branch's own repeated discipline -- and check for an already-available CEL/ext capability
before building a checklist item from scratch, per this session's own finding above.

### Verification state

`go build ./...`, `go vet ./...`, `make fmt` all pass with no output. `make gosec`: 9 pre-existing
individually-waived findings, zero new. `make govulncheck`: 0 vulnerabilities in this module's own code
or imported packages (3 unrelated vulnerabilities in required-but-unused modules, unaffected). `go test
./internal/archtest/...` passes clean (no new `text/template`-importing package this session, so no
allowlist change needed). `go run ./tools/gendocs` is idempotent; `go run ./tools/docs-lint` passes at
182 files.

RULE 0: built the real `pleiades` binary fresh, brought up `examples/webserver_lab`'s `plain` SSH
container for real, ran `pleiades init`/`add-host`/`add-credential` into a scratch project, wrote a
runbook with one task gated on a five-filter combined `when_cel` condition (`isEmptyOrWhitespace`,
`isAbsolutePath`, `camelToSnake`, `octalToSymbolicPerms`, `regexExtract`) and a second gated on a
deliberately false one (`humanToBytes("1KiB") == 2048`); `pleiades validate` passed clean, `pleiades run`
executed the real task over real SSH ("changed") and skipped the second with the real expression named
in the skip reason. Container torn down afterward; the example's own committed files were never touched.

**Full-repo `go test -race ./...` ran to completion with zero failures across 128 packages**, real
containers included (`LOCALSTACK_AUTH_TOKEN` sourced as described above).

`go run ./tools/coverage-check` reports **175 packages measured, none below their recorded floor**, with
the token exported. One `coverage-floor.json` change, recorded with a written reason in the file's own
`_comment`: `pkg/filters` **raised** from 98.5 to 98.9 (measured 99.0). `internal/engine` measures 94.1%,
above its existing 93.2 floor; left unchanged, matching Phase 51's own decision at the identical reading.

### Commit message

Drafted, not run; nothing beyond `2e705ea` is committed.

```
feat(engine,filters): Phase 53's 16 string, encoding & path filters

Phase 53 (String, Encoding & Path Filters, PLAN.md Section 36's Part
XII) built end to end. No forge change was needed: every one of this
phase's functions uses only string/int/bool, all already well-known
since Phase 50, so internal/forge/filterscaffold was used as-is to
scaffold every function before it was hand-implemented, the same
discipline the last two sessions established.

A real Pattern Entry Gate finding, caught before writing any filter
code: the checklist's own next item, after the expected base64
rejection, names starts-with/ends-with/contains/regex-match as string
condition predicates to build. Verified directly against a bare
cel.NewEnv() with no extensions and no filtersLib() at all:
"x".startsWith("y"), "x".endsWith("y"), "x".contains("y"), and
"x".matches("y") all compile and evaluate today with zero Phase 50-53
work -- these four are part of CEL's own core standard library, not an
extension the checklist's own wording happened to flag the way it
flagged base64. Building filters.startsWith/endsWith/contains/
regexMatch as thin wrappers around methods already reachable with no
filters. prefix at all would have been exactly the kind of redundant
work the base64 rejection warns against, just unnamed. Only
IsAbsolutePath and IsEmptyOrWhitespace, genuinely absent from core
CEL, were built from that item; the release-gate test includes a case
proving the four native ones work with no filter at all.

The 16 functions, across three category files matching this phase's
own natural groupings: pkg/filters/encoding.go (URLEncode/URLDecode
via net/url.QueryEscape/QueryUnescape, documented as encoding a space
to + rather than %20; StringToHex/HexToString; BytesToHuman/
HumanToBytes, binary base-1024, lossy above 1024 by the same design
ls -lh/du -h already are), pkg/filters/stringutil.go (CamelToSnake --
reusing internal/forge/filterscaffold's own acronym-run algorithm
verbatim, duplicated rather than imported since pkg/ may not import
internal/ -- SnakeToCamel, MaskSecret, RegexExtract -- named capture
group extraction, RE2 syntax so a pathological pattern cannot become a
resource-exhaustion vector -- and IsEmptyOrWhitespace), and
pkg/filters/path.go (WindowsPathToPOSIX/POSIXPathToWindows -- a bare
separator swap, never touching a drive letter -- OctalToSymbolicPerms/
SymbolicToOctalPerms, and IsAbsolutePath, checking POSIX, Windows
drive-absolute, and Windows UNC conventions all at once).

RegexExtract needed a hand-written binding: this codebase's first
three-argument filter, and filterscaffold's bindingFuncFor has no
typed OverloadOpt past arity two (an accepted, documented gap, the
same way arity zero was before GenerateUUIDv4 needed it last session).
The forge scaffolded the real cel.Function/cel.Overload block with a
cel.FunctionBinding /* TODO: arity */ placeholder exactly as designed;
filled in by hand as a real func(...ref.Val) ref.Val taking a
length-3 slice.

Two round-trip pairs, each with one real, documented asymmetry:
OctalToSymbolicPerms("0755") comes back from SymbolicToOctalPerms as
"755", not "0755" (the symbolic form cannot distinguish a redundant
leading zero from the canonical form, so the reverse always emits the
shorter one, the same one chmod(1) itself prints).
BytesToHuman/HumanToBytes round-trip exactly only for a value whose
scaled form needs two decimal digits or fewer; BytesToHuman(1500)
formats as "1.46KiB", and HumanToBytes of that reconstructs 1495, the
same lossy rounding every human-readable size formatter already has.
Both are stated in the functions' own doc comments and proven with
representative inputs chosen to be exact, not hidden.

Tests: table-driven tests per function including this phase's own
named round-trip cases. Four Fuzz targets, zero panics across hundreds
of thousands of executions. Every function proven callable through the
real, unmodified engine.NewCELEvaluator()/Program.Eval via a compiled
when_cel expression, plus a five-function combined condition with a
negative control. A whitebox test file exercises every one of the 16
new bindings' "argument not convertible" defensive branch, including
RegexExtract's own wrong-arity case. docs/reference/filters/index.md
picked up all 16 new entries with zero hand-written doc changes (55
total).

coverage-floor.json: pkg/filters RAISED from 98.5 to 98.9 (measured
99.0; every one of this phase's 16 functions reached 100% on its own).
internal/engine measures 94.1%, the same reading Phase 51 got, left at
its existing 93.2 floor unchanged to match that decision.

go test -race ./... ran clean across all 128 packages. go run
./tools/coverage-check reports 175 packages measured, none below
floor, with LOCALSTACK_AUTH_TOKEN exported. make gosec: 9 pre-existing
waived findings, zero new. make govulncheck: clean. RULE 0: the real
pleiades binary, built fresh, ran a scratch runbook against a real,
running examples/webserver_lab SSH container, gating one real
ssh_exec task on a five-filter combined when_cel condition (true, ran)
and a second on a deliberately false one (skipped, named in the skip
reason), via real pleiades validate and pleiades run.
```
