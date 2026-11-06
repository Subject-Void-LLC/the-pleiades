# Handoff Document

Rewrite the "Current Status" section when stopping mid-task or handing off, per `.AGENTS/AGENTS.md`.

## Current Status (this session)

**2026-11-05. Branch `fix/facts-sweep-gatherers`, from `main` at `dd91a345` (PR #117 merged). Twelve commits:
`a21b15e7` (a value that follows itself in a history reads as one, 936), `6539ed41` (`file.remove`'s check refuses a
forbidden removal by default), `55ea70bc` (a family is read or `facts.gather` says it was not, 925 to 929),
`564bbe70` (`gather_subset` and `filter` read as Ansible and the store read them, 930, 931), `77a1da13` (a secret that
is not UTF-8 is found, and such a key refused, 932, 933), `7f7a8ed3` (item keys, filter classes and launchd, 932, 934,
935), `2393a634` (the fuzz target judges a family's answer); then `60f50721` (`klauspost/compress` v1.20.2 for
GO-2026-6865, 938) and the fp-reviewer's follow-ups, `c70f147e` (file, 937), `9deef5c8` (playbook, 925), `93894ddd`
(redact, 933) and `529c032c` (facts, 925, 928, 934, 935). History was rebuilt; read `git log dd91a345..HEAD` for
hashes. Not yet through pr-check, push or a pull request. `main` is red on govulncheck until this branch merges.**

### What works (each test failed on the old code where its entry says so)

- **A family is read, or the task says it was not** (FAILURE_PATTERNS 925): a source whose listing fails, or answers
  without its usual structure, fails the task; a family no source answers fails a subset the task named and is a
  warning under `all`. `TestGather_AFamilyIsReadOrTheRunSaysItWasNot` (real SSH harness, 5 of its first 6 cases red
  before), `TestReadOutcome`, `TestFamilyVerdict`, `TestStructureChecks`, `TestParseWindowsObject_OnlyAWholeObject`.
- **Service sources read what they mean**: systemd only where it booted (926, `TestStructureChecks`); an OpenRC
  supervised service's state is its first word (927, `TestParseOpenRC_ASupervisedServiceIsRunning`); a FreeBSD daemon
  a non-root login cannot see is unknown (928, `TestParseRC_AHiddenDaemonIsUnknown`); `rc-update -v show` (929);
  launchd reports disabled daemons and its status is `enabled` or `disabled` (935,
  `TestDarwinParsers_ReadBrewPkgutilAndLaunchd`).
- **Filter and subsets as Ansible reads them**: the filter reads the stored key and refuses a family name alone (930,
  two cases of the family test); `gather_subset` is order-free (931, `TestGatherNamed_FollowsAnsiblesRuleInAnyOrder`);
  fnmatch's `[!x]` (934, `TestGatherMatches_ReadsFnmatchsNegation`).
- **Bytes that are not UTF-8**: item keys keep them apart and `ValidKey` refuses them (932,
  `TestItemKey_ABytesThatIsNotUTF8IsItsOwn`, `TestValidKey_RefusesWhatIsNotUTF8`); a secret holding one is still
  found before a fact is stored (933, security, low; `TestPrepare_ASecretThatIsNotUTF8IsStillFound`).
- **A repeated value in a history reads once** (936): the `factstest` conformance case "a value that follows itself in
  the history reads as one".
- **`file.remove`'s check refuses a forbidden removal by default**, `check_forbidden: warn` keeps the warning:
  `TestCheckRemove_AForbiddenRemovalIsRefusedByDefault`, and `TestCheckRemove_RecordsOnlyARemovalTheRealRunCanMake`
  is now the warn case.
- **Review follow-ups** (fp-reviewer, 2026-11-05, all before push):
  - 925: callers that named services where no manager runs now gather `[packages]` or reach services through `all`
    (`TestCLI_FactsReleaseGate`, against the real Alpine sshd container, asserts the `[services]` refusal stores
    nothing; `TestGather_PackagesAndServicesMatchThisMachine`); a converted `service_facts` emits
    `[all, !hardware, !packages, !ports, !users]` and warns where no manager is read (`TestModules_Map`). Windows
    ports, users and groups say their source is absent when the cmdlet is missing
    (`TestGather_AWindowsHostWithoutTheCmdletsSaysSo`, a modeled host, not yet a real Windows one); rpm counts only
    with its database; a failing `pkgutil --pkg-info` fails the probe; Linux port sections split only at line starts
    (`TestStructureChecks`). The live suite still passes on real Rocky and Alpine OpenRC.
  - 928: hidden processes are read from `sysctl -n security.bsd.see_other_uids`, not `ps -p 1`, which a jail never
    shows.
  - 933's sibling in `internal/redact`: a secret that is not UTF-8 is masked in its JSON spelling in a run's report
    (`TestValue_ASecretThatIsNotUTF8IsMaskedAfterJSON`, red on the old code).
  - 934: fnmatch's `[^a]` and backslash (`TestGatherMatches_ReadsFnmatchsNegation`). 935: a disabled launchd daemon
    is reported only while its plist is installed (fixture `com.example.removed`).
  - 937: `file.remove`'s check refuses only a no the device gave and warns where it cannot say (root under BusyBox).
    `TestRemovableScript_RefusesWhatItCannotVouchFor` measures exit 3 under BusyBox as root through `unshare -r`; no
    test drives the warning through a real BusyBox root login.
- **GO-2026-6865** (938, security): `klauspost/compress` v1.20.0 panics on two-block frames, reached from
  `api.ProjectHandler.StreamSyncLogs` through the compressing response writer. Bumped to v1.20.2 in `60f50721`. It
  failed only govulncheck on main's push run 42131068662; the advisory came after PR #117's checks passed. CI_LESSONS
  27.
- `FuzzGatherParsers` extended: fuzzed 90 s, about 2.6 million inputs, no failure.
- Passing under `~/.local/bin/capped` 4 to 4.5G, `GOMAXPROCS=4`: `go test ./internal/facts/...`,
  `./internal/catalog/facts` (and its live suite with `-tags integration` against real Rocky and Alpine OpenRC
  containers), `./internal/catalog/file`, and `internal/archtest`'s `TestCatalogDataDocsMatchTheRegistry`. Gendocs
  regenerated.

### Current blocker

None. Next is pr-check for this branch. `main` is red on govulncheck (GO-2026-6865) until this branch merges. One
owner question is pending (below); it does not block the pull request.

### Open

- **Answered 2026-11-05, "Plumb it in":** Phase 46's `file.remove` item ("unless the task or its settings chain says
  warn"). The task parameter is built on this branch; the next PR builds a method-readable path for launch-chain
  settings (System, Org, Project, Template, most specific wins, snapshotted on the job, the task's own value over all)
  on both tiers and reads `check_forbidden` through it. The item stays open until that PR.
- Phase 41's "Provide Commit Message" checkbox is the standard process gate (in about 200 places, ticked in finished
  phases), not stray text; leave it.
- **Owner question, before 41d.** Exact dating of a value `facts.Resumed` writes back needs per-sighting history,
  which conflicts with write-on-change. Options: leave it documented, per-sighting rows with a retention bound, or the
  first and last sighting per spell.
- Scope recorded: 0.4.0 ships every required item, 117b, 117c and 105 included (owner, 2026-11-05).
- Carried: `internal/catalog/gate/runner.go` `deviceRunner.stop` refuses only `pid <= 0` (a device printing `1` would
  get `kill` to init under become; low severity, needs a test seam, not yet a failure pattern), and the items carried
  in the archive's previous entry.
- API credit: $20.53 of $92 spent as of the last report. Expires 2026-11-08.

### Next, in order

1. `make pr-check` through the monitor, push, `make branch-containers`, the pull request. Merge only when green,
   `record-actions`, pull `main` and clean.
2. The settings-chain PR for methods, `file.remove`'s `check_forbidden` its first reader (owner, "Plumb it in").
3. The facts churn PR, sweep items 4, 5, 6 and 10: `ansible_distribution` values through Ansible's table, measured
   against `ansible.builtin.setup` in a container; macOS per-session sshd jobs; Windows per-user service instances;
   service runtime state and macOS swap as volatile or not emitted.
4. Phase 41 (encryption at rest, identity facts), 41b's two items, 41c's two, then 41d to 41g.
5. 117b, 117c, 105.
6. Release 0.4.0 (no tag until the owner says).

### Files changed on this branch

`internal/catalog/facts/` (`gather.go`, `gather_bsd_internal_test.go`, `gather_bsd_items.go`, `gather_bsd_more.go`,
`gather_family_test.go` (new), `gather_fuzz_internal_test.go`, `gather_outcome.go` (new),
`gather_outcome_internal_test.go` (new), `gather_packages.go`, `gather_ports.go`, `gather_services.go`,
`gather_subsets.go`, `gather_transport.go`, `gather_users.go`, `gather_win.go`, `gather_win_items.go`,
`gather_test.go`, `gather_win_internal_test.go`, `probes_export.go`, `live/rpm_test.go`), `internal/catalog/file/`
(`remove.go`, `predicted_test.go`), `internal/facts/` (`chain.go`, `chain_test.go`, `facts.go`, `file_store.go`,
`late.go`, `nested_test.go`, `factstest/conformance.go`), `internal/forge/catalogdata/` (`collections_files.go`,
`collections_gating.go`), `internal/forge/playbook/` (`modules_misc.go`, `modules_test.go`), `internal/redact/`
(`literals.go`, `value.go`, `value_test.go`), `pkg/remotefile/` (`locate.go`, `locate_test.go`),
`cmd/pleiades/facts_release_gate_test.go`, `go.mod`, `go.sum`, generated `docs/reference/ansible-modules.md`,
`docs/reference/modules/facts/gather.md`, `docs/reference/modules/file/remove.md`,
`docs/reference/schemas/module-catalog.json`, `internal/api/wellknown/module-catalog.json`. Records (uncommitted):
`FAILURE_PATTERNS.md` and its archive, `CI_LESSONS.md`, `HANDOFF_DOCUMENT.md`, `HANDOFF_ARCHIVE.md`, the new changelog
fragments `facts-gather-reads-or-says.fixed.md`, `facts-secret-not-utf8.security.md`,
`file-remove-check-refuses.fixed.md`, `klauspost-compress-go-2026-6865.security.md`, and
`file-remove-check-warns.fixed.md` deleted (its warning never shipped; the refusal fragment replaces it).

Lab: unchanged; no lab VM use was reported for this session.
