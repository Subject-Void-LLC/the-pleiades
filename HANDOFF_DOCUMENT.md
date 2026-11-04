# Handoff Document

Rewrite the "Current Status" section when stopping mid-task or handing off, per `.AGENTS/AGENTS.md`.

## Current Status (this session)

**2026-11-04. Branch `feat/facts-41c-2`, three commits on `main` at `30de7528` (PR #110 merged): `e84c0dc3` (feat),
`1fb1ebb1` (lab gates), `ae4c5657` (review fixes), plus the records (uncommitted). Reviewed; every finding fixed. Not
through `make pr-check`, push or a pull request yet.**

### What works (each proven by a test that ran)

- **Phase 41c-2: `facts.gather` gains `files`, `ports` and `users`** on Linux, FreeBSD, macOS and Windows, one fact
  per item, in the one `gather_subset` registry (`TestGatherSelection_FollowsAnsiblesRules`,
  `TestGatherSelection_FilesFollowItsPaths`).
- `files`: at most 64 absolute paths; metadata, and SHA-256 and size only for a world-readable regular file (owner
  decision 2026-11-04; any other file is `digest: withheld` and never hashed on the device). A path the login cannot
  reach is `unreadable`, never `absent`; a path naming another machine or a Windows device is refused.
  `TestGather_FilesReportDigestsAndNeverCallUnreadableAbsent`, `TestGatherFilePaths_RefusesWhatCannotBeReadSafely`,
  `TestGatherFilesFor_HoldsEachPathToTheDevicesOwnForm`, `TestGather_RefusesAPathWrittenForTheOtherKindOfSystem`.
- `ports`: `port.<proto>:<addr>:<port>`, from `/proc/net` plus `/proc/*/fd` (byte order asked of the device),
  `sockstat -46lq`, `netstat -anvl`, and `Get-NetTCPConnection`/`Get-NetUDPEndpoint`.
  `TestGather_PortsFindsAListenerAndItsProcess`, the `TestParse*` tests in `gather_items_internal_test.go`.
- `users`: `/etc/passwd` and `/etc/group` through `cut -d: -f1,3-` (the password field never leaves the device),
  `dscl . -readall`, `Get-LocalUser`/`Get-LocalGroup`. `TestGather_UsersMatchThisMachinesFilesAndCarryNoPassword`,
  `TestUserProbes_NeverCarryThePasswordField`.
- Every OS, all passing on 2026-11-04 after the review fixes: `TestCLI_FactsReleaseGate` (Alpine sshd container),
  `TestCLI_FactsGatherOnFreeBSDMatchesItsOwnTools` (bsd-check, FreeBSD 15.1),
  `TestCLI_FactsGatherOnMacOSMatchesItsOwnTools` (the lab Mac, macOS 27.0.1),
  `TestCLI_FactsGatherOnWindowsMatchesItsOwnTools` (win-check, Server 2025). `FuzzGatherParsers` covers every new
  parser (86 seeds in `testdata/parser-seeds.json`; about 3.4 million executions per two-minute run, clean).
- Records: FAILURE_PATTERNS 866 to 877, LESSONS 390 to 392. Phase 41c is 7 of 11 (open gate: Fuzz/Stress Test).

### Current blocker

None. Next is the gate.

### Open

- **Owner:** Windows keeps every bound UDP endpoint (it reports no peer), and every platform keeps unconnected UDP on
  ephemeral ports. Deliberate (dropping the ephemeral range would hide WireGuard or a backdoor), but those facts churn
  until 41d marks facts gone.
- `~/pleiades-lab/bin/pleiades` (built 2026-10-24) hangs after sealing a run (`net.ssh.ping` finished in 0.15 s; the
  process never exited). Main's build does not. This session's lab work used a fresh main build from the scratchpad;
  the owner may want the lab binary refreshed.
- Two `windowsAbsolute` copies (`internal/catalog/facts`, `internal/catalog/exec/winrm`) with different rules and no
  test holding them together (867).
- Whether to commit the argus fix, still uncommitted in `~/argus`.
- Carried forward: a fish host onboarded before #110 stays raw until onboarded again (858); vuln-corpus finding
  `C13_auto-roboto_2026-11-03` pending review; argus `expand_seeds` wrote fuzz files into the repository despite
  `persist:false`; `internal/archtest` misses zero-width characters (851); a Runner publish failing mid-split leaves
  earlier batches stored (853); no fact marks a package or service gone (41d); whether a Remote Management Users
  member alone can read the Windows probes is unproven.
- Owner questions carried forward: Phase 41's encryption item depends on Phase 5 (not in v0.4.0); external Collection
  Landlock has no network rules; the Controller does not check a Runner's reported order token or label; the
  `secret_mask` residuals (836); whether `file.remove`'s check should refuse rather than warn.

### Next, in order

1. `make pr-check` through the monitor, push, `make branch-containers`, then the pull request (failure-pattern
   classes in the body, no AI attribution). Merge only when green, `record-actions`, pull `main` and clean.
2. The keyed-digest pull request (owner's hybrid: plain digest for world-readable, HMAC at the store boundary for
   restricted, stats stripped in the engine; the open 41c item).
3. The sweep-fix pull request: an API Batch review of merged code ran on 2026-11-04; its results are in
   `.IGNORE/claude-api/out/sweep-2026-11-04/` and are being verified.
4. 41c-3, then 41d to 41g, 117b, 117c, 105, then release 0.4.0 (no tag until the owner says).

### Files changed this session

`internal/catalog/facts/` (`gather.go`, `gather_subsets.go`, `gather_transport.go`, `gather_win.go`, new
`gather_files.go`, `gather_ports.go`, `gather_users.go`, `gather_bsd_more.go`, `gather_win_items.go`, tests,
`testdata/parser-seeds.json`, `testdata/fuzz/FuzzGatherParsers/c370165e73df7491`), `pkg/collection/facts.go`,
`internal/engine/facts_test.go`, `internal/forge/catalogdata/collections_gating.go`, `cmd/pleiades/`
(`facts_release_gate_test.go`, `facts_lab_release_gate_test.go`), generated `docs/reference/modules/facts/gather.md`,
`docs/reference/modules/index.md`, `docs/reference/schemas/module-catalog.json`,
`internal/api/wellknown/module-catalog.json`, `changelog/facts-files-ports-users.added.md`. Records:
`FAILURE_PATTERNS.md` and its archive (866 to 877), `LESSONS_LEARNED.md` and its archive (390 to 392),
`HANDOFF_DOCUMENT.md`, `HANDOFF_ARCHIVE.md`, and the ignored `.SPECIFICATION/IMPLEMENTATION.md` (41c) and
`.SPECIFICATION/SECURITY_ATTESTATION.md` (PW.5).

Lab: bsd-check and win-check are powered off (ACPI); the lab Mac was used read-only; the `C:\pleiades-41c2` fixture on
win-check is confirmed removed.
