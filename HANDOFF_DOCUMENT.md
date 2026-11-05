# Handoff Document

Rewrite the "Current Status" section when stopping mid-task or handing off, per `.AGENTS/AGENTS.md`.

## Current Status (this session)

**2026-11-05. PR #115 (`fix/ssh-sweep-b`, SSH PR B) merged as `75f3a520`: every check green first time, the advisory
`fast-macos` included; pr-check at `47f40deb` passed, bsd-check of `pkg/remoteexec` passed, branch-containers run
42074118567 passed 6/6 after 917's fixture fix. Now on `fix/redact-nested-bytes` from `75f3a520`: 918 (a secret in
nested `[]byte`, in logs and stats), cherry-picked as `c619cdf1` and `25b1fd27`; not through pr-check, push or a pull
request yet.**

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
- Test fixes: FreeBSD `/dev/stdin` probe (906), wall-clock bound removed (914), a vacuous pass closed (915), and a
  fixture that wrote its own marker to stderr (916, found by bsd-check).
- Passing on 2026-11-05 (all under `~/.local/bin/capped 2500M`, `GOMAXPROCS=4`, `-race -count=1`): `pkg/remoteexec`
  whole package (about 69 s, twice); `internal/redact`; `internal/archtest`'s GoSource tests;
  `TestSSHContainer_ACommandCutForItsOutputIsEndedNotWaitedOn` (1.2 s, real sshd); `go vet -tags integration` of
  `pkg/remoteexec`, `internal/archtest`, `internal/transport/ssh`. `make pr-check` at `44e72fe5` (monitor, `--cap
  4600M`): pass, about 21 min, 63 packages (a first run at `--cap 4000M` was stopped by the memory guard after 10 s, no
  test failed). bsd-check of `pkg/remoteexec`: failed at `44e72fe5` on 916 (1 run in 3), passed at `31af3e90` (5m20s),
  so `detachStop`'s `${#p}`, `${d%/*}` and `[ -O ]` are proven on FreeBSD's sh.
- Records: FAILURE_PATTERNS 896 to 916 (891 and 851 given their resolutions), LESSONS 398 and 399, CI_LESSONS 26,
  changelog fragment `changelog/ssh-detach-stop-and-log-masking.security.md`.

### Incident this session

The first version of a red-first test ran the old stop script's `kill -KILL -1` through `remoteexectest`'s local sh
and killed the user session twice (the second time at 2026-11-05 08:56:03 EST, proven by `journalctl -u
user@1000.service`; the first was read then as "WSL crashed"). The owner
restarted it with `sudo systemctl start user@1000.service`; `capped` works again. FAILURE_PATTERNS 907, LESSONS 398
and 399.

### Current blocker

None. Next is pr-check for `fix/redact-nested-bytes`.

### Open

- `internal/catalog/gate/runner.go` `deviceRunner.stop` refuses only `pid <= 0`: a hostile device printing `1` as its
  pid marker would get `kill -TERM 1` and `kill -KILL 1`, as root to init under become. No group kill, low severity,
  needs a test seam. Not fixed, not yet a failure pattern.
- Carried: `Conn.Close` returns an error after a sever; a sever timer already running when `finish` is called can
  close a non-pooled `Conn` that `stopDetached` then reuses (it redials). Routes are not followed on a Runner, by
  external Collections, or by the onboarding SSH probe; a no-`sh` device onboarded before #110 fails at `/bin/sh`
  until onboarded again, and an older Runner reads `no posix sh` as POSIX.
- Carried: FAILURE_PATTERNS 448 (`pkg/tftpxfer` on macOS); `internal/setup`'s `Fields` comment; 867, 853, 41d's
  items, vuln-corpus `C13_auto-roboto_2026-11-03` pending review, the owner decisions of 2026-11-04 (see the
  archive's earlier entries).
- API credit: $20.53 of $92 spent as of the last report; no new spend was reported this session. Expires 2026-11-08.

### Next, in order

1. `fix/redact-nested-bytes`: `make pr-check` through the monitor (`--cap 5200M` fits; 4600M was stopped by the
   memory guard at 119 packages), push, `make branch-containers` (it picks the container packages from the diff), the pull request.
   Merge only when green, `record-actions`, pull `main` and clean.
2. PR #115 is done: `record-actions` ran for it and both branch-containers runs; the 19 fixture failures are annotated
   with 917.
3. Facts sweep PRs from `VERDICTS-facts.md`: security items 2, 3 and 12, plus 7 and 13, first; then correctness 1, 8,
   9, 11, 14, 15 and 16; then churn 4, 5, 6 and 10.
4. 41c-3, then 41d to 41g.
5. `file.remove`'s check refuses by default.
6. 117b, 117c, 105.
7. Release 0.4.0 (no tag until the owner says).

### Files changed on this branch

`pkg/remoteexec/` (`conn.go`, `conn_watch.go`, `detach.go`, `detach_script.go` (new), `detach_script_test.go` (new),
`detach_test.go`, `process.go`, `stream.go`, `stream_cancel_test.go` (new)), `internal/transport/ssh/`
(`ssh_cancel_container_test.go`, `ssh_detach_container_test.go` (new), `ssh_refused_exec_container_test.go` (new)),
`internal/redact/` (`masker.go`, `attr_values_test.go` (new), `edges_test.go`, `redact_bench_test.go`),
`internal/archtest/source_chars_test.go`, `pkg/aitask/fuzz_test.go`, `pkg/iosparse/py_internal_test.go`. Records:
`FAILURE_PATTERNS.md` and its archive, `LESSONS_LEARNED.md` and its archive, `CI_LESSONS.md`, `HANDOFF_DOCUMENT.md`,
`HANDOFF_ARCHIVE.md`, `changelog/ssh-detach-stop-and-log-masking.security.md` (new).

Lab: bsd-check was started twice on 2026-11-05 by `tools/bsdcheck` for `pkg/remoteexec` and shut down after each run.
