# pyright: strict
"""monitor.py watch: follow a log something else writes, or replay one, on a server of its own.
"""

from __future__ import annotations

import os
import signal
import sys
import threading
import time
from types import FrameType
from typing import Mapping, Optional

import actions
import history
import live
import redact
import server
from monitorcli import Options
from monitorconf import HISTORY_DIR, PR_DIR, REPO_ROOT, SAVE_SECONDS
from runloops import finish_run, git_head, memory_loop, pr_dir_loop, start_thread
from watchapp import App


def start_common(opts: Options, mode: str, store: history.HistoryStore, redactor: redact.Redactor,
                 live_run: Optional[live.LiveRun] = None,
                 run_id: Optional[str] = None) -> tuple[App, server.ThreadingHTTPServer]:
    """Starts Actions polling and the server.

    Args:
        opts: The command line.
        mode: run, watch or serve.
        store: The history.
        redactor: Masks Actions text.
        live_run: The run being watched, or None.
        run_id: Its history id.

    Returns:
        (the app, the running server). Exits with status 2 when the port is
        taken.
    """
    poller: Optional[actions.ActionsPoller] = None
    if not opts.no_actions:
        poller = actions.ActionsPoller(REPO_ROOT, store, redactor)
        poller.start()
    app = App(mode, store, live_run, run_id, poller)
    try:
        httpd = server.start_server(app, opts.port)
    except OSError as exc:
        print("monitor: cannot serve on 127.0.0.1:%d: %s" % (opts.port, exc.strerror or exc), file=sys.stderr)
        sys.exit(2)
    print("monitor: dashboard at http://127.0.0.1:%d/" % opts.port, flush=True)
    return app, httpd


def wait_for_ctrl_c(seconds: Optional[float] = None) -> None:
    """Blocks until Ctrl-C (or SIGTERM), or until seconds pass.

    Args:
        seconds: The longest wait, or None to wait for the signal alone.
    """
    done = threading.Event()

    def on_signal(signum: int, frame: Optional[FrameType]) -> None:
        """Ends the wait.

        Args:
            signum: The signal.
            frame: Unused.
        """
        done.set()

    signal.signal(signal.SIGINT, on_signal)
    signal.signal(signal.SIGTERM, on_signal)
    if seconds is None:
        while not done.wait(1):
            pass
    else:
        done.wait(seconds)


def follow_log(path: str, live_run: live.LiveRun, store: history.HistoryStore, run_id: str,
               pace: float, save: bool) -> None:
    """Feeds a log into the run, following it until its summary and a quiet spell.

    A log that already holds a finished run is a replay and is never saved.

    Args:
        path: The log.
        live_run: The run.
        store: The history.
        run_id: The history id to save under.
        pace: Seconds between lines already in the log (0 for none).
        save: Whether to save a run that was still going at attach.
    """
    tail = live.FileTail(path)
    first = tail.read_lines()
    for line in first:
        live_run.feed_main(line)
        if pace > 0:
            time.sleep(pace)
    with live_run.lock:
        state = live_run.state
        state.attached_late = bool(first)
        finished_at_attach = state.verdict not in (None, "running")
        if first:
            state.note("attached to a log already %d line(s) long: those lines show the time they were read" % len(first))
        if finished_at_attach:
            state.note("replay of a finished log: times are when each line was read, except bars marked 'record'")
    save = save and not finished_at_attach
    meta: Mapping[str, object] = {"mode": "watch"}
    log = store.open_log(run_id) if save else None
    quiet_since, last_save = time.time(), 0.0
    while True:
        lines = tail.read_lines()
        for line in lines:
            masked = live_run.feed_main(line)
            if log is not None:
                log.write(masked + "\n")
        now = time.time()
        if lines:
            quiet_since = now
        if save and now - last_save > SAVE_SECONDS:
            store.save_run(run_id, live_run.full_snapshot(), meta)
            last_save = now
        with live_run.lock:
            verdict, code = live_run.state.verdict, live_run.state.exit_code
        if verdict not in (None, "running") and now - quiet_since > 3:
            break
        time.sleep(0.5)
    rc = code if code is not None else (0 if verdict == "pass" else 1)
    if save:
        finish_run(live_run, store, run_id, meta, rc, log)
        print("monitor: the run ended; saved as %s" % run_id, flush=True)
    else:
        live_run.poll_pr_dir()
        live_run.end(time.time(), rc)


def cmd_watch(opts: Options) -> int:
    """Attaches to a run something else started, or replays a finished log.

    Args:
        opts: The command line.

    Returns:
        0 after Ctrl-C, or 2 when the log is missing.
    """
    if opts.log and not os.path.isfile(opts.log):
        print("monitor: no log at %s" % opts.log, file=sys.stderr)
        return 2
    redactor = redact.Redactor.from_environment()
    live_run = live.LiveRun(redactor, os.path.abspath(opts.pr_dir) if opts.pr_dir else PR_DIR)
    store = history.HistoryStore(HISTORY_DIR)
    run_id = store.unique_run_id(time.time(), git_head(REPO_ROOT))
    app, httpd = start_common(opts, "watch", store, redactor, live_run, run_id)
    stop = threading.Event()
    start_thread(lambda: memory_loop(app, live_run, stop), "memory")
    start_thread(lambda: pr_dir_loop(live_run, stop), "pr-dir")
    log = opts.log
    if log:
        start_thread(lambda: follow_log(log, live_run, store, run_id, opts.pace, not opts.no_save), "follow")
    else:
        with live_run.lock:
            live_run.state.records_valid = True
            live_run.state.note("no --log: only .coverage/pr is read, so the static checks and pass progress are not visible")
    wait_for_ctrl_c()
    stop.set()
    httpd.shutdown()
    return 0
