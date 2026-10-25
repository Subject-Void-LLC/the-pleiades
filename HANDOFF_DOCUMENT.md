# Handoff Document

Rewrite the "Current Status" section when stopping mid-task or handing off, per `.AGENTS/AGENTS.md`.

## Current Status (this session)

**2026-10-24. Branch `feat/virt-cml`, off `main` at `0caadb17`, tip `d6f47b1f` (38 commits) plus these uncommitted
records. Phase 127d is built and proven; next is `make pr-check`, the push and the pull request. Not pushed. Tracker:
127d is 11 of 13 done (open: the Walk tier item, the Release Gate, the commit message).**

### What works

- The ten `virt.cml.*` methods, the `cml_controller` device type, the IOS login seed (type 9 secrets), `import-cml
  --controller` and `--via console`, and the console transport through a CML controller. Proven on the DevNet sandbox
  by `TestCLI_BuildsACMLLabAndReachesItsNode` (`040c5fd3`).
- The blind CVD lab, run 1: turn A met all three requirements in 105 minutes ($19.12), graded from the routers
  (`~/pleiades-cvd/runs/claude-run1/grade-a.txt`); turn B tore the lab down by `pleiades rollback` in 4.5 minutes
  (`grade-b.txt`). After-action report: `~/pleiades-cvd/runs/AAR-turn-a.md`.
- Fixed from the lab's findings: `net.ios.config` answers questions (`prompts`, `ask`, `accept`) and fails at once on
  an unanswered one (`a2495ddb`); IKE pre-shared keys masked and `crypto key` command words kept (`fa10a343`,
  `d6f47b1f`, FAILURE_PATTERNS 640); `node.create` takes `interfaces` (`c4e231df`); the `transport: telnet` hint
  (`f33aff1f`); `pleiades doc <device type>`, `doc --dir`, `adhoc --timeout 40s`, `init --dir` making its directory
  (`3436e0d6`).
- Gate work: prscope direct importers, pr-check record stamps and `LEGS=` reruns, `tools/failures watch-pr`.
- Records: FAILURE_PATTERNS 637 to 642, LESSONS 322 to 326, seven new changelog fragments.

### Blocker

- None. `make pr-check` has not run on this tip yet.

### Open

- The blind lab's rerun of turn A stopped at 29.5 minutes on the account's weekly usage limit. Afterwards another
  agent, citing Groundconfig, worked in the CML sandbox from 20:23 to 21:17, so the sandbox's `pleiades-cvd` lab is up
  and modified. Phase 129's harness item (lock the sandbox for a turn) covers it.
- The Groundconfig external Collection is built in `~/Groundconfig/pleiades`, not committed. The owner is deciding
  whether to share it.
- Proposed, not approved: Phase 128 (IOS candidate merge), 129 (what the blind lab asked for), 130 (firewalls and
  IDS/IPS).
- 127d's Walk tier item: a Runner refuses a console server; a `cml_controller` task from a Runner is untested.
- 638: no test drives a `virt.cml` failure through to the journal. 640: Mask is still a denylist.
- Carried: `TestCheck_RealRoutersAgainstRealBatfish`'s readiness wait, 629's GNS3 retries, 618's hard link residual,
  574, 610, 611, 612, 602, 598, 562, 560, 551, 470, 471.

### Next, in order

1. Commit these records, then `make pr-check` on the clean tip; push; open the pull request; `go run ./tools/failures
   watch-pr -pr N`.
2. After it merges: the release tooling pull request (a dry-run binary release; no tag until the owner says).
3. Then Phase 117b part 3, then Phase 126.

### Files changed

- This branch: `internal/catalog/virt/cml`, `pkg/cml`, `pkg/cmlconsole`, `pkg/iossecret`, `pkg/devicetls`,
  `pkg/netcfg`, `pkg/netcli`, `internal/catalog/net/ios`, `internal/engine` (login seed, console dialer),
  `cmd/pleiades` (doc, adhoc, init, import-cml), `tools/prscope`, `tools/prreport`, `tools/gatereceipt`,
  `tools/testgate`, `tools/failures`, docs and generated references.
- This session's records: `FAILURE_PATTERNS.md`, `FAILURE_PATTERNS_ARCHIVE.md`, `LESSONS_LEARNED.md`,
  `LESSONS_LEARNED_ARCHIVE.md`, `HANDOFF_DOCUMENT.md`, `HANDOFF_ARCHIVE.md`, seven files under `changelog/`, and
  `.SPECIFICATION/IMPLEMENTATION.md` (127d's blind lab item ticked).

### Lab state

Changed: the CML sandbox's `pleiades-cvd` lab is up and modified (see Open). The VirtualBox lab was not reported
changed; last recorded, `bsd-check`, `win-check`, `lab-dc1` and `lab-lin1` are saved, and
`~/pleiades-lab/bin/pleiades` is built from `main` at `0caadb17`.
