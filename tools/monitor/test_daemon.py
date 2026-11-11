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
import socket  # noqa: E402
import subprocess  # noqa: E402
import tempfile  # noqa: E402
import threading  # noqa: E402
import time  # noqa: E402
import unittest  # noqa: E402
from typing import Dict, List, Optional, Tuple  # noqa: E402

import daemon  # noqa: E402
import history  # noqa: E402
import live  # noqa: E402
import paths  # noqa: E402
import redact  # noqa: E402
import roadmap  # noqa: E402
import runfile  # noqa: E402
import server  # noqa: E402
import timeline  # noqa: E402
from jsonutil import JSONObject, JSONValue, as_object, get_objects, get_str, loads  # noqa: E402

HERE = os.path.dirname(os.path.abspath(__file__))
# R1 and R2 are run ids in the form history.py gives them.
R1 = "20261111T120000Z-aaaaaaaaaaaa"
R2 = "20261111T130000Z-bbbbbbbbbbbb"


def free_port() -> int:
    """Finds a free loopback port.

    Returns:
        The port.
    """
    with socket.socket() as s:
        s.bind(("127.0.0.1", 0))
        port: int = s.getsockname()[1]
        return port


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
            port = free_port()
            app = make_app(d, port)
            httpd = server.start_server(app, port)
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
            port = free_port()
            app = make_app(d, port)
            for i in range(5):
                app.timeline.append("memory", "machine", {"available_mb": i})
            app.timeline.append("daemon", "machine", {"event": "start"})
            httpd = server.start_server(app, port)
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
            port = free_port()
            monitor = os.path.join(HERE, "monitor.py")
            base = [sys.executable, "-B", monitor]
            try:
                r = subprocess.run(base + ["ensure", "--port", str(port), "--state-dir", d, "--quiet"], timeout=30)
                self.assertEqual(r.returncode, 0)
                deadline = time.monotonic() + 10
                health = None
                while time.monotonic() < deadline and health is None:
                    health = daemon.healthz(port, 0.5)
                    time.sleep(0.2)
                assert health is not None
                self.assertEqual(health.get("state_dir"), d)
                pid = health.get("pid")
                again = subprocess.run(base + ["ensure", "--port", str(port), "--state-dir", d],
                                       capture_output=True, text=True, timeout=30)
                self.assertIn("running", again.stdout)
                second = subprocess.run(base + ["daemon", "--port", str(port), "--state-dir", d, "--no-actions"],
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
            app = make_app(d, free_port())
            docs = [actions.run_to_json(r) for r in runs[:2]]
            app.record_action_runs(docs, historical=True)
            app.record_action_runs(docs, historical=True)
            lines = list(timeline.read(paths.timeline_dir(d), kinds=["actions."]))
            self.assertEqual(len(lines), 4)
            self.assertLess(float(str(lines[0]["t"])), time.time() - 86400)


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
            port = free_port()
            app = make_app(d, port)
            app.timeline.append("memory", "machine", {"available_mb": 4321})
            httpd = server.start_server(app, port)
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
