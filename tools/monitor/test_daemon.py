# pyright: strict
"""Tests of the status daemon: the timeline, the run hand-off, the single
instance, run selection for /wait, and the roadmap panel.

    cd tools/monitor && ../../.venv/bin/python -B -m unittest -v test_daemon

Every test works in its own temporary state directory and port; none
touches the real one or port 8765.
"""

from __future__ import annotations

import sys

sys.dont_write_bytecode = True

import http.client  # noqa: E402
import json  # noqa: E402
import os  # noqa: E402
import subprocess  # noqa: E402
import tempfile  # noqa: E402
import threading  # noqa: E402
import time  # noqa: E402
import unittest  # noqa: E402
import unittest.mock  # noqa: E402
import warnings  # noqa: E402
from typing import Dict, List, Optional, Tuple  # noqa: E402

import daemon  # noqa: E402
import githistory  # noqa: E402
import history  # noqa: E402
import live  # noqa: E402
import paths  # noqa: E402
import redact  # noqa: E402
import roadmap  # noqa: E402
import runfile  # noqa: E402
import server  # noqa: E402
import timeline  # noqa: E402
from jsonutil import JSONObject, JSONValue, as_object, get_int, get_list, get_objects, get_str, loads  # noqa: E402

HERE = os.path.dirname(os.path.abspath(__file__))
# R1 and R2 are run ids in the form history.py gives them.
R1 = "20261111T120000Z-aaaaaaaaaaaa"
R2 = "20261111T130000Z-bbbbbbbbbbbb"


def serve(app: server.Backend) -> Tuple[server.ThreadingHTTPServer, int]:
    """Serves a backend on a port the kernel picks as it binds.

    A port found free, closed and then bound again can be taken in between
    (the kernel hands it to an outgoing connection), so no test picks one.

    Args:
        app: The backend.

    Returns:
        (the running server, its port).
    """
    httpd = server.start_server(app, 0)
    port = httpd.server_port
    if isinstance(app, daemon.DaemonApp):
        app.port = port
    return httpd, port


def lock_port(d: str) -> int:
    """Reads the port a test's daemon bound, from its lock file.

    Args:
        d: The state directory.

    Returns:
        The port, or 0 before the daemon has bound one.
    """
    return get_int(daemon.read_lock_info(d), "port") or 0


def get(port: int, path: str, timeout: float = 30.0) -> Tuple[int, str, bytes]:
    """GETs a path from the loopback server.

    Args:
        port: The port.
        path: The path and query.
        timeout: Seconds to wait.

    Returns:
        (status, content type, body).
    """
    conn = http.client.HTTPConnection("127.0.0.1", port, timeout=timeout)
    try:
        conn.request("GET", path)
        resp = conn.getresponse()
        return resp.status, resp.getheader("Content-Type") or "", resp.read()
    finally:
        conn.close()


def get_json(port: int, path: str, timeout: float = 30.0) -> JSONObject:
    """GETs a path and decodes its JSON object.

    Args:
        port: The port.
        path: The path and query.
        timeout: Seconds to wait.

    Returns:
        The object ({} when it is not one).
    """
    return as_object(loads(get(port, path, timeout)[2].decode("utf-8"))) or {}


class TimelineTest(unittest.TestCase):
    """The JSONL history."""

    def test_append_read_and_rotate(self) -> None:
        """Lines go to their month's file, are read back in order, filtered by
        kind, family, src and time, and a truncated last line is skipped."""
        with tempfile.TemporaryDirectory() as d:
            tl = timeline.Timeline(d, redact.Redactor([]))
            oct_t = time.mktime((2026, 10, 31, 12, 0, 0, 0, 0, -1))
            nov_t = time.mktime((2026, 11, 2, 12, 0, 0, 0, 0, -1))
            tl.append("memory", "machine", {"available_mb": 4000}, oct_t)
            tl.append("gate.stage", R1, {"seq": 1, "name": "fast", "status": "pass"}, nov_t)
            tl.append("gate.run", R1, {"seq": 2, "verdict": "pass"}, nov_t + 1)
            names = sorted(os.listdir(d))
            self.assertEqual(names, ["history-2026-10.jsonl", "history-2026-11.jsonl"])
            with open(os.path.join(d, names[1]), "a", encoding="utf-8") as f:
                f.write('{"v": 1, "t": 17944')  # a crash mid-line
            kinds = [get_str(x, "kind") for x in timeline.read(d)]
            self.assertEqual(kinds, ["memory", "gate.stage", "gate.run"])
            self.assertEqual([get_str(x, "kind") for x in timeline.read(d, kinds=["gate."])], ["gate.stage", "gate.run"])
            self.assertEqual(len(list(timeline.read(d, since=oct_t))), 2)
            self.assertEqual(len(list(timeline.read(d, src="machine"))), 1)
            mode = os.stat(os.path.join(d, names[0])).st_mode & 0o777
            self.assertEqual(mode, 0o600)

    def test_unchanged_values_are_written_once_across_restarts(self) -> None:
        """record_if_changed skips a repeat, and a new writer seeded from the
        files skips it too; gate events are deduplicated by seq."""
        with tempfile.TemporaryDirectory() as d:
            tl = timeline.Timeline(d, redact.Redactor([]))
            self.assertTrue(tl.record_if_changed("roadmap.phase", "41", {"done": 3}))
            self.assertFalse(tl.record_if_changed("roadmap.phase", "41", {"done": 3}))
            self.assertTrue(tl.record_gate_event(R1, {"seq": 1, "type": "stage", "at": 5.0}))
            self.assertFalse(tl.record_gate_event(R1, {"seq": 1, "type": "stage", "at": 5.0}))
            again = timeline.Timeline(d, redact.Redactor([]))
            self.assertEqual(again.seed(), 2)
            self.assertFalse(again.record_if_changed("roadmap.phase", "41", {"done": 3}))
            self.assertFalse(again.record_gate_event(R1, {"seq": 1, "type": "stage"}))
            self.assertTrue(again.record_if_changed("roadmap.phase", "41", {"done": 4}))
            self.assertTrue(again.record_gate_event(R1, {"seq": 2, "type": "run"}))
            self.assertEqual(len(list(timeline.read(d))), 4)

    def test_secrets_are_masked(self) -> None:
        """A secret-named environment value never reaches the file."""
        with tempfile.TemporaryDirectory() as d:
            tl = timeline.Timeline(d, redact.Redactor(["hunter2-secret-value"]))
            tl.append("gate.stage", R1, {"line": "password=hunter2-secret-value"})
            with open(timeline.files(d)[0], encoding="utf-8") as f:
                self.assertNotIn("hunter2-secret-value", f.read())


class HandOffTest(unittest.TestCase):
    """A run publishes through files; the daemon's view republishes the same events."""

    def test_view_replays_the_run_exactly(self) -> None:
        """Every event the run's broker publishes reaches the view with the
        same seq and fields, the closing line closes it, and a view attached
        late (a daemon restarted mid-run) sees the same sequence."""
        with tempfile.TemporaryDirectory() as d:
            store = history.HistoryStore(d)
            run = live.LiveRun(redact.Redactor([]), d)
            pub = runfile.RunPublisher(d, store, R1, run, {"root": d, "branch": "b"})
            pub.start()
            manifest = runfile.read_manifest(d)
            assert manifest is not None
            self.assertEqual((get_str(manifest, "run_id"), manifest.get("pid")), (R1, os.getpid()))
            view = runfile.RunView(store, manifest)
            run.broker.publish("stage", {"name": "fast", "status": "pass"})
            run.broker.publish("stage", {"name": "win", "status": "fail"})
            first = view.pump(alive=True)
            self.assertEqual([(e["seq"], e["name"]) for e in first], [(1, "fast"), (2, "win")])
            run.end(time.time(), 1)
            pub.finish()
            view.pump(alive=True)
            self.assertTrue(view.broker.is_closed())
            self.assertIsNone(runfile.read_manifest(d))
            late = runfile.RunView(store, manifest)
            late.pump(alive=False)
            self.assertEqual(late.broker.since(0), view.broker.since(0))
            self.assertEqual(late.broker.since(0)[-1]["type"], "run")
            snap = view.snapshot()
            self.assertIsNotNone(snap)

    def test_a_dead_run_is_closed_as_lost(self) -> None:
        """A run process gone without its closing line closes with verdict lost."""
        with tempfile.TemporaryDirectory() as d:
            store = history.HistoryStore(d)
            run = live.LiveRun(redact.Redactor([]), d)
            pub = runfile.RunPublisher(d, store, R2, run, {})
            pub.start()
            run.broker.publish("stage", {"name": "fast", "status": "running"})
            view = runfile.RunView(store, runfile.read_manifest(d) or {})
            fresh = view.pump(alive=False)
            self.assertEqual(fresh[-1]["verdict"], "lost")
            self.assertTrue(view.broker.is_closed())
            pub.stop.set()

    def test_one_gate_at_a_time(self) -> None:
        """A second holder of the gate lock is refused, in another process too."""
        with tempfile.TemporaryDirectory() as d:
            lock = runfile.GateLock(paths.gate_lock(d))
            self.assertTrue(lock.acquire())
            code = ("import sys; sys.path.insert(0, %r); import runfile, paths; "
                    "sys.exit(0 if runfile.GateLock(paths.gate_lock(%r)).acquire() else 3)") % (HERE, d)
            self.assertEqual(subprocess.run([sys.executable, "-B", "-c", code]).returncode, 3)
            lock.release()
            self.assertEqual(subprocess.run([sys.executable, "-B", "-c", code]).returncode, 0)


def make_app(d: str, port: int) -> daemon.DaemonApp:
    """Builds a daemon backend over a temporary state directory.

    Args:
        d: The state directory.
        port: Its port.

    Returns:
        The backend.
    """
    app = daemon.DaemonApp(d, d, port, redact.Redactor([]))
    app.alive = lambda pid: pid == os.getpid()  # the test process stands in for monitor.py run
    return app


class WaitSelectionTest(unittest.TestCase):
    """Which run a /wait answers from, over real HTTP."""

    def test_wait_before_the_run_starts_is_answered_by_it(self) -> None:
        """A /wait that arrives before the gate's manifest waits for that gate
        and answers with its first failure, naming the run."""
        with tempfile.TemporaryDirectory() as d:
            app = make_app(d, 0)
            httpd, port = serve(app)
            store = history.HistoryStore(d)
            run = live.LiveRun(redact.Redactor([]), d)
            results: List[JSONObject] = []
            waiter = threading.Thread(target=lambda: results.append(get_json(port, "/wait?for=fail,end&timeout=20")))
            try:
                waiter.start()
                time.sleep(0.3)
                pub = runfile.RunPublisher(d, store, R1, run, {})
                pub.start()
                app.follow_manifest()
                run.broker.publish("stage", {"name": "fast", "status": "pass"})
                run.broker.publish("stage", {"name": "bsd", "status": "fail"})
                app.pump()
                waiter.join(10)
                self.assertEqual(len(results), 1)
                ev = as_object(results[0].get("event")) or {}
                self.assertEqual((results[0].get("run_id"), ev.get("name")), (R1, "bsd"))
                # Named by id, and from the start, the same answer.
                again = get_json(port, "/wait?for=fail&run=" + R1 + "&timeout=2")
                self.assertEqual((as_object(again.get("event")) or {}).get("name"), "bsd")
                run.end(time.time(), 1)
                pub.finish()
                app.pump()
                end = get_json(port, "/wait?for=end&run=" + R1 + "&timeout=2")
                self.assertEqual((as_object(end.get("event")) or {}).get("verdict"), "fail")
                # Just ended: a /wait naming no run still answers from it.
                recent = get_json(port, "/wait?for=end&timeout=2")
                self.assertEqual(recent.get("run_id"), R1)
                # Long ended: it waits for the next run, and times out.
                view = app.current_view()
                assert view is not None
                view.ended = time.time() - daemon.RECENT_SECONDS - 1
                stale = get_json(port, "/wait?for=end&timeout=1")
                self.assertEqual((stale.get("timeout"), stale.get("event")), (True, None))
                self.assertEqual(get_json(port, "/wait?run=nope&timeout=1").get("timeout"), True)
            finally:
                httpd.shutdown()
                httpd.server_close()

    def test_timeline_and_health_routes(self) -> None:
        """/timeline serves JSONL filtered and limited; /healthz names the protocol."""
        with tempfile.TemporaryDirectory() as d:
            app = make_app(d, 0)
            for i in range(5):
                app.timeline.append("memory", "machine", {"available_mb": i})
            app.timeline.append("daemon", "machine", {"event": "start"})
            httpd, port = serve(app)
            try:
                code, ctype, body = get(port, "/timeline?kind=memory&limit=2")
                lines = [json.loads(x) for x in body.decode().splitlines()]
                self.assertEqual((code, ctype), (200, "application/x-ndjson"))
                self.assertEqual([x["data"]["available_mb"] for x in lines], [3, 4])
                self.assertEqual(get(port, "/timeline?since=abc")[0], 400)
                health = get_json(port, "/healthz")
                self.assertEqual((health.get("protocol"), health.get("roadmap")), (daemon.PROTOCOL, False))
                code, _, body = get(port, "/roadmap.json")
                self.assertEqual(code, 404)
                self.assertIn(b"panels.json", body)
            finally:
                httpd.shutdown()
                httpd.server_close()


class SingleInstanceTest(unittest.TestCase):
    """One daemon per state directory, started by ensure."""

    def test_ensure_starts_one_and_a_second_exits(self) -> None:
        """ensure starts a daemon that answers /healthz; a second ensure leaves
        it alone; a second daemon started by hand exits at once; SIGTERM
        stops it and the timeline records start and stop."""
        with tempfile.TemporaryDirectory() as d:
            monitor = os.path.join(HERE, "monitor.py")
            base = [sys.executable, "-B", monitor]
            try:
                # Port 0: the daemon binds any free port and names it in its lock.
                r = subprocess.run(base + ["ensure", "--port", "0", "--state-dir", d, "--quiet", "--no-actions"],
                                   timeout=60)
                self.assertEqual(r.returncode, 0, daemon_log(d))
                # ensure returns only once its daemon answers /healthz: no polling here.
                port = lock_port(d)
                health = daemon.healthz(port)
                self.assertIsNotNone(health, daemon_log(d))
                assert health is not None
                self.assertEqual(health.get("state_dir"), d)
                pid = health.get("pid")
                again = subprocess.run(base + ["ensure", "--port", "0", "--state-dir", d, "--no-actions"],
                                       capture_output=True, text=True, timeout=60)
                self.assertIn("running", again.stdout)
                second = subprocess.run(base + ["daemon", "--port", "0", "--state-dir", d, "--no-actions"],
                                        capture_output=True, text=True, timeout=30)
                self.assertIn("already running", second.stdout)
                self.assertEqual((daemon.healthz(port) or {}).get("pid"), pid)
            finally:
                info = daemon.read_lock_info(d) or {}
                pid_any = info.get("pid")
                if isinstance(pid_any, int):
                    os.kill(pid_any, 15)
                    deadline = time.monotonic() + 10
                    while daemon.lock_held(d) and time.monotonic() < deadline:
                        time.sleep(0.1)
            self.assertFalse(daemon.lock_held(d))
            events_seen = [(x.get("data") or {}) for x in timeline.read(paths.timeline_dir(d), kinds=["daemon"])]
            words = [get_str(as_object(e) or {}, "event") for e in events_seen]
            self.assertEqual(words[:1] + words[-1:], ["start", "stop"])


def runs_daemon(pid: int) -> bool:
    """Reports whether pid's own command line runs monitor.py daemon, read here
    rather than through the code under test, so cleanup never signals another process.

    Args:
        pid: The process.

    Returns:
        True for a status daemon.
    """
    try:
        with open("/proc/%d/cmdline" % pid, "rb") as f:
            args = [a.decode() for a in f.read().split(b"\0") if a]
    except OSError:
        return False
    return any(a.endswith("monitor.py") and args[i + 1] == "daemon" for i, a in enumerate(args[:-1]))


def stop_daemon(d: str) -> None:
    """Stops the daemon a test started in state directory d, and only it.

    Args:
        d: The state directory.
    """
    pid = (daemon.read_lock_info(d) or {}).get("pid")
    if isinstance(pid, int) and pid > 1 and runs_daemon(pid):
        os.kill(pid, 15)
        deadline = time.monotonic() + 10
        while daemon.lock_held(d) and time.monotonic() < deadline:
            time.sleep(0.1)


def daemon_log(d: str) -> str:
    """Reads the log of the daemons a test started, for a failure message.

    Args:
        d: The state directory.

    Returns:
        The log, or a note that there is none.
    """
    try:
        with open(paths.daemon_log(d), encoding="utf-8", errors="replace") as f:
            return f.read()
    except OSError:
        return "(no daemon log)"


def copy_monitor(dest: str, protocol: Optional[int] = None) -> str:
    """Copies the monitor's files, optionally as an older protocol, to start a daemon from them.

    Args:
        dest: The directory to create.
        protocol: The PROTOCOL the copy's daemon.py declares, or None to keep it.

    Returns:
        dest.
    """
    os.makedirs(dest)
    for name in os.listdir(HERE):
        if name.endswith((".py", ".html", ".js")) and not name.startswith("test_"):
            with open(os.path.join(HERE, name), "rb") as src, open(os.path.join(dest, name), "wb") as out:
                out.write(src.read())
    if protocol is not None:
        path = os.path.join(dest, "daemon.py")
        with open(path, encoding="utf-8") as f:
            text = f.read()
        old = "PROTOCOL = %d\n" % daemon.PROTOCOL
        assert old in text
        with open(path, "w", encoding="utf-8") as f:
            f.write(text.replace(old, "PROTOCOL = %d\n" % protocol, 1))
    return dest


def start_copy(src: str, d: str) -> Tuple["subprocess.Popen[bytes]", int]:
    """Starts a daemon from a copy of the monitor and waits until it answers.

    The daemon binds port 0 and names the port in its lock once bound, which
    is the readiness this waits on; a daemon that never gets there fails the
    test with its log.

    Args:
        src: The copy.
        d: The state directory.

    Returns:
        (the daemon's process, its port).
    """
    os.makedirs(d, exist_ok=True)
    with open(paths.daemon_log(d), "ab") as log:
        proc = subprocess.Popen([sys.executable, "-B", os.path.join(src, "monitor.py"), "daemon", "--port", "0",
                                 "--state-dir", d, "--no-actions"], stdout=log, stderr=log, stdin=subprocess.DEVNULL)
    deadline = time.monotonic() + 20
    while time.monotonic() < deadline and proc.poll() is None:
        port = lock_port(d)
        health = daemon.healthz(port, 0.5) if port else None
        if health is not None and health.get("pid") == proc.pid:
            return proc, port
        time.sleep(0.1)
    proc.kill()
    raise AssertionError("the copied daemon did not answer:\n" + daemon_log(d))


class Signals:
    """Stands in for os.kill: records every real signal and delivers only to allowed pids.

    Attributes:
        sent: Each (pid, signal) asked for, signal 0 left out.
        allow: The pids a signal is really delivered to.
        real: The real os.kill.
    """

    sent: List[Tuple[int, int]]
    allow: List[int]

    def __init__(self, allow: Optional[List[int]] = None) -> None:
        """Prepares the stand-in.

        Args:
            allow: The pids a signal is really delivered to.
        """
        self.sent = []
        self.allow = list(allow or [])
        self.real = os.kill

    def __call__(self, pid: int, sig: int) -> None:
        """Records a signal, and delivers it only when allowed (signal 0 always).

        Args:
            pid: The target.
            sig: The signal.
        """
        if sig == 0:
            self.real(pid, sig)
            return
        self.sent.append((pid, sig))
        if pid in self.allow:
            self.real(pid, sig)


class FakeHealthApp(daemon.DaemonApp):
    """A backend whose /healthz claims any pid, as an older daemon of this state directory.

    Attributes:
        fake_pid: The pid /healthz reports.
    """

    fake_pid: int = 0

    def health(self) -> object:
        """Claims to be an older daemon with fake_pid.

        Returns:
            The /healthz document.
        """
        return {"ok": True, "protocol": 0, "pid": self.fake_pid, "port": self.port, "state_dir": self.state_dir,
                "source": HERE}


class EnsureSafetyTest(unittest.TestCase):
    """ensure signals only a process proven to be the daemon, and starts exactly one."""

    def test_unprovable_lock_holders_are_never_signalled(self) -> None:
        """With the lock held and /healthz agreeing with the lock file, a pid of
        0, 1, an unrelated live process, a dead process, this test process, or
        an empty lock file each get no signal and start nothing."""
        import fcntl
        sleeper = subprocess.Popen(["sleep", "60"])
        dead = subprocess.Popen(["true"])
        dead.wait()
        try:
            for label, pid in (("pid 0", 0), ("pid 1", 1), ("unrelated", sleeper.pid), ("dead", dead.pid),
                               ("this process", os.getpid()), ("empty", None)):
                with self.subTest(label), tempfile.TemporaryDirectory() as d:
                    app = FakeHealthApp(d, d, 0, redact.Redactor([]))
                    app.fake_pid = pid or 0
                    httpd, port = serve(app)
                    fd = os.open(paths.daemon_lock(d), os.O_RDWR | os.O_CREAT, 0o600)
                    fcntl.flock(fd, fcntl.LOCK_EX | fcntl.LOCK_NB)
                    if pid is not None:
                        os.write(fd, json.dumps({"pid": pid, "port": port, "protocol": 0, "source": HERE}).encode())
                    signals = Signals()
                    try:
                        with unittest.mock.patch("os.kill", signals):
                            code = daemon.ensure(port, quiet=True, state_dir=d, wait=0.5)
                    finally:
                        os.close(fd)
                        httpd.shutdown()
                        httpd.server_close()
                    self.assertEqual(signals.sent, [])
                    self.assertEqual(code, 1)
                    self.assertFalse(os.path.exists(paths.daemon_log(d)), "ensure started a daemon")
        finally:
            sleeper.kill()
            sleeper.wait()

    def test_an_older_daemon_is_replaced_and_only_it_is_signalled(self) -> None:
        """A proven daemon of an older protocol gets one SIGTERM, and a daemon of
        this protocol, from these files, takes its place on the same port."""
        with tempfile.TemporaryDirectory() as root:
            d = os.path.join(root, "state")
            old, _ = start_copy(copy_monitor(os.path.join(root, "old"), protocol=0), d)
            signals = Signals(allow=[old.pid])
            try:
                # The daemon ensure starts outlives the call by design; its Popen is dropped running.
                with unittest.mock.patch("os.kill", signals), warnings.catch_warnings():
                    warnings.simplefilter("ignore", ResourceWarning)
                    code = daemon.ensure(0, quiet=True, state_dir=d, no_actions=True)
                self.assertEqual(code, 0, daemon_log(d))
                self.assertEqual(signals.sent, [(old.pid, 15)])
                self.assertEqual(old.wait(10), 0)
                health = daemon.healthz(lock_port(d)) or {}
                self.assertEqual((health.get("protocol"), health.get("source")), (daemon.PROTOCOL, HERE))
                self.assertNotEqual(health.get("pid"), old.pid)
            finally:
                if old.poll() is None:
                    old.kill()
                stop_daemon(d)

    def test_two_ensures_at_once_start_one_daemon(self) -> None:
        """Two ensure processes started together both succeed, and exactly one
        daemon ever runs: none starts only to find the lock taken."""
        with tempfile.TemporaryDirectory() as d:
            cmd = [sys.executable, "-B", os.path.join(HERE, "monitor.py"), "ensure", "--port", "0", "--state-dir", d,
                   "--no-actions", "--quiet"]
            try:
                procs = [subprocess.Popen(cmd) for _ in range(2)]
                codes = [p.wait(60) for p in procs]
                self.assertEqual(codes, [0, 0], daemon_log(d))
                log = daemon_log(d)
                self.assertEqual(log.count("statusd: serving"), 1, log)
                self.assertNotIn("already running", log)
            finally:
                stop_daemon(d)

    def test_a_daemon_whose_files_changed_is_replaced_from_them(self) -> None:
        """A daemon of this protocol is kept while its files are unchanged and
        replaced by an ensure from the same files once one changes; an ensure
        from another tree leaves it alone, so two trees never take turns."""
        with tempfile.TemporaryDirectory() as root:
            d = os.path.join(root, "state")
            copy = copy_monitor(os.path.join(root, "copy"))
            ensure = [sys.executable, "-B", os.path.join(copy, "monitor.py"), "ensure", "--port", "0", "--state-dir", d,
                      "--no-actions"]
            try:
                self.assertEqual(subprocess.run(ensure + ["--quiet"], timeout=60).returncode, 0, daemon_log(d))
                first = daemon.healthz(lock_port(d)) or {}
                self.assertEqual(first.get("source"), copy)
                self.assertEqual(first.get("fingerprint"),
                                 daemon.source_fingerprint(copy, [str(n) for n in get_list(first, "source_files")]))
                self.assertIn("daemon.py", get_list(first, "source_files"))
                kept = subprocess.run(ensure, capture_output=True, text=True, timeout=60)
                self.assertIn("running", kept.stdout)
                with open(os.path.join(copy, "timeline.py"), "a", encoding="utf-8") as f:
                    f.write("# changed after the daemon started\n")
                # An ensure from this tree, not the daemon's, judges only by protocol.
                with warnings.catch_warnings():
                    warnings.simplefilter("ignore", ResourceWarning)
                    self.assertEqual(daemon.ensure(0, quiet=True, state_dir=d, no_actions=True), 0)
                self.assertEqual((daemon.healthz(lock_port(d)) or {}).get("pid"), first.get("pid"))
                replaced = subprocess.run(ensure, capture_output=True, text=True, timeout=60)
                self.assertEqual(replaced.returncode, 0, replaced.stderr + daemon_log(d))
                self.assertIn("its files changed", replaced.stdout)
                second = daemon.healthz(lock_port(d)) or {}
                self.assertNotEqual(second.get("pid"), first.get("pid"))
                self.assertNotEqual(second.get("fingerprint"), first.get("fingerprint"))
            finally:
                stop_daemon(d)

    def test_a_probe_never_looks_like_a_holder(self) -> None:
        """A probe in progress (a shared lock) neither looks like a daemon to
        another probe nor makes a starting daemon give up; a real holder does."""
        import fcntl
        with tempfile.TemporaryDirectory() as d:
            history.ensure_private_dir(d)
            probe = os.open(paths.daemon_lock(d), os.O_RDWR | os.O_CREAT, 0o600)
            fcntl.flock(probe, fcntl.LOCK_SH | fcntl.LOCK_NB)
            self.assertFalse(daemon.lock_held(d))
            timer = threading.Timer(0.3, os.close, args=(probe,))
            timer.start()
            fd = daemon.take_lock(d, 1234)
            timer.join()
            self.assertIsNotNone(fd)
            assert fd is not None
            try:
                self.assertTrue(daemon.lock_held(d))
                self.assertEqual((daemon.read_lock_info(d) or {}).get("pid"), os.getpid())
            finally:
                os.close(fd)

    def test_the_lock_file_reads_whole_while_it_is_rewritten(self) -> None:
        """A reader that lands between the rewrite and the truncate still reads
        the new document, not an error, and the file is never empty."""
        with tempfile.TemporaryDirectory() as d:
            path = paths.daemon_lock(d)
            with open(path, "w", encoding="utf-8") as f:
                f.write(json.dumps({"pid": 99999, "port": 1, "source": "/a/much/longer/path/than/the/next/one"}) + "\n")
            fd = os.open(path, os.O_RDWR)
            try:
                os.pwrite(fd, (json.dumps({"pid": 4242, "port": 2}) + "\n").encode(), 0)
                self.assertEqual((daemon.read_lock_info(d) or {}).get("pid"), 4242)
            finally:
                os.close(fd)


class RoadmapPanelTest(unittest.TestCase):
    """The roadmap panel, against a stand-in script."""

    def write_script(self, d: str, body: str) -> str:
        """Writes a stand-in roadmap script and a roadmap beside it.

        Args:
            d: The directory.
            body: The script.

        Returns:
            The script's path.
        """
        script = os.path.join(d, "fake_roadmap.py")
        with open(script, "w", encoding="utf-8") as f:
            f.write(body)
        with open(os.path.join(d, "ROADMAP.md"), "w", encoding="utf-8") as f:
            f.write("# roadmap\n")
        return script

    def test_model_is_cached_until_a_file_changes(self) -> None:
        """The model drops details from the overview, keeps them on request,
        is reused while nothing changed, and is rebuilt when the roadmap is."""
        body = '''import json, sys, os
count = os.path.join(os.path.dirname(__file__), "count")
n = int(open(count).read()) + 1 if os.path.exists(count) else 1
open(count, "w").write(str(n))
print(json.dumps({"summary": {"work_items": 10, "work_items_done": n, "next_item": {"phase": "1", "title": "t"}},
                  "phases": [{"id": "1", "state": "in progress", "done": n, "total": 10, "version": "v1",
                              "items": [{"title": "a", "details": "long"}]}],
                  "argv": sys.argv[1:]}))
'''
        with tempfile.TemporaryDirectory() as d:
            script = self.write_script(d, body)
            panels = os.path.join(d, "panels.json")
            with open(panels, "w", encoding="utf-8") as f:
                json.dump({"roadmap": {"script": script}}, f)
            panel = roadmap.RoadmapPanel(panels)
            first = panel.model()
            phases = get_objects(first, "phases")
            self.assertEqual(phases[0].get("items"), [{"title": "a"}])
            self.assertIs(panel.model(), first)
            detailed = panel.model(["1"], True)
            self.assertIn("--phase", json.dumps(detailed.get("argv")))
            time.sleep(0.02)
            with open(os.path.join(d, "ROADMAP.md"), "a", encoding="utf-8") as f:
                f.write("more\n")
            os.utime(os.path.join(d, "ROADMAP.md"), (time.time() + 5, time.time() + 5))
            rebuilt = panel.model()
            self.assertIsNot(rebuilt, first)
            self.assertEqual(roadmap.summary_value(rebuilt).get("next_item"), {"phase": "1", "title": "t"})
            self.assertEqual(roadmap.phase_values(rebuilt)["1"]["state"], "in progress")

    def test_a_failing_script_is_reported(self) -> None:
        """A script that exits non-zero or prints junk becomes {"error": ...}."""
        with tempfile.TemporaryDirectory() as d:
            panels = os.path.join(d, "panels.json")
            for body, word in (("import sys; sys.exit('broken roadmap')", "exited"), ("print('nope')", "not JSON")):
                script = self.write_script(d, body)
                with open(panels, "w", encoding="utf-8") as f:
                    json.dump({"roadmap": {"script": script}}, f)
                self.assertIn(word, get_str(roadmap.RoadmapPanel(panels).model(), "error"))
            with open(panels, "w", encoding="utf-8") as f:
                json.dump({"roadmap": {"script": "relative.py"}}, f)
            self.assertFalse(roadmap.RoadmapPanel(panels).configured())


class ActionsBackfillTest(unittest.TestCase):
    """Reading every branch's runs, against canned API answers."""

    def test_pages_jobs_cache_and_timeline(self) -> None:
        """Reads pages until a short one, reads jobs only for runs not cached
        complete, saves in batches, and the daemon dates each timeline line
        by when the run ended."""
        import actions

        def run_doc(i: int, branch: str) -> JSONObject:
            """One run as the API lists it."""
            return {"id": i, "name": "ci", "head_branch": branch, "head_sha": "ab" * 20, "status": "completed",
                    "conclusion": "success", "created_at": "2026-10-0%dT10:00:00Z" % (1 + i % 9),
                    "run_started_at": "2026-10-0%dT10:00:00Z" % (1 + i % 9),
                    "updated_at": "2026-10-0%dT10:30:00Z" % (1 + i % 9), "html_url": "https://github.com/x/%d" % i}

        calls: List[str] = []
        page_size = actions.BACKFILL_PAGE

        def api(path: str) -> Tuple[JSONValue, Optional[str]]:
            """Answers like gh api."""
            calls.append(path)
            if "/jobs" in path:
                return {"jobs": [{"id": 1, "name": "fast", "status": "completed", "conclusion": "success",
                                  "started_at": "2026-10-01T10:01:00Z", "completed_at": "2026-10-01T10:20:00Z",
                                  "html_url": "https://github.com/j", "steps": []}]}, None
            page = int(path.rsplit("page=", 1)[1])
            n = page_size if page == 1 else 3
            return {"workflow_runs": [run_doc((page - 1) * page_size + k + 1, "b%d" % (k % 2)) for k in range(n)]}, None

        saved: List[int] = []
        cache: Dict[str, JSONObject] = {"2": {"id": 2, "status": "completed",
                                              "jobs": [{"id": 9, "name": "old", "status": "completed"}]}}
        runs, err = actions.backfill(cache, api, pause=lambda s: None, save=lambda rs: saved.append(len(rs)))
        self.assertIsNone(err)
        self.assertEqual(len(runs), page_size + 3)
        self.assertEqual(sum(1 for c in calls if "/jobs" in c), page_size + 3 - 1)
        self.assertEqual(sum(saved), page_size + 3)
        self.assertEqual([j["name"] for j in runs[1]["jobs"]], ["old"])

        with tempfile.TemporaryDirectory() as d:
            app = make_app(d, 0)
            docs = [actions.run_to_json(r) for r in runs[:2]]
            app.record_action_runs(docs, historical=True)
            app.record_action_runs(docs, historical=True)
            lines = list(timeline.read(paths.timeline_dir(d), kinds=["actions."]))
            self.assertEqual(len(lines), 4)
            self.assertLess(float(str(lines[0]["t"])), time.time() - 86400)


class GitHistoryTest(unittest.TestCase):
    """The main line's history, against a real repository made for the test."""

    def commit(self, repo: str, when: str, subject: str, catalog: Optional[JSONObject] = None) -> None:
        """Makes one commit dated when, changing the catalog when one is given.

        Args:
            repo: The repository.
            when: The commit's date, ISO 8601.
            subject: Its subject.
            catalog: The module catalog to write, or None for an empty commit.
        """
        if catalog is not None:
            path = os.path.join(repo, githistory.CATALOG)
            os.makedirs(os.path.dirname(path), exist_ok=True)
            with open(path, "w", encoding="utf-8") as f:
                json.dump(catalog, f)
            subprocess.run(["git", "add", "-A"], cwd=repo, check=True, capture_output=True)
        env = dict(os.environ, GIT_AUTHOR_DATE=when, GIT_COMMITTER_DATE=when, GIT_AUTHOR_NAME="t", GIT_AUTHOR_EMAIL="t@t",
                   GIT_COMMITTER_NAME="t", GIT_COMMITTER_EMAIL="t@t", GIT_CONFIG_GLOBAL=os.devnull,
                   GIT_CONFIG_SYSTEM=os.devnull)
        subprocess.run(["git", "commit", "-q", "--allow-empty", "--no-verify", "-m", subject], cwd=repo, env=env,
                       check=True, capture_output=True)

    def test_days_totals_catalog_and_one_write_a_value(self) -> None:
        """Counts commits and merged pull requests by UTC day, carries the
        catalog's counts forward from the day it changed, dates each line by
        the day's last commit, and writes a day again only when it changed."""
        with tempfile.TemporaryDirectory() as d:
            repo = os.path.join(d, "repo")
            os.makedirs(repo)
            subprocess.run(["git", "init", "-q", "-b", "main", repo], check=True, capture_output=True)
            self.commit(repo, "2026-08-01T10:00:00Z", "feat: the first")
            self.commit(repo, "2026-08-01T12:00:00Z", "Merge pull request #1 from x/y")
            self.commit(repo, "2026-08-03T09:00:00Z", "feat: a catalog",
                        {"a.b": {"status": "implemented"}, "a.c": {"status": "declared"}})
            self.commit(repo, "2026-08-03T11:00:00Z", "feat: implement a.c",
                        {"a.b": {"status": "implemented"}, "a.c": {"status": "implemented"}})
            self.commit(repo, "2026-08-05T09:00:00Z", "Merge pull request #2 from x/z")

            found = githistory.resolve(repo)
            assert found is not None
            self.assertEqual(found[0], "main")
            got = githistory.days(repo, found[0])
            self.assertEqual([g[0] for g in got], ["2026-08-01", "2026-08-03", "2026-08-05"])
            self.assertEqual(got[0][2], {"commits": 2, "commits_total": 2, "merged_prs_total": 1, "methods": None,
                                         "methods_implemented": None})
            self.assertEqual(got[1][2], {"commits": 2, "commits_total": 4, "merged_prs_total": 1, "methods": 2,
                                         "methods_implemented": 2})
            self.assertEqual(got[2][2]["merged_prs_total"], 2)
            self.assertEqual(got[2][2]["methods_implemented"], 2)
            self.assertEqual(time.strftime("%Y-%m-%dT%H", time.gmtime(got[1][1])), "2026-08-03T11")

            state = os.path.join(d, "state")
            app = make_app(state, 0)
            app.root = repo
            app.record_git()
            app.record_git()
            lines = list(timeline.read(paths.timeline_dir(state), kinds=["git.day"]))
            self.assertEqual([get_str(ln, "src") for ln in lines], ["2026-08-01", "2026-08-03", "2026-08-05"])
            self.assertTrue(os.path.exists(os.path.join(paths.timeline_dir(state), "history-2026-08.jsonl")))

            self.commit(repo, "2026-08-05T10:00:00Z", "fix: one more")
            app.record_git()
            lines = list(timeline.read(paths.timeline_dir(state), kinds=["git.day"], src="2026-08-05"))
            self.assertEqual([(as_object(ln.get("data")) or {}).get("commits") for ln in lines], [1, 2])
            self.assertEqual(len(list(timeline.read(paths.timeline_dir(state), kinds=["git.day"]))), 4)

    def test_outside_a_repository_nothing_is_written(self) -> None:
        """A root git does not know writes no line and raises nothing."""
        with tempfile.TemporaryDirectory() as d:
            self.assertIsNone(githistory.count_methods("not json"))
            env_dir = os.path.join(d, "plain")
            os.makedirs(env_dir)
            app = make_app(os.path.join(d, "state"), 0)
            app.root = env_dir
            if githistory.resolve(env_dir) is None:
                app.record_git()
                self.assertEqual(list(timeline.read(paths.timeline_dir(os.path.join(d, "state")), kinds=["git.day"])), [])


class SecurityTest(unittest.TestCase):
    """The Security tab's model, against a small repository."""

    def test_everything_pending_is_called_out(self) -> None:
        """Sign-off, open, overdue and due-soon reviews, an unproven phase, a
        stale waiver, an unreleased fragment and a failed scan are each
        pending, urgent groups ranked before the calm ones; in-progress
        controls are listed but not counted as pending."""
        import security
        now = time.mktime((2026, 11, 11, 12, 0, 0, 0, 0, -1))
        with tempfile.TemporaryDirectory() as root:
            os.makedirs(os.path.join(root, "changelog"))
            with open(os.path.join(root, "changelog", "pin-images.security.md"), "w", encoding="utf-8") as f:
                f.write("Pins every base image by digest. More text here.\n")
            with open(os.path.join(root, "kept.go"), "w", encoding="utf-8") as f:
                f.write("package x\n")
            with open(os.path.join(root, "gosec-waivers.json"), "w", encoding="utf-8") as f:
                json.dump({"waivers": [{"file": "kept.go", "line": "1", "rule_id": "G104", "reason": "Fine."},
                                       {"file": "gone.go", "line": "9", "rule_id": "G304", "reason": "Was fine."}],
                           "skips": []}, f)
            with open(os.path.join(root, "Makefile"), "w", encoding="utf-8") as f:
                f.write("GOSEC_VERSION       ?= v2.30.0\nGOVULNCHECK_VERSION ?= v1.6.0\n")
            controls: List[Dict[str, object]] = [
                {"id": "A", "title": "drafted", "state": "drafted, awaiting review", "statements_done": 1, "statements_total": 1,
                 "review_due": "2026-11-20", "implemented_by": [{"phase": "7", "proven": False, "status": "done"}], "problems": []},
                {"id": "B", "title": "open", "state": "open", "statements_done": 0, "statements_total": 2, "review_due": "2026-10-01",
                 "implemented_by": [], "problems": []},
                {"id": "C", "title": "busy", "state": "in progress", "statements_done": 1, "statements_total": 3, "review_due": None,
                 "implemented_by": [], "problems": []},
            ]
            model = as_object(loads(json.dumps({"security": {"summary": {"controls": 3}, "stages": [], "frameworks": [],
                                                             "problems": [], "controls": controls}})))
            runs = get_objects(as_object(loads(json.dumps({"runs": [{"url": "https://github.com/x/1", "branch": "main", "number": 1,
                "jobs": [{"name": "vulns", "status": "completed", "conclusion": "failure", "failed": True, "end": now - 3600}]}]}))), "runs")
            doc = security.build(root, model, runs, now)
            groups = [get_str(g, "group") for g in get_objects(doc, "pending")]
            self.assertEqual(groups, ["scanner", "overdue", "signoff", "open", "due_soon", "unproven", "waiver", "release", "in_progress"])
            self.assertEqual(doc["pending_total"], 8)
            self.assertEqual(sum(1 for w in get_objects(doc, "waivers") if get_str(w, "stale")), 1)
            self.assertEqual(doc["pins"], {"gosec": "v2.30.0", "govulncheck": "v1.6.0", "go": "", "toolchain": ""})
            self.assertEqual(get_str(get_objects(doc, "unreleased")[0], "summary"), "Pins every base image by digest.")


class McpTest(unittest.TestCase):
    """The MCP server, as a real child process talking to a real daemon backend."""

    def test_initialize_list_and_call(self) -> None:
        """Answers initialize with tools, lists every tool as read-only, and
        answers status and timeline from the daemon; a notification gets no reply."""
        with tempfile.TemporaryDirectory() as d:
            app = make_app(d, 0)
            app.timeline.append("memory", "machine", {"available_mb": 4321})
            httpd, port = serve(app)
            try:
                msgs: List[JSONObject] = [
                    {"jsonrpc": "2.0", "id": 1, "method": "initialize", "params": {"protocolVersion": "2025-06-18"}},
                    {"jsonrpc": "2.0", "method": "notifications/initialized"},
                    {"jsonrpc": "2.0", "id": 2, "method": "tools/list"},
                    {"jsonrpc": "2.0", "id": 3, "method": "tools/call", "params": {"name": "status", "arguments": {}}},
                    {"jsonrpc": "2.0", "id": 4, "method": "tools/call",
                     "params": {"name": "timeline", "arguments": {"kind": "memory"}}},
                    {"jsonrpc": "2.0", "id": 5, "method": "tools/call", "params": {"name": "nope"}},
                ]
                stdin = "".join(json.dumps(m) + "\n" for m in msgs)
                env = dict(os.environ, STATUS_PORT=str(port))
                out = subprocess.run([sys.executable, "-B", os.path.join(HERE, "mcp_status.py")], input=stdin,
                                     capture_output=True, text=True, timeout=60, env=env)
                replies = [json.loads(x) for x in out.stdout.splitlines()]
                self.assertEqual([r["id"] for r in replies], [1, 2, 3, 4, 5])
                self.assertEqual(replies[0]["result"]["protocolVersion"], "2025-06-18")
                names = {t["name"] for t in replies[1]["result"]["tools"]}
                self.assertEqual(names, {"status", "gate", "gate_wait", "actions", "roadmap", "timeline", "history", "security"})
                self.assertTrue(all(t["annotations"]["readOnlyHint"] for t in replies[1]["result"]["tools"]))
                status = json.loads(replies[2]["result"]["content"][0]["text"])
                self.assertEqual(status["daemon"]["protocol"], daemon.PROTOCOL)
                self.assertIn("panels.json", status["roadmap"]["error"])
                lines = json.loads(replies[3]["result"]["content"][0]["text"])
                self.assertEqual(lines[0]["data"]["available_mb"], 4321)
                self.assertEqual(replies[4]["error"]["code"], -32602)
            finally:
                httpd.shutdown()
                httpd.server_close()


if __name__ == "__main__":
    unittest.main()
