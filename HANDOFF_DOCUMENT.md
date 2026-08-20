# Handoff Document

Rewrite the "Current Status" section when stopping mid-task or handing off, per `.AGENTS/AGENTS.md`.

## Current Status (this session)

**Branch `feature/Filter-Infrastructure-n-CEL-Wiring`, off `main`. HEAD is `3327add`, Phase 54's forge
tuning and 17 validation & business-logic filters, committed by the user themselves between sessions (not
by the assistant; no live go-ahead has been given this session, so this session never ran `git commit`).
Everything below is implemented, tested, and verified on top of that commit, but uncommitted: no such word
has been given yet this session.**

This session opened with two direct requests in sequence: first, whether the last session's filter work
(Phase 54) had surfaced any further forge tuning need, and second, to move on to Phase 55: Time, Date &
Scheduling Filters.

### What landed

**Forge-tuning decision: no change needed, verified rather than assumed.** Before writing any filter
code, all 25 of Phase 55's argument/return shapes were run through the real `pleiades forge new-filter`
CLI: `int`/`string`/`bool` unary and binary overloads, two `map[string]any` + `string` binary overloads
(`isBusinessHour`, `isMaintenanceWindow`), and four arity-three `string, string, int` overloads
(`deltaSeconds`, `deltaDays`, `isOlderThan`, `isExpiringWithin`). Zero errors across all 25 invocations,
confirming Phase 54's own `bindingFuncFor`/`bindingFunc` fix for arity three and up is real, general, and
gets reused correctly a second time (this phase's four three-argument filters are the first genuine reuse
of that fix by a later phase, not just a second occurrence within the same phase that built it).
`internal/forge/filterscaffold` is untouched this session.

**Phase 55: 25 time, date and scheduling filters**, across three files:

- `pkg/filters/timeconvert.go` (12 functions): `EpochToISO8601`/`ISO8601ToEpoch` (the latter's `-1`
  sentinel is a documented realistic-domain restriction -- pre-1970 timestamps aren't real device data --
  not a general "malformed" signal); `FileTimeToEpoch`/`EpochToFileTime` (Windows FileTime, 100ns
  intervals since 1601-01-01), deliberately **total** functions with no sentinel at all: a raw `int` has
  no "malformed" state the way a string does, and a legitimate FileTime predating 1970 produces a
  genuinely negative epoch, so no int value is safely reserved as "never real" the way it is for
  `ISO8601ToEpoch`; `ShiftTimezone` (`time.LoadLocation`); `AddSeconds` (a documented `maxDeltaSeconds`
  bound against `time.Duration`'s int64-nanosecond overflow); `DeltaSeconds`/`DeltaDays` (a required
  `fallback` argument, not a sentinel, because a delta is genuinely signed and small -- Phase 50's
  SafeInt-style contract, reused here for the same underlying reason it exists there); `RoundToHour`
  (floors via `time.Date` reconstruction from the timestamp's own Y/M/D/H fields, **not**
  `time.Time.Truncate(time.Hour)`, a real, proven pitfall for any non-whole-hour UTC offset like India's
  +05:30 -- `TestRoundToHour_NonWholeHourOffset` demonstrates the naive approach actually would have been
  wrong, not just theoretically risky); `HumanizeDuration` (a compact, day-aware format, e.g.
  `"1d2h3m4s"`, distinct from `time.Duration.String()`'s own hour-only rendering); `BootTimeFromUptime`/
  `UptimeFromBootTime`.
- `pkg/filters/calendar.go` (11 functions): `IsPast`/`IsFuture`/`IsOlderThan`/`IsExpiringWithin`, each
  taking an explicit `asOf` reference timestamp rather than reading the wall clock -- PLAN.md Section 36
  requires every filter to be "a pure, deterministic function of its arguments, with no ... execution
  context," and `time.Now()` inside a filter would violate that directly, so there is no `IsPast(iso)`
  single-argument overload and there never will be one; `StartOfDay`/`StartOfWeek`/`StartOfMonth` (floors
  only, matching the checklist's literal "boundary" wording rather than adding an unrequested `EndOf*`
  family); `IsLeapYear`; `DayOfWeek`; `IsBusinessHour` (schedule plus timestamp: `start`/`end` "HH:MM"
  strings inclusive of both ends, an optional `days` list defaulting to Monday-Friday when the key is
  absent entirely but treated as malformed when present with the wrong type, an overnight window
  documented as explicitly unsupported rather than silently wrong); `IsMaintenanceWindow` (window plus
  timestamp: `start`/`end` ISO8601 strings inclusive of both ends).
- `pkg/filters/cron.go` (extended, 2 new functions): `CronNextRun`/`CronPreviousRun`, built on Phase 54's
  parser exactly as that phase's own doc comment anticipated. Two real additions beyond what Phase 54
  left in place: `cronSchedule` gained `domWildcard`/`dowWildcard` bookkeeping (was the day-of-month or
  day-of-week field literally `"*"` in the source text, not merely equivalent-by-value to it) so
  `dateMatches` can OR the two fields together when both are restricted, matching real cron(8)'s own
  well-known day-field quirk rather than a simplified AND reading
  (`TestCronNextRun_DayFieldsUseCronsRealORSemantics` proves the two readings disagree by three months on
  a real example, not a contrived one); and a day-then-minute bounded search
  (`cronSearchBoundDays`, a little over four years) that skips an entire non-matching day in one step
  rather than checking all 1440 of its minutes, so an unsatisfiable expression like `"0 0 31 2 *"`
  (February 31st never exists) terminates in ~68 microseconds instead of looping forever or burning
  millions of minute-checks (`BenchmarkCronNextRun`'s own worst-case subtest measures this directly).

### Read this first

**No commit without the user's own live word in the current conversation.** Unchanged. Nothing has been
asked for yet this session.

**Never use the Agent or Workflow tool to delegate without being asked, even with Ultracode on.**
Unchanged. Held again this session: every forge check, filter, test, and doc change was written directly.

**A pure-function constraint (PLAN.md Section 36's "no execution context") is a real design constraint,
not boilerplate to skim past.** It is what settles, cleanly and without guessing, the one question this
phase could easily have gotten wrong: whether `IsPast`/`IsFuture`/`IsExpiringWithin` read the wall clock
internally or take an explicit reference timestamp. The checklist's own wording for `IsBusinessHour`
("schedule plus timestamp") and `IsMaintenanceWindow` ("window plus timestamp") independently confirms
the same answer -- both are two-argument, neither reaches for `time.Now()`.

**A raw `int` argument and a parseable string argument need different "malformed input" designs, and
conflating them produces an unsafe sentinel.** `IntToIP`/`IPToInt` (Phase 51) use `-1`/`""` sentinels
because their domain is bounded and a value outside it is unambiguously wrong. `ISO8601ToEpoch` reuses
that shape because "predates 1970" is a real, statable, honest domain restriction for this platform's
data. `FileTimeToEpoch`/`EpochToFileTime` do **not** get a sentinel, because their result domain is the
full `int` range with no honest restriction available -- forcing one on would have meant silently
misclassifying a real answer as an error. `DeltaSeconds`/`DeltaDays` don't get one either, for the same
reason, but they do have a real "malformed input" case (an unparseable timestamp string) to signal, so
they take a `fallback` argument instead. Three genuinely different shapes for what looks like one
recurring question ("what do I return on bad input"), each chosen on its own merits rather than picking
one pattern and forcing every function into it.

**`LOCALSTACK_AUTH_TOKEN` must be exported before a full `coverage-check`/`-race` run.** Exported
correctly again this session (`.IGNORE/.localstack.env`'s `token=` field).

**The environment reset mid-session this time** (a background `coverage-check` run and the scratch RULE 0
project both vanished along with the session-scratchpad directory; the docker container survived and was
reused). Real repository file edits were unaffected -- `git status` after the reset showed the identical
13-file diff as before it. The lesson: verify state directly after any gap (`git status`, `docker ps`,
whether a background log file still exists) rather than assuming a prior background command's result is
still available.

**The `examples/webserver_lab` `plain` SSH-container RULE 0 pattern reused cleanly a fourth time.** Same
shape as the last three sessions: `docker compose up -d plain`, a scratch `pleiades init` project,
`add-host`/`add-credential`, a scratch runbook.

### The remainder, in order

Phase 55 is done. Every phase in Part XII from here still depends only on Phase 50's `filtersLib()`
aggregation point:

1. **Phase 56: Security & Cryptography Filters.** Expected rejection named in its own Pattern Entry
   Gate: no function may silently also verify a signature, or be mistakable for verification, when it
   only parses (`ParseJWTPayloadUnverified` is the named risk).
2. **Phase 57: Cloud Provider Data Filters.**
3. **Phase 58: File, Text & Log Filters.** Explicitly excludes a text-diff generator; that is a separate
   future decision, not this phase's tail end.

Skim each phase's own header before starting it rather than assuming a one-line summary is the whole
scope, per this branch's own repeated discipline -- and check whether the forge needs tuning for the new
phase's own argument/return shapes before assuming "probably fine" a second time in a row (this session's
own check took under a minute: batch every shape through the real CLI, read the exit codes).

### Verification state

`go build ./...`, `go vet ./...`, `make fmt` all pass with no output. `make gosec`: 9 pre-existing
individually-waived findings, zero new. `make govulncheck`: 0 vulnerabilities in this module's own code or
imported packages (3 unrelated vulnerabilities in required-but-unused modules, unaffected). `go test
./internal/archtest/...` passes clean. `go run ./tools/gendocs` is idempotent; `go run ./tools/docs-lint`
passes clean (184 files scanned).

RULE 0: built the real `pleiades` binary fresh, brought up `examples/webserver_lab`'s `plain` SSH
container for real, ran `pleiades init`/`add-host`/`add-credential` into a scratch project, wrote a
runbook with one task gated on a five-filter combined `when_cel` condition (`isBusinessHour`,
`isValidCronExpr`, `cronNextRun`, `isPast`, `humanizeDuration`) and a second gated on a deliberately false
one (`isExpiringWithin` outside its window); `pleiades validate` passed clean, `pleiades run` executed the
real task over real SSH ("changed") and skipped the second with the real expression named in the skip
reason. Container torn down afterward; the example's own committed files were never touched.

**Full-repo `go test -race ./...` ran to completion with zero failures (128 packages).**

`go run ./tools/coverage-check` reports **175 packages measured, none below their recorded floor**, with
the token exported. Two `coverage-floor.json` changes, each recorded with a written reason in the file's
own `_comment`: `pkg/filters` **raised** from 99.2 to 99.4 (measured 99.5); `internal/engine` **raised**
from 93.8 to 94.6 (measured 95.0, and unlike Phase 54, every one of this phase's 25 new bindings reached
100% -- none of Phase 55's parameters is `any`-typed, so every "argument not convertible" defensive branch
has a real, constructible failing input this time).

### Commit message

Drafted, not run; nothing beyond `3327add` is committed.

```
feat(engine,filters): Phase 55's 25 time, date & scheduling filters

Two deliverables, per this session's own opening request: decide
whether Phase 54's forge tuning left anything further to do before
Phase 55, then build Phase 55 (PLAN.md Section 36's Part XII, Time,
Date & Scheduling Filters) end to end.

Forge check: no change needed this time. All 25 of this phase's
argument/return shapes (string/int/bool unary and binary, two
map[string]any+string binary overloads, four arity-three
string,string,int overloads) were run through the real
pleiades forge new-filter CLI before any filter was hand-written,
confirming Phase 54's arity-three-plus bindingFunc fix generalizes
and gets reused correctly by a later phase, not just within the
phase that built it. internal/forge/filterscaffold is untouched.

The 25 functions, across three files. pkg/filters/timeconvert.go
(12): EpochToISO8601/ISO8601ToEpoch (the latter's -1 sentinel is a
documented realistic-domain restriction, pre-1970 timestamps are not
real device data, not a general malformed signal); FileTimeToEpoch/
EpochToFileTime, deliberately total functions with no sentinel at
all, since a raw int has no malformed state and a legitimate
pre-1970 FileTime produces a genuinely negative epoch;
ShiftTimezone; AddSeconds, bounded against time.Duration's own
int64-nanosecond overflow; DeltaSeconds/DeltaDays, taking a required
fallback argument rather than a sentinel since a delta is genuinely
signed and small; RoundToHour, which floors via time.Date
reconstruction from the timestamp's own Y/M/D/H fields rather than
time.Time.Truncate(time.Hour), a real, proven pitfall for a
non-whole-hour UTC offset like +05:30; HumanizeDuration, a compact
day-aware format distinct from time.Duration.String()'s own
hour-only rendering; BootTimeFromUptime/UptimeFromBootTime.

pkg/filters/calendar.go (11): IsPast/IsFuture/IsOlderThan/
IsExpiringWithin, each taking an explicit asOf reference timestamp
rather than reading the wall clock, since PLAN.md Section 36
requires every filter to be a pure, deterministic function of its
arguments with no execution context; StartOfDay/StartOfWeek/
StartOfMonth, floors only; IsLeapYear; DayOfWeek; IsBusinessHour
(schedule plus timestamp, an optional days list defaulting to
Monday-Friday, an overnight window documented as unsupported rather
than silently wrong); IsMaintenanceWindow (window plus timestamp).

pkg/filters/cron.go (2 new, extending Phase 54's parser):
CronNextRun/CronPreviousRun. cronSchedule gained domWildcard/
dowWildcard bookkeeping so its day-of-month and day-of-week fields
OR together when both are restricted, matching real cron(8)'s own
well-known day-field behavior rather than a simplified AND reading;
proven against a real example where the two readings disagree by
three months. A day-then-minute bounded search
(cronSearchBoundDays, a little over four years) skips a whole
non-matching day in one step, so an unsatisfiable expression
terminates in microseconds rather than looping forever.

Tests: table-driven per function. Two Adversarial Pattern
Justification proofs the checklist named explicitly:
TestShiftTimezone_RoundTripsAcrossDSTBoundary (two UTC instants
straddling a real DST transition each shift to the correct offset
and shift back exactly) and
TestFileTimeToEpoch_RoundTripsAcrossLeapYearBoundary (a leap day and
the day after it round-trip exactly, with the elapsed seconds
between them proven to be exactly 12 hours, not off by one). A third
proof surfaced organically: TestCronNextRun_DayFieldsUseCronsRealORSemantics.
Six Fuzz targets, one per distinct timestamp-parsing call shape
rather than twenty-five near-identical wrappers around the one
shared parseISO8601 helper, zero panics across hundreds of
thousands of executions each. A benchmark file, including
BenchmarkCronNextRun's own worst-case unsatisfiable-expression
subtest. Every function proven callable through the real,
unmodified engine.NewCELEvaluator()/Program.Eval via a compiled
when_cel expression, plus a combined condition against a realistic
stat payload with a negative control. A whitebox test file exercises
every new binding's "argument not convertible" defensive branch,
including all four three-argument bindings' own wrong-arity cases;
unlike Phase 54, every one of these 25 bindings reaches 100%, since
no Phase 55 parameter is any-typed.

docs/reference/filters/index.md picked up all 25 new entries with
zero hand-written doc changes.

coverage-floor.json: pkg/filters RAISED from 99.2 to 99.4 (measured
99.5). internal/engine RAISED from 93.8 to 94.6 (measured 95.0).

go test -race ./... ran clean across the whole repository (128
packages). go run ./tools/coverage-check reports 175 packages
measured, none below floor, with LOCALSTACK_AUTH_TOKEN exported.
make gosec: 9 pre-existing waived findings, zero new. make
govulncheck: clean. RULE 0: the real pleiades binary, built fresh,
ran a scratch runbook against a real, running examples/webserver_lab
SSH container, gating one real ssh_exec task on a five-filter
combined when_cel condition (true, ran) and a second on a
deliberately false one (skipped, named in the skip reason), via real
pleiades validate and pleiades run.
```
