# Handoff Document

Rewrite the "Current Status" section when stopping mid-task or handing off, per `.AGENTS/AGENTS.md`.

## Current Status (this session)

**2026-11-04. Branch `fix/journal-test-planted-values`, from `main` at `f72af1bd` (PR #112 merged; its required checks
passed first time, and the advisory `fast-macos (1)` failed on a repeat of FAILURE_PATTERNS 448). Two commits:
`43942bd8` (the journal test flake) and `80c500de` (`controller setup` writes `PLEIADES_FACTS_DIGEST_KEY`), plus the
records (uncommitted). fp-reviewer is running. Not through `make pr-check`, push or a pull request yet.**

### What works (each proven by a test that ran)

- **The journal test no longer fails by chance** (`43942bd8`, FAILURE_PATTERNS 890). `TestJournalAdmitsOnlyDeclaredKeys`
  planted `"0644"`, which the entry's random hex run ID and DAG version can spell; it failed main's post-merge run after
  PR #111 with nothing leaked. It now plants `"planted-mode-rw-r--r--"`, and `assertNoValueLeaked` fails at once on a
  planted value made only of hex digits and dashes (negative control: `"0644"` put back is refused at once).
  `internal/engine` `TestJournal*` passed three times.
- **`controller setup` writes `PLEIADES_FACTS_DIGEST_KEY`** (`80c500de`): into `.env` for Compose, and into the Helm
  Secret with `existingSecretHasFactsDigestKey: true` in the values. A re-run over an older `.env` adds only the key;
  the key is never replaced (class Repoint). `RenderHelm` refuses a key the Controller would refuse. Passing:
  `go test ./internal/setup`, `go test ./tools/helm-lint`, `make helm-lint` (the setup profile checks the Controller
  reads the key, with a negative control), `cmd/controller` `TestSetupReleaseGate_NonInteractiveReachesAWorkingSignIn`
  (the real binary writes a key the Controller's decode and `DigestKeys` accept, and never prints it) and
  `TestLoadFactDigestKeys`. `tests/e2e` was vetted only, not run.
- The argus fix is committed in `~/argus` (`b74c1b0`, `3fd801f`), not pushed.
- Records: FAILURE_PATTERNS 890 (a repeat of 236) and a third occurrence added to 448; CI_LESSONS 25; ledger entries
  annotated (890, 448).

### Current blocker

None. Next is fp-reviewer's report, then the gate.

### Open

- **FAILURE_PATTERNS 448 is open: `pkg/tftpxfer` times out on macOS.** Third time on PR #112 (run 41904826019): a Put
  request, the server sending to `127.0.0.1:<client port>` with no reply read by the client on `[::]`. Fix candidates:
  pin the client to `udp4` and `127.0.0.1`, or a recorded, counted darwin retry.
- On a Runner a task's register holds `sha256_sealed`, not `hmac_sha256` (documented). CLI keyed digests re-key if the
  project master key rotates. Private files are now read in full to hash them (audit and I/O cost for large or audited
  paths, as Ansible's stat reads them).
- The Runner now shows the login name in output as the CLI does (owner rule: allowlist identifiers). `pkg/netconf`
  keeps its own copy of the identifier allowlist; `internal/loader` and the legacy adapter still mask the username
  (stricter, so the tiers differ for external Collections).
- `~/pleiades-lab/bin/pleiades` (built 2026-10-24) hangs after sealing a run; main's build does not.
- API credit: $20.53 of $92 spent (as of the last session, 2026-11-04).
- Carried forward: Windows and ephemeral UDP endpoints churn until 41d (owner); two `windowsAbsolute` copies with no
  test holding them together (867); a fish host onboarded before #110 stays raw until onboarded again (858);
  vuln-corpus finding `C13_auto-roboto_2026-11-03` pending review; argus `expand_seeds` wrote fuzz files despite
  `persist:false`; `internal/archtest` misses zero-width characters (851); a Runner publish failing mid-split leaves
  earlier batches stored (853); no fact marks a package or service gone (41d); whether a Remote Management Users
  member alone can read the Windows probes is unproven.
- Owner decisions 2026-11-04 (to build): Phase 41 resolves facts at rest in 0.4.0; Landlock network rules are out of
  0.4.0; the Controller checks a Runner's order token and label (a Phase 41b item); `secret_mask` residuals (836):
  the approved fix holds affected facts until the masking tasks ran, then guards by value (a Phase 41b item);
  `file.remove`'s check is configurable and refuses by default (a Phase 46 item); 41c's `pkg/` deviation is accepted,
  and the listers move to `pkg/` when a second user appears.

### Next, in order

1. This pull request: fp-reviewer's findings, `make pr-check` through the monitor, push, `make branch-containers`,
   then the pull request (failure-pattern classes in the body, no AI attribution). Merge only when green,
   `record-actions`, pull `main` and clean.
2. The verified sweep fixes, from `.IGNORE/claude-api/out/sweep-2026-11-04/VERDICTS-*.md`.
3. 41c-3.
4. 41d to 41g.
5. `file.remove`'s check refuses by default.
6. 117b, 117c, 105.
7. Release 0.4.0 (no tag until the owner says).

### Files changed this session

`internal/engine/journal_entry_test.go` (`43942bd8`); `internal/setup/` (`class.go`, `envfile.go`, `helm.go`,
`plan.go`, `run.go`, `run_helm.go`, `summary.go`, tests), `tools/helm-lint/setupprofile.go`,
`cmd/controller/setup_release_gate_test.go`, `tests/e2e/packaging_kind_setup_test.go`,
`docs/10-running-in-production.md`, `changelog/setup-writes-facts-digest-key.added.md` (`80c500de`). Records:
`FAILURE_PATTERNS.md` and its archive (890, 448), `CI_LESSONS.md` (25), the ignored failure ledgers,
`HANDOFF_DOCUMENT.md`, `HANDOFF_ARCHIVE.md`, and the ignored `.SPECIFICATION/IMPLEMENTATION.md` (a dated note on 41c's
keyed-digest deviation; nothing ticked).

Lab: unchanged, as far as the facts given say. bsd-check and win-check are powered off (ACPI); none of this branch's tests
needed a lab machine.
