# pyright: strict
"""The dashboard's HTTP server, bound to 127.0.0.1 only.

Routes (GET only):

    /                 dashboard.html
    /app.js           dashboard.js
    /roadmap.js       roadmap.js, the roadmap tab
    /security.js      security.js, the security tab
    /healthz          who is serving: protocol, pid, the latest run
    /security.json    the attestation, scanner exceptions, unreleased security
                      changes and everything pending (security.py)
    /state            the live run, the Actions poll and memory
    /history          every saved run, newest first, with stage durations
    /history/<id>     one saved run
    /actions-history  every Actions run seen, with its jobs
    /binsize          the binary size ledger, grouped for charting
    /events           the live run's changes as Server-Sent Events, from
                      ?since=<seq> (default 0), until the run ends
    /wait             one answer when the run gives one: ?for=fail,end
                      (fail: a stage failed or was stopped; end: the run
                      ended; stage: any stage change; any: every event),
                      ?since=<seq>, ?timeout=<seconds> (default 3600),
                      ?run=<run id> (default: the latest run while it is
                      live or just ended, else the next one to start)
    /timeline         the JSONL history, one object a line:
                      ?since=<epoch seconds>, ?kind=a,b (a kind ending
                      in "." is a family: gate.), ?src=<src>, ?limit=<n>
                      (the newest n; default and most 100000)
    /roadmap.json     the roadmap model: ?phase=<id> (repeatable),
                      ?details=1; {"error": ...} with 404 when none is
                      configured

/events and /wait are the observer side: a page or a script subscribes and
is told, instead of polling /state. Each event carries a seq, so a
subscriber that reconnects passes the last one it saw and misses nothing.

A request whose Host header is not this machine's loopback is refused, so a
page on another site cannot read the dashboard through DNS rebinding.
"""

from __future__ import annotations

import json
import os
import re
import threading
import time
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
from collections import deque
from typing import Deque, Dict, Iterable, List, Optional, Protocol, Sequence, Tuple, Type
from urllib.parse import parse_qs

import events
from jsonutil import JSONObject

HERE = os.path.dirname(os.path.abspath(__file__))

# CSP is the page's Content-Security-Policy: scripts from this server and
# the one pinned ECharts file, no inline script, the page's two typefaces
# from Google Fonts (system fonts stand in without them), nothing sent elsewhere.
CSP = ("default-src 'none'; script-src 'self' https://cdn.jsdelivr.net; "
       "style-src 'self' 'unsafe-inline' https://fonts.googleapis.com; font-src https://fonts.gstatic.com; "
       "img-src 'self' data:; connect-src 'self'; "
       "base-uri 'none'; form-action 'none'; frame-ancestors 'none'")

# STATIC maps a path to the sibling file it serves and its type.
STATIC: Dict[str, Tuple[str, str]] = {
    "/": ("dashboard.html", "text/html; charset=utf-8"),
    "/app.js": ("dashboard.js", "text/javascript; charset=utf-8"),
    "/roadmap.js": ("roadmap.js", "text/javascript; charset=utf-8"),
    "/security.js": ("security.js", "text/javascript; charset=utf-8"),
}
HISTORY_PATH = re.compile(r"^/history/([A-Za-z0-9_-]+)$")

# WAIT_MAX bounds how long one /wait or /events request is held open.
WAIT_MAX = 6 * 3600.0
# KEEPALIVE is how often /events sends a comment while nothing happens, so
# a proxy or a client's own timeout does not end a quiet stream.
KEEPALIVE = 15.0
# WAIT_KINDS are what /wait?for= may name.
WAIT_KINDS = ("fail", "end", "stage", "any")
# TIMELINE_LIMIT bounds the lines one /timeline answer holds.
TIMELINE_LIMIT = 100000


class Backend(Protocol):
    """What the server reads its answers from (monitor.App)."""

    def state(self) -> object:
        """Returns the /state document."""
        ...

    def history(self) -> object:
        """Returns the /history document."""
        ...

    def history_run(self, run_id: str) -> Optional[object]:
        """Returns one saved run, or None."""
        ...

    def actions_history(self) -> object:
        """Returns the /actions-history document."""
        ...

    def binsize(self) -> object:
        """Returns the /binsize document."""
        ...

    def broker(self) -> Optional[events.Broker]:
        """Returns the live run's event broker, or None with no live run."""
        ...

    def select_broker(self, run_id: Optional[str], deadline: float) -> Tuple[Optional[events.Broker], Optional[str]]:
        """Chooses the run a /wait answers from, waiting until deadline for one."""
        ...

    def health(self) -> object:
        """Returns the /healthz document."""
        ...

    def waits_for_runs(self) -> bool:
        """Reports whether a /wait with no run yet should wait for one (the
        daemon) rather than answer 404 (a watch server)."""
        ...

    def timeline_lines(self, since: float, kinds: Optional[List[str]], src: Optional[str]) -> Iterable[JSONObject]:
        """Returns the timeline's lines, oldest first."""
        ...

    def roadmap(self, phases: Sequence[str], details: bool) -> object:
        """Returns the roadmap model, or {"error": why}."""
        ...

    def security(self) -> object:
        """Returns the /security.json document."""
        ...


def query(path: str) -> Dict[str, List[str]]:
    """Parses a request path's query string.

    Args:
        path: The request path.

    Returns:
        Each parameter's values.
    """
    return parse_qs(path.split("?", 1)[1]) if "?" in path else {}


def int_param(q: Dict[str, List[str]], name: str, default: int, lo: int, hi: int) -> Optional[int]:
    """Reads a whole-number query parameter held to a range.

    Args:
        q: The parsed query.
        name: The parameter.
        default: Its value when absent.
        lo: The least it may be.
        hi: The most it may be.

    Returns:
        The value, or None when it is not a number in range.
    """
    values = q.get(name)
    if not values:
        return default
    try:
        n = int(values[0])
    except ValueError:
        return None
    return n if lo <= n <= hi else None


def allowed_host(host: str, port: int) -> bool:
    """Decides whether a Host header names this server on loopback.

    Args:
        host: The request's Host header.
        port: The port served.

    Returns:
        True for 127.0.0.1:<port> or localhost:<port>.
    """
    return host in ("127.0.0.1:%d" % port, "localhost:%d" % port)


def make_handler(app: Backend, port: int) -> Type[BaseHTTPRequestHandler]:
    """Builds the request handler class.

    Args:
        app: Where answers come from.
        port: The port served, which the Host header must name.

    Returns:
        A BaseHTTPRequestHandler subclass.
    """

    class Handler(BaseHTTPRequestHandler):
        """Answers the dashboard's GET requests."""

        server_version = "pr-check-monitor"

        def log_message(self, format: str, *args: object) -> None:
            """Keeps request lines out of the terminal pr-check prints to.

            Args:
                format: Unused.
                *args: Unused.
            """

        def send_body(self, code: int, body: bytes, ctype: str) -> None:
            """Sends a response with the page's security headers.

            Args:
                code: The HTTP status.
                body: The body.
                ctype: Its Content-Type.
            """
            self.send_response(code)
            self.send_header("Content-Type", ctype)
            self.send_header("Content-Length", str(len(body)))
            self.send_header("Cache-Control", "no-store")
            self.send_header("X-Content-Type-Options", "nosniff")
            self.send_header("Referrer-Policy", "no-referrer")
            self.send_header("X-Frame-Options", "DENY")
            self.send_header("Content-Security-Policy", CSP)
            self.end_headers()
            self.wfile.write(body)

        def send_json(self, value: object, code: int = 200) -> None:
            """Sends a value as JSON.

            Args:
                value: Anything json.dumps accepts.
                code: The HTTP status.
            """
            self.send_body(code, json.dumps(value).encode("utf-8"), "application/json")

        def do_GET(self) -> None:
            """Answers one GET."""
            if not allowed_host(self.headers.get("Host", ""), port):
                self.send_body(403, b"forbidden\n", "text/plain")
                return
            path = self.path.split("?", 1)[0]
            if path in STATIC:
                name, ctype = STATIC[path]
                with open(os.path.join(HERE, name), "rb") as f:
                    self.send_body(200, f.read(), ctype)
                return
            if path in ("/events", "/wait"):
                self.serve_events(path)
            elif path == "/state":
                self.send_json(app.state())
            elif path == "/history":
                self.send_json(app.history())
            elif path == "/actions-history":
                self.send_json(app.actions_history())
            elif path == "/binsize":
                self.send_json(app.binsize())
            elif path == "/security.json":
                self.send_json(app.security())
            elif path == "/healthz":
                self.send_json(app.health())
            elif path == "/timeline":
                self.serve_timeline()
            elif path == "/roadmap.json":
                q = query(self.path)
                phases = [p for v in q.get("phase", []) for p in v.split(",") if p][:20]
                doc = app.roadmap(phases, q.get("details") == ["1"])
                self.send_json(doc, 404 if isinstance(doc, dict) and "error" in doc else 200)
            else:
                m = HISTORY_PATH.match(path)
                run = app.history_run(m.group(1)) if m else None
                if run is not None:
                    self.send_json(run)
                elif m:
                    self.send_json({"error": "no such run"}, 404)
                else:
                    self.send_body(404, b"not found\n", "text/plain")

        def serve_events(self, path: str) -> None:
            """Answers /events (a stream) or /wait (one answer).

            Args:
                path: The request path with its query.
            """
            q = query(self.path)
            # A browser's EventSource reconnects naming the last event it saw.
            last = self.headers.get("Last-Event-ID", "")
            if last.isdigit() and "since" not in q:
                q["since"] = [last]
            since = int_param(q, "since", 0, 0, 1 << 62)
            timeout = int_param(q, "timeout", 3600, 1, int(WAIT_MAX))
            if since is None or timeout is None:
                self.send_json({"error": "since and timeout are whole numbers in range"}, 400)
                return
            run_id = (q.get("run") or [""])[0] or None
            if path == "/wait":
                wanted = [w for part in q.get("for", ["fail,end"]) for w in part.split(",") if w]
                if not wanted or any(w not in WAIT_KINDS for w in wanted):
                    self.send_json({"error": "for is a list of %s" % ", ".join(WAIT_KINDS)}, 400)
                    return
                if not app.waits_for_runs() and app.broker() is None:
                    self.send_json({"error": "no live run: start one with monitor.py run"}, 404)
                    return
                deadline = time.monotonic() + float(timeout)
                broker, chosen = app.select_broker(run_id, deadline)
                if broker is None:
                    self.send_json({"event": None, "seq": since, "timeout": True, "run_id": run_id})
                    return
                answer = self.wait_for(broker, since, max(0.0, deadline - time.monotonic()), wanted)
                answer["run_id"] = chosen
                self.send_json(answer)
                return
            broker = app.broker() if run_id is None else app.select_broker(run_id, time.monotonic())[0]
            if broker is None:
                self.send_json({"error": "no live run: start one with monitor.py run"}, 404)
                return
            self.stream(broker, since, float(timeout))

        def serve_timeline(self) -> None:
            """Answers /timeline with JSONL, the newest ?limit lines of what matches."""
            q = query(self.path)
            try:
                since = float((q.get("since") or ["0"])[0])
            except ValueError:
                self.send_json({"error": "since is epoch seconds"}, 400)
                return
            limit = int_param(q, "limit", TIMELINE_LIMIT, 1, TIMELINE_LIMIT)
            if limit is None:
                self.send_json({"error": "limit is a whole number from 1 to %d" % TIMELINE_LIMIT}, 400)
                return
            kinds = [k for v in q.get("kind", []) for k in v.split(",") if k] or None
            src = (q.get("src") or [""])[0] or None
            kept: Deque[JSONObject] = deque(maxlen=limit)
            for line in app.timeline_lines(since, kinds, src):
                kept.append(line)
            body = "".join(json.dumps(line, sort_keys=True, separators=(",", ":")) + "\n" for line in kept)
            self.send_body(200, body.encode("utf-8"), "application/x-ndjson")

        def wait_for(self, broker: events.Broker, since: int, timeout: float, wanted: List[str]) -> Dict[str, object]:
            """Holds the request until an event answers wanted.

            Args:
                broker: The run's broker.
                since: The last seq the caller saw.
                timeout: Seconds to wait at most.
                wanted: The kinds of event that answer.

            Returns:
                {"event": the first matching event, "seq": its seq}, or
                {"event": None, "seq": the last seq seen, "timeout": true},
                or "closed": true when the run ended with none.
            """
            deadline = time.monotonic() + timeout
            seq = since
            while True:
                left = deadline - time.monotonic()
                fresh = broker.wait(seq, max(0.0, left))
                for e in fresh:
                    seq = int(str(e["seq"]))
                    if events.matches(e, wanted):
                        return {"event": e, "seq": seq}
                if broker.is_closed() and not broker.since(seq):
                    return {"event": None, "seq": seq, "closed": True}
                if time.monotonic() >= deadline:
                    return {"event": None, "seq": seq, "timeout": True}

        def stream(self, broker: events.Broker, since: int, timeout: float) -> None:
            """Streams events as text/event-stream until the run ends.

            Args:
                broker: The run's broker.
                since: The last seq the caller saw.
                timeout: Seconds to hold the stream at most.
            """
            self.send_response(200)
            self.send_header("Content-Type", "text/event-stream")
            self.send_header("Cache-Control", "no-store")
            self.send_header("X-Content-Type-Options", "nosniff")
            self.end_headers()
            deadline = time.monotonic() + timeout
            seq = since
            try:
                while time.monotonic() < deadline:
                    fresh = broker.wait(seq, KEEPALIVE)
                    for e in fresh:
                        seq = int(str(e["seq"]))
                        self.wfile.write(("id: %d\nevent: %s\ndata: %s\n\n" % (seq, e["type"], json.dumps(e))).encode("utf-8"))
                    if not fresh:
                        self.wfile.write(b": keepalive\n\n")
                    self.wfile.flush()
                    if broker.is_closed() and not broker.since(seq):
                        self.wfile.write(b"event: closed\ndata: {}\n\n")
                        return
            except (BrokenPipeError, ConnectionResetError):
                return

    return Handler


def start_server(app: Backend, port: int) -> ThreadingHTTPServer:
    """Serves the dashboard on 127.0.0.1 in a daemon thread.

    Args:
        app: Where answers come from.
        port: The port.

    Returns:
        The running server; call shutdown() to stop it.

    Raises:
        OSError: The port cannot be bound.
    """
    httpd = ThreadingHTTPServer(("127.0.0.1", port), make_handler(app, port))
    httpd.daemon_threads = True
    threading.Thread(target=httpd.serve_forever, name="http", daemon=True).start()
    return httpd
