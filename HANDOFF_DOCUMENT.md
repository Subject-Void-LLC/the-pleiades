# Handoff Document

Rewrite the "Current Status" section when stopping mid-task or handing off, per `.AGENTS/AGENTS.md`.

## Current Status (this session)

**2026-10-25. Branch `feat/secrets-ios-129a`, off `main` at `21ecd314` (PR #90's merge). Phase 129a (named secrets
and IOS sessions), the first of four PRs in the approved plan. Two commits: `2b12066f` (the code) and `20e1ac92` (the
docs). These records are uncommitted. Not pushed; no pull request yet.**

### What works

- PR #90 (`feat/virt-cml`, Phase 127d) merged 2026-10-25. Blind CVD run 3 is done and graded (turn A 45 PASS, 2
  FAIL at the 2-hour limit; turn C 47 PASS, 1 FAIL; turn B 13 PASS, 1 FAIL): `~/pleiades-cvd/runs/AAR-run3.md`. The
  owner approved the plan for Phases 129a to 129d: `/home/noot/.claude/plans/lazy-riding-lightning.md`.
- Phase 129a's code and docs are committed: named secrets (`pleiades add-secret`, `{{ secret.<name>.<field> }}` in
  `net.ios.config` lines and prompt answers), `net.ios.checkpoints`, and the IOS output fixes.
- The live CML gate `TestCLI_ANamedSecretKeysAnIKEv2TunnelAndNeverShows` (`cmd/pleiades/cml_secret_release_gate_test.go`)
  passed in 83.8 s, `-v` showing PASS and not SKIP, at the working tree just before `2b12066f`.
- Measured on the CML sandbox this session: the IOS `%` notices (FAILURE_PATTERNS 646); `dir unix:`'s line format
  (`145407  -rw-  1999  Oct 26 2026 01:16:14 +00:00  pleiades-e1beac8a0f57.cfg`); `net.ios.checkpoints` listed and
  pruned live on R2. For 129b, from the 2.7.2 `openapi.json`: link state is `PUT
  /labs/{lab}/links/{link}/state/start|stop`, enum `DEFINED_ON_CORE|STOPPED|STARTED`; capture is `PUT
  .../capture/start` (`maxpackets` or `maxtime`, one required, and `bpfilter`), `PUT .../capture/stop`, `GET
  .../capture/status|key`, and `GET /pcap/{key}` (`application/cap`); a link object carries `link_capture_key`.
- FAILURE_PATTERNS 644, 646, 650 (ping, traceroute, terminal) and 651 are fixed in `2b12066f`. 653, an IKEv2 key in
  `net.ios.command`'s show output, was found live and fixed in the same commit, and the fix was verified live.
- Records this session: FAILURE_PATTERNS 644 to 653, LESSONS 327 to 332.

### Blocker

- None.

### Open

- Phase 129a in the tracker: 0 of 8, open gates Release Gate and Provide Commit Message. No item is ticked yet.
- The redaction audit item is replaced by the owner's direction: Genie is the source, built in as `pkg/iosparse`'s
  schema test `TestSchemas_SecretLookingFieldsAreDecided` plus `netcfg.MaskFields`. Nothing is downloaded (the owner
  said so).
- Still open: FAILURE_PATTERNS 645, 647, 648, 649 and 652 (129c, 129d), and 650's rollback side (with 652, 129c).
- Phase 129 itself: the blind-lab harness and blind run 4 (0 of 2). 127d: 12 of 13, the Walk tier item open.
- Proposed, not approved: Phase 128 (IOS candidate merge) and 130 (firewalls and IDS/IPS).
- The Groundconfig external Collection is built in `~/Groundconfig/pleiades`, not committed; sharing is undecided.
- Carried: `TestCheck_RealRoutersAgainstRealBatfish`'s readiness wait, 629's GNS3 retries, 618's hard link residual,
  640 (Mask is still a denylist), 638, 574, 610, 611, 612, 602, 598, 560, 551, 470, 471.

### Owner decisions this session

- Mask learning applies only to "dynamically learned" named secrets; there is no automatic import from a vault.
- Genie/pyATS is the masking source, using what is built in.

### Next, in order

1. Commit these records.
2. Measure coverage for the changed packages, then `make pr-check` on the committed, clean tree.
3. Push and open the pull request; tick 129a's items with their evidence.
4. Then the Groundconfig corpus rebuild, 129b (CML and GNS3 link stop, start and captures), 129c (Batfish,
   rollback), 129d (CLI, inventory, templates, skill), the harness, and blind run 4.
5. Deferred by the owner's reorder: the release tooling PR, Phase 117b part 3, Phase 126.

### Files changed

- `2b12066f`: `internal/credential/` (secret store, generate, file store), `pkg/secretref/`, `pkg/sdk/named_secrets.go`,
  `pkg/netcfg/` (`mask.go`, `fields.go`), `pkg/netcli/` (`netcli.go`, `dialog.go`, `read_failure.go`),
  `pkg/clidialog/clidialog.go`, `pkg/iosparse/secret_fields_test.go`, `internal/engine/` (`render_params.go`,
  `runbook_context.go`, `executor.go`, `executor_secrets.go`), `internal/validate/` (`template_rule.go`,
  `literal_secret.go`), `internal/catalog/net/`, `internal/forge/catalogdata/`, `internal/clispec/clispec.go`,
  `cmd/pleiades/` (`secret.go`, the CML gate), with tests.
- `20e1ac92`: `CLAUDE.md`, four changelog fragments, `docs/16-network-verification.md`, the regenerated reference
  pages and module catalogs.
- Records (uncommitted): `FAILURE_PATTERNS.md`, `FAILURE_PATTERNS_ARCHIVE.md`, `LESSONS_LEARNED.md`,
  `LESSONS_LEARNED_ARCHIVE.md`, `HANDOFF_DOCUMENT.md`, `HANDOFF_ARCHIVE.md`.

### Lab state

Changed: the measurement lab on the CML sandbox was rolled back, and the controller holds `default-lab` only. The
sandbox reservation is as recorded before (`tests/cml/.env` refreshed 2026-10-25 15:51, valid about two days). The
`pleiades-cvd` lab was deleted by run 3's turn B. The Batfish container is removed. The `~/pleiades-cvd` harness
changed for run 3 (`lib.sh`, `setup.sh`, `turn.sh`, `grade.sh`, `prompt-a.txt`, new `prompt-c-fresh.txt`, `run3.sh`,
`run3-rest.sh`, `run3-rest2.sh`; originals kept as `runs/*.bak-run2`), and `~/pleiades-cvd/bin/pleiades-real` is
rebuilt at `d17b732f` (the old one is `pleiades-real.prev`). Unchanged: the VirtualBox lab was not touched this session;
last recorded, `bsd-check`, `win-check`, `lab-dc1` and `lab-lin1` are saved.
