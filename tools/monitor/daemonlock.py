# pyright: strict
"""The daemon's lock, and the proof that the process it names is the daemon.

The lock file says who holds statusd.lock (its pid, port and protocol), but
only as a hint: nothing is signalled until prove_daemon shows that the pid
answers /healthz on that port, for this state directory, and runs
monitor.py daemon.
"""

from __future__ import annotations

import http.client
import json
import os
import subprocess
import time
import urllib.request
from typing import Optional, Tuple

import history
import locks
import paths
import runfile
from daemonconf import PROTOCOL
from jsonutil import JSONObject, JSONValue, as_object, get_int, get_str, loads


def read_lock_info(state_dir: str) -> Optional[JSONObject]:
    """Reads what the daemon wrote into its lock file.

    The file is only a hint: a pid in it can be stale, so nothing acts on
    it without prove_daemon. The first JSON value is read, so a reader that
    lands between write_lock_info's write and its truncate still gets the
    new document whole.

    Args:
        state_dir: The state directory.

    Returns:
        Its pid, port and protocol, or None when it holds no JSON object.
    """
    try:
        with open(paths.daemon_lock(state_dir), encoding="utf-8") as f:
            text = f.read().strip()
    except OSError:
        return None
    if not text:
        return None
    try:
        value: JSONValue = json.JSONDecoder().raw_decode(text)[0]
    except ValueError:
        return None
    return as_object(value)


def write_lock_info(fd: int, info: JSONObject) -> None:
    """Writes who holds the lock into the lock file, which is never left empty.

    The lock is an flock on this very file, so the content cannot be renamed
    into place: a new file would carry no lock. It is written over the old
    content from the start and only then cut to its length, so a reader sees
    the old document, the new one, or the new one followed by the old one's
    tail (which read_lock_info ignores), never an empty file.

    Args:
        fd: The lock file, held.
        info: Its pid, port, protocol, start and source.
    """
    raw = (json.dumps(info, sort_keys=True) + "\n").encode("utf-8")
    os.pwrite(fd, raw, 0)
    os.ftruncate(fd, len(raw))


def lock_held(state_dir: str) -> bool:
    """Reports whether a daemon holds the lock, without looking like one to another probe.

    Args:
        state_dir: The state directory.

    Returns:
        True when some process holds it.
    """
    return locks.held(paths.daemon_lock(state_dir))


def lock_info(port: int, started: float) -> JSONObject:
    """Builds what the lock file says about this daemon.

    Args:
        port: The port it serves (0 until a port asked as 0 is bound).
        started: When it started.

    Returns:
        Its pid, port, protocol, start and source directory.
    """
    return {"pid": os.getpid(), "port": port, "protocol": PROTOCOL, "started": started, "source": paths.HERE}


def take_lock(state_dir: str, port: int, started: Optional[float] = None) -> Optional[int]:
    """Takes the daemon lock and writes who holds it.

    A probe (lock_held, in an ensure) holds the lock shared for an instant,
    so the lock is tried for locks.TAKE_SECONDS before it counts as another
    daemon's: a starting daemon must not lose to a probe and exit.

    Args:
        state_dir: The state directory.
        port: The port this daemon serves.
        started: When it started; now by default.

    Returns:
        The descriptor, held for the process's life, or None when another daemon holds it.
    """
    history.ensure_private_dir(state_dir)
    fd = os.open(paths.daemon_lock(state_dir), os.O_RDWR | os.O_CREAT | history.BINARY, 0o600)
    if not locks.take(fd):
        os.close(fd)
        return None
    write_lock_info(fd, lock_info(port, started if started is not None else time.time()))
    return fd


def healthz(port: int, timeout: float = 2.0) -> Optional[JSONObject]:
    """Asks a daemon who it is.

    Args:
        port: Its port.
        timeout: Seconds to wait.

    Returns:
        Its /healthz document, or None when nothing answers as a daemon.
    """
    try:
        req = urllib.request.Request("http://127.0.0.1:%d/healthz" % port, headers={"Host": "127.0.0.1:%d" % port})
        with urllib.request.urlopen(req, timeout=timeout) as resp:  # noqa: S310 (loopback only)
            raw: bytes = resp.read()
            return as_object(loads(raw.decode("utf-8")))
    except (OSError, ValueError, http.client.HTTPException):  # URLError is an OSError
        return None


def same_dir(a: str, b: str) -> bool:
    """Reports whether two paths name the same directory.

    Args:
        a: One path.
        b: The other.

    Returns:
        True when both are set and resolve to one place.
    """
    return bool(a) and bool(b) and os.path.realpath(a) == os.path.realpath(b)


def prove_daemon(state: str, info: Optional[JSONObject], health: Optional[JSONObject]) -> Optional[str]:
    """Decides whether the lock's pid is proven to be this state directory's daemon.

    Only a proven daemon is ever signalled. A pid of 0 or 1 is refused
    outright (kill(0) signals the caller's whole process group); the process
    must answer /healthz on the lock's port with that same pid and this state
    directory; and its own command line must run monitor.py daemon, so a
    pid reused after a reboot never passes.

    Args:
        state: The state directory.
        info: The lock file's content, or None.
        health: What /healthz answered on the lock's port, or None.

    Returns:
        None when proven, else why not.
    """
    pid = get_int(info, "pid") or 0
    if pid <= 1:
        return "the lock names no process that may be signalled (pid %d)" % pid
    if pid == os.getpid():
        return "the lock names this process"
    if health is None:
        return "nothing answers as a status daemon on port %s" % get_int(info, "port")
    if get_int(health, "pid") != pid:
        return "port %s is answered by pid %s, not the lock's pid %d" % (get_int(info, "port"), health.get("pid"), pid)
    if not same_dir(get_str(health, "state_dir"), state):
        return "pid %d serves another state directory" % pid
    if not runfile.is_status_daemon(pid):
        return "pid %d does not run monitor.py daemon" % pid
    return None


def await_daemon(state: str, port: int, wait: float,
                 proc: Optional["subprocess.Popen[bytes]"] = None) -> Tuple[Optional[Tuple[int, JSONObject]], str]:
    """Waits for the lock's holder to be proven the daemon (it may still be starting).

    Args:
        state: The state directory.
        port: The port to ask when the lock names none.
        wait: Seconds to keep trying.
        proc: A daemon just started, whose early exit ends the wait.

    Returns:
        ((its pid, its /healthz), "") once proven, else (None, why not).
    """
    deadline = time.monotonic() + wait
    while True:
        info = read_lock_info(state)
        dport = get_int(info, "port") or port
        health = healthz(dport, 1.0) if dport > 0 else None
        why = prove_daemon(state, info, health)
        if why is None and health is not None:
            return (get_int(info, "pid") or 0, health), ""
        if proc is not None and proc.poll() is not None and not lock_held(state):
            return None, "it exited with status %s" % proc.returncode
        if time.monotonic() >= deadline:
            return None, why or "it did not answer"
        time.sleep(0.1)
