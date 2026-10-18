

## 504. The OS checks' stop test put its stand-in go last on the PATH, so the real go failed at once and the test read that as a stop (a repeat of 54) (fixed before push)

**Symptom.** 2026-10-18, the failure-pattern review of the feat/wincheck branch: `TestTestAndStopScripts_RunUnderSh`
passed in 0.056 s, where a real stop of a running test takes seconds.

**Root cause.** The test's stand-in `go`, which sleeps so a stop has something to end, sat in the directory the
script appends to the end of the PATH. The real `go` came first, failed in the empty tree within 20 ms, and the
test took its non-zero exit for a stop; its "process group is gone" check passed because none had existed. Even
with the stand-in first, Linux's `/bin/sh` (dash) has no job control without a terminal, so `set -m` gave the
job no group and the stop script's group kill reached nothing: the stop was tested nowhere. 54's lesson (a
passing test proved nothing about the feature it exercised) was recorded, but nothing makes a stand-in prove it
ran, so the class repeated in new code.

**Fix.** The stand-in comes first on the PATH and records its process ID; the test waits until it runs, stops the
run (once with the stop script, once with a SIGTERM to the script, as a task timeout delivers it), and requires
every recorded process gone, the pid file removed and a non-zero status. The scripts stop a run's group and then
its process tree (`pgrep -P`), which works without job control, and the test script traps SIGTERM to do the same.
Both tests failed against the old scripts and pass against the new.

**Lesson.** A test that uses a stand-in asserts the stand-in ran; a test of a stop asserts something was running
before it and nothing after.

## 505. Three tests assumed a Linux or macOS host: printf's leftover arguments, /bin/bash, and FreeBSD as the "other" system (a repeat of 420) (fixed)

**Symptom.** 2026-10-18, the first `make bsd-check` over this branch's packages on the lab's FreeBSD 15.1 box:
`TestCommand_RunsAndReportsWhatHappened` failed with "printf: missing format character",
`TestShell_HonorsTheExecutableParameter` and `TestShell_UsesTheDevicesShell` with "/bin/bash: not found", and
`TestRun_ReportsEachOutcomeTruthfully` found its "other-os" check passing instead of skipping.

**Root cause.** The command test ran `printf` with no shell, so its format had no conversion and three arguments
were left over, which GNU's printf ignores and FreeBSD's refuses. The shell tests used `/bin/bash` as the
non-default shell, which FreeBSD's base system lacks. The gate test used `freebsd` as an operating system the
device does not run. 420's fix made the fast tier pass on macOS; no machine ran FreeBSD until bsdcheck.

**Fix.** `echo` in place of `printf`; `/bin/sh` under another name, through a link in a temporary directory, as
the non-default shell; and the "other" system chosen from the running one.

**Lesson.** A test's host is one of several systems; tools, paths and "some other OS" are chosen from what every
supported system has, or from the running one, and each system's leg is what proves it.

## 506. The Runner's per-task child died on the SIGTERM meant to let it stop, so a timed-out command kept running on the device (fixed)

**Symptom.** 2026-10-18, the failure-pattern review: on the Walk tier a task timeout could not reach a command
running on a device, though `pkg/remoteexec` now sends a cancelled command SIGTERM.

**Root cause.** The Runner ends a task's child, when the task's context ends, with SIGTERM and a forced kill after
`childWaitDelay`, "giving the child a chance to exit cleanly". The child ran its method on `context.Background()`
and handled no signal, so SIGTERM killed it at once: the method's context never ended, no SSH signal was sent, and
the dropped connection left a quiet command running.

**Fix.** Both child modes run their method in a context SIGTERM ends (`cmd/runner/child.go`), so the method is
cancelled and pkg/remoteexec signals the command before the child exits, within the grace.

**Lesson.** A graceful stop has two halves: the side that sends the signal and waits, and the side that handles
it. Proving the first says nothing about the second.

## 507. A failed node's undo was stripped in the collection bridge only, so a refused prediction reported one (fixed before push)

**Symptom.** 2026-10-18, the failure-pattern review: a real run refused because its method answered with a
prediction reached the executor's new failure branch with its success-shaped stats, `inverse` included, and printed
them under `--json` and `--verbose`, against the changelog's "its recorded undo is left out".

**Root cause.** The undo was left out in `collectionActionExecutor` only; the executor's own refusal
(`refusePrediction`) and any other action executor returning stats with an error went past it.

**Fix.** The executor strips the undo from every failed node's stats before register_mask and before keeping them.

**Lesson.** A rule about what an outcome may carry is enforced where every outcome passes, not where most of them
are made.
