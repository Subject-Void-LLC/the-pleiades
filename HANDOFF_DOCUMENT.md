# Handoff Document

Rewrite the "Current Status" section when stopping mid-task or handing off, per `.AGENTS/AGENTS.md`.

## Current Status (this session)

**Branch `feature/Filter-Infrastructure-n-CEL-Wiring`, off `main`. HEAD is `5de3f89`, the merge of
PR #19 (`feature/Catalog-First-Tier`, 70 of 77 catalog methods). Everything below is implemented,
tested, and verified on top of that commit, but uncommitted: no such word has been given yet this
session.**

This session opened with a direct pointer at `.SPECIFICATION/IMPLEMENTATION.md`'s Phase 50 ("Filter
Infrastructure & CEL Wiring", Part XII: The Filter Library), on a fresh branch cut for exactly this
task. This is a new initiative, not a continuation of `feature/Catalog-First-Tier`'s own module-catalog
remainder: that branch's PR merged and its own remainder list (S3 object primitives, `DockerCapable`'s
Phase 73 rename, and so on) stays exactly where the prior HANDOFF entries left it, unrelated to what
follows here. Plan mode was used before any code: the Part XII intro and Phase 50's own checklist were
read in full, then the real tree was grepped and read against every claim the checklist makes, per this
project's own standing "check the spec before wiring an interface literally" discipline.

### What the spec check found before writing any code

Four verified deviations from the checklist's own literal wording, none of them a scope narrowing (the
checklist's substance is unchanged), each recorded in `.SPECIFICATION/IMPLEMENTATION.md`'s own Phase 50
closing notes:

1. **The real CEL env declares three variables (`stat`, `nodes`, `vars`), not the one the checklist's
   own prose names.** All three are retained unchanged; `NewCELEvaluator`'s option list was refactored
   into two new exported functions (`CELVariableOptions()`, `CELLibraryOptions()`) rather than touched
   in place.
2. **`base64.encode` takes `bytes` and returns `string`; `base64.decode` takes `string` and returns
   `bytes`.** Both cel-go extension functions are real and reachable, but not symmetric the way their
   names suggest. Documented explicitly on the generated filter reference, since cel-go's own godoc
   does not surface at runtime and nothing else would have caught this for a runbook author.
3. **`ext.Network()` installs a `cel.CustomTypeAdapter` that wraps whatever adapter is already
   configured.** Recorded as an ordering comment in `cel.go`: any future `EnvOption` that also needs a
   custom type adapter must be listed ahead of `ext.Network()` in `CELLibraryOptions()`, or its own
   wrapping gets silently shadowed.
4. **cel-go ships machine-readable function documentation** (`common/decls.FunctionDecl.Documentation()`),
   populated richly by `cel.OptionalTypes()` and not at all by `ext.Network`/`ext.Encoders` as of
   `v0.30.0`. This is what made a zero-hand-typed generated filter reference possible at all; see below.

### What landed

**`pkg/filters`** (`filters.go`, `cast.go`): `SafeInt`/`SafeFloat`/`SafeBool`, each behind a shared
`MaxInputBytes = 4096` cap checked before any parse (justified in its own doc comment against the
widest scalar any later Part XII phase parses, an FQDN at 253 bytes). `SafeBool` accepts
`strconv.ParseBool`'s vocabulary case-insensitively plus `yes`/`no`/`on`/`off`, matching YAML 1.1/
Ansible truthiness and real device CLI output. `SafeFloat` additionally refuses a parsed NaN or
infinity into the fallback, a deliberate divergence from `strconv.ParseFloat` (a NaN silently makes
every downstream comparison false, the opposite of "safe"). All three trim whitespace first (captured
CLI output routinely carries a trailing `\r`). Zero `cel-go` import, zero `internal/` import, matching
`pkg/policy`/`pkg/retry`'s existing shape; `internal/archtest`'s pre-existing
`TestPkgNeverImportsInternal` covers it automatically. 100.0% test coverage.

**`internal/engine/cel_filters.go`**: `filtersLib() cel.EnvOption`, a named `cel.SingletonLibrary`
(`"pleiades.filters"`) registering `filters.safeInt`/`safeFloat`/`safeBool`. Real, verified deviation
from the checklist's own plain-`string`-typed description: the CEL declaration types the first argument
`dyn`, not `string`, converting to a CEL string via `ConvertToType` inside the binding before ever
calling into `pkg/filters` (whose exported Go signatures stay exactly `string`-typed as specified). This
was necessary, not a preference: `stat`/`nodes`/`vars` are all `map(string, dyn)`, so a `string`-typed
declaration would throw a no-such-overload evaluation error the moment a device reported the same field
as a native int on one firmware and a string on the next, defeating the entire premise of a "safe" cast.

**`internal/engine/cel.go`**: `NewCELEvaluator` now builds its `cel.EnvOption` list from
`CELVariableOptions()` (the three pre-existing variable declarations, unchanged) and
`CELLibraryOptions()` (`ext.Network()`, `ext.Encoders()`, `cel.OptionalTypes()`, `filtersLib()`, in that
order), both newly exported so `tools/gendocs` can build the identical environment rather than a
hand-copied one. `Program.Eval`'s bool-only contract is unchanged; a filter is always a sub-expression
feeding a boolean condition.

**`tools/gendocs/filters.go`** (new `generateFilters` step): builds the real baseline `cel.Env` from
`CELVariableOptions()` alone, extends it one `CELLibraryOptions()` entry at a time, and diffs
`cel.Env.Functions()`/`cel.Env.Macros()` **by overload ID, not function name**, after each step. This
matters concretely: `ext.Network()` adds two new overloads to the standard library's own pre-existing
`"string"` conversion function; a name-level diff would have missed both entirely. Every function name,
signature, and example on the generated page (`docs/reference/filters/index.md`) comes from the live,
diffed environment, including `filters.safeInt`/`safeFloat`/`safeBool`'s own `cel.FunctionDocs`/
`cel.OverloadExamples` — there is no second, hand-maintained table anywhere to drift from the real
registration. `docs/reference/index.md` and `docs/reference/task-keys.md`'s `when`/`when_cel` rows now
link to it. Two internal-spec citations (`` `PLAN.md` Section 36 ``) that leaked into the generated
page's own hand-written prose were caught by `docs-lint` and rewritten out before this was done — worth
noting since it is exactly the kind of leak this repository's own lint exists to catch, and it did.

**Tests**: `pkg/filters/cast_test.go` (table-driven), `cast_fuzz_test.go` (`FuzzSafeInt`/`FuzzSafeFloat`/
`FuzzSafeBool`, each asserting the real invariant — fallback or exactly what `strconv` parses, not just
"no panic" — 15s each, zero failures), `cast_bench_test.go`. `internal/engine/cel_filters_test.go`:
every cast filter and every inherited cel-go function proven callable through the real, unmodified
`engine.NewCELEvaluator()`/`Program.Eval` via compiled `when_cel`-shaped expressions, not bare Go calls;
a collision-freedom test paired with a genuine negative control (see below); a cost-limit stress case
chaining filters inside the same nested-comprehension shape `TestCELEngine_RejectsExpensiveComprehension`
already uses, with a positive control proving a realistic filter-chained condition stays well under
`defaultCELCostLimit`; an over-length (5MB) input falling back promptly. `tools/gendocs/filters_test.go`:
a positional-coupling guard (`filterStageLabels()` must match `CELLibraryOptions()`'s own length) and a
content-presence check on the generated page.

**RULE 0 end-to-end proof, beyond the test suite.** Built the real `cmd/pleiades` binary, ran `pleiades
init` into a scratch project, and wrote a runbook with four `when_cel` tasks exercising
`filters.safeInt`, `filters.safeBool`, `cidr()/ip()` (`ext.Network`), and `.orValue()`
(`cel.OptionalTypes`). `pleiades validate` passed clean; `pleiades run` executed it for real:
`tasks[0]: ok`, `tasks[2]: ok`, `tasks[3]: ok`, and `tasks[1]` skipped, reporting the exact expression
responsible: `filters.safeBool("maybe", false)` evaluated false, `"maybe"` correctly falling back to the
caller's own `false` rather than any built-in default. This is the actual path a user takes, not only a
package test.

**A real finding from writing the negative control, worth carrying forward.** The first attempt at the
collision test registered `filters.safeInt`'s exact overload ID a second time and expected `cel.NewEnv`
to reject it — it did not. Reading `common/decls.FunctionDecl.AddOverload` directly showed why:
re-registering the *identical* overload ID with an *identical* signature is cel-go's own documented
idempotent redefinition, not a collision, which is exactly what lets a `SingletonLibrary` be composed
safely. The real collision shape is an *overlapping* signature under a *different* ID, which is what the
test now actually constructs. A control that had not been checked against the real behavior first would
have passed while proving nothing, which is precisely the failure mode `.AGENTS/AGENTS.md`'s "always run
a control first" rule (written for `gopls` queries) also protects against here.

### Read this first

**A branch cut mid-history can reset files that look monotonic.** `HANDOFF_DOCUMENT.md` and
`coverage-floor.json` on this branch reflect the state at PR #19's merge point (through `c21b253`, 70 of
77 methods), not the later, still-uncommitted state a prior session's own HANDOFF entry described (it
mentioned `net.cli.*`/`net.ios.config` at `5003d9a` and a `coverage-floor.json` correction for
`internal/catalog/net/cli`/`net/ios`, neither of which is present here). Nothing is wrong: PR #19 simply
did not include those later, still-uncommitted commits, and this branch was cut from `main` after the
merge. The stale `HANDOFF_DOCUMENT.md` Current Status this session found on disk (the Windows
`svc.windows.*`/`win.feature.*` batch) has been archived to `HANDOFF_ARCHIVE.md` as a new "Previous
session" entry, per `.AGENTS/AGENTS.md`'s own rule, rather than overwritten. A future session resuming
catalog work (as opposed to filter work) should re-check `net.cli`/`net.ios`'s recorded
`coverage-floor.json` values against a fresh measurement rather than assuming that prior correction
still needs applying, since it is not clear from this branch alone whether it landed elsewhere or was
lost.

**Module names are `xxx.xxx.xxx`.** `FAILURE_PATTERNS.md` #158; unaffected here, `filters.*` is a
different, expression-engine-function namespace from an FQCN, and `PLAN.md` Section 36 is explicit that
the two never cross-reference each other.

**No commit without the user's own live word in the current conversation.** Unchanged. Nothing below has
been asked for yet.

**Never use the Agent or Workflow tool to delegate without being asked, even with Ultracode on.**
Unchanged (`pleiades_no_unrequested_delegation`). Held again this session.

**Check the spec before wiring an interface literally.** Held a third time this branch (after the prior
sessions' NETCONF-vs-interactive-CLI and `DockerCapable` corrections): this session's own four verified
findings above are the same pattern, just inside a third-party dependency's real behavior instead of
this repository's own spec tree. The lesson generalizes past `.SPECIFICATION/`: before wiring against
any interface (this repository's own, or a dependency's), read the real declaration, not the checklist's
summary of it.

### The remainder, in order

Phase 50 is done; every phase in Part XII from here depends on it and none of them are started. In the
order `.SPECIFICATION/IMPLEMENTATION.md` lists them:

1. **Phase 51: Network & Addressing Filters.** CIDR/netmask/wildcard-mask conversion, subnet split and
   summarization, `ClassifyIP` (the confirmed `ext.Network` gap: no `isPrivate` anywhere in it), MAC
   address normalization, VLAN/ASN validation, Cisco IOS interface short/long form only (explicitly not
   Junos/Arista: no device type implements either capability today).
2. **Phase 52: Structured Data Filters.** JSON flatten/unflatten, deep/shallow merge, CSV via
   `encoding/csv`, `Pluck`, YAML/JSON conversion, one opinionated XML-to-JSON mapping, `GenerateUUIDv4`.
3. **Phase 53: String, Encoding & Path Filters.**
4. **Phase 54: Validation & Business-Logic Predicate Filters.**
5. **Phase 55: Time, Date & Scheduling Filters.**
6. **Phase 56: Security & Cryptography Filters.**
7. **Phase 57: Cloud Provider Data Filters.**
8. **Phase 58: File, Text & Log Filters.**

Every one of these consumes `filtersLib()` unchanged (no new pattern expected per each phase's own
Pattern Entry Gate) and registers into the same aggregation point this phase built. Skim each phase's
own header before starting it rather than assuming the one-line summary above is the whole scope, per
the discipline restated above.

### Verification state

`go build ./...`, `go vet ./...`, `make fmt` (gofmt-clean) all pass with no output. `make gosec`: 9
pre-existing individually-waived findings, zero new. `make govulncheck`: 0 vulnerabilities in this
module's own code or the packages it imports (3 unrelated vulnerabilities exist in required-but-unused
modules, unaffected). `go test ./internal/archtest/...` passes clean, confirming `pkg/filters` carries
no `internal/` dependency. `go run ./tools/gendocs` is idempotent (verified by running it three times
and hashing output); `git status --porcelain docs/reference` shows only the expected diff (the new
`filters/` page, the reference index link, the two `task-keys.md` rows). `go run ./tools/docs-lint`:
179 files scanned, clean, after the two internal-citation leaks above were fixed.

**Full-repo `go test -race ./...` ran to completion with zero failures across 127 packages** (57 more
report no test files), real containers included (`LOCALSTACK_AUTH_TOKEN` sourced correctly via
`export LOCALSTACK_AUTH_TOKEN=$(cut -d= -f2 .IGNORE/.localstack.env)`, per
`host_system_crashes.md`'s own documented gotcha: the file's key is `token`, not
`LOCALSTACK_AUTH_TOKEN`, and a plain `source`+`export` silently skips every gated test instead of
failing).

`go run ./tools/coverage-check` reports **174 packages measured, none below their recorded floor**.
`pkg/filters` (this session's new package, entered in `coverage-floor.json` at 100.0) is among them, not
in the "no floor recorded yet" informational list; every package in that list is pre-existing, unrelated
to this session's own changes. `internal/engine` measures 93.2%, exactly its recorded floor, no
regression.

### Commit message

Drafted, not run; nothing beyond `5de3f89` is committed.

```
feat(engine): filter infrastructure and CEL wiring (Phase 50)

Every execution primitive this roadmap has built targets a device: a
Collection is a namespaced, capability-gated Task.FQCN dispatched
against an inventory item. There has been no pure value transform
anywhere, no deterministic function of plain arguments with no
device, no capability and no execution context. PLAN.md Section 36
names this gap; this closes the foundation every later filter phase
(51-58) will register into.

CEL is the only reachable path for this: internal/engine/action.go's
builtinActionExecutor.Execute is still a hardcoded two-case switch,
so a Collection-shaped filter family would be exactly as unreachable
as http.request already is. when/when_or/when_cel all compile
through engine.NewCELEvaluator and run today.

pkg/filters (stdlib only, zero cel-go import, zero internal/ import,
matching pkg/policy/pkg/retry's existing shape) adds SafeInt/
SafeFloat/SafeBool: a value that is present but malformed returns a
caller-supplied fallback instead of throwing, the one real gap
cel.OptionalTypes() does not cover (that solves the different,
already-solved missing-value case via ?./.orValue()). All three
share a MaxInputBytes cap checked before parsing, since a custom
cel.Function with no registered cost estimator is charged a flat
cost of one per call regardless of argument size and so cannot be
relied on to catch an attacker-sized string. No hand-written
default/mandatory filter was built: CEL evaluates call arguments
eagerly, so filters.default(stat.missing, y) would still throw
evaluating stat.missing before default ever ran.

internal/engine/cel_filters.go is the sole translation layer,
registering filters.safeInt/safeFloat/safeBool as (dyn, T) -> T CEL
functions: dyn, not the plain string the pkg/filters Go signatures
use, because stat/nodes/vars are all map(string, dyn) and a
string-typed declaration would throw a no-such-overload error the
moment a device reported a field as a native int on one firmware and
a string on the next. internal/engine/cel.go's NewCELEvaluator now
builds its option list from two new exported functions,
CELVariableOptions() and CELLibraryOptions() (ext.Network(),
ext.Encoders(), cel.OptionalTypes(), filtersLib()), so
tools/gendocs can build the identical environment rather than a
hand-copied one.

tools/gendocs/filters.go generates docs/reference/filters/index.md
by diffing the real CEL environment against a bare baseline one
library at a time, by overload ID rather than function name (ext.
Network adds new overloads to the standard library's own
pre-existing "string" function, which a name-level diff would have
missed). Every function name, signature and example on the page
comes from the live, diffed environment, including this phase's own
cel.FunctionDocs/cel.OverloadExamples -- nothing here is a second,
hand-maintained copy that could drift from the real registration.

Tests mirror this package's own established shape: table-driven and
fuzz tests for the three cast functions (100.0% coverage), and
release-gate proof through the real, unmodified engine.
NewCELEvaluator()/Program.Eval via compiled when_cel expressions, not
bare Go calls, for every cast filter and every inherited cel-go
function (ext.Network, ext.Encoders, cel.OptionalTypes). The
collision-freedom test is paired with a genuine negative control: an
identical overload ID re-registered with an identical signature is
cel-go's own documented idempotent redefinition (confirmed by
reading common/decls.FunctionDecl.AddOverload), not a collision, so
the control instead constructs an overlapping signature under a
different ID, the real shape AddOverload's own collision check
exists to catch. A cost-limit stress case chains filters inside the
same nested-comprehension shape TestCELEngine_
RejectsExpensiveComprehension already uses, with a positive control
proving a realistic filter-chained condition stays well under
defaultCELCostLimit.

go test -race ./... ran clean across all 127 packages with real
containers included. go run ./tools/gendocs is idempotent and
git status shows only the expected diff. go run ./tools/docs-lint
passes at 179 files after two internal-spec citations that leaked
into the generated page's own prose were caught and rewritten. make
gosec: 9 pre-existing waived findings, zero new. make govulncheck:
clean.
```
