# pyright: strict
"""The MCP server's client of the status daemon: loopback HTTP to a daemon that already runs.

It tells a refused connection (no daemon) from a timeout (a busy one),
whatever shape the timeout takes: socket.timeout or TimeoutError raised by a
read, or a URLError whose reason is one, as urlopen raises for a connect
that times out (as a refused connect on Windows can, since Windows retries
a refused SYN for about two seconds before it reports the refusal).
"""

from __future__ import annotations

import http.client
import socket
import urllib.error
import urllib.request
from typing import List, Protocol, Tuple

from jsonutil import JSONValue, loads


class Opener(Protocol):
    """urlopen's shape, so a test can stand in for it."""

    def __call__(self, url: urllib.request.Request, *, timeout: float) -> http.client.HTTPResponse:
        """Opens url.

        Args:
            url: The request.
            timeout: Seconds to wait.

        Returns:
            The response.
        """
        ...


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
    opener: Opener

    def __init__(self, port: int, opener: Opener = urllib.request.urlopen) -> None:
        """Prepares a client.

        Args:
            port: The daemon's port.
            opener: urlopen, or a stand-in.
        """
        self.port = port
        self.opener = opener

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
            OSError: Anything else that stopped the request, a broken answer included.
        """
        url = "http://127.0.0.1:%d%s" % (self.port, path)
        for attempt in range(2):
            try:
                req = urllib.request.Request(url, headers={"Host": "127.0.0.1:%d" % self.port})
                with self.opener(req, timeout=timeout) as resp:  # noqa: S310 (loopback only)
                    body: bytes = resp.read()
                    return int(resp.status), body
            except urllib.error.HTTPError as exc:
                body = exc.read()
                return int(exc.code), body
            except http.client.HTTPException as exc:
                # A broken answer (IncompleteRead, BadStatusLine) is no OSError; say it as one.
                raise OSError("the daemon's answer broke off: %s" % exc) from exc
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
