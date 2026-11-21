# pyright: strict
"""The status daemon: one always-on page per machine, and its single-instance rule.

    monitor.py daemon [--port 8765]     run it in the foreground
    monitor.py ensure [--port 8765]     start it when it is not running; safe to call any number of times

The daemon holds statusd.lock (an flock) for its whole life, so a second
one exits at once; the lock file also says its pid, port and PROTOCOL, which
is how ensure decides whether to leave a running daemon alone (the same or a
newer protocol) or replace it (an older one, or one whose source directory
was removed with its worktree; only while no gate runs). The file is only a
hint: ensure signals the pid it names only once that process answers
/healthz on the lock's port with the same pid and runs monitor.py daemon
(prove_daemon), and never a pid of 0 or 1.

It serves what monitor.py run used to serve itself, plus the roadmap and the
timeline:

- every gate run on this machine, followed through its files (runfile.py),
  so /state, /events and /wait answer as before, whichever worktree ran it;
- GitHub Actions for the branch the latest gate ran on;
- MemAvailable;
- the roadmap panel (roadmap.py), when panels.json configures one;
- the timeline (timeline.py): every value above, appended as it changes.

It never starts a gate and never signals one, so restarting it cannot touch
a run in progress.
"""

from __future__ import annotations

import faulthandler
import os
import platform
import signal
import sys
import threading
import time
from types import FrameType
from typing import Callable, Optional

import actions
import locks
import memwatch
import paths
import redact
import server
# The daemon's parts, by responsibility; each name a caller reads as
# daemon.<name> is imported here and listed in __all__.
from daemonapp import DaemonApp
from daemonconf import (ACTIONS_TIMELINE_SECONDS, BACKFILL_SECONDS, DEFAULT_PORT, GIT_SECONDS, LOG_SECONDS,
                        MANIFEST_SECONDS, MEMORY_TIMELINE_SECONDS, OPT_OUT, PROTOCOL, RECENT_SECONDS, ROADMAP_SECONDS,
                        switched_off)
from daemonensure import cap_log, ensure
from daemonlock import healthz, lock_held, lock_info, read_lock_info, take_lock, write_lock_info
from fingerprint import source_fingerprint
from owner import OWNER_SECONDS, owner_alive, start_marker, watched_owner

__all__ = ["DEFAULT_PORT", "DaemonApp", "OPT_OUT", "PROTOCOL", "RECENT_SECONDS", "bind", "cap_log", "ensure",
           "healthz", "lock_held", "loop", "read_lock_info", "run_daemon", "source_fingerprint", "start_loop",
           "switched_off", "take_lock"]


def loop(stop: threading.Event, every: float, step: Callable[[], None], name: str) -> None:
    """Runs step every `every` seconds until stop; a failing step is logged, never raised.

    Args:
        stop: Ends the loop.
        every: Seconds between steps.
        step: The work.
        name: For the log.
    """
    while not stop.is_set():
        try:
            step()
        except Exception as exc:  # one bad step must not end the daemon
            print("statusd: %s failed: %s: %s" % (name, type(exc).__name__, exc), file=sys.stderr, flush=True)
        stop.wait(every)


def start_loop(stop: threading.Event, every: float, step: Callable[[], None], name: str) -> None:
    """Starts loop in a daemon thread.

    Args:
        stop: Ends the loop.
        every: Seconds between steps.
        step: The work.
        name: The thread's name.
    """
    threading.Thread(target=loop, args=(stop, every, step, name), name=name, daemon=True).start()


def bind(app: DaemonApp, port: int, stop: threading.Event) -> Optional[server.ThreadingHTTPServer]:
    """Binds the port, retrying while something else holds it (an older monitor.py run).

    Args:
        app: The backend.
        port: The port.
        stop: Ends the retries.

    Returns:
        The running server, or None when stopped first.
    """
    told = False
    while not stop.is_set():
        try:
            return server.start_server(app, port)
        except OSError as exc:
            if not told:
                print("statusd: 127.0.0.1:%d is taken (%s); retrying every 5s" % (port, exc.strerror or exc),
                      file=sys.stderr, flush=True)
                told = True
        stop.wait(5)
    return None


def start_trace(started: float) -> Callable[[str], None]:
    """Makes the start-up trace: one log line a step, with the seconds since start.

    A daemon that takes its lock and never answers then shows in its log the
    last step it reached. Where the platform has SIGUSR1, the trace also
    arms faulthandler on it, so `kill -USR1 <pid>` writes every thread's
    stack to the log (the test helpers do, when a daemon does not answer).

    Args:
        started: When the daemon started.

    Returns:
        trace(step), which prints the line.
    """
    usr1 = getattr(signal, "SIGUSR1", None)
    if usr1 is not None:
        faulthandler.register(usr1, file=sys.stderr, all_threads=True)

    def trace(step: str) -> None:
        """Prints one start-up step.

        Args:
            step: What was reached.
        """
        print("statusd: start-up %.2fs: %s" % (time.time() - started, step), file=sys.stderr, flush=True)

    return trace


def run_daemon(port: int = DEFAULT_PORT, no_actions: bool = False, state_dir: Optional[str] = None) -> int:
    """Runs the daemon until SIGTERM or Ctrl-C.

    Args:
        port: The port on 127.0.0.1.
        no_actions: Whether to skip polling GitHub Actions.
        state_dir: The state directory; the main clone's by default.

    Returns:
        0, also when another daemon already runs.
    """
    if not locks.SUPPORTED:
        print("statusd: " + locks.UNSUPPORTED, flush=True)
        return 0
    state = state_dir or paths.state_dir()
    started = time.time()
    fd = take_lock(state, port, started)
    if fd is None:
        info = read_lock_info(state) or {}
        print("statusd: already running (pid %s, port %s)" % (info.get("pid"), info.get("port")), flush=True)
        return 0
    trace = start_trace(started)
    trace("lock taken (pid %d, Python %s, %s)" % (os.getpid(), platform.python_version(), platform.platform()))
    redactor = redact.Redactor.from_environment()
    root = paths.main_root()
    app = DaemonApp(state, root, port, redactor)
    trace("backend built (root %s)" % root)
    if not no_actions:
        app.poller = actions.ActionsPoller(root, app.store, redactor, root_fn=app.current_root)
        app.poller.start()
    seeded = app.timeline.seed()
    stop = threading.Event()

    def on_signal(signum: int, frame: Optional[FrameType]) -> None:
        """Stops the daemon.

        Args:
            signum: The signal.
            frame: Unused.
        """
        stop.set()

    signal.signal(signal.SIGTERM, on_signal)
    signal.signal(signal.SIGINT, on_signal)
    app.timeline.append("daemon", "machine", {"event": "start", "pid": os.getpid(), "port": port,
                                              "protocol": PROTOCOL, "source": paths.HERE, "lines_read": seeded})

    def follow() -> None:
        """Follows the manifest and pumps every open run."""
        app.follow_manifest()
        app.pump()

    last_mem = [0.0]

    def sample() -> None:
        """Samples memory for the page, and once a minute for the timeline."""
        now, mb = time.time(), memwatch.read_available_mb()
        if mb is None:
            return
        app.add_memory(now, mb)
        if now - last_mem[0] >= MEMORY_TIMELINE_SECONDS:
            last_mem[0] = now
            app.timeline.append("memory", "machine", {"available_mb": mb, "swap_used_mb": memwatch.read_swap_used_mb()},
                                now)

    start_loop(stop, MANIFEST_SECONDS, follow, "follow")
    start_loop(stop, memwatch.SAMPLE_SECONDS, sample, "memory")
    start_loop(stop, ACTIONS_TIMELINE_SECONDS, app.record_actions, "actions-timeline")
    start_loop(stop, ROADMAP_SECONDS, app.record_roadmap, "roadmap-timeline")
    start_loop(stop, GIT_SECONDS, app.record_git, "git-timeline")
    start_loop(stop, BACKFILL_SECONDS, app.backfill_actions, "actions-backfill")

    def trim_log() -> None:
        """Caps the log, when it is the one this process writes (spawn's, not a terminal)."""
        log = paths.daemon_log(state)
        try:
            mine, there = os.fstat(sys.stderr.fileno()), os.stat(log)
        except (OSError, ValueError):
            return
        if (mine.st_dev, mine.st_ino) == (there.st_dev, there.st_ino):
            cap_log(log)

    start_loop(stop, LOG_SECONDS, trim_log, "log")
    # OWNER never stops the repository's own daemon, only a test's.
    owner = watched_owner(state, paths.state_dir())
    if owner:
        owner_started = start_marker(owner)

        def watch_owner() -> None:
            """Stops the daemon once the process that owns it (a test suite) is gone."""
            if not owner_alive(owner, owner_started):
                print("statusd: owner pid %d is gone; stopping" % owner, flush=True)
                stop.set()

        start_loop(stop, OWNER_SECONDS, watch_owner, "owner")
    trace("binding 127.0.0.1:%d" % port)
    httpd = bind(app, port, stop)
    if httpd is not None:
        # Port 0 asks for any free port (tests): name the one bound, here and
        # in the lock, which is how ensure learns where to ask /healthz.
        app.port = httpd.server_port
        if app.port != port:
            write_lock_info(fd, lock_info(app.port, started))
        print("statusd: serving http://127.0.0.1:%d/ (pid %d, protocol %d, state %s)"
              % (app.port, os.getpid(), PROTOCOL, state), flush=True)
    while not stop.wait(1):
        pass
    if httpd is not None:
        httpd.shutdown()
    if app.poller is not None:
        app.poller.stop.set()
    app.timeline.append("daemon", "machine", {"event": "stop", "pid": os.getpid()})
    os.close(fd)
    return 0
