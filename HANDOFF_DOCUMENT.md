# Handoff Document

Rewrite the "Current Status" section when stopping mid-task or handing off, per `.AGENTS/AGENTS.md`.

## Current Status (this session)

**2026-10-26. Branch `feat/lab-captures-129b`, off `origin/main` at `21ecd314` (PR #90's merge), 10 commits: Phase 129a
(`2b12066f`, `20e1ac92`, `81b14992`, records `42358323`, review fixes `80d0da4d`), 129b (`03ec94ed`), 129c (`2d54d27c`)
and 129d (`2d9cd379`, `7e941028`, review fixes `ac7af458`). Phases 129a to 129d are built, on one branch for one pull
request. These records are uncommitted. Not pushed, and `make pr-check` has not run: the owner said "don't worry about
pr-check yet" and "one push per repo".**

### What works

- The live CML gate `TestCLI_ANamedSecretKeysAnIKEv2TunnelAndNeverShows` passed in Phase 129d: named secrets, the P1-P2
  link stopped and started, a capture of it counted by `net.pcap.summary` (40 ESP packets between the routers, none in
  the clear), and `wait.routes` on P1 over SSH and on P2 through the console. It ran before the review fixes in
  `ac7af458`; no rerun since is recorded.
- Batfish: `TestCheck_DMVPNInAFrontDoorVRF` (live, pinned 2026.08.27.3685): VRF route checks, reach from an interface,
  NHRP refused until accepted. Rollback later-run kinds and provisional checks: `TestBuild_TellsLaterRunsApart`,
  `TestProvisionalChecks_AFailedProvisionalCheckIsUnchecked`.
- CLI: `remove-host`, `remove-credential`, adhoc host forms, `--help` exit 0 (`TestEveryCommandsHelpExitsZero`),
  duration timeouts, YAML aliases, `doc filters` and `doc conditions`, the `device` root (`TestDeviceRoot_*`),
  `wait.routes` for IOS (`TestIOSRoutes`), the netcli console fix (`TestCommand_PassesOverTheDevicesOwnMessages`).
- Tracker: 129a 7 of 8, 129b 3 of 5, 129c 3 of 4, 129d 9 of 10; every open one is a Release Gate (pr-check and
  Actions) or a commit message, besides 129b's GNS3 item. Phase 129 itself: 0 of 2.
- Records: FAILURE_PATTERNS 654 to 667; 562, 645, 647, 648, 649 and 652 marked fixed (647, 649 and 652 with stated
  residuals) and 650's rollback side updated; LESSONS 333 to 337; four new changelog fragments; two PW.1 statements in
  the security attestation.

### Blocker

- None. `make pr-check` waits on the owner's word.

### Open

- Blind CVD run 4 started 2026-10-26 01:17 through `~/pleiades-cvd/run4.sh`, with a binary built at `7e941028`, so
  without `ac7af458`'s fixes. Driver log at 02:00: turn A stopped by a usage limit after 2328 s, graded 41 PASS, 0
  FAIL; turn C started.
- Left open by the review and stated: F7 `wait.routes` lost its plan-time capability check (no any-of capability; said
  in its Doc); F8 provisional rollback checks are Crawl tier only (docs/10); F9 `authored_rollback_empty` is trusted
  from the journal row.
- Residuals in the records: 647 (hosts given to a no-device method are still ignored), 649 (other node kinds still
  print as numbers), 650 and 652 (a diagnostic run, and changes that cancel out, still need `--despite-run`).
- Tests not yet written for fixes in `ac7af458`: a `rollback_on_failure` undo step reading `device` (656), validate's
  refusal of `all_at_plan_time` with `device` (657), `link.stop` on a link that never ran (666), the capture `dest`
  refusal (667), and a non-IP frame read back as `unexamined` (659).
- Deferred: an assert method (none exists; FAILURE_PATTERNS 660), splitting run 4's turn A (kept for comparability),
  GNS3 (`git stash@{0}`, "gns3 link suspend and capture": the live test still fails, the device type and methods are
  unbuilt; FAILURE_PATTERNS 664).
- Seen under load and passing alone (the class of 61, no new entry): two CLI release-gate tests in a full
  `cmd/pleiades` run (a reaper context deadline, an sshd fixture port refused), and a `wait` live test where FRR said
  "% command failed, could not lock running DS" during run 4.
- Carried from before: `TestCheck_RealRoutersAgainstRealBatfish`'s readiness wait, 629's GNS3
  retries, 618's hard link residual, 640 (Mask is still a denylist), 638, 574, 610, 611, 612, 602, 598, 560, 551, 470,
  471. Proposed, not approved: Phases 128 and 130. Groundconfig stays uncommitted, its sharing undecided.

### Next, in order

1. Commit these records.
2. When the owner says so: measure coverage for the changed packages, then `make pr-check` on the committed, clean tree.
3. Push once, open the pull request for 129a to 129d, and tick each Release Gate with its evidence when Actions passes.
4. Grade run 4 when it ends and write its after-action report; then the Phase 129 items.
5. Then the deferred list above, and the release tooling PR, Phase 117b part 3 and Phase 126 in the owner's order.

### Files changed

- `03ec94ed` (129b): `pkg/cml/` (`capture.go`, `find_link.go`, `cmltest`), `pkg/pcapsum/`,
  `internal/catalog/virt/cml/{link,capture}/`, `internal/catalog/net/pcap/`, catalogdata, the CML gate, docs.
- `2d54d27c` (129c): `internal/catalog/net/batfish/`, `internal/rollback/`, `internal/engine/` (journal, provisional
  checks), `internal/ent/` and migrations sqlite 0038 and postgres 0035, `cmd/pleiades/rollback.go`, docs.
- `2d9cd379`, `7e941028`, `ac7af458` (129d): `cmd/pleiades/` (adhoc, removehost, main, journal, doc topics, skill,
  validate, run text), `internal/engine/` (yaml aliases, timeouts, device root, executor, handlers), `internal/validate/`,
  `internal/render/filter.go`, `internal/catalog/wait/routes.go`, `pkg/netcli/`, `pkg/sdk/shell.go`, `tools/gendocs/`,
  docs and changelog fragments.
- Records (uncommitted): `FAILURE_PATTERNS.md`, `FAILURE_PATTERNS_ARCHIVE.md`, `LESSONS_LEARNED.md`,
  `LESSONS_LEARNED_ARCHIVE.md`, `HANDOFF_DOCUMENT.md`, `HANDOFF_ARCHIVE.md`, `changelog/runbook-yaml-aliases.fixed.md`,
  `changelog/validate-unmatched-target.added.md`, `changelog/capture-files-and-unexamined.changed.md`,
  `changelog/skill-teardown-and-new-forms.changed.md`; git-ignored, `.SPECIFICATION/IMPLEMENTATION.md` and
  `.SPECIFICATION/SECURITY_ATTESTATION.md`.

### Lab state

Changed: blind CVD run 4 is running on the CML sandbox (`~/pleiades-cvd`; run 3's results and sandbox archived to
`runs/claude-run3` and `claude-run3`), with a Batfish container up for it, and `~/pleiades-cvd/bin/pleiades-real` is
rebuilt at `7e941028`. Unchanged: the VirtualBox lab was not touched this session; last recorded, `bsd-check`,
`win-check`, `lab-dc1` and `lab-lin1` are saved.
