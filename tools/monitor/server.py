# pyright: strict
"""The dashboard's HTTP server, bound to 127.0.0.1 only.

Routes (GET only):

    /                 dashboard.html
    /app.js           dashboard.js
    /state            the live run, the Actions poll and memory
    /history          every saved run, newest first, with stage durations
    /history/<id>     one saved run
    /actions-history  every Actions run seen, with its jobs

A request whose Host header is not this machine's loopback is refused, so a
page on another site cannot read the dashboard through DNS rebinding.
"""

from __future__ import annotations

import json
import os
import re
import threading
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
from typing import Dict, Optional, Protocol, Tuple, Type

HERE = os.path.dirname(os.path.abspath(__file__))

# CSP is the page's Content-Security-Policy: scripts from this server and
# the one pinned ECharts file, no inline script, nothing sent elsewhere.
CSP = ("default-src 'none'; script-src 'self' https://cdn.jsdelivr.net; "
       "style-src 'self' 'unsafe-inline'; img-src 'self' data:; connect-src 'self'; "
       "base-uri 'none'; form-action 'none'; frame-ancestors 'none'")

# STATIC maps a path to the sibling file it serves and its type.
STATIC: Dict[str, Tuple[str, str]] = {
    "/": ("dashboard.html", "text/html; charset=utf-8"),
    "/app.js": ("dashboard.js", "text/javascript; charset=utf-8"),
}
HISTORY_PATH = re.compile(r"^/history/([A-Za-z0-9_-]+)$")


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
            if path == "/state":
                self.send_json(app.state())
            elif path == "/history":
                self.send_json(app.history())
            elif path == "/actions-history":
                self.send_json(app.actions_history())
            else:
                m = HISTORY_PATH.match(path)
                run = app.history_run(m.group(1)) if m else None
                if run is not None:
                    self.send_json(run)
                elif m:
                    self.send_json({"error": "no such run"}, 404)
                else:
                    self.send_body(404, b"not found\n", "text/plain")

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
