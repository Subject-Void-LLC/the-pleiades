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

import json
import os
import signal
import subprocess
import sys
import threading
import time
import urllib.error
import urllib.request
from types import FrameType
from typing import Callable, Dict, Iterator, List, Optional, Sequence, Tuple

import actions
import binsize
import events
import githistory
import history
import locks
import memwatch
import paths
import prstate as ps
import redact
import roadmap
import runfile
import security
import server
import timeline
from jsonutil import JSONObject, JSONValue, as_object, get_float, get_int, get_list, get_objects, get_str, loads

# PROTOCOL rises whenever what the daemon serves or reads changes in a way an
# older daemon would get wrong. ensure replaces a daemon with a lower one.
PROTOCOL = 1
DEFAULT_PORT = 8765
# RECENT_SECONDS is how long after a run ended a /wait naming no run still
# answers from it, rather than waiting for the next run.
RECENT_SECONDS = 30.0
# KEEP_VIEWS bounds how many finished runs the daemon keeps followed.
KEEP_VIEWS = 8
MANIFEST_SECONDS = 0.5
MEMORY_TIMELINE_SECONDS = 60.0
ACTIONS_TIMELINE_SECONDS = 20.0
ROADMAP_SECONDS = 60.0
# GIT_SECONDS is how often the main line is looked at for new commits.
GIT_SECONDS = 300.0
# SECURITY_SECONDS bounds how stale /security.json may be.
SECURITY_SECONDS = 30.0
# BACKFILL_SECONDS is how often every branch's Actions runs are read again,
# beside the minute-by-minute poll of the one branch in use.
BACKFILL_SECONDS = 6 * 3600.0
MAX_APP_MEMORY = 8640
CAPPED = os.path.expanduser("~/.local/bin/capped")
# DAEMON_CAP is the memory the daemon and its roadmap runs may use together.
DAEMON_CAP = "400M"
# STOP_SECONDS is how long ensure waits for a replaced daemon to let go of its lock.
STOP_SECONDS = 10.0


class DaemonApp:
    """What the HTTP server reads, in the daemon.

    Attributes:
        state_dir: The shared state directory.
        root: The main clone.
        port: The port served.
        store: The run history.
        timeline: The JSONL history.
        panel: The roadmap panel.
        poller: The Actions poller, or None.
        cond: Guards views, current and memory, and wakes /wait callers when a run appears.
        views: Each followed run by id.
        current: The latest run's id, or None.
        memory: Recent MemAvailable samples.
        started: When the daemon started.
        alive: Whether a run's process (by pid) still runs; a stand-in in tests.
        security_cache: The last /security.json and when it was built.
        git_seen: The main line's commit when its history was last recorded.
        git_catalog: The module catalog's counts already read, by commit.
    """

    state_dir: str
    root: str
    port: int
    store: history.HistoryStore
    timeline: timeline.Timeline
    panel: roadmap.RoadmapPanel
    poller: Optional[actions.ActionsPoller]
    cond: threading.Condition
    views: Dict[str, runfile.RunView]
    current: Optional[str]
    memory: List[ps.MemorySample]
    started: float
    alive: Callable[[int], bool]
    security_cache: Optional[Tuple[float, JSONObject]]
    git_seen: str
    git_catalog: Dict[str, Optional[Tuple[int, int]]]

    def __init__(self, state_dir: str, root: str, port: int, redactor: redact.Redactor,
                 poller: Optional[actions.ActionsPoller] = None) -> None:
        """Builds the daemon's view.

        Args:
            state_dir: The shared state directory.
            root: The main clone.
            port: The port served.
            redactor: Masks what is written to the timeline.
            poller: The Actions poller, or None.
        """
        self.state_dir = state_dir
        self.root = root
        self.port = port
        self.store = history.HistoryStore(state_dir)
        self.timeline = timeline.Timeline(paths.timeline_dir(state_dir), redactor)
        self.panel = roadmap.RoadmapPanel(paths.panels(state_dir))
        self.poller = poller
        self.cond = threading.Condition()
        self.git_seen = ""
        self.git_catalog = {}
        self.views = {}
        self.current = None
        self.memory = []
        self.started = time.time()
        self.alive = runfile.process_alive
        self.security_cache = None

    # ---- following runs ----

    def follow_manifest(self) -> Optional[runfile.RunView]:
        """Reads the manifest and follows a run it names that is new.

        Returns:
            The newly followed run, or None.
        """
        doc = runfile.read_manifest(self.state_dir)
        if doc is None:
            return None
        run_id = get_str(doc, "run_id")
        with self.cond:
            if run_id in self.views:
                return None
            view = runfile.RunView(self.store, doc)
            self.views[run_id] = view
            self.current = run_id
            ended = [v for v in self.views.values() if v.ended is not None]
            for old in sorted(ended, key=lambda v: v.attached)[: max(0, len(self.views) - KEEP_VIEWS)]:
                del self.views[old.run_id]
            self.cond.notify_all()
        self.timeline.append("daemon", "machine", {"event": "attach", "run_id": run_id,
                                                   "root": doc.get("root"), "branch": doc.get("branch"),
                                                   "commit": doc.get("commit")})
        return view

    def pump(self) -> None:
        """Reads every open run's new events and writes them to the timeline."""
        with self.cond:
            open_views = [v for v in self.views.values() if not v.broker.is_closed()]
        for view in open_views:
            for e in view.pump(self.alive(view.pid())):
                self.timeline.record_gate_event(view.run_id, e)
            if view.broker.is_closed():
                with self.cond:
                    self.cond.notify_all()

    def current_view(self) -> Optional[runfile.RunView]:
        """Returns the latest run followed.

        Returns:
            It, or None before any.
        """
        with self.cond:
            return self.views.get(self.current) if self.current else None

    def current_root(self) -> str:
        """Names the checkout the latest gate ran in, for the Actions poller.

        Returns:
            Its root, or the main clone.
        """
        view = self.current_view()
        root = get_str(view.manifest, "root") if view is not None else ""
        return root if root and os.path.isdir(root) else self.root

    # ---- server.Backend ----

    def add_memory(self, t: float, mb: float) -> None:
        """Keeps a memory sample for the page.

        Args:
            t: When it was taken.
            mb: MemAvailable in MB.
        """
        with self.cond:
            self.memory.append((t, mb))
            del self.memory[: max(0, len(self.memory) - MAX_APP_MEMORY)]

    def state(self) -> object:
        """Builds the /state document.

        Returns:
            The latest run as it last wrote itself, Actions and memory.
        """
        view = self.current_view()
        with self.cond:
            memory = self.memory[-1500:]
        acts: JSONObject = self.poller.snapshot() if self.poller is not None else {
            "branch": None, "runs": [], "polled": None, "error": "Actions polling is off (--no-actions)"}
        run = view.snapshot() if view is not None else None
        gate: Optional[JSONObject] = None
        if view is not None:
            gate = {"run_id": view.run_id, "live": not view.broker.is_closed(), "root": view.manifest.get("root"),
                    "branch": view.manifest.get("branch"), "pid": view.pid()}
        return {"mode": "daemon", "now": time.time(), "run_id": view.run_id if view else None, "run": run,
                "gate": gate, "actions": acts, "memory": memory, "memory_floor_mb": memwatch.THRESHOLD_MB}

    def history(self) -> object:
        """Builds the /history document.

        Returns:
            {"runs": every saved run's summary}.
        """
        return {"runs": self.store.list_runs()}

    def history_run(self, run_id: str) -> Optional[object]:
        """Loads one saved run.

        Args:
            run_id: The run id.

        Returns:
            The run, or None.
        """
        return self.store.load_run(run_id)

    def actions_history(self) -> object:
        """Builds the /actions-history document.

        Returns:
            {"runs": every Actions run seen, newest first}.
        """
        runs = sorted(self.store.load_actions().values(), key=lambda r: get_str(r, "created"), reverse=True)
        return {"runs": runs}

    def binsize(self) -> object:
        """Builds the /binsize document from the main clone's ledger.

        Returns:
            The ledger's comparable groups, or why there are none.
        """
        ledger = os.path.join(self.root, ".IGNORE", "ci", "binsize.jsonl")
        doc = binsize.read_ledger(ledger)
        doc["path"] = os.path.relpath(ledger, self.root)
        return doc

    def broker(self) -> Optional[events.Broker]:
        """Returns the latest run's broker, for /events.

        Returns:
            It, or None before any run.
        """
        view = self.current_view()
        return view.broker if view is not None else None

    def select_broker(self, run_id: Optional[str], deadline: float) -> Tuple[Optional[events.Broker], Optional[str]]:
        """Chooses the run a /wait answers from, waiting for one when there is none yet.

        Naming a run waits for that run to be followed. Naming none takes the
        latest run while it is live or ended under RECENT_SECONDS ago, and
        otherwise waits for the next run to start: the gate an agent just
        launched may not have written its manifest when its /wait arrives.

        Args:
            run_id: The run named by ?run=, or None.
            deadline: time.monotonic() to give up at.

        Returns:
            (its broker, its id), or (None, None) when none came in time.
        """
        with self.cond:
            if run_id:
                while run_id not in self.views:
                    left = deadline - time.monotonic()
                    if left <= 0:
                        return None, None
                    self.cond.wait(min(left, 1.0))
                return self.views[run_id].broker, run_id
            seen = self.current
            while True:
                view = self.views.get(self.current) if self.current else None
                if view is not None:
                    if self.current != seen or not view.broker.is_closed():
                        return view.broker, view.run_id
                    if view.ended is not None and time.time() - view.ended < RECENT_SECONDS:
                        return view.broker, view.run_id
                left = deadline - time.monotonic()
                if left <= 0:
                    return None, None
                self.cond.wait(min(left, 1.0))

    def waits_for_runs(self) -> bool:
        """Reports that a /wait with no run waits for the next one.

        Returns:
            True.
        """
        return True

    def health(self) -> object:
        """Builds the /healthz document.

        Returns:
            Who this daemon is.
        """
        view = self.current_view()
        return {"ok": True, "protocol": PROTOCOL, "pid": os.getpid(), "port": self.port, "started": self.started,
                "source": paths.HERE, "state_dir": self.state_dir,
                "run_id": view.run_id if view is not None else None,
                "gate_live": view is not None and not view.broker.is_closed(),
                "roadmap": self.panel.configured()}

    def timeline_lines(self, since: float, kinds: Optional[List[str]], src: Optional[str]) -> Iterator[JSONObject]:
        """Reads the timeline.

        Args:
            since: Only lines after this epoch time.
            kinds: Only these kinds, or all.
            src: Only this src, or all.

        Returns:
            The lines, oldest first.
        """
        return timeline.read(self.timeline.directory, since, kinds, src)

    def roadmap(self, phases: Sequence[str], details: bool) -> object:
        """Returns the roadmap model.

        Args:
            phases: Only these phases, or all.
            details: Whether items keep their details.

        Returns:
            The model, or {"error": why}.
        """
        return self.panel.model(phases or None, details)

    def security(self) -> JSONObject:
        """Builds the /security.json document, at most every SECURITY_SECONDS.

        Returns:
            The attestation, scanner exceptions and everything pending.
        """
        with self.cond:
            cached = self.security_cache
        if cached is not None and time.time() - cached[0] < SECURITY_SECONDS:
            return cached[1]
        model = self.panel.model()
        runs = list(self.store.load_actions().values())
        if self.poller is not None:
            runs += get_objects(self.poller.snapshot(), "runs")
        doc = security.build(self.root, None if "error" in model else model, runs)
        with self.cond:
            self.security_cache = (time.time(), doc)
        return doc

    # ---- recording ----

    def record_roadmap(self) -> None:
        """Writes the roadmap's totals and each phase that changed."""
        model = self.panel.model()
        if "error" in model:
            return
        self.timeline.record_if_changed("roadmap.summary", "roadmap", roadmap.summary_value(model))
        for pid, value in roadmap.phase_values(model).items():
            self.timeline.record_if_changed("roadmap.phase", pid, value)
        doc = self.security()
        att = as_object(doc.get("attestation")) or {}
        self.timeline.record_if_changed("security.summary", "security", {
            "attestation": att.get("summary"), "pending": doc.get("pending_total"),
            "waivers": len(get_list(doc, "waivers")), "unreleased": len(get_list(doc, "unreleased"))})
        for c in get_objects(att, "controls"):
            self.timeline.record_if_changed("security.control", get_str(c, "id"), {
                k: c.get(k) for k in ("state", "statements_done", "statements_total", "review_due", "signed_on")})

    def record_git(self) -> None:
        """Writes the main line's history, a value a day, when it has moved.

        Every day is dated by its last commit, so the first call fills the
        timeline back to the first commit and a later one writes only the
        days that changed.
        """
        found = githistory.resolve(self.root)
        if found is None or found[1] == self.git_seen:
            return
        for day, when, value in githistory.days(self.root, found[0], self.git_catalog):
            self.timeline.record_if_changed("git.day", day, value, when)
        self.git_seen = found[1]

    def record_actions(self) -> None:
        """Writes each Actions run and job, of the latest poll, whose status or conclusion changed."""
        if self.poller is None:
            return
        self.record_action_runs(get_objects(self.poller.snapshot(), "runs"), historical=False)

    def record_action_runs(self, runs: List[JSONObject], historical: bool) -> None:
        """Writes each run and job whose status or conclusion changed.

        Args:
            runs: Runs with their jobs, as JSON.
            historical: Date each line by when the run or job ended (a
                backfill) rather than now (a poll).
        """
        for run in runs:
            rid = str(get_int(run, "id") or "")
            if not rid:
                continue
            when = (get_float(run, "end") or get_float(run, "start")) if historical else None
            self.timeline.record_if_changed("actions.run", rid, {
                k: run.get(k) for k in ("name", "title", "branch", "sha", "status", "conclusion", "event",
                                        "number", "attempt", "start", "end", "url")}, when)
            for job in get_objects(run, "jobs"):
                jwhen = (get_float(job, "end") or get_float(job, "start") or when) if historical else None
                self.timeline.record_if_changed("actions.job", "%s/%s" % (rid, get_str(job, "name")), {
                    k: job.get(k) for k in ("name", "status", "conclusion", "start", "end", "failed")}, jwhen)

    def backfill_actions(self) -> None:
        """Reads every workflow run on every branch into actions.json and the timeline."""
        if self.poller is None:
            return
        redactor = self.timeline.redactor

        def save(runs: List[actions.ActionsRun]) -> None:
            """Saves one batch as it lands.

            Args:
                runs: The batch.
            """
            docs = [as_object(redactor.mask_tree(actions.run_to_json(r))) or {} for r in runs]
            self.store.save_actions(docs)
            self.record_action_runs(docs, historical=True)

        runs, err = actions.backfill(self.store.load_actions(), self.poller.api, save=save)
        self.timeline.append("daemon", "machine", {"event": "actions-backfill", "runs": len(runs),
                                                   "error": redactor.mask(err) if err else None})


# ---- the lock ----

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


def take_lock(state_dir: str, port: int) -> Optional[int]:
    """Takes the daemon lock and writes who holds it.

    A probe (lock_held, in an ensure) holds the lock shared for an instant,
    so the lock is tried for locks.TAKE_SECONDS before it counts as another
    daemon's: a starting daemon must not lose to a probe and exit.

    Args:
        state_dir: The state directory.
        port: The port this daemon serves.

    Returns:
        The descriptor, held for the process's life, or None when another daemon holds it.
    """
    history.ensure_private_dir(state_dir)
    fd = os.open(paths.daemon_lock(state_dir), os.O_RDWR | os.O_CREAT, 0o600)
    if not locks.take(fd):
        os.close(fd)
        return None
    write_lock_info(fd, {"pid": os.getpid(), "port": port, "protocol": PROTOCOL, "started": time.time(),
                         "source": paths.HERE})
    return fd


# ---- the loops ----

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


def run_daemon(port: int = DEFAULT_PORT, no_actions: bool = False, state_dir: Optional[str] = None) -> int:
    """Runs the daemon until SIGTERM or Ctrl-C.

    Args:
        port: The port on 127.0.0.1.
        no_actions: Whether to skip polling GitHub Actions.
        state_dir: The state directory; the main clone's by default.

    Returns:
        0, also when another daemon already runs.
    """
    state = state_dir or paths.state_dir()
    fd = take_lock(state, port)
    if fd is None:
        info = read_lock_info(state) or {}
        print("statusd: already running (pid %s, port %s)" % (info.get("pid"), info.get("port")), flush=True)
        return 0
    redactor = redact.Redactor.from_environment()
    root = paths.main_root()
    app = DaemonApp(state, root, port, redactor)
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
    httpd = bind(app, port, stop)
    if httpd is not None:
        print("statusd: serving http://127.0.0.1:%d/ (pid %d, protocol %d, state %s)"
              % (port, os.getpid(), PROTOCOL, state), flush=True)
    while not stop.wait(1):
        pass
    if httpd is not None:
        httpd.shutdown()
    if app.poller is not None:
        app.poller.stop.set()
    app.timeline.append("daemon", "machine", {"event": "stop", "pid": os.getpid()})
    os.close(fd)
    return 0


# ---- ensure ----

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
    except (OSError, ValueError, urllib.error.URLError):
        return None


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
    log = os.open(paths.daemon_log(state), os.O_WRONLY | os.O_CREAT | os.O_APPEND, 0o600)
    try:
        return subprocess.Popen(cmd, cwd=paths.main_root(), stdin=subprocess.DEVNULL, stdout=log, stderr=log,
                                start_new_session=True, close_fds=True)
    finally:
        os.close(log)


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
        0 when a daemon runs (or was left alone on purpose), 1 when one could
        not be started or the lock's holder could not be identified.
    """
    state = state_dir or paths.state_dir()

    def say(text: str) -> None:
        """Prints unless quiet.

        Args:
            text: The line.
        """
        if not quiet:
            print("statusd: " + text, flush=True)

    history.ensure_private_dir(state)
    guard = os.open(paths.ensure_lock(state), os.O_RDWR | os.O_CREAT, 0o600)
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
        if runfile.read_manifest(state) is not None:
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

