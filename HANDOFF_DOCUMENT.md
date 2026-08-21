# Handoff Document

Rewrite the "Current Status" section when stopping mid-task or handing off, per `.AGENTS/AGENTS.md`.

## Current Status (this session)

**Branch `feature/Filter-Infrastructure-n-CEL-Wiring`, off `main`. HEAD is `e31dbe2` (the CI
`LOCALSTACK_AUTH_TOKEN` wiring fix), committed by the user themselves between sessions (not by the
assistant; no live go-ahead has been given this session, so this session never ran `git commit`).
Everything below is implemented, tested, and verified on top of that commit, but uncommitted: no such
word has been given yet this session.**

This session opened with the same two-part request as the last several: whether Phase 57's work had
surfaced any further forge tuning need, and to move on to Phase 58: File, Text & Log Filters.

### What landed

**Forge-tuning decision: one real, needed addition, verified rather than assumed.** Phase 58 needed
`GzipCompress(content string) []byte` and `GzipDecompress(data []byte) string` -- CEL bytes, not a CEL
string, since gzip's compressed output is arbitrary binary data, not necessarily valid UTF-8. `[]byte`/
`cel.BytesType` was not yet a well-known shape: running both directions through the real
`pleiades forge new-filter` CLI before writing any code produced unfilled `// TODO` conversion/wrap
stubs on both sides, exactly the same pre-tuning gap Phases 52 and 54 found for their own new shapes.
Added `"[]byte": "cel.BytesType"` to `internal/forge/filterscaffold`'s `wellKnownCELTypes` table, plus
matching cases in `conversionFor`/`wrapperFor`/`exampleArg`, and a new `celToBytes`/`wrapBytes` pair in
`internal/engine/cel_filters.go` mirroring `celToStringList`/`wrapStringList`'s own shape. Verified
against the real CLI a second time afterward: both directions now generate complete code with zero
TODOs. Also verified directly against cel-go's own source (`common/types/provider.go`'s `NativeToValue`
switch, `bytes.go`'s `Bytes.ConvertToNative`) before relying on either conversion direction, rather than
assuming a Go `[]byte` round-trips through `types.DefaultTypeAdapter` correctly. Every other function
this phase needed (`PathJoin` returning `[]string -> string`, `SyslogParse` returning
`string -> map[string]any`, `PayloadChunker` returning `[]any -> []any` with each element itself a
`[]any` chunk) reused an already-well-known shape; `PayloadChunker`'s own nested-list case was verified
directly against a real `cel.Program` (indexing and `.size()` at both list levels) before relying on it,
since no prior phase had put a `[]any` *inside* a `[]any` before.

**Phase 58: 8 file, text & log filters**, across three files:

- `pkg/filters/logtext.go` (new, 4 functions): `SyslogParse` (RFC 5424 and legacy RFC 3164, sharing one
  key set across both formats -- `format`, `facility`, `severity`, `version`, `timestamp`, `hostname`,
  `app_name`, `proc_id`, `msg_id`, `structured_data`, `message` -- with only the `<PRI>` prefix as a hard
  parse gate; everything after it degrades field by field rather than failing the whole line, since RFC
  3164 is a legacy, loosely followed convention in real logs. RFC 5424's own NILVALUE `"-"` is passed
  through verbatim, never translated to `""`. STRUCTURED-DATA comes back as raw bracketed text, not
  decoded into SD-PARAM pairs -- the checklist names RFC 5424 parsing, not a second grammar on top of
  it); `LineEndingConvert` (lf/crlf, case-insensitive style); `TrimNormalizeWhitespace`; `PayloadChunker`
  (see the forge-tuning note above for its `[]any`-of-`[]any` shape).
- `pkg/filters/compress.go` (new, 2 functions): `GzipCompress`/`GzipDecompress`, stdlib `compress/gzip`,
  strictly in-memory (`bytes.Buffer`/`bytes.Reader`, never a temp file). `GzipDecompress` caps its own
  *output*, not just its input -- see the real finding below.
- `pkg/filters/path.go` (2 functions appended to the existing file): `PathJoin` (POSIX-style, via stdlib
  `path.Join` rather than `path/filepath.Join`, so the result cannot vary by the platform pleiades
  itself was compiled for -- the same reasoning `IsAbsolutePath`'s own doc comment already gives for
  avoiding `path/filepath.IsAbs`); `PathExtractExtension` (matches stdlib `path.Ext`'s own semantics
  exactly, including its "a dotfile's whole name is its extension" edge case, deliberately not
  reinvented as a different convention).

**A real Schema/Injection Hardening finding, fixed and recorded, not just checked off.** A first-draft
`GzipDecompress` bounded its own input (`MaxStructuredInputBytes`, reused rather than a new phase-
specific bound -- this is document-shaped content, the same reasoning that constant's own doc comment
already gives) but not its *output*. Gzip allows extreme compression ratios for pathological input, so a
small, well-within-cap compressed value can still decompress into an unbounded allocation -- a
decompression bomb. Caught by a deliberate adversarial test
(`TestGzipDecompress/decompression_bomb_refused`, a real 64 MiB payload compressing to well under the 1
MiB input cap), fixed with a new `maxGzipDecompressedBytes` (16 MiB) cap enforced via `io.LimitReader`,
and recorded as `FAILURE_PATTERNS.md` entry 163 before the Schema/Injection Hardening box was checked.

**Coverage needed no floor adjustment this phase -- a genuine change from every prior phase in this
Part.** The initial full run measured `pkg/filters` at 98.8%, just under the 98.9% floor Phase 57 had
recorded. Six real, reachable branches were missing a test case (a short RFC 5424 line missing trailing
fields, no content after MSGID, a malformed line with no SD marker at all, an RFC 3164 line with no
`": "` tag/message separator, plus the two SD-scanner branches for an empty tail and a non-bracket,
non-dash tail) -- all six got real new test cases. What remained after that is exactly one documented,
source-verified-unreachable guard: `strconv.Atoi` on `rfc5424VersionPattern`'s own capture group
(`[1-9][0-9]{0,2}`, 1-3 digits, max value 999) can never actually fail. Measured 99.0% after the real
fixes, comfortably above the existing 98.9% floor -- `coverage-floor.json` was **not** touched.
`internal/engine` measured 95.5%, also comfortably above its existing 95.2% floor, likewise untouched.

### Read this first

**No commit without the user's own live word in the current conversation.** Unchanged.

**Never use the Agent or Workflow tool to delegate without being asked, even with Ultracode on.**
Unchanged. Held again this session: every forge check, filter, test, and doc change was written
directly.

**A genuinely new CEL shape (`[]byte`) is worth adding to `wellKnownCELTypes` for real, not leaving as a
per-call `:celType` override.** The same judgment call Phases 52 and 54 made for their own new shapes:
when a shape recurs (here, twice in one phase -- both Gzip directions), teach the scaffolder the real
conversion/wrap helpers rather than accepting a hand-written TODO stub every time it comes up again.

**An engine-side conversion helper claim (`NativeToValue([]byte)` -> `types.Bytes`, not a per-element CEL
list) is worth a real, scratch-program check against cel-go's own source before the doc comment states
it as fact.** This session's own version of the discipline entry 162 in `FAILURE_PATTERNS.md` already
records for a stdlib claim: read `common/types/provider.go`'s `NativeToValue` switch and `bytes.go`'s
`Bytes.ConvertToNative` directly, then write what was actually verified.

**A cap on a function's input length is not the same control as a cap on its output length**, and a
decompression function is exactly the shape where the two diverge on purpose (that is the whole point
of compression). `FAILURE_PATTERNS.md` entry 163 has the full story; the short version is: whenever a
filter's own job is to expand a value rather than transform it in place, its input-length cap does not
protect the caller, and a second, output-side cap needs its own separate justification.

**`LOCALSTACK_AUTH_TOKEN` must be exported before a full `coverage-check`/`-race` run, or unrelated
packages report false regressions; it must also be a real repository secret in
`.github/workflows/ci.yml` for CI specifically.** Unchanged from last session's own fix (`e31dbe2`);
this session's own full local run, with the token exported, showed zero regressions anywhere in the
repository outside this phase's own two packages, both of which were closed for real (see above).

**The `examples/webserver_lab` `plain` SSH-container RULE 0 pattern reused cleanly a seventh time.**

### The remainder, in order

Phase 58 is done. This closes out Part XII (PLAN.md Section 36, the Filter Library) -- every phase from
50 through 58 is now built, tested, wired, and documented. Part XIII (PLAN.md Section 36's own next
section, the Chart Collection, Phases 59 through however many chart-type phases it names) is the next
work in this roadmap, but was not requested this session and has not been started. Skim its own intro
(the ECharts dependency decision, the `pkg/charts` + `pleiades chart` CLI shape, the "generated Collection
stub stays honestly inert until Phase 16's dispatcher reaches it" caveat) before assuming a one-line
summary is the whole scope, the same discipline every phase in this Part has needed.

### Verification state

`go build ./...`, `go vet ./...`, `make fmt` all pass with no output. `make gosec`: 9 pre-existing
individually-waived findings, zero new. `make govulncheck`: 0 vulnerabilities in this module's own code
or imported packages (3 unrelated vulnerabilities in required-but-unused modules, unaffected). `go test
./internal/archtest/...` passes clean. `go run ./tools/gendocs` is idempotent; `go run ./tools/docs-lint`
passes clean (187 files scanned).

RULE 0: built the real `pleiades` binary fresh, brought up `examples/webserver_lab`'s `plain` SSH
container for real, ran `pleiades init`/`add-host`/`add-credential` into a scratch project, wrote a
runbook with one task gated on a six-filter combined `when_cel` condition (`syslogParse`,
`pathExtractExtension`, `pathJoin`, a `gzipCompress`/`gzipDecompress` round trip, `trimNormalizeWhitespace`,
`payloadChunker`) and a second gated on a deliberately wrong `lineEndingConvert` comparison;
`pleiades validate` passed clean, `pleiades run` executed the real task over real SSH ("changed") and
skipped the second with the real expression named in the skip reason. Container torn down afterward; the
example's own committed files were never touched (confirmed via `git status --porcelain`). One real
mistake caught and fixed along the way: the scratch inventory's first attempt used `ssh_host`/`ssh_port`
property keys, which `internal/inventory/devices/linux/server.go` does not read (it reads `host`/`port`);
the run failed dialing `:22` on an empty host, corrected by rebuilding the scratch inventory with the
right keys before re-running.

**Full-repo `go test -race ./...` ran to completion with zero failures (128 packages, confirmed by
reading the log directly rather than trusting a piped exit code).**

`go run ./tools/coverage-check`, run with `LOCALSTACK_AUTH_TOKEN` exported: **175 packages measured, zero
below their recorded floor** -- this phase's own two packages (`pkg/filters` at 99.0% against a 98.9%
floor, `internal/engine` at 95.5% against a 95.2% floor) both cleared their existing floors with real
margin, so `coverage-floor.json` needed no edit at all this phase, unlike every phase before it in this
Part.

### Commit message

Drafted, not run; nothing beyond `e31dbe2` is committed.

```
feat(engine,filters): Phase 58's forge tuning and 8 file, text & log filters

Two deliverables, per this session's own opening request: decide
whether Phase 57's work left anything further to do before Phase 58,
then build Phase 58 (PLAN.md Section 36's Part XII, File, Text & Log
Filters) end to end. This closes out Part XII: every phase from 50
through 58 is now built, tested, wired, and documented.

Forge check: GzipCompress/GzipDecompress needed CEL bytes (gzip's
compressed output is not necessarily valid UTF-8, so a CEL string
would be silently wrong), a shape no prior phase had used. Running
both directions through the real pleiades forge new-filter CLI before
writing any code produced unfilled TODO conversion/wrap stubs on both
sides. Added "[]byte" -> cel.BytesType to
internal/forge/filterscaffold's wellKnownCELTypes table plus matching
conversionFor/wrapperFor/exampleArg cases, and a new
celToBytes/wrapBytes pair in internal/engine/cel_filters.go mirroring
celToStringList/wrapStringList. Re-ran the CLI afterward: both
directions now generate complete code with zero TODOs. Both
directions verified against cel-go's own common/types/provider.go and
bytes.go source before relying on them, not assumed.

The 8 functions, across three files. pkg/filters/logtext.go (4, new):
SyslogParse (RFC 5424 and legacy RFC 3164, one shared key set across
both formats, only the <PRI> prefix as a hard parse gate, everything
else degrading field by field); LineEndingConvert (lf/crlf);
TrimNormalizeWhitespace; PayloadChunker (splits a list into
fixed-size chunks, each chunk itself a nested []any -- verified
directly against a real cel.Program that nested lists round-trip
correctly through wrapDynList/celToAny before relying on it, no new
well-known shape needed).

pkg/filters/compress.go (2, new): GzipCompress/GzipDecompress, stdlib
compress/gzip, strictly in-memory. A first-draft GzipDecompress capped
its own input but not its output; gzip's own extreme compression
ratios mean a small, well-within-cap compressed value can still
decompress unboundedly (a decompression bomb). Caught by a deliberate
adversarial test, fixed with a new maxGzipDecompressedBytes (16 MiB)
output cap via io.LimitReader, recorded as FAILURE_PATTERNS.md entry
163.

pkg/filters/path.go (2, appended to the existing file): PathJoin
(POSIX-style via stdlib path.Join, not path/filepath.Join, so the
result cannot vary by build platform); PathExtractExtension (matches
stdlib path.Ext's own semantics exactly).

Tests: table-driven per function. Fuzz targets for SyslogParse,
PathJoin, PathExtractExtension (this phase's own named checklist
requirement) plus GzipDecompress (parses untrusted binary input,
matching this Part's "every non-trivial parser gets a fuzz target"
convention). A benchmark file. Every function proven callable through
the real, unmodified engine.NewCELEvaluator()/Program.Eval via a
compiled when_cel expression, plus a combined condition against a
realistic log-processing stat payload with a negative control. A
whitebox test file exercises every new binding's "argument not
convertible" defensive branch, including celToBytes/wrapBytes
directly.

The initial coverage run surfaced six real, reachable branches missing
a test case; all six got real new test cases. What remained is one
documented, source-verified-unreachable strconv.Atoi guard. Measured
99.0% for pkg/filters (98.9% floor) and 95.5% for internal/engine
(95.2% floor) -- both comfortably above their existing floors, so
coverage-floor.json needed no edit this phase, a first for this Part.

docs/reference/filters/index.md picked up all 8 new entries with zero
hand-written doc changes.

go test -race ./... ran clean across the whole repository (128
packages). go run ./tools/coverage-check: 175 packages measured, zero
below their recorded floor. make gosec: 9 pre-existing waived
findings, zero new. make govulncheck: clean. RULE 0: the real
pleiades binary, built fresh, ran a scratch runbook against a real,
running examples/webserver_lab SSH container, gating one real
exec.command task on a six-filter combined when_cel condition (true,
ran) and a second on a deliberately wrong lineEndingConvert comparison
(skipped, named in the skip reason), via real pleiades validate and
pleiades run.
```
