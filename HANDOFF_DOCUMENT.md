# Handoff Document

Rewrite the "Current Status" section when stopping mid-task or handing off, per `.AGENTS/AGENTS.md`.

## Current Status (this session)

**Branch `feature/Phase-79-96c-46-Closeout`, cut from `main` at `ce7b012` (PR #43), 2026-09-27.
Phases 79, 96c and 46 are done (tracker 35/35, 13/13, 33/33), which finishes v0.2.0 (638/638).**
Committed on this branch as 13 commits, one logical change each, and gated again on the committed
tip with `make push-gate` before the push. Rules the user set this session:
dogfood as much as possible and build Collections and modules as needed; reuse first, DRY modules
unless it confuses users (memory `reuse-first-dry-modules`).

### What was done

- **Housekeeping:** `golang.org/x/mod` v0.38.0 to v0.40.0 (GO-2026-6179/6180; `sumdb` and
  `sumdb/tlog` are in no build graph, though `x/mod/semver` is in the module's own through
  `ariga.io/atlas`, and the ent tool's; `x/tools` moved to v0.49.0 with it). Coverage floors
  `pkg/devicetls` 92.9 (it had fallen to 91.8 when `864b34f` added `CAPEM` untested; a test added),
  `pkg/external` 93.0, and `pkg/bsdinstall` 94.3.
- **Phase 79:** email fuzz through the real store; four defects found and fixed (FAILURE_PATTERNS
  370 to 373): invalid UTF-8 repaired into another stored address, a NUL failing fast on Postgres
  without the decoy, an oversized password as an existence oracle, `UserID` 0 on success. One rule now,
  `auth.NormalizeEmail`, on the raw input via the new `termsafe.CheckLine`.
- **Phase 96c:** the outage release gate (real NATS behind Toxiproxy, mutation-checked; the helper is
  now `testsupport.NATSThroughToxiproxy`) and the dedup-table benchmark (about 220 B per entry, 2.5
  times the memory at 5 minutes as at 2).
- **Phase 46:** the classifier cross-check (`reviewedCheckClass`, ten reviewed pairs); every check held
  to its real run on real apt, dnf, firewalld (Rocky 9.6 under systemd, no `--privileged`), a real
  Windows host (the WinRM gates, rewritten onto the same `checkThenRun`), toybox, and FreeBSD.
- **FreeBSD, built through dogfooding:** `virt.vbox.vm.install` takes `installer: windows | freebsd`
  (strategy per installer, `pkg/bsdinstall`); the FreeBSD base installs from the staged DVD by watching
  the VM's screen and typing a command that reports on the serial port; clones are seeded by nuageinit;
  `generic_ssh` onboarding grants FreeBSD POSIX file access. Found and fixed: `pkg/remotefile` read BSD
  modes without setuid/setgid/sticky (375), `adhoc` typed `mode=0755` as a number (376), and a test
  file named `*_freebsd_test.go` silently never ran (374).

### Lab state

`freebsd-base` (installed, snapshot `base`, off) and `bsd-lab` (running, 192.168.56.40, onboarded as
`generic_ssh` with login `pleiades`) are new, with `ubuntu-lab` and `win-lab` still running: about 6 GiB
of VMs, the agreed ceiling. `~/pleiades-lab/inventory.yaml` holds `bsd-lab`. `win-lab`'s TelnetClient
is left enabled (the gate's documented end state). `~/pleiades-lab/bin/pleiades` is still the old build;
these runs used this branch's binary. The gates run as:
`PLEIADES_WINRM_PROJECT=~/pleiades-lab PLEIADES_WINRM_DEVICE=win-lab PLEIADES_WINRM_TEST_SERVICE=SysMain
PLEIADES_WINRM_TEST_FEATURE=TelnetClient` and `PLEIADES_BSD_PROJECT=~/pleiades-lab PLEIADES_BSD_DEVICE=bsd-lab`.

### Gate run (overnight, 2026-09-27/28, every step of `make push-gate`, capped, one at a time)

That run was on the uncommitted tree, so it could write no receipt; this is its evidence, step by step.
The committed tip then ran `make push-gate` again for the receipt the push needs.

- **Passed:** `push-gate-race` (only `internal/catalog/cloud/aws/s3`, a listed package, failed under load,
  and all 12 of its tests passed alone); `push-gate-integration` (196 packages, nothing tolerated);
  `build`, `vet`, `fmt`, `tidy-check`, `docs-lint`, `helm-lint`, `templ-gen-check`; `gosec` and
  `govulncheck` earlier in the session, with only test files changed since.
- **`docs-gen-check`** fails by construction on an uncommitted tree (`git diff --exit-code`); `gendocs`
  was shown stable against the working tree by hashing before and after, with no untracked files.
- **`test-repeat` found two tests that fail when run twice, now fixed** (`-count=5` green):
  `TestUsersView_RefusesAnUnusableAddressAtTheControl`, mine, created a fixed address in the package's
  process-wide database (now `uniqueName`); and a failure that predates this branch in `internal/catalog/wait`, where
  `deadPort` poisoned `remoteexec`'s process-wide breaker for a port the kernel then gave a later
  test's live server (FAILURE_PATTERNS 377; `deadPort` now takes `remoteexec.SnapshotForTest`).
- **`push-gate-coverage` found `internal/catalog/virt/vbox/vm` at 96.7% against its 97.2% floor**, from the
  freebsd installer's untested failure branches. `freebsd_failures_test.go` now fails each host call the
  installer adds (mutation-checked twice), and the package is at 97.4%. On the rerun, 251 packages were
  none below floor, but 16 failures were tolerated as passing alone: `internal/ent` (listed) and
  `TestToyboxChmod_FiveDigitModesMeanWhatTheySay`, whose message coverage-check does not keep. Docker
  logged no OOM or abnormal exit then, and it passes alone in 5 s. Unlike its BusyBox twin it
  downloads toybox from landley.net on every run; if it recurs, that is the first suspect, and caching
  the checksummed download is the remedy. Not listed as flaky on one unexplained sighting.

### Decisions for the user

1. **The broker's memory at the derived dedup window: now demonstrated, not reasoned.** Against the chart's
   own broker (same image and flags, `--memory=512m`, no GOMEMLIMIT) at 7,000 256-byte log msgs/s: the 5m
   window was **OOM-killed** at about 5 minutes; the old 2m window survived 10 minutes but peaked 3 MiB
   under the limit and saw-toothed near it; with **no message id**, the broker stayed flat at 37 MiB while
   storing 2.31 million messages. So the per-line id is the whole cost, the old window was already a near
   miss (the reasoned 15,000 msgs/s for it was wrong: LESSONS 253), and 96c turns it into a kill. Anyone
   who can run a job that prints a lot can drive the rate. Candidates: no message id on log events (the
   evidence says this alone removes it; changes `event.Bus`'s publish contract), GOMEMLIMIT on the broker
   (helps the 2m case's headroom, not the 5m live table), or a larger limit. Recorded in 96c's evidence.
2. **Phase 80:** an address profile (PRECIS, RFC 8265) before an identity provider can create users.
3. Carried: Phase 113 has no Pattern Entry Gate item (the one attestation problem); the generic gate
   still writes `fqcn:` runbooks and passes `--password` on argv; `persist_connections` levels.

### Files changed

See `git status`: `internal/auth/email.go`, `internal/termsafe`, `internal/localauth`, `internal/access`,
the users form, `pkg/bsdinstall` (new), `internal/catalog/virt/vbox/vm` (`install_os.go`, install,
clone seed, the console watcher), `pkg/vboxmanage` (`SetConsoleLog`, `FreeBSD()`, the fake's screens and
guests), `pkg/cloudinit` (`Shell`, exported markers, `ConsoleText`), `pkg/remotefile`, the SSH probe,
`cmd/pleiades` (`adhoc`, the new gates and device helper), `internal/runner`, `internal/event`,
`internal/testsupport`, `internal/forge/playbook`, `internal/forge/catalogdata`, the VirtualBox lab example,
docs 02 and 10, generated references, five changelog fragments (plus `local-login-address-hardening`),
CLAUDE.md, FAILURE_PATTERNS 370 to 377, LESSONS 250 to 253. The overnight gate run added three test
changes: `internal/catalog/virt/vbox/vm/freebsd_failures_test.go` (new),
`internal/catalog/wait/connection_test.go` and `internal/ui/resources/users_test.go`. Local only: IMPLEMENTATION.md,
SECURITY_ATTESTATION.md.
