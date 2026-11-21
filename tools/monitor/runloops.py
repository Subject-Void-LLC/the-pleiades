# pyright: strict
"""The threads run and watch share: memory samples, .coverage/pr, saving, and the end of a run.
"""

from __future__ import annotations

import subprocess
import sys
import threading
import time
from typing import Callable, Mapping, Optional, TextIO

import history
import live
import memwatch
from monitorconf import SAVE_SECONDS, SampleHook
from watchapp import App


def git_head(root: str) -> str:
    """Reads HEAD's commit.

    Args:
        root: The repository.

    Returns:
        The full commit, or an empty string.
    """
    try:
        out = subprocess.run(["git", "rev-parse", "HEAD"], cwd=root, capture_output=True, text=True, timeout=10)
    except (OSError, subprocess.TimeoutExpired):
        return ""
    return out.stdout.strip() if out.returncode == 0 else ""


def start_thread(target: Callable[[], None], name: str) -> threading.Thread:
    """Runs a function in a daemon thread.

    Args:
        target: The function.
        name: The thread's name.

    Returns:
        The started thread.
    """
    t = threading.Thread(target=target, name=name, daemon=True)
    t.start()
    return t


def memory_loop(app: App, live_run: Optional[live.LiveRun], stop: threading.Event,
                on_sample: Optional[SampleHook] = None) -> None:
    """Samples MemAvailable every SAMPLE_SECONDS until stop is set.

    Args:
        app: Keeps samples for the page.
        live_run: Keeps samples with the run, until it ends; or None.
        stop: Ends the loop.
        on_sample: Called with each sample (the watchdog), or None.
    """
    while not stop.is_set():
        now, mb = time.time(), memwatch.read_available_mb()
        if mb is not None:
            app.add_memory(now, mb)
            if live_run is not None and not live_run.finished.is_set():
                live_run.add_memory(now, mb)
        if on_sample is not None:
            on_sample(mb, now)
        stop.wait(memwatch.SAMPLE_SECONDS)


def pr_dir_loop(live_run: live.LiveRun, stop: threading.Event) -> None:
    """Reads .coverage/pr into the run every two seconds until stop is set.

    Args:
        live_run: The run.
        stop: Ends the loop.
    """
    while not stop.is_set():
        try:
            live_run.poll_pr_dir()
        except Exception as exc:  # a bad poll must not end the monitor
            print("monitor: reading .coverage/pr failed: %s" % type(exc).__name__, file=sys.stderr)
        stop.wait(2)


def save_loop(store: history.HistoryStore, live_run: live.LiveRun, run_id: str,
              meta: Mapping[str, object], stop: threading.Event) -> None:
    """Saves the run to history every SAVE_SECONDS until stop is set.

    Args:
        store: The history.
        live_run: The run.
        run_id: Its history id.
        meta: Fields saved with it.
        stop: Ends the loop.
    """
    while not stop.wait(SAVE_SECONDS):
        store.save_run(run_id, live_run.full_snapshot(), meta)


def finish_run(live_run: live.LiveRun, store: history.HistoryStore, run_id: str,
               meta: Mapping[str, object], rc: int, log: Optional[TextIO]) -> None:
    """Closes a run and saves it with its copied report and leg logs.

    Args:
        live_run: The run.
        store: The history.
        run_id: Its history id.
        meta: Fields saved with it.
        rc: Its exit status.
        log: Its open log, closed here; or None.
    """
    live_run.poll_pr_dir()
    live_run.copy_outputs(store, run_id)
    live_run.end(time.time(), rc)
    store.save_run(run_id, live_run.full_snapshot(), meta)
    if log is not None:
        log.close()
