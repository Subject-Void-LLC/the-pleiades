# pyright: strict
"""The process a test's daemon must not outlive: OWNER, and how its life is checked.

A test suite sets OWNER to its own pid. Every daemon it starts inherits the
variable and, unless it serves the repository's own state directory, stops
once that pid is gone, so a suite killed by a timeout leaves no daemon
behind. A session's own daemon is never started with it, and the real state
directory's daemon ignores it even if it were.

A pid can be reused. Where the platform says when a process started (Linux's
/proc, else ps -o lstart=), the daemon remembers its owner's start and treats
a different one as gone. ps's lstart has one-second resolution, and that
path has run on Linux only, not yet on macOS: there, a pid reused within the
same second, or a ps that prints no lstart, is not detected.
"""

from __future__ import annotations

import os
import subprocess
from typing import Optional

# OWNER names the environment variable holding the owner's pid.
OWNER = "PLEIADES_STATUS_DAEMON_OWNER"
# OWNER_SECONDS is how often a daemon with an owner checks that it still runs.
OWNER_SECONDS = 1.0
# POSIX says whether os.kill(pid, 0) only asks. On Windows it ends the process,
# so there no liveness is probed at all (and no daemon runs there).
POSIX = os.name == "posix"


def owner_pid() -> int:
    """Reads the pid of the process this daemon must not outlive, from OWNER.

    Returns:
        The pid, or 0 when OWNER is unset or names no process that could be one
        (not a number, or 0 or 1).
    """
    raw = os.environ.get(OWNER, "")
    pid = int(raw) if raw.isdigit() else 0
    return pid if pid > 1 else 0


def watched_owner(state: str, default_state: str) -> int:
    """Names the owner a daemon of this state directory watches.

    Args:
        state: The directory the daemon serves.
        default_state: The repository's own state directory (paths.state_dir()).

    Returns:
        owner_pid(), or 0 for the repository's own directory, whose daemon
        OWNER must never stop.
    """
    if os.path.realpath(state) == os.path.realpath(default_state):
        return 0
    return owner_pid()


def start_marker(pid: int) -> Optional[str]:
    """Reads when a process started, as text that changes when its pid is reused.

    Args:
        pid: The process.

    Returns:
        Linux's /proc start time in clock ticks, else ps's lstart, or None
        when neither answers.
    """
    try:
        with open("/proc/%d/stat" % pid, encoding="utf-8") as f:
            # The command name (field 2) may hold spaces; fields after its ")" are plain.
            return f.read().rsplit(")", 1)[1].split()[19]
    except (OSError, IndexError):
        pass
    if not POSIX:
        return None
    try:
        out = subprocess.run(["ps", "-o", "lstart=", "-p", str(pid)], capture_output=True, text=True, timeout=5)
    except (OSError, subprocess.TimeoutExpired):
        return None
    return out.stdout.strip() or None


def owner_alive(pid: int, started: Optional[str] = None) -> bool:
    """Reports whether the owner still exists.

    Args:
        pid: The owner's pid.
        started: Its start_marker when the daemon began, or None.

    Returns:
        False once no process has that pid, or the one that has it started at
        another time; True while it runs, also when this user may not signal
        it. Off POSIX always True, since os.kill(pid, 0) would end it there.
    """
    if not POSIX:
        return True
    try:
        os.kill(pid, 0)
    except ProcessLookupError:
        return False
    except PermissionError:
        return True
    if started is not None:
        now = start_marker(pid)
        if now is not None and now != started:
            return False
    return True
