# Handoff Document

Rewrite the "Current Status" section when stopping mid-task or handing off, per `.AGENTS/AGENTS.md`.

## Current Status (this session)

**2026-11-22. Four branches of SSH and certificate key work. `main` is at `48334fdc`: #227 (identity facts,
`b7f83669`) and #228 (`tls.certificate.serial`, branch D `feat/tls-certificate-serial`) merged today. The design for
all four was checked against edge cases in an outside design review and accepted by the owner on 2026-11-22. These
records were written in the worktree of `feat/ssh-host-key-facts`. This records pass ran no test and no gate; the
proof below is the branches' own, as reported.**

### What works

- **A, `fix/ssh-user-key-signatures`**, pushed; `make pr-check` passed at `9674037f`; its `make branch-containers` was
  running when this was written. An RSA user key no longer signs with SHA-1 unless the device sets
  `ssh_allow_legacy_user_key_signatures` (FAILURE_PATTERNS 1372), project sync is held to SHA-2 too (1373), and a
  failed later hop closes the bastions before it (1374, security, on main and in v0.3.0). Proof:
  `TestSSHContainer_AnRSAKeySignsSHA1OnlyWhereTheRecordAllowsIt`, `TestGitSyncer_AnRSAKeyNeverSignsSHA1`,
  `TestHops_AFailedLegClosesTheHopsBeforeIt`.
- **B, `feat/credential-key-types`**, pushed; `make pr-check` passed at `e0efd842`; its `make branch-containers` was
  running. `add-credential` records each key's type and fingerprint, and `--generate --key-type` chooses the key. A
  sealed key is described by what it seals (1377). Proof: `TestSaveAndLookup_RefuseAHeaderNamingAnotherKey`.
- **C, `feat/ssh-host-key-facts`**, rebased on `b7f83669`, not yet gated. `facts.gather` records `ssh.host_key.<type>`
  from a verified session only. Review fixes are in: a borrowed connection keeps its host keys (1375,
  `TestLend_EveryConnFieldIsPlaced`), and every family reserved since v0.3.0 has its breaking fragment (1376).
- Records: FAILURE_PATTERNS 1372 to 1380, LESSONS 481, CI_LESSONS 43, dated tracker notes (nothing ticked).

### Blocker

- C waits for A: both change `pkg/remoteexec` and conflict there.

### Open

- FAILURE_PATTERNS 1378 (security, owner decision): the credential vault's AES-GCM passes no associated data, so a
  sealed secret and its record can be moved to another device's entry. The fix needs a stored-format migration. Not
  started.
- FAILURE_PATTERNS 1379: a baseline taken before a release reads each new stable fact key as `added` (drift, exit 3).
  Documented in the changelog only; a baseline records no declared key set.
- FAILURE_PATTERNS 1380 (for the owner): `tests/mac/.env`, `tests/bsd/.env` and `tests/win/.env` are missing from the
  main clone, so every pr-check today passed with its Mac, FreeBSD and Windows legs not run. Restoring them needs the
  machine addresses.
- A's lab proof: no real device that takes only `ssh-rsa` with no `server-sig-algs` (IOS 15.2, or OpenSSH before 7.2)
  has been reached; the proof is a container sshd.
- Tracker, Phase 143: the "Certificate facts" item says onboarding records the certificate "as stable facts", but
  onboarding writes discovery facts into `discovered`; and the owner's 2026-11-19 decision that `kubernetes_cluster`
  records `tls.*` certificate facts looks unbuilt (both reported).
- Carried and not re-checked: FAILURE_PATTERNS 1371 and the rest of the last handoff's open list
  (`HANDOFF_ARCHIVE.md`).

### Next steps

1. Merge A when its branch-containers run and its pull request's checks pass.
2. Merge B next: its RSA key-type note assumes A's SHA-2 rule.
3. Rebase C on the new `main`, resolve `pkg/remoteexec` against A, commit these records, then `make pr-check`, push,
   `make branch-containers`, and open its pull request.
4. Ask the owner about 1378 and 1380.

### Files changed

Branch C: `pkg/remoteexec` (`hostkeys.go`, `conn.go`, `pool.go`, `remoteexec.go`, `remoteexectest/twoservers.go` and
tests), `pkg/sshfacts`, `internal/catalog/facts` (`gather_hostkeys.go`, `gather.go`, `gather_transport.go` and tests),
`pkg/collection/facts_reserved.go`, `internal/forge/catalogdata/collections_gating.go`, the generated catalogs,
reference and `first-party.json`, `docs/10-running-in-production.md`, `docs/11-extending-pleiades.md`, and the
changelog fragments `ssh-host-key-facts.added.md`, `fact-families-reserved-after-0-3-0.breaking.md` and
`facts-identity.added.md`. Records in this pass: `FAILURE_PATTERNS.md`, `FAILURE_PATTERNS_ARCHIVE.md`,
`LESSONS_LEARNED.md`, `LESSONS_LEARNED_ARCHIVE.md`, `CI_LESSONS.md`, `HANDOFF_DOCUMENT.md`, `HANDOFF_ARCHIVE.md`, and
the git-ignored tracker in the main clone.

Lab: not reported to this records pass; this pass started and stopped no VM.
