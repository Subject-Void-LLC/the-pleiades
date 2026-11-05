# Handoff Document

Rewrite the "Current Status" section when stopping mid-task or handing off, per `.AGENTS/AGENTS.md`.

## Current Status (this session)

**2026-11-05. Branch `fix/ssh-sweep-b` (SSH PR B), from `main` at `793377ff` (PR #114 merged). Eleven commits:
`bd9fc13b`, `49688a6e`, `eaafb354`, `f041ed28`, `2dcb1f18`, `ec9b0a23` (2026-11-04), then `6bef8e91`, `987603a2`,
`a1243fae`, `b34d029e`, `3aa5337a` (2026-11-05), plus the records (uncommitted). Not through `make pr-check`,
bsd-check, push, `make branch-containers` or a pull request yet.**

### What works (each fix's test failed on the old code where its entry says so)

- **Streamed SSH commands cancel** (FAILURE_PATTERNS 896, closing 891's open part), a command cut for its output is
  ended (897), and a command sshd refuses names no command in its error (898). Real sshd tests in
  `internal/transport/ssh`.
- **Detached (`async:`) commands:** a cut-short command never starts (899), `~/.ssh/rc` output no longer breaks polls
  (900), no `setsid` on BusyBox runs attached (901), lost starts and polls stop the job by a client-chosen name (902).
- **The stop never signals a pid a job cannot have** (907): only digits, no leading zero, at most 7 digits, more than
  1, under an owned base in an owned HOME, and only the process group (909). It says whether the job was stopped, had
  already finished, or may be running (908, 910), and a finished job leaves no directory (911). Dangerous pids are
  tested only against a logging `kill` behind a control (`TestDetachStop_NeverSignalsAPidAJobCannotHave`).
- **Log masking** (`internal/redact`): non-string values (903, 904) and secret-named keys inside maps and structs (912).
- **archtest** refuses every invisible character class (905, closing 851's gap); its symlink case skips where links
  cannot be made (913).
- Test fixes: FreeBSD `/dev/stdin` probe (906), wall-clock bound removed (914), a vacuous pass closed (915).
- Passing on 2026-11-05 (all under `~/.local/bin/capped 2500M`, `GOMAXPROCS=4`, `-race -count=1`): `pkg/remoteexec`
  whole package (about 69 s, twice); `internal/redact`; `internal/archtest`'s GoSource tests;
  `TestSSHContainer_ACommandCutForItsOutputIsEndedNotWaitedOn` (1.2 s, real sshd); `go vet -tags integration` of
  `pkg/remoteexec`, `internal/archtest`, `internal/transport/ssh`.
- Records: FAILURE_PATTERNS 896 to 915 (891 and 851 given their resolutions), LESSONS 398 and 399, CI_LESSONS 26,
  changelog fragment `changelog/ssh-detach-stop-and-log-masking.security.md`.

### Incident this session

The first version of a red-first test ran the old stop script's `kill -KILL -1` through `remoteexectest`'s local sh
and killed the user session twice (the second time at 2026-11-05 08:56:03 EST, proven by `journalctl -u
user@1000.service`; the first was read then as "WSL crashed"). The owner
restarted it with `sudo systemctl start user@1000.service`; `capped` works again. FAILURE_PATTERNS 907, LESSONS 398
and 399.

### Current blocker

None. Next is the gate.

### Open

- `internal/catalog/gate/runner.go` `deviceRunner.stop` refuses only `pid <= 0`: a hostile device printing `1` as its
  pid marker would get `kill -TERM 1` and `kill -KILL 1`, as root to init under become. No group kill, low severity,
  needs a test seam. Not fixed, not yet a failure pattern.
- `internal/redact`: a `[]byte` field inside a struct holding a registered secret is still printed as base64 (JSON)
  or decimal (text), and missed (912's open note).
- bsd-check of `pkg/remoteexec` has not run since `detachStop` changed; it uses `${#p}`, `${d%/*}` and `[ -O ]`, which
  FreeBSD's sh is reasoned, not proven, to support.
- Carried: `Conn.Close` returns an error after a sever; a sever timer already running when `finish` is called can
  close a non-pooled `Conn` that `stopDetached` then reuses (it redials). Routes are not followed on a Runner, by
  external Collections, or by the onboarding SSH probe; a no-`sh` device onboarded before #110 fails at `/bin/sh`
  until onboarded again, and an older Runner reads `no posix sh` as POSIX.
- Carried: FAILURE_PATTERNS 448 (`pkg/tftpxfer` on macOS); `internal/setup`'s `Fields` comment; 867, 853, 41d's
  items, vuln-corpus `C13_auto-roboto_2026-11-03` pending review, the owner decisions of 2026-11-04 (see the
  archive's earlier entries).
- API credit: $20.53 of $92 spent as of the last report; no new spend was reported this session. Expires 2026-11-08.

### Next, in order

1. `make pr-check` through the monitor (`.venv/bin/python tools/monitor/monitor.py run`).
2. bsd-check of `pkg/remoteexec`.
3. Push, `make branch-containers`, then the pull request (failure-pattern classes in the body, no AI attribution
   anywhere). Merge only when green, `record-actions`, pull `main` and clean.
4. Facts sweep PRs from `VERDICTS-facts.md`: security items 2, 3 and 12, plus 7 and 13, first; then correctness 1, 8,
   9, 11, 14, 15 and 16; then churn 4, 5, 6 and 10.
5. 41c-3, then 41d to 41g.
6. `file.remove`'s check refuses by default.
7. 117b, 117c, 105.
8. Release 0.4.0 (no tag until the owner says).

### Files changed on this branch

`pkg/remoteexec/` (`conn.go`, `conn_watch.go`, `detach.go`, `detach_script.go` (new), `detach_script_test.go` (new),
`detach_test.go`, `process.go`, `stream.go`, `stream_cancel_test.go` (new)), `internal/transport/ssh/`
(`ssh_cancel_container_test.go`, `ssh_detach_container_test.go` (new), `ssh_refused_exec_container_test.go` (new)),
`internal/redact/` (`masker.go`, `attr_values_test.go` (new), `edges_test.go`, `redact_bench_test.go`),
`internal/archtest/source_chars_test.go`, `pkg/aitask/fuzz_test.go`, `pkg/iosparse/py_internal_test.go`. Records:
`FAILURE_PATTERNS.md` and its archive, `LESSONS_LEARNED.md` and its archive, `CI_LESSONS.md`, `HANDOFF_DOCUMENT.md`,
`HANDOFF_ARCHIVE.md`, `changelog/ssh-detach-stop-and-log-masking.security.md` (new).

Lab: no lab machine was used this session, as far as the facts given say. bsd-check ran on 2026-11-04 (906's
failure); its state after that run was not reported.
