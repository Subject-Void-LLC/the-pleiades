# Handoff Document

Rewrite the "Current Status" section when stopping mid-task or handing off, per `.AGENTS/AGENTS.md`.

## Current Status (this session)

**2026-11-04. Branch `feat/facts-keyed-digest`, three commits on `main` at `f19fa172` (PR #111 merged): `98005436`
(fix(native)), `3e6bb38c` (feat), `70d3032b` (review fixes), plus the records (uncommitted). Reviewed (fp-reviewer and
two independent diff reviews); every finding fixed. Not through `make pr-check`, push or a pull request yet.**

### What works (each proven by a test that ran)

- **A private file's digest is kept keyed, never raw** (owner decisions 2026-11-04). `facts.gather` hands it over
  only as `sha256_raw` (`collection.RawDigestField`, held equal to `internal/facts.RawDigestField` by
  `TestFactCaps_AgreeAcrossTheBoundary`). `internal/engine/fact_digests.go` rewrites it right after a method returns,
  on the in-process and the Runner paths: the CLI keys it with HMAC-SHA-256 under a key derived lazily from the
  project's master key (`facts.NewLazyKeyer`, id `project:<16hex>`); a Runner seals it with HPKE (`internal/sealed`,
  ML-KEM-768+X25519) to the Controller's key on `wire.DispatchPayload.FactSealKey`, bound to job, device and fact; an
  executor told neither drops it, with a task warning. The executor scrubs every node's stats; `facts.ScrubDigests`
  (in `Prepare`) drops raw at any depth and in typed values. Tests: `internal/facts/keyed_test.go`,
  `internal/engine/fact_digests_test.go` (both paths; removing the rewrite fails three tests),
  `internal/facts/entstore/digests_test.go` (real SQLite through `Handle`).
- **The Controller** derives an HPKE key pair and an HMAC key (id `controller:<16hex>`) from
  `PLEIADES_FACTS_DIGEST_KEY` (`entstore.DigestKeys`), read before it binds or migrates; without it no such digest is
  stored and it warns. `cmd/controller/fact_digest_keys_test.go`, `TestFactDigestKeyReleaseGate_AMalformedKeyStopsTheControllerFirst`
  (real binary: exit 1, no database, no listener). Helm `secrets.factsDigestKey` / `existingSecretHasFactsDigestKey`
  with refusals, Compose pass-through, production guide "Digests of files only some may read".
- **End to end, all passing 2026-11-04:** `TestFactsReleaseGate_APrivateFilesDigestIsStoredOnlyKeyed` (real Runner,
  NATS and consumer: the stored value equals the Controller's HMAC of the real digest, no row holds the raw, device ID
  and name differ); `TestCLI_FactsReleaseGate` (exact project HMAC, stable over two gathers, new after a change); the
  FreeBSD, macOS and Windows lab gates (keyed shape, a change shows); `internal/archtest`; `make helm-lint`;
  `GOOS=windows` vet.
- **A Runner no longer counts the login's username as a secret** (`98005436`, FAILURE_PATTERNS 878): it calls
  `engine.CredentialSecrets` as the CLI does, so facts naming the login are stored on the Walk tier.
- Records: FAILURE_PATTERNS 878 to 888, LESSONS 393 to 395. Phase 41c is 8 of 11 (open gate: Fuzz/Stress Test).

### Current blocker

None. Next is the gate.

### Open

- `pleiades controller setup` does not generate `PLEIADES_FACTS_DIGEST_KEY` yet (follow-up pull request).
- On a Runner a task's register holds `sha256_sealed`, not `hmac_sha256` (documented). CLI keyed digests re-key if the
  project master key rotates. Private files are now read in full to hash them (audit and I/O cost for large or audited
  paths, as Ansible's stat reads them).
- The Runner now shows the login name in output as the CLI does (owner rule: allowlist identifiers). `pkg/netconf`
  keeps its own copy of the identifier allowlist; `internal/loader` and the legacy adapter still mask the username
  (stricter, so the tiers differ for external Collections).
- `~/pleiades-lab/bin/pleiades` (built 2026-10-24) hangs after sealing a run; main's build does not.
- The argus fix is still uncommitted in `~/argus`. API credit: $20.53 of $92 spent (2026-11-04).
- Carried forward: Windows and ephemeral UDP endpoints churn until 41d (owner); two `windowsAbsolute` copies with no
  test holding them together (867); a fish host onboarded before #110 stays raw until onboarded again (858);
  vuln-corpus finding `C13_auto-roboto_2026-11-03` pending review; argus `expand_seeds` wrote fuzz files despite
  `persist:false`; `internal/archtest` misses zero-width characters (851); a Runner publish failing mid-split leaves
  earlier batches stored (853); no fact marks a package or service gone (41d); whether a Remote Management Users
  member alone can read the Windows probes is unproven.
- Owner questions carried forward: Phase 41's encryption item depends on Phase 5 (not in v0.4.0); external Collection
  Landlock has no network rules; the Controller does not check a Runner's reported order token or label; the
  `secret_mask` residuals (836); whether `file.remove`'s check should refuse rather than warn.

### Next, in order

1. `make pr-check` through the monitor, push, `make branch-containers`, then the pull request (failure-pattern
   classes in the body, no AI attribution). Merge only when green, `record-actions`, pull `main` and clean.
2. A small pull request: `controller setup` generates `PLEIADES_FACTS_DIGEST_KEY` .
3. The verified sweep-fix pull request(s), from `.IGNORE/claude-api/out/sweep-2026-11-04/VERDICTS-*.md`.
4. 41c-3, then 41d to 41g, 117b, 117c, 105, then release 0.4.0 (no tag until the owner says).

### Files changed this session

`internal/facts/` (`keyed.go`, `facts.go`, `entstore/digests.go`, `entstore/subscriber.go`, tests), `internal/engine/`
(`fact_digests.go`, `collection_action.go`, `executor.go`, `facts.go`, `become.go`, tests), `internal/sealed/`
(`key.go`, `sealed.go`, test), `internal/adapters/native/` (`adapter.go`, `fact_seal.go`, `ipc_parent.go`, tests),
`internal/dispatch/` (`worker_config.go`, `worker_devices.go`), `internal/catalog/facts/` (`gather.go`,
`gather_files.go`, `gather_win_items.go`, tests), `pkg/collection/facts.go`, `pkg/wire/dispatch.go`, `cmd/controller/`
(`main.go`, `builtin_catalog.go`, `fact_digest_keys.go`, tests), `cmd/pleiades/` (`seal.go`, `run_pipeline.go`,
tests, lab gates), `cmd/runner/facts_release_gate_test.go`, `internal/forge/catalogdata/collections_gating.go`,
`helm/the-pleiades/` (values, schema, templates), `docker-compose.yml`, `docs/10-running-in-production.md`, generated
`docs/reference/modules/facts/gather.md`, `docs/reference/schemas/module-catalog.json`,
`internal/api/wellknown/module-catalog.json`, `changelog/facts-keyed-digests.security.md`,
`changelog/facts-files-ports-users.added.md`. Records: `FAILURE_PATTERNS.md` and its archive (878 to 888),
`LESSONS_LEARNED.md` and its archive (393 to 395), `HANDOFF_DOCUMENT.md`, `HANDOFF_ARCHIVE.md`, and the ignored
`.SPECIFICATION/IMPLEMENTATION.md` (41c) and `.SPECIFICATION/SECURITY_ATTESTATION.md` (PW.5).

Lab: unchanged. bsd-check and win-check are powered off (ACPI); the lab Mac was used for its gate.
