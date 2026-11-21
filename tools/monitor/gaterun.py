# pyright: strict
"""monitor.py run: make --trace pr-check under capped, watched, saved and handed to the daemon.

The gate runs in its own process group, which Stopper signals as a whole
(never by name): on Ctrl-C, and when the memory watchdog trips.
"""

from __future__ import annotations

import os
import re
import signal
import subprocess
import sys
import threading
import time
from types import FrameType
from typing import List, Mapping, Optional

import actions
import daemon
import history
import live
import memwatch
import paths
import redact
import runfile
from jsonutil import get_str
from monitorcli import Options
from monitorconf import CAPPED, HISTORY_DIR, PR_DIR, REPO_ROOT
from runloops import finish_run, git_head, memory_loop, pr_dir_loop, save_loop, start_thread
from watchapp import App


class Stopper:
    """Signals pr-check's whole process group, never by name.

    Attributes:
        proc: pr-check, a session (and process group) leader.
        run: The run, where the reason is noted.
        terminated_at: When SIGTERM was sent, or None.
        killed: Whether SIGKILL was sent.
    """

    proc: subprocess.Popen[bytes]
    run: live.LiveRun
    terminated_at: Optional[float]
    killed: bool

    def __init__(self, proc: subprocess.Popen[bytes], live_run: live.LiveRun) -> None:
        """Prepares to stop proc.

        Args:
            proc: pr-check, started with start_new_session.
            live_run: The run, where the reason is noted.
        """
        self.proc = proc
        self.run = live_run
        self.terminated_at = None
        self.killed = False

    def signal_group(self, sig: signal.Signals) -> None:
        """Sends a signal to the process group, ignoring a group already gone.

        Args:
            sig: The signal.
        """
        try:
            os.killpg(self.proc.pid, sig)
        except ProcessLookupError:
            pass

    def note(self, reason: str, text: str) -> None:
        """Records why the run is being stopped.

        Args:
            reason: ctrl-c or watchdog.
            text: A sentence for the page.
        """
        with self.run.lock:
            self.run.state.stopped_by = reason
            self.run.state.note(text)

    def term(self, reason: str, text: str) -> None:
        """Sends SIGTERM once.

        Args:
            reason: ctrl-c or watchdog.
            text: A sentence for the page and the terminal.
        """
        if self.terminated_at is not None or self.proc.poll() is not None:
            return
        self.terminated_at = time.time()
        self.note(reason, text)
        print("\nmonitor: %s; sending SIGTERM to pr-check's process group" % text, file=sys.stderr, flush=True)
        self.signal_group(signal.SIGTERM)

    def kill(self, reason: str, text: str) -> None:
        """Sends SIGKILL once.

        Args:
            reason: ctrl-c or watchdog.
            text: A sentence for the page and the terminal.
        """
        if self.killed or self.proc.poll() is not None:
            return
        self.killed = True
        self.note(reason, text)
        print("\nmonitor: %s; sending SIGKILL to pr-check's process group" % text, file=sys.stderr, flush=True)
        self.signal_group(signal.SIGKILL)


def check_run_options(opts: Options) -> Optional[str]:
    """Refuses run options that cannot work.

    Args:
        opts: The command line.

    Returns:
        A reason to refuse, or None.
    """
    if not re.match(r"^[1-9][0-9]*[MG]$", opts.cap):
        return "--cap is a size like 4500M or 4G, not %r" % opts.cap
    if opts.parallelism < 0:
        return "--parallelism is 0 or more"
    if not os.access(CAPPED, os.X_OK):
        return "%s is missing; pr-check is never run uncapped here" % CAPPED
    return None


def cmd_run(opts: Options) -> int:
    """Runs make --trace pr-check under the monitor.

    Args:
        opts: The command line.

    Returns:
        pr-check's exit status (128 plus the signal when one killed it), or
        2 when the options are refused.
    """
    refusal = check_run_options(opts)
    if refusal:
        print("monitor: " + refusal, file=sys.stderr)
        return 2
    gate = runfile.GateLock(paths.gate_lock(HISTORY_DIR))
    if not gate.acquire():
        holder = runfile.read_manifest(HISTORY_DIR) or {}
        print("monitor: another gate is running on this machine (run %s in %s, pid %s); one at a time"
              % (get_str(holder, "run_id") or "?", get_str(holder, "root") or "?", holder.get("pid")), file=sys.stderr)
        return 2
    if daemon.ensure(opts.port, quiet=True) != 0:
        print("monitor: the status page is not running; the gate runs anyway and is saved to history",
              file=sys.stderr)
    redactor = redact.Redactor.from_environment()
    live_run = live.LiveRun(redactor, PR_DIR)
    commit = git_head(REPO_ROOT)
    live_run.state.commit = commit[:12]
    store = history.HistoryStore(HISTORY_DIR)
    run_id = store.unique_run_id(time.time(), commit)
    app = App("run", store, live_run, run_id)
    meta: Mapping[str, object] = {"mode": "run", "cap": opts.cap, "parallelism": opts.parallelism,
                                  "root": REPO_ROOT}
    publisher = runfile.RunPublisher(HISTORY_DIR, store, run_id, live_run, {
        "root": REPO_ROOT, "branch": actions.current_branch(REPO_ROOT) or "", "commit": commit,
        "cap": opts.cap, "parallelism": opts.parallelism})
    publisher.start()
    print("monitor: run %s; dashboard at http://127.0.0.1:%d/" % (run_id, opts.port), flush=True)
    print("monitor: wait with: curl -s 'http://127.0.0.1:%d/wait?for=fail,end&run=%s&timeout=21600'"
          % (opts.port, run_id), flush=True)

    log = store.open_log(run_id)
    swap_baseline = memwatch.read_swap_used_mb()
    cmd = ["env", "PR_CHECK_PARALLELISM=%d" % opts.parallelism, CAPPED, opts.cap, "make", "--trace", "pr-check"]
    print("monitor: running make --trace pr-check under capped %s (history %s)" % (opts.cap, run_id), flush=True)
    # start_new_session is setsid(): pr-check gets its own session and
    # process group, so Ctrl-C here does not reach it directly and a stop
    # signals exactly its group.
    proc = subprocess.Popen(cmd, cwd=REPO_ROOT, stdin=subprocess.DEVNULL, stdout=subprocess.PIPE,
                            stderr=subprocess.STDOUT, start_new_session=True)
    stopper = Stopper(proc, live_run)
    stdout = proc.stdout
    assert stdout is not None  # stdout=PIPE always gives one

    def read_output() -> None:
        """Tees pr-check's output to the terminal and the run log, masked."""
        for raw in iter(stdout.readline, b""):
            masked = live_run.feed_main(raw.decode("utf-8", errors="replace"))
            log.write(masked + "\n")
            sys.stdout.write(masked + "\n")
            sys.stdout.flush()

    swap_logged: List[int] = [0]

    def on_sample(mb: Optional[int], now: float) -> None:
        """Applies the watchdog to one memory sample.

        Args:
            mb: MemAvailable in MB, or None.
            now: When it was taken.
        """
        if proc.poll() is not None:
            return
        swap = memwatch.read_swap_used_mb()
        step = memwatch.swap_step(swap, swap_baseline)
        if step > swap_logged[0]:
            swap_logged[0] = step
            line = "monitor: swap in use %d MB, %d MB more than when the run began" % (swap or 0, (swap or 0) - (swap_baseline or 0))
            log.write(line + "\n")
            print(line, file=sys.stderr, flush=True)
        if mb is not None and memwatch.should_kill(stopper.terminated_at, now, mb):
            stopper.kill("watchdog", "MemAvailable still %d MB a minute after SIGTERM" % mb)
        elif memwatch.should_terminate(mb):
            stopper.term("watchdog", "the watchdog stopped the run: MemAvailable %d MB, under %d MB"
                         % (mb or 0, memwatch.THRESHOLD_MB))
        elif memwatch.swap_grown(swap, swap_baseline):
            stopper.term("watchdog", "the watchdog stopped the run: swap in use grew %d MB since it began, %d MB allowed"
                         % ((swap or 0) - (swap_baseline or 0), memwatch.SWAP_GROWTH_MB))

    interrupts: List[int] = [0]

    def on_interrupt(signum: int, frame: Optional[FrameType]) -> None:
        """Stops pr-check: SIGTERM first (it puts its VMs back), SIGKILL on a second.

        Args:
            signum: The signal.
            frame: Unused.
        """
        interrupts[0] += 1
        if interrupts[0] == 1:
            stopper.term("ctrl-c", "stopped by Ctrl-C")
        else:
            stopper.kill("ctrl-c", "stopped by a second Ctrl-C; a lab VM may be left running")

    signal.signal(signal.SIGINT, on_interrupt)
    signal.signal(signal.SIGTERM, on_interrupt)
    stop = threading.Event()
    reader = start_thread(read_output, "reader")
    start_thread(lambda: memory_loop(app, live_run, stop, on_sample), "memory")
    start_thread(lambda: pr_dir_loop(live_run, stop), "pr-dir")
    start_thread(lambda: save_loop(store, live_run, run_id, meta, stop), "save")

    while proc.poll() is None:
        time.sleep(0.5)
    reader.join(timeout=10)
    rc = proc.returncode
    if rc < 0:
        rc = 128 - rc  # killed by signal -rc, as a shell reports it
    finish_run(live_run, store, run_id, meta, rc, log)
    stop.set()
    publisher.finish()
    gate.release()
    print("monitor: pr-check ended, verdict %s, exit %d; saved as %s"
          % (live_run.snapshot()["verdict"], rc, run_id), flush=True)
    return rc
