# Handoff Document

Rewrite the "Current Status" section when stopping mid-task or handing off, per `.AGENTS/AGENTS.md`.

## Current Status (this session)

**2026-10-31. Branch `feat/agent-friendly-frr-and-hosts`, 13 commits ahead of `main` at `ea70badf` (engine and become;
net fixes; virt.clab; cli; docs; records; mcp; journal column; sdk and validate coverage tests; testgate durations;
net.linux; the container-list fix `46c5c464`; the pre-PR review fixes `3cdae87a`). Clean tree apart from these records.
Not pushed, no pull request.**

### pr-check state

- Run 3 (commit `90d204a0`) passed every stage: all 50 container-test packages (4010 s), images, coverage and sizes. It
  failed only on `durations` (FAILURE_PATTERNS 745), fixed in `c6701288` and checked with `go run ./tools/testgate
  -durations-check .coverage/pr/containers.json`.
- A later run (after net.linux) failed within its first minute in the devtools stage on `tools/internal/flakegate`
  `TestEveryContainerPackageIsListed`: `internal/catalog/net/linux` was not on the Makefile's container list
  (FAILURE_PATTERNS 754), fixed in `46c5c464`.
- **Everything since run 3 (durations, net.linux, the container-list fix, the pre-PR review fixes in `3cdae87a`, and these
  records) has NOT been through a full gate.** Run `make pr-check` through the monitor again (about 100 minutes) before
  pushing. Stop the old monitor by its exact PID first and confirm
  the new run id (LESSONS 362); leave the tree alone until the verdict (LESSONS 288).

### What works (each proven by a test that ran)

- **net.frr.config check and save (FAILURE_PATTERNS 739).** `TestConfig_ASaveNeedsTheDirectoryToo`
  (`frr_dirperm_live_test.go`), `frr_login_live_test.go` and `frr_become_live_test.go` against a real FRR container.
  Blind lab run 1's second failure ("file would belong to netops:frr") was NOT reproduced.
- **`hosts:` as a list** (`internal/engine/hosts_list.go`), `add-host --tag`, `set-host --tags`, `net.batfish.check`
  `expect_devices`.
- **`trust-host --first-connect` refuses a device not flagged ephemeral unless `--reason` is given.** It BREAKS scripts, so
  it belongs in a minor release, not a 0.3.x patch (`changelog/trust-host-first-connect-ephemeral.breaking.md`).
- **`become`** (LESSONS 358): `pkg/remoteexec/become.go`, `sdk` `WithBecome`, `internal/engine/become.go`,
  `internal/validate/become_rule.go`, journal `Become` (now with its ent column and migrations, FAILURE_PATTERNS 743 and
  LESSONS 361), rollback `Step.Become`. Covered in `pkg/sdk` and `internal/validate` by their own tests (FAILURE_PATTERNS 744).
- **The `virt.clab.*` collection** (44 methods, `pkg/clabapi`). `TestLive_ARealLabEndToEnd` passes against the real
  privileged server.
- **New this round: `net.linux.bridge`, `net.linux.address`, `net.linux.route`** (`pkg/linuxnet` specs,
  `internal/catalog/net/linux`). `TestLinux_BridgeAddressAndRouteOnARealRouter` runs on a real router with real sudo; the
  frr and ovs lab images got `iproute2` and `sudo`. Catalog: 259 methods, 257 implemented, 235 that can predict.

### Pre-PR review (fp-reviewer, 2026-10-31): six findings, all fixed in `3cdae87a`, each with a test

- `become` on an external Collection's method was silently not applied (FAILURE_PATTERNS 748): `BecomeRule` and
  `collection_action.go` now refuse it; `TestBecomeRule`, `TestCollectionActionExecutor_BecomeIsRefusedOnAnExternalMethod`.
- A `rollback_on_failure` undo ran as the login (749): `asAccountOf`; `TestRollbackOnFailure_AnUndoRunsAsTheAccountTheChangeRanAs`.
- The clab login token was not masked in an error (750): `TestClab_TheLoginTokenIsMaskedInAnError`.
- `import-containerlab --api` mapped a wildcard port to this machine's loopback (751): the API server's host is used now.
- `net.linux.route` refuses an ECMP route its undo could not restore (752):
  `TestLinux_BridgeAddressAndRouteOnARealRouter`.
- `trust-host --first-connect --reason` records the reason after the trust (753):
  `TestTrustHost_FirstConnectNeedsAnEphemeralDeviceOrAReason`.

### Records this session

FAILURE_PATTERNS 739 to 754 (repeats: 739 of 564 and 722; 740 of 642 and 90; 744 of 427 and 731; 746 of 428 and 637;
747 of 153 and LESSONS 288; 748 in kind of 578 and LESSONS 244; 750 of 626, 394 and 457; 751 of 740, 642 and 90; 752 of
582; 754 of 239 and 447; 742, 747 and 748 open in part); LESSONS 358 to 364. Not yet written to `CI_LESSONS.md` or annotated
in the failure ledgers (`go run ./tools/failures annotate`); 743 to 747 and 754 were all found by pr-check or the repo's own
guards, so they are candidates.

### Collection coverage audit (2026-10-30; three auditors, estimates, not measurements)

- About half the methods have real-path proof; typical coverage is about 30% of each product's surface.
- Strongest: `virt.clab` about 80%, `container` about 75%.
- Weakest: `net.junos` and `net.eos` 0% (declared), `cloud.aws` about 1 to 2% (LocalStack only), `edge` about 2%,
  `net.catalyst` about 3%, `net.batfish` and `net.ovs` about 10%, `net.netconf` about 12%, `fusion` about 10 to 15%,
  `pkg` about 15% (apt and dnf only).
- Biggest gaps by value: `container.image.build`, apk, zypper and pacman, `svc` reload, mask and status, `file`
  find, replace, fetch and ini_file, `identity` ssh keys and sudoers, `win` registry, AWS describe, NETCONF reads, CML
  topology import, OVS flows.
- The roadmap item "Every method proven on every OS it targets" in `.SPECIFICATION/IMPLEMENTATION.md` stays OPEN.

### Open

- **Owner decisions pending:** (1) the `trust-host --first-connect` refusal is breaking, so it needs a minor release, not
  a 0.3.x patch; (2) `JournalEntry.Become` as a kind 5 author label (FAILURE_PATTERNS 742, `internal/archtest/journal_test.go`).
- **Not done:** the playbook converter still DROPS `become` with a review note (`internal/forge/playbook/keywords.go`,
  `codes.go`); it should map it to native `become` and refuse other methods. The Walk tier refuses `become` until a
  dispatch carries it, and a Walk-tier runbook with `become` runs its earlier tasks before the refusal at the task (late,
  but it fails closed; FAILURE_PATTERNS 748).
- Edgeshark install and uninstall are state-aware, but their real POST was never run live (it pulls images).
  `virt.clab.capture.packetflix` needs Edgeshark running (the server answers 503).
- `make resources-record` would rewrite about 1,665 lines of `tools/testgate/resources.json` with inflated machine-wide
  figures (1.4 GB for `pkg/sdk`); it was NOT committed and needs its own look (FAILURE_PATTERNS 745).
- `import-containerlab --api` has code and a test beside it (`cmd/pleiades/importcontainerlab_api.go`, in `b478bc5e`); I
  did not run it here. The earlier note calling it unbuilt was stale. Its wildcard-port mapping was corrected in
  `3cdae87a` (FAILURE_PATTERNS 751).
- The whole `cmd/pleiades` package test run exceeds go test's 10 minute default (use testgate or pr-check).
  `TestCLI_NetTraceFromAContainerThroughARouter` once failed with a Docker "no SSH greeting" start failure; the cause is
  unproven.
- Older items unchanged: FAILURE_PATTERNS 560 (open until a sighting prints the engine's list) and 734; Phase 131 R4 and
  the Phase 87 to 89 gates.

### Next, in order

1. Commit these records, run `make commitgate`, run `go test ./tools/internal/flakegate -run TestEveryContainerPackageIsListed`, run `make pr-check` through the monitor, push, one pull request. Name the
   failure-pattern classes in the PR body.
2. Then the not-done list above, starting with the converter's `become` mapping.

### Files changed this session

See `git log ea70badf..HEAD`. Records: `FAILURE_PATTERNS.md`, `FAILURE_PATTERNS_ARCHIVE.md` (739 to 754),
`LESSONS_LEARNED.md`, `LESSONS_LEARNED_ARCHIVE.md` (358 to 364), `HANDOFF_DOCUMENT.md`. Changelog fragments are in
`changelog/` (including `net-linux.added.md`).

Unchanged: bsd-check and win-check are powered off (shut down through ACPI, not saved); not touched this session.
