# pyright: strict
"""How a gate run hands itself to the status daemon: through files only.

The run process (monitor.py run) parses pr-check's output exactly as it
always has, and publishes what it sees three ways, all under the shared
state directory (paths.py):

- gate.lock, held for the run's life, so a second gate on this machine is
  refused instead of fighting the first for memory;
- live.json, the manifest: which run, which worktree, which process;
- runs/<id>/events.jsonl, every broker event in order and then a closing
  line, and runs/<id>/live.json, the run's state rewritten every second.

The daemon (RunView) follows those files and republishes the events on a
broker of its own, so /events and /wait answer exactly as they did when the
run served them itself, and a daemon restarted mid-run catches up from the
start of the file. Nothing travels the other way: the run never waits on
the daemon, and a daemon that is down costs the run nothing.
"""

from __future__ import annotations

import json
import os
import subprocess
import threading
import time
from typing import Dict, List, Optional

import events
import live
import locks
from history import RUN_ID, HistoryStore, read_json, write_json
from jsonutil import JSONObject, as_object, get_float, get_int, get_str, loads
import paths

# MANIFEST_VERSION is the manifest's schema version.
MANIFEST_VERSION = 1
# SNAPSHOT_SECONDS is how often a run rewrites its live.json.
SNAPSHOT_SECONDS = 1.0
EVENTS_NAME = "events.jsonl"
SNAPSHOT_NAME = "live.json"


def read_manifest(state: str) -> Optional[JSONObject]:
    """Reads the running gate's manifest.

    Args:
        state: The state directory.

    Returns:
        The manifest, or None when there is none of a version this reads.
    """
    doc = as_object(read_json(paths.manifest(state)))
    if doc is None or doc.get("v") != MANIFEST_VERSION or not RUN_ID.match(get_str(doc, "run_id")):
        return None
    return doc


def process_alive(pid: int, marker: str = "monitor.py") -> bool:
    """Reports whether pid is still a monitor process (not a reused pid).

    Args:
        pid: The process.
        marker: Text its command line must hold.

    Returns:
        True while it runs.
    """
    if pid <= 0:
        return False
    try:
        os.kill(pid, 0)
    except ProcessLookupError:
        return False
    except PermissionError:
        return True
    try:
        with open("/proc/%d/cmdline" % pid, "rb") as f:
            return marker.encode() in f.read()
    except OSError:
        return True  # no /proc (macOS): trust kill(0)


# PROC is where a process's command line is read; a test points it elsewhere
# to take the path a machine without /proc takes.
PROC = "/proc"
# DAEMON_COMMANDS are the monitor.py subcommands that run the status daemon.
DAEMON_COMMANDS = ("daemon", "serve")


def process_cmdline(pid: int) -> Optional[List[str]]:
    """Reads a process's command line.

    Linux has it in /proc/<pid>/cmdline, one argument per NUL. A machine
    without /proc (macOS, the BSDs) is asked through ps, which prints the
    whole line as one string, here split on spaces.

    Args:
        pid: The process.

    Returns:
        Its arguments, or None when it does not exist or cannot be read.
    """
    if pid <= 0:
        return None
    try:
        with open(os.path.join(PROC, str(pid), "cmdline"), "rb") as f:
            raw = f.read()
        return [a.decode("utf-8", errors="replace") for a in raw.split(b"\0") if a]
    except FileNotFoundError:
        if os.path.isdir(os.path.join(PROC, "self")):
            return None  # /proc exists, so the process does not
    except OSError:
        return None
    try:
        out = subprocess.run(["ps", "-o", "command=", "-p", str(pid)], capture_output=True, text=True, timeout=5,
                             stdin=subprocess.DEVNULL)
    except (OSError, subprocess.TimeoutExpired):
        return None
    line = out.stdout.strip()
    return line.split() if out.returncode == 0 and line else None


def is_status_daemon(pid: int) -> bool:
    """Reports whether pid runs the status daemon: monitor.py daemon (or serve).

    A pid read from a file can be stale: after a reboot it may name any
    process at all. Only a process whose own command line runs the daemon
    passes, so nothing else is ever signalled as one.

    Args:
        pid: The process.

    Returns:
        True when an argument named monitor.py is followed by daemon or serve.
    """
    args = process_cmdline(pid)
    if not args:
        return False
    return any(os.path.basename(a) == "monitor.py" and args[i + 1] in DAEMON_COMMANDS
               for i, a in enumerate(args[:-1]))


def gate_running(state: str) -> bool:
    """Reports whether a gate runs on this machine: whether a process holds gate.lock.

    The manifest is no answer: a gate killed with SIGKILL never removes it,
    while the kernel drops a dead process's flock at once.

    Args:
        state: The state directory.

    Returns:
        True while a gate holds the lock.
    """
    return locks.held(paths.gate_lock(state))


class GateLock:
    """The one-gate-at-a-time lock, an flock held for the process's life.

    Attributes:
        path: The lock file.
        fd: Its descriptor while held, or None.
    """

    path: str
    fd: Optional[int]

    def __init__(self, path: str) -> None:
        """Prepares the lock.

        Args:
            path: The lock file.
        """
        self.path = path
        self.fd = None

    def acquire(self) -> bool:
        """Takes the lock, waiting only out a probe (gate_running) that holds it for an instant.

        Returns:
            True when this process now holds it.
        """
        os.makedirs(os.path.dirname(self.path), mode=0o700, exist_ok=True)
        fd = os.open(self.path, os.O_RDWR | os.O_CREAT, 0o600)
        if not locks.take(fd):
            os.close(fd)
            return False
        self.fd = fd
        return True

    def release(self) -> None:
        """Lets the lock go."""
        if self.fd is not None:
            os.close(self.fd)
            self.fd = None


class RunPublisher:
    """The run side: writes the manifest, the event file and the snapshots.

    Attributes:
        state: The state directory.
        run_dir: This run's history directory.
        live_run: The run being published.
        manifest: What live.json says.
        lock: Serializes appends to the event file.
        stop: Ends the snapshot thread.
        thread: The snapshot thread, once started.
    """

    state: str
    run_dir: str
    live_run: live.LiveRun
    manifest: JSONObject
    lock: threading.Lock
    stop: threading.Event
    thread: Optional[threading.Thread]

    def __init__(self, state: str, store: HistoryStore, run_id: str, live_run: live.LiveRun,
                 info: JSONObject) -> None:
        """Prepares to publish a run.

        Args:
            state: The state directory.
            store: The history the run is saved to.
            run_id: Its history id.
            live_run: The run.
            info: More manifest fields (root, branch, commit, cap, parallelism).
        """
        self.state = state
        self.run_dir = store.create_run(run_id)
        self.live_run = live_run
        self.manifest = {"v": MANIFEST_VERSION, "run_id": run_id, "pid": os.getpid(), "started": time.time()}
        self.manifest.update(info)
        self.lock = threading.Lock()
        self.stop = threading.Event()
        self.thread = None

    def start(self) -> None:
        """Starts publishing: listens to the broker, writes the manifest, and
        rewrites the snapshot every SNAPSHOT_SECONDS."""
        self.live_run.broker.listen(self.on_event)
        self.write_snapshot()
        write_json(paths.manifest(self.state), self.manifest)
        self.thread = threading.Thread(target=self.loop, name="publish", daemon=True)
        self.thread.start()

    def on_event(self, event: Optional[events.Event]) -> None:
        """Appends one event to the event file, or the closing line.

        Args:
            event: The event, or None when the broker closed.
        """
        line = event if event is not None else {"type": "closed", "at": time.time()}
        raw = (json.dumps(line, sort_keys=True) + "\n").encode("utf-8")
        with self.lock:
            fd = os.open(os.path.join(self.run_dir, EVENTS_NAME), os.O_WRONLY | os.O_CREAT | os.O_APPEND, 0o600)
            try:
                os.write(fd, raw)
            finally:
                os.close(fd)

    def write_snapshot(self) -> None:
        """Rewrites live.json from the run's state."""
        write_json(os.path.join(self.run_dir, SNAPSHOT_NAME), self.live_run.snapshot())

    def loop(self) -> None:
        """Rewrites the snapshot until stop is set; a failed write is skipped."""
        while not self.stop.wait(SNAPSHOT_SECONDS):
            try:
                self.write_snapshot()
            except OSError:
                pass

    def finish(self) -> None:
        """Writes the last snapshot and removes the manifest, when it is still this run's."""
        self.stop.set()
        if self.thread is not None:
            self.thread.join(timeout=5)
        try:
            self.write_snapshot()
        except OSError:
            pass
        current = read_manifest(self.state)
        if current is not None and get_str(current, "run_id") == get_str(self.manifest, "run_id"):
            try:
                os.remove(paths.manifest(self.state))
            except OSError:
                pass


class RunView:
    """The daemon side: one run, followed through its files.

    Attributes:
        run_id: The run.
        manifest: Its manifest as first read.
        run_dir: Its history directory.
        broker: The run's events, republished in order.
        tail: Follows the event file.
        attached: When the daemon began following it.
        ended: When its broker closed, or None.
        snap_mtime: The snapshot's mtime when last read.
        snap: The snapshot last read.
    """

    run_id: str
    manifest: JSONObject
    run_dir: str
    broker: events.Broker
    tail: live.FileTail
    attached: float
    ended: Optional[float]
    snap_mtime: float
    snap: Optional[JSONObject]

    def __init__(self, store: HistoryStore, manifest: JSONObject) -> None:
        """Follows the run a manifest names.

        Args:
            store: The history its files are in.
            manifest: Its manifest.
        """
        self.run_id = get_str(manifest, "run_id")
        self.manifest = manifest
        self.run_dir = store.run_dir(self.run_id)
        self.broker = events.Broker()
        self.tail = live.FileTail(os.path.join(self.run_dir, EVENTS_NAME))
        self.attached = time.time()
        self.ended = None
        self.snap_mtime = 0.0
        self.snap = None

    def pid(self) -> int:
        """Returns the run process's pid.

        Returns:
            It, or 0 when the manifest has none.
        """
        return get_int(self.manifest, "pid") or 0

    def drain(self) -> List[events.Event]:
        """Republishes every whole line written since the last read; a closing
        line closes the broker.

        Returns:
            The events newly published.
        """
        fresh: List[events.Event] = []
        for raw in self.tail.read_lines():
            try:
                obj = as_object(loads(raw))
            except ValueError:
                continue
            if obj is None:
                continue
            kind = get_str(obj, "type")
            if kind == "closed":
                self.close()
                break
            fields: Dict[str, object] = {k: v for k, v in obj.items() if k not in ("seq", "type", "at")}
            e = self.broker.publish(kind or "stage", fields, get_float(obj, "at"))
            if e is not None:
                fresh.append(e)
        return fresh

    def pump(self, alive: Optional[bool] = None) -> List[events.Event]:
        """Republishes the events written since the last call.

        A run whose process is gone without its closing line is closed here
        with a run event whose verdict is "lost", so nobody waits on it.

        Args:
            alive: Whether the run process lives; checked when None.

        Returns:
            The events newly published, the synthetic one included.
        """
        if self.broker.is_closed():
            return []
        fresh = self.drain()
        if self.broker.is_closed():
            return fresh
        if alive is None:
            alive = process_alive(self.pid())
        if alive:
            return fresh
        # One last read: the process may have written its end and exited between the two.
        fresh += self.drain()
        if not self.broker.is_closed():
            e = self.broker.publish("run", {"verdict": "lost", "exit_code": None,
                                            "reason": "the run process ended without closing its events"})
            if e is not None:
                fresh.append(e)
            self.close()
        return fresh

    def close(self) -> None:
        """Closes the broker and notes when."""
        self.broker.close()
        if self.ended is None:
            self.ended = time.time()

    def snapshot(self) -> Optional[JSONObject]:
        """Reads the run's state as it last wrote it, rereading only when it changed.

        Returns:
            The state, or None before the first snapshot.
        """
        path = os.path.join(self.run_dir, SNAPSHOT_NAME)
        try:
            mtime = os.stat(path).st_mtime
        except OSError:
            return self.snap
        if mtime != self.snap_mtime:
            doc = as_object(read_json(path))
            if doc is not None:
                self.snap, self.snap_mtime = doc, mtime
        return self.snap
