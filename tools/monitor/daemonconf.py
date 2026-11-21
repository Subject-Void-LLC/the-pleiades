# pyright: strict
"""The status daemon's settings: its protocol, port, intervals and bounds, and its opt-out.

Every other daemon module reads these from here, so a copy of the monitor
that declares an older PROTOCOL (a test's) is that protocol throughout.
"""

from __future__ import annotations

import os


# PROTOCOL rises with every change to what the daemon serves or stores (a
# route, a field of an answer, a file it writes or how): ensure replaces a
# daemon with a lower one and leaves a higher one alone. A daemon of the same
# protocol is also replaced when it runs from the files ensure runs from and
# they changed since it loaded them (its fingerprint), which covers a change
# that forgot the bump, but only from that one tree.
PROTOCOL = 2
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
# LOG_MAX is how large statusd.log may grow before its tail is kept aside and
# it starts again; LOG_KEEP is how much of the tail is kept, in
# statusd.log.1. Together they hold the log under LOG_MAX + LOG_KEEP.
LOG_MAX = 5 * 1024 * 1024
LOG_KEEP = 1024 * 1024
# LOG_SECONDS is how often the daemon checks its own log's size.
LOG_SECONDS = 600.0
# OPT_OUT names the environment variable that switches the daemon off: set to
# anything but the empty string, ensure starts nothing and the MCP server says so.
OPT_OUT = "PLEIADES_NO_STATUS_DAEMON"


# OWNER names the environment variable a test suite sets to its own pid: a
# daemon started with it set (it passes to every child) stops itself once
# that process is gone, so a suite killed by a timeout leaves no daemon
# behind. A session's own daemon is never started with it.
OWNER = "PLEIADES_STATUS_DAEMON_OWNER"
# OWNER_SECONDS is how often a daemon with an owner checks that it still runs.
OWNER_SECONDS = 1.0


def switched_off() -> bool:
    """Reports whether this user switched the daemon off.

    Returns:
        True when OPT_OUT is set and not empty.
    """
    return bool(os.environ.get(OPT_OUT))


def owner_pid() -> int:
    """Reads the pid of the process this daemon must not outlive, from OWNER.

    Returns:
        The pid, or 0 when OWNER is unset or names no process that could be one
        (not a number, or 0 or 1).
    """
    raw = os.environ.get(OWNER, "")
    pid = int(raw) if raw.isdigit() else 0
    return pid if pid > 1 else 0


def owner_alive(pid: int) -> bool:
    """Reports whether the owner still exists.

    Args:
        pid: The owner's pid.

    Returns:
        False once no process has that pid; True while one does, also one
        this user may not signal.
    """
    try:
        os.kill(pid, 0)
    except ProcessLookupError:
        return False
    except PermissionError:
        return True
    return True
