# pyright: strict
"""The MCP server's client of the status daemon: loopback HTTP to a daemon that already runs.

It tells a refused connection (no daemon) from a timeout (a busy one).
"""

from __future__ import annotations

import socket
import urllib.error
import urllib.request
from typing import List, Tuple

from jsonutil import JSONValue, loads


class NoDaemon(OSError):
    """Nothing listens on the daemon's port: no daemon runs."""


class DaemonSlow(OSError):
    """A daemon took the request and did not answer in time, twice: it runs, slowly."""


def cause(exc: BaseException) -> BaseException:
    """Unwraps the error urllib wraps a connection's failure in.

    Args:
        exc: What urlopen or a read raised.

    Returns:
        URLError's reason when it is an exception, else exc.
    """
    if isinstance(exc, urllib.error.URLError) and isinstance(exc.reason, BaseException):
        return exc.reason
    return exc


def timed_out(exc: BaseException) -> bool:
    """Reports whether a request failed by waiting too long.

    Args:
        exc: What it raised.

    Returns:
        True for a timeout (socket.timeout, its own class before Python 3.10).
    """
    return isinstance(cause(exc), (socket.timeout, TimeoutError))


class DaemonClient:
    """Reads a daemon that is already running, over loopback HTTP.

    It never starts one: a contributor whose session lists this server has
    not asked for a background process. A refused connection is NoDaemon; a
    timeout is retried once, and a second one is DaemonSlow, so a busy
    daemon is never reported as absent.

    Attributes:
        port: The daemon's port.
    """

    port: int

    def __init__(self, port: int) -> None:
        """Prepares a client.

        Args:
            port: The daemon's port.
        """
        self.port = port

    def get(self, path: str, timeout: float = 15.0) -> Tuple[int, bytes]:
        """GETs a path from the running daemon.

        Args:
            path: The path and query.
            timeout: Seconds to wait for the answer.

        Returns:
            (status, body).

        Raises:
            NoDaemon: The connection was refused.
            DaemonSlow: The daemon timed out twice.
            OSError: Anything else that stopped the request.
        """
        url = "http://127.0.0.1:%d%s" % (self.port, path)
        for attempt in range(2):
            try:
                req = urllib.request.Request(url, headers={"Host": "127.0.0.1:%d" % self.port})
                with urllib.request.urlopen(req, timeout=timeout) as resp:  # noqa: S310 (loopback only)
                    body: bytes = resp.read()
                    return int(resp.status), body
            except urllib.error.HTTPError as exc:
                body = exc.read()
                return int(exc.code), body
            except OSError as exc:
                if timed_out(exc):
                    if attempt == 0:
                        continue  # a busy daemon gets a second chance, as a missing one never does
                    raise DaemonSlow("no answer in %g seconds" % timeout) from exc
                if isinstance(cause(exc), ConnectionRefusedError):
                    raise NoDaemon(str(cause(exc))) from exc
                raise
        raise DaemonSlow("no answer in %g seconds" % timeout)

    def json(self, path: str, timeout: float = 15.0) -> JSONValue:
        """GETs a path and decodes its JSON.

        Args:
            path: The path and query.
            timeout: Seconds to wait.

        Returns:
            The decoded value (an error object stays an object).
        """
        _, body = self.get(path, timeout)
        return loads(body.decode("utf-8", errors="replace"))

    def jsonl(self, path: str) -> List[JSONValue]:
        """GETs a JSONL path.

        Args:
            path: The path and query.

        Returns:
            Each line decoded.
        """
        _, body = self.get(path)
        out: List[JSONValue] = []
        for line in body.decode("utf-8", errors="replace").splitlines():
            try:
                out.append(loads(line))
            except ValueError:
                continue
        return out
