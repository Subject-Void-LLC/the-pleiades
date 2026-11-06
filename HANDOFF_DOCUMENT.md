# Handoff Document

Rewrite the "Current Status" section when stopping mid-task or handing off, per `.AGENTS/AGENTS.md`.

## Current Status (this session)

**2026-11-06. Branch `fix/open-patterns-fable`, in the worktree `/home/noot/auto-roboto-fable`, rebased on `main` at
`53fd1eec` (PR #121 merged). Three commits: `f129e543` (610), `dd7e7905` (611) and `999e9694` (677). Not yet through
pr-check, push or a pull request.**

### What works

- **Three open failure patterns are fixed**, each from a second model's proposal (Fable 5.1 through the Batch API,
  about $5.86 for the three), applied by an agent and proved with a test that fails on `main`'s code. Two of the three
  proposals needed correcting (LESSONS 403).
  - 610: `ShowInterfaces` records the medium on an "Encapsulation X, medium is Y" line, a deliberate difference from
    genieparser (still broken at 26.7) that `pkg/iosparse/doc.go` lists. `TestShowInterfaces_MediumLine`, on two real
    genieparser fixtures with one line swapped. Caveat: the medium lines come from genieparser's source comments; no
    device capture has one.
  - 611: `ShowVlan` joins wrapped lines with a `strings.Builder`, output unchanged (fixtures, fuzz seeds, a
    300,000-input differential test). `TestShowVlan_WrappedRunIsLinear` counts bytes for 1,024 against 4,096 lines,
    bound 6: about 15 before (10 under `-race`), about 3.8 after (3.3). The proposed 2x/3x bound passed on the old code
    under `-race`, so it was rebuilt.
  - 677: on `main` the generate run wrote 96 files (8 CML, 88 stubs for the 44 map-registered `virt.clab` methods) and
    rewrote `internal/catalog/builtins.go`. `forge new-collection --skip-existing` now also skips an entry
    `collection.Lookup` holds, and gencatalog imports the holding package (`holdingPackage`). The run now writes 0
    files. `TestForgeNewCollectionSkipExisting_SkipsARegisteredMethod`,
    `TestWriteCatalogBuiltins_ImportsThePackageThatHoldsTheMethods`.
- **631 was already fixed**, by `76cd78ce` (2026-10-24): prscope runs every direct importer of a changed package. The
  index now says so; importers of importers still do not run. 940's archive entry has a note, since it called 631 open.

### Current blocker

None. Next is pr-check for this branch.

### Open

- (a) `cmd/pleiades` measured 66.8% coverage locally against its 69.4 floor. The shortfall predates this branch (its 5
  new statements there are covered), and CI's coverage job passed the same floor on PRs #119 to #121, so local and CI
  measure it differently. Not a failure pattern yet; if pr-check's scoped coverage step refuses it, read why first.
- (b) The fuzzer stalls at 0 execs/sec after about 15 s on this machine, on `main`'s code too. Cause unknown.
- (c) `stash@{0}` (2026-11-04, `fix/ssh-sweep-b`) is not dropped: the drop was denied as irreversible, so it is the
  owner's to do. Its patch is saved in the previous session's scratchpad.
- (d) Carried from #121: readiness paths without the fast failure (`ForHTTP` waits, `ForListeningPort`-only sites,
  Ryuk); 602 as a possible repeat of 470, unproven; 474's exhaustion odds under the 20000-32767 ephemeral range, to
  watch. The run settings gaps (the CLI journal does not record `--setting`; no end-to-end test takes
  `method_settings` from a launch or the rollback route to a Runner; a run setting behind a generic `svc.*`/`pkg.*`
  dispatcher is never filled). The owner question on dating a value `facts.Resumed` writes back (before 41d).
- (e) Open failure patterns that need hardware or owner input: 448, 483 (its CI sighting), 551, 560, 602, 664, 674,
  689, 690, 726, 742, 747 and 767.
- API credit: $55.28 left, expiring 2026-11-08.

### Next, in order

1. `make pr-check` through the monitor, push, `make branch-containers`, the pull request. Merge only when green,
   `record-actions`, pull `main` and clean.
2. The facts churn PR, sweep items 4, 5, 6 and 10.
3. Phase 41, 41b to 41g, then 117b, 117c, 105, then release 0.4.0 (no tag until the owner says).

### Files changed on this branch

`pkg/iosparse/` (`doc.go`, `fixtures_test.go`, `parse_test.go`, `show_interfaces.go`, `show_interfaces_test.go`,
`show_vlan.go`, `show_vlan_test.go`), `cmd/pleiades/` (`forge_new_collection.go`, `forge_new_collection_skip_test.go`,
`forge_scaffold_io.go`), `tools/gencatalog/` (`builtins.go`, `builtins_test.go`, `main.go`). Records (uncommitted):
`FAILURE_PATTERNS.md` and its archive (610, 611, 631 and 677 resolved; a note on 940), `LESSONS_LEARNED.md` and its
archive (403), `HANDOFF_DOCUMENT.md`, `HANDOFF_ARCHIVE.md`, `changelog/net-ios-parse-interface-medium.fixed.md`,
`changelog/net-ios-parse-wrapped-vlan-lines.fixed.md`.

Machine: unchanged since PR #120 (the ephemeral port range stays 20000-32767). Lab: unchanged; no lab VM use was
reported for this session.
