# Handoff Document

Rewrite the "Current Status" section when stopping mid-task or handing off, per `.AGENTS/AGENTS.md`.

## Current Status (this session)

**2026-11-04. Branch `fix/ssh-sweep-2026-11-04`, from `main` at `a176a48b` (PR #113 merged; every required check
passed first time, the advisory `fast-macos` included). Five commits: `a50bc048` (a cancelled command is never a
completed one), `9ca3ef7c` (a routed device is dialed through its route), `01e5c792` (the pre-#110 shell value),
`72474f70` (coverage tests), `bd004d1e` (fp-reviewer's findings), plus the records (uncommitted). Built from
`.IGNORE/claude-api/out/sweep-2026-11-04/VERDICTS-ssh.md` items 1, 2, 3, 4, 5, 9 and 10. Not through `make pr-check`,
push, `make branch-containers` or a pull request yet.**

### What works (each proven by a test that failed on the old code and passes now)

- **A cancelled SSH command fails with the cancellation** (FAILURE_PATTERNS 891). Against the real sshd container it
  used to return partial stdout with no error, or exit 143; a done context still sent the command; a failed stdin read
  was sent as a clean end of input. `internal/transport/ssh/ssh_cancel_container_test.go`,
  `pkg/remoteexec/conn_cancel_test.go`.
- **A Collection method and `net.ssh.ping` reach a routed device through its bastion** on `run` and `adhoc`
  (892). `cmd/pleiades` `TestCLI_ACollectionMethodReachesARoutedDeviceThroughItsBastion`.
- **A fish or tcsh host onboarded before #110 gets the `sh` wrapper** (893, closing 858's open half): new value
  `no posix sh`, the old `not posix` read as unrecorded.
- **fp-reviewer's two findings are fixed** (894, a repeat of 146 and 398; 895, C15):
  `TestConnectVia_ARoutedTargetsCircuitRecovers`, `TestRun_ACancelledCommandThroughAStalledBastionIsBounded`.
- Passing: `go test -race -tags integration` for `pkg/remoteexec` (94.7%, floor 94.5), `pkg/sdk` (100),
  `pkg/inventory` (100), `internal/catalog/net/ssh` (94.9, floor 92.1), `internal/transport/ssh` (96.7); all of
  `internal/archtest`; `GOOS=windows` vet of the changed packages.
- PR #113: its fp-reviewer findings were closed except `internal/setup`'s `Fields` comment (still open); its gate
  passed; `record-actions` ran for the PR and for run 41915386917 (nothing new).
- Records: FAILURE_PATTERNS 891 to 895 (858's index line updated, its archive entry given the resolution), LESSONS
  396 and 397.

### Current blocker

None. Next is the gate.

### Open

- **Carried to the next SSH pull request:** `pkg/remoteexec/stream.go` (`Conn.Start`, `Process`, subsystems) has
  891's cancelled-as-completed bug (fp-reviewer F4); verdicts items 6, 7 and 12 (detach), 8 and 13 (redact `Attr`
  kinds and groups), 11 (archtest invisible characters).
- Smaller, noted: `Conn.Close` returns an error after a sever (only `tools/maccheck` reads it); a sever timer already
  running when `finish` is called can close a non-pooled `Conn` that `stopDetached` then reuses (it redials).
- Not confirmed by the verdicts, so not built: a session cut by the output cap is never signalled; a refused exec puts
  the whole command (which may embed a secret) in the error.
- Routes are not followed on a Runner, by external Collections, or by the onboarding SSH probe (stated in docs/11 and
  the changelog). A no-`sh` device onboarded before #110 fails at `/bin/sh` until onboarded again, and an older Runner
  reads `no posix sh` as POSIX.
- Carried forward: FAILURE_PATTERNS 448 (`pkg/tftpxfer` on macOS) is still open; `internal/setup`'s `Fields`
  comment; the other items in the archive's previous entry (867, 851, 853, 41d's items, vuln-corpus
  `C13_auto-roboto_2026-11-03` pending review, the owner decisions of 2026-11-04).
- API credit: $20.53 of $92 spent (no new spend this session); expires 2026-11-08.

### Next, in order

1. This pull request: `make pr-check` through the monitor, push, `make branch-containers`, then the pull request
   (failure-pattern classes in the body, no AI attribution). Merge only when green, `record-actions`, pull `main` and
   clean.
2. SSH PR B (the carried items above).
3. Facts sweep PRs from `VERDICTS-facts.md`: security items 2, 3 and 12, plus 7 and 13, first; then correctness 1, 8,
   9, 11, 14, 15 and 16; then churn 4, 5, 6 and 10.
4. 41c-3.
5. 41d to 41g.
6. `file.remove`'s check refuses by default.
7. 117b, 117c, 105.
8. Release 0.4.0 (no tag until the owner says).

### Files changed this session

`pkg/remoteexec/` (`conn.go`, `conn_watch.go` (new), `script.go`, `remoteexec.go`, `hop.go`, `tunnel.go`,
`detach.go`, `abandon_test.go`, `conn_cancel_test.go`, `connectvia_test.go`), `pkg/sdk/` (`connect.go`,
`connect_test.go`), `pkg/inventory/` (`execshell.go`, `execshell_test.go`), `internal/catalog/net/ssh/` (`ping.go`,
`ping_route_test.go`), `internal/transport/ssh/ssh_cancel_container_test.go`, `cmd/pleiades/route_collection_test.go`,
`docs/11-extending-pleiades.md`, `CLAUDE.md` (the shell value), `changelog/ssh-cancel-route-legacy-shell.security.md`,
and `~/argus/MCP_USAGE_LOG.md` rows 198 to 207. Records: `FAILURE_PATTERNS.md` and its archive (891 to 895, 858),
`LESSONS_LEARNED.md` and its archive (396, 397), `HANDOFF_DOCUMENT.md`, `HANDOFF_ARCHIVE.md`.

Lab: unchanged, as far as the facts given say. bsd-check and win-check are powered off (ACPI); no test listed used a
lab machine.
