# Handoff Document

Rewrite the "Current Status" section when stopping mid-task or handing off, per `.AGENTS/AGENTS.md`.

## Current Status (this session)

**2026-10-28. Branch `feat/authoring-parity-v0-3-0`, off `main` at `122b8c92`, 36 commits (oldest `e0b243fd`, newest
`a0489de8`), plus these records. Not pushed, no pull request, `make pr-check` has not run, no tag cut. When these
records were written the tree also held uncommitted code edits (25 paths in `internal/catalog`, `internal/engine`,
`internal/render`, `pkg/netcfg`, `pkg/containerengine`, `pkg/collection`) that these records do not describe.**

The branch is v0.3.0 "Paranoid Porpoise": Crawl and Walk plus authoring parity. The previous status (archived) covers
the first 16 commits; this one covers `e8f37785..a0489de8`.

### What works

- Templates (`d554c7d7`, Phase 87): `{% if %}`, `{% for %}` with `loop.*`, `{% set %}`, comments, `raw` and whitespace
  control; comparisons, `and`/`or`/`not`/`in`, `~`, `is` tests; arithmetic, inline if, computed subscripts, macros and
  includes refused by name; bounded (10000 iterations, 1 Mi steps, nesting 16). A param holding only a statement renders;
  `file.template` takes `trim_blocks` and `lstrip_blocks`. Tests in `internal/render` (`TestStatements`,
  `TestStatementBounds`, `TestExpressionRefusals`), `TestStatementOnlyParamsRender`, `TestGuardedReads_KeepTheRootRules`.
- `net.ios.config` check mode and structural backup masking (`2dbfa586`): `netcfg.MaskChecked`, new `netcfg.MaskXML`
  for `net.netconf.config` (FAILURE_PATTERNS 704, 705). `net.ios.facts` gathers everything by default, as stats too.
- Phase 131 R1 to R5 across the built-ins (`b617af7f`): `pkg/farside`, declared Choices, Returns held to the code,
  absence as `exists: false`, whole describe results; archtests `TestNoClientCutsTheFarSideToAFirstLine`,
  `TestAParamSayingOneOfDeclaresItsChoices`, `TestEveryRecordedKeyIsDeclared`, `TestALookupAnswersAbsenceAsData`,
  `TestEveryReturnSaysWhenItIsReturned` (FAILURE_PATTERNS 709 to 712, 715).
- Walk number types (`1cb9929c`, `pkg/wire/numbers.go`, FAILURE_PATTERNS 706): `TestTypedStats_MeanTheSameOnBothTiers`.
- Versioned JSON contracts (`03b5a80c`): `run-report.json`, `doc-filters.json`, `doc-conditions.json`, schema_version 1;
  `TestCLI_RunReportFitsItsSchema`, `TestDocJSON_FitsItsSchema`.
- CML (`083d266f`, `db9465d0`): `lab.info` whole lab and running-node count; `lab.start` reports what it started and the
  controller's headroom, and names a node-limit refusal; `node.create` documents vty transport and port names.
  `rollback_on_failure` accepts `net.ios.config` again (FAILURE_PATTERNS 707, `TestCLI_DocCreateSkillsRollbackExampleValidates`).
- `file.template` proven through pleiades on macOS and FreeBSD (reported by the session; no record of those runs is
  in the repository); it found FAILURE_PATTERNS 708 (`fb323b4a`).
- Release: `CurrentRelease` is 0.3.0 (`ed2c66c9`), install docs from a real 0.3.0 build (`66205f5c`), notes in
  `docs/releases/v0.3.0.md` (`62010139`), patch releases may add (`docs/13-releases-and-stability.md`, `abf62285`),
  changelog fragments retired into the notes (`a0489de8`). Ledger moves of 2026-10-28: v0.3.0's unbuilt phases to v0.4.0
  (117b, 117c) and v0.5.0 (41, 81, 114a, 115a, 115b, 117d, 122e, 122f, 126).
- Lab run 7 at `abf62285` (`~/pleiades-cvd/runs/AAR-run7-comparison.md`): turn A Opus 44-0, Sonnet 44-0, Haiku 26-12
  (the controller's node cap refused its start); turn B regraded 14-0, except Haiku 13-1. Harness fixes: FAILURE_PATTERNS
  716.
- Records: FAILURE_PATTERNS 704 to 716, LESSONS 352 to 354. Roadmap: Phase 131 at 16 of 21, Phase 88 at 10 of 14.

### Blocker

- None. The gate has not run, and the uncommitted edits above must be committed or set aside first.

### Open

- Gate not exercised: full `cmd/pleiades` and `cmd/runner`, the Mac, BSD, Windows and Windows ARM legs, real floors.
- Security (FAILURE_PATTERNS 704): an IOS secret under eight characters, or a NETCONF datastore secret, in the backup of
  a run before `2dbfa586` was readable in its report; rotate any such secret on a device that is not lab-only.
- Phase 131: the Audit table was not found; NETCONF's `RPCError` text is not bounded through `pkg/farside` (client audit
  and R1); R4 and R5 are proven for the methods named, not shown for every method.
- Phases 87 to 89: `set_fact` was built as `vars.<name>`, not `nodes.<name>` (item open for the owner); no Walk-tier
  `until` test; no journal stage for a `failed_when` override; `with_items` deprecation not decided in writing;
  changed_when/failed_when under a loop not tested; Adversarial, Schema/Injection and Release Gate items open. The
  Documentation Gates of 87, 88 and 89 cite four changelog fragments retired in `a0489de8`.
- Run 7 ranked items still open: one source for device configuration, rollback stepping over subsumed runs, the docs
  list (item 7) and the messages (item 8). The capacity slots have not run in a lab turn.
- Carried: Groundconfig's two failing entries and uncommitted knowledge; FAILURE_PATTERNS 668 and 677; not built on
  purpose: handlers, `serial:`, `async`/`poll`, rescue across Walk segments, other `with_*` forms.

### Next, in order

1. Commit these records (`FAILURE_PATTERNS*`, `LESSONS_LEARNED*`, `HANDOFF_*`) apart from the code edits.
   `.SPECIFICATION/IMPLEMENTATION.md` is git-ignored.
2. Settle the uncommitted code edits, then run the gate: `.venv/bin/python tools/monitor/monitor.py run --cap 4500M`,
   waited on with one background `curl` on `/wait?for=fail,end`. On a failure, stop, prove the failing packages alone,
   fix, rerun.
3. Push once and open one pull request: the failure-pattern classes checked, run 7's table with its caveats, no AI
   attribution.
4. After it merges, the owner decides the tag and the next work.

### Files changed this session

`pkg/netcfg` (`checked.go`, `rest.go`, `xml*.go`), `internal/catalog/net/ios` (config, check, facts),
`internal/catalog/net/netconf/config.go`, `internal/render`, `internal/engine` (render params), `internal/validate`
(guarded reads, choices, rollback rule), `internal/forge/playbook`, `pkg/wire/numbers.go`, `pkg/farside`, about 160
files across `internal/catalog` and `pkg` for R1 to R5, `internal/archtest`, `internal/clireport`,
`internal/jsonschema`, `tools/gendocs`, `pkg/cml`, `internal/catalog/virt/cml`, `pkg/remotefile/put.go`,
`cmd/pleiades` (doc, skill, add-credential, import-cml), docs and `docs/releases/v0.3.0.md`, `changelog/` (emptied).

### Lab state

Run 7's turn B tore down each instance's lab (only `default-lab` remains on the controller). The harness gained the
instance filter (`grade.sh`) and capacity slots (`run5-instance.sh`). `bsd-check` was saved again
(`virt.vbox.vm.stop mode=save`) after the FreeBSD `file.template` proof. Two small test files are left under
`/tmp/pleiades-template-proof` on the Mac (it stopped answering SSH during cleanup) and in the saved `bsd-check`; remove
each with `file.remove recurse=true` when the machine is next up.
