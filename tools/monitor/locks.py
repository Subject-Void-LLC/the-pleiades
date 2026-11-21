# pyright: strict
"""The flock locks the status daemon and the gate share, and how they are probed.

A lock is held with LOCK_EX for a process's whole life: the daemon's
statusd.lock and a gate's gate.lock. Anything that only wants to know
whether a lock is held probes it with LOCK_SH | LOCK_NB. An exclusive holder
refuses a shared probe, and two shared probes never refuse each other, so
two probes running at once each see the truth.

A probe still holds its shared lock for an instant, and a process taking the
lock for real in that instant is refused. take() therefore retries for
TAKE_SECONDS before it decides the lock belongs to someone else: a probe
lets go within microseconds, and a real holder never does.

Windows has no fcntl. The import is guarded so every module still loads
there; SUPPORTED is False, the daemon and ensure say UNSUPPORTED and stop,
and a lock is then always taken and never held, which leaves a gate run
without its one-at-a-time rule (it needs capped, which Windows lacks too).
"""

from __future__ import annotations

import importlib
import os
import sys
import time
from typing import Optional, Protocol, cast

# TAKE_SECONDS is how long take() keeps trying before it gives up.
TAKE_SECONDS = 1.0
# TAKE_STEP is the pause between two tries.
TAKE_STEP = 0.02


class Fcntl(Protocol):
    """The part of the fcntl module used here.

    Attributes:
        LOCK_EX: An exclusive lock.
        LOCK_SH: A shared lock.
        LOCK_NB: Do not wait.
    """

    LOCK_EX: int
    LOCK_SH: int
    LOCK_NB: int

    def flock(self, fd: int, operation: int, /) -> None:
        """Takes or drops a lock on fd."""
        ...


def load_fcntl() -> Optional[Fcntl]:
    """Imports fcntl where the platform has it.

    Returns:
        The module, or None (Windows).
    """
    try:
        return cast(Fcntl, importlib.import_module("fcntl"))
    except ImportError:
        return None


FCNTL = load_fcntl()
# SUPPORTED says whether this platform has flock, which the daemon needs.
SUPPORTED = FCNTL is not None
# UNSUPPORTED is the one line said where it does not.
UNSUPPORTED = "the status daemon is not supported on this platform (%s has no fcntl flock)" % sys.platform


def try_flock(fd: int, shared: bool = False) -> bool:
    """Takes a lock on fd once, without waiting.

    Args:
        fd: An open descriptor of the lock file.
        shared: Take LOCK_SH (a probe) rather than LOCK_EX (a holder).

    Returns:
        True when this descriptor now holds the lock.
    """
    if FCNTL is None:
        return True  # no flock here: nothing is ever held
    op = (FCNTL.LOCK_SH if shared else FCNTL.LOCK_EX) | FCNTL.LOCK_NB
    try:
        FCNTL.flock(fd, op)
    except OSError:
        # EWOULDBLOCK means another holder; any other error is treated the
        # same way, so a lock that cannot be checked is never taken for free.
        return False
    return True


def take(fd: int, seconds: float = TAKE_SECONDS) -> bool:
    """Takes the exclusive lock, retrying while a probe briefly holds it.

    Args:
        fd: An open descriptor of the lock file.
        seconds: How long to keep trying.

    Returns:
        True when this descriptor now holds the lock.
    """
    deadline = time.monotonic() + seconds
    while True:
        if try_flock(fd):
            return True
        if time.monotonic() >= deadline:
            return False
        time.sleep(TAKE_STEP)


def held(path: str) -> bool:
    """Reports whether some process holds the exclusive lock on path.

    The probe takes a shared lock and lets it go at once, so it never looks
    like a holder to another probe.

    Args:
        path: The lock file.

    Returns:
        True when an exclusive holder refused the probe.
    """
    try:
        fd = os.open(path, os.O_RDONLY)
    except OSError:
        return False
    try:
        return not try_flock(fd, shared=True)
    finally:
        # Closing the only descriptor of this open file releases the probe's lock.
        os.close(fd)
