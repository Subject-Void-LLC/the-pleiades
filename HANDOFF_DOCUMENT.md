# Handoff Document

Rewrite the "Current Status" section when stopping mid-task or handing off, per `.AGENTS/AGENTS.md`.

## Current Status (this session)

**2026-11-19. Branch `docs/records-phase41-files` (records only, from `main` at `7c619582`, the #209 merge). Merged
since the last handoff: #208 (systemd read-backs) and #209 (the last records). Open: #210 (branch
`feat/facts-read-port`, head `daf0739e`: the read port in the CLI, the device facts link, Walk-tier fact benchmarks,
and file and port fact hardening), CI pending.**

### What works

- `make pr-check` passed at `daf0739e`: the fast, repeat and containers passes and every step. The Mac, FreeBSD and
  Windows legs did not run in it (none was configured for that run); the lab tests below ran on their own.
- The CLI's runs read stored facts through the engine's read port (`cmd/pleiades/facts_reader.go`,
  `facts_reader_test.go`). A device's response links to its facts (`internal/api/hateoas_facts_link_test.go`).
  Benchmarks of the Walk tier's fact path (`internal/adapters/native/facts_bench_test.go`,
  `internal/facts/entstore/bench_test.go`).
- The suspected file and port weaknesses from the last handoff, and a fourth (hard links), were each reproduced by a
  test that failed on the old code, then fixed: FAILURE_PATTERNS 1279 to 1282. `internal/catalog/facts` passed on the
  machine legs run on their own, outside pr-check: macOS 27.2.1 (`tools/maccheck` at `2739093e`) and FreeBSD with its
  ACL test on bsd-check's ZFS pool (`tools/bsdcheck` at `7b138c07`, the ACL cases not skipped); Linux ACLs and
  attributes in Debian with `acl` and `attr`, BusyBox's `unlisted` in Alpine; and
  `TestCLI_FactsGatherOnWindowsHoldsACLsLabelsJunctionsAndStreams` holds Windows ACLs,
  labels, junctions and streams on win-check (PowerShell 5.1). A file fact now says `acl` (listed, unreadable or
  unlisted), and `file_attached.<path>` carries a path's extended attributes or streams as names and one keyed digest.
- Records: FAILURE_PATTERNS 1279 to 1290, LESSONS 456 to 458, CI_LESSONS 36 and 37.

### Blocker

- None. #210 waits on its CI.

### Open

- Phase 41, in order. (1) The read port: #210 gives it a caller in the CLI only and changes nothing under
  `cmd/runner`. (2) Facts at rest, next (see Next steps). (3) Identity facts on FreeBSD, macOS and Windows, and
  per-component serials for network gear (lab). Then the items re-opened on 2026-11-19 (41b item 2, 41c items 7 and
  11, 41h item 12), 41c's remaining read-backs, 41d to 41g, and 41h's Release Gate.
- Carried: Ansible names for the identity facts wait on a lab comparison; `config.running_digest` has not run on a real
  device; FAILURE_PATTERNS 1267 is open; #202's residual (per-batch signing, Phase 105); the one-off
  `TestSubscriber_HoldsEachBatchToItsDevicesClaim` flake is explained (FAILURE_PATTERNS 1291: its no-value check
  finds `9.6` in slog's timestamp), and its fix is left for later.
- Practices, not code: commit before any lab leg and read the commit it names (1288); after heavy builds, check WSL's
  page cache before starting a lab VM, and ask the owner to drop it (1289).
- Owner approvals of 2026-11-19: Samba AD DC as a privileged container, in opt-in lab runs only; a directory harness
  (OpenLDAP with SSSD or nslcd, and Samba AD) built with Phase 41c's users item and reused by Phase 80. Owner
  decisions pending: none.

### Next steps

1. Merge #210 once its checks pass on its own head commit (CI_LESSONS 35); refresh the local PR log in the same
   command (LESSONS 455).
2. Commit these records, gate and merge them.
3. Facts at rest, its own PR, by the owner's tokenization decision: key the fact hash with an HMAC; seal keys and
   values; look rows up by a keyed token bound to the device; seal the Runner's `job.facts` batches to the
   Controller; check whether the journal's `stat_keys` keeps item keys; vacuum after the migration; keep versioned keys
   during rotation.
4. Then Phase 41's order under Open.

### Files changed

#210 (46 files): `internal/catalog/facts` (the ports and files probes, ACLs, attached data, the Windows script and its
standard-input transport, and their tests), `cmd/pleiades/facts_reader.go`, `run_pipeline.go` and the Windows lab gate,
`internal/api/links.go` and `router.go`, `internal/apispec/apispec.go`, `internal/auth/hateoas.go`,
`pkg/collection/facts_reserved.go`, `internal/forge/catalogdata/collections_gating.go`, two benchmarks, docs and the
generated reference, and nine changelog fragments. This branch: `FAILURE_PATTERNS.md`, `FAILURE_PATTERNS_ARCHIVE.md`,
`LESSONS_LEARNED.md`, `LESSONS_LEARNED_ARCHIVE.md`, `CI_LESSONS.md`, `HANDOFF_DOCUMENT.md`, `HANDOFF_ARCHIVE.md`.
`.SPECIFICATION/IMPLEMENTATION.md` (git-ignored) was updated in the session, not on this branch.

Lab: changed. bsd-check now has a ZFS pool `pleiadesacl` at `/pleiades-acl` (lab runbook
`~/pleiades-lab/runbooks/bsd/check-08-zfs-acl.yaml`, owner-approved); each ACL test run makes and destroys a dataset of
its own in it. bsd-check started after the owner dropped WSL's page cache (1289). Both bsd-check and win-check were
left powered off (ACPI shutdown, state `poweroff`); the Mac was cleaned after its runs (49 GB free).
