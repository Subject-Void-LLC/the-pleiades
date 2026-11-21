# pyright: strict
"""ensure: start the daemon when none runs, and decide whether to replace one that does.

One ensure at a time holds statusd.ensure.lock. A running daemon is left
alone when it is of this protocol or newer and its files are unchanged, and
replaced (only while no gate runs) when it is older, its source is gone, or
it runs from these files and they changed since it started.
"""

from __future__ import annotations

import os
import signal
import subprocess
import sys
import time
from typing import Callable, Optional

import history
import locks
import paths
import runfile
from daemonconf import CAPPED, DAEMON_CAP, DEFAULT_PORT, LOG_KEEP, LOG_MAX, OPT_OUT, PROTOCOL, STOP_SECONDS, switched_off
from daemonlock import await_daemon, lock_held, same_dir
from fingerprint import source_fingerprint
from jsonutil import JSONObject, get_int, get_list, get_str


def cap_log(path: str, max_bytes: int = LOG_MAX, keep: int = LOG_KEEP) -> bool:
    """Keeps a log under max_bytes: past it, its last keep bytes go to <path>.1 and it starts empty.

    The daemon writes the log through a descriptor opened O_APPEND (spawn),
    so after the truncate its next write lands at the new end, not past a
    hole. The kept tail starts at a whole line.

    Args:
        path: The log.
        max_bytes: The size that sets it off.
        keep: How much of the tail to keep.

    Returns:
        Whether the log was cut.
    """
    try:
        size = os.stat(path).st_size
    except OSError:
        return False
    if size <= max_bytes:
        return False
    with open(path, "rb") as f:
        f.seek(max(0, size - keep))
        tail = f.read(keep)
    if size > keep:
        tail = tail[tail.find(b"\n") + 1:]  # drop the part line the cut landed in
    history.write_private(path + ".1", tail)
    os.truncate(path, 0)
    return True


def spawn(port: int, state: str, no_actions: bool = False) -> "subprocess.Popen[bytes]":
    """Starts a daemon detached from the caller: its own session, its own log.

    Args:
        port: Its port.
        state: The state directory.
        no_actions: Start it without GitHub Actions polling (tests).

    Returns:
        The started process (capped execs, so it is the daemon itself).
    """
    history.ensure_private_dir(state)
    cmd = [sys.executable, "-B", os.path.join(paths.HERE, "monitor.py"), "daemon", "--port", str(port),
           "--state-dir", state]
    if no_actions:
        cmd.append("--no-actions")
    if os.access(CAPPED, os.X_OK):
        cmd = [CAPPED, DAEMON_CAP] + cmd
    cap_log(paths.daemon_log(state))
    log = os.open(paths.daemon_log(state), os.O_WRONLY | os.O_CREAT | os.O_APPEND | history.BINARY, 0o600)
    try:
        return subprocess.Popen(cmd, cwd=paths.main_root(), stdin=subprocess.DEVNULL, stdout=log, stderr=log,
                                start_new_session=True, close_fds=True)
    finally:
        os.close(log)


def replace_reason(health: JSONObject) -> Optional[str]:
    """Decides whether a proven daemon should be replaced by this one.

    Args:
        health: Its /healthz.

    Returns:
        Why it should be, or None to leave it running.
    """
    source = get_str(health, "source")
    if source and not os.path.isfile(os.path.join(source, "dashboard.html")):
        return "its source %s is gone" % source
    proto = get_int(health, "protocol") or 0
    if proto < PROTOCOL:
        return "protocol %d is older than %d" % (proto, PROTOCOL)
    if proto > PROTOCOL or not same_dir(source, paths.HERE):
        # Newer, or another tree's: that tree's own ensure judges its files,
        # so two trees of one protocol never replace each other's daemon.
        return None
    files = [n for n in get_list(health, "source_files") if isinstance(n, str)]
    if files and get_str(health, "fingerprint") != source_fingerprint(paths.HERE, files):
        return "its files changed since it started"
    return None


def ensure(port: int = DEFAULT_PORT, quiet: bool = False, state_dir: Optional[str] = None,
           wait: float = 10.0, no_actions: bool = False) -> int:
    """Makes sure one daemon of this protocol or newer is running.

    One ensure at a time holds statusd.ensure.lock while it decides and
    starts, so two sessions starting together start one daemon. A daemon is
    replaced only once prove_daemon shows the lock's pid is that daemon.

    Args:
        port: The port it should serve.
        quiet: Print only problems.
        state_dir: The state directory; the main clone's by default.
        wait: Seconds to wait for a daemon to answer.
        no_actions: Start it without GitHub Actions polling (tests).

    Returns:
        0 when a daemon runs (or was left alone on purpose, or is switched
        off, or this platform cannot run one), 1 when one could not be
        started or the lock's holder could not be identified.
    """
    # Both lines print even when quiet: they say why nothing runs.
    if switched_off():
        print("statusd: switched off by %s; nothing was started" % OPT_OUT, flush=True)
        return 0
    if not locks.SUPPORTED:
        print("statusd: " + locks.UNSUPPORTED, flush=True)
        return 0
    state = state_dir or paths.state_dir()

    def say(text: str) -> None:
        """Prints unless quiet.

        Args:
            text: The line.
        """
        if not quiet:
            print("statusd: " + text, flush=True)

    history.ensure_private_dir(state)
    guard = os.open(paths.ensure_lock(state), os.O_RDWR | os.O_CREAT | history.BINARY, 0o600)
    try:
        # Long enough for another ensure to stop an old daemon and start a new one.
        if not locks.take(guard, STOP_SECONDS + 2 * wait):
            print("statusd: another ensure did not finish; nothing was started", file=sys.stderr)
            return 1
        return ensure_held(port, state, say, wait, no_actions)
    finally:
        os.close(guard)


def ensure_held(port: int, state: str, say: Callable[[str], None], wait: float, no_actions: bool) -> int:
    """ensure's work, once it holds statusd.ensure.lock.

    Args:
        port: The port a started daemon serves.
        state: The state directory.
        say: Prints a line unless quiet.
        wait: Seconds to wait for a daemon to answer.
        no_actions: Start it without GitHub Actions polling.

    Returns:
        ensure's status.
    """
    if lock_held(state):
        found, why = await_daemon(state, port, wait)
        if found is None:
            print("statusd: the daemon lock is held, but %s; nothing was signalled" % why, file=sys.stderr)
            return 1
        pid, health = found
        reason = replace_reason(health)
        if reason is None:
            say("running (pid %d, port %s, protocol %s)" % (pid, health.get("port"), health.get("protocol")))
            return 0
        if runfile.gate_running(state):
            say("the running daemon (pid %d) is kept while a gate runs, though %s" % (pid, reason))
            return 0
        say("replacing the running daemon (pid %d): %s" % (pid, reason))
        try:
            os.kill(pid, signal.SIGTERM)
        except OSError:
            pass
        deadline = time.monotonic() + STOP_SECONDS
        while lock_held(state) and time.monotonic() < deadline:
            time.sleep(0.1)
        if lock_held(state):
            print("statusd: the older daemon (pid %d) did not stop" % pid, file=sys.stderr)
            return 1
    proc = spawn(port, state, no_actions)
    found, why = await_daemon(state, port, wait, proc)
    if found is None:
        print("statusd: the daemon did not start (%s); see %s" % (why, paths.daemon_log(state)), file=sys.stderr)
        return 1
    say("started (pid %d, port %s, log %s)" % (found[0], found[1].get("port"), paths.daemon_log(state)))
    return 0
