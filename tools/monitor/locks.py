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
"""

from __future__ import annotations

import fcntl
import os
import time

# TAKE_SECONDS is how long take() keeps trying before it gives up.
TAKE_SECONDS = 1.0
# TAKE_STEP is the pause between two tries.
TAKE_STEP = 0.02


def try_flock(fd: int, shared: bool = False) -> bool:
    """Takes a lock on fd once, without waiting.

    Args:
        fd: An open descriptor of the lock file.
        shared: Take LOCK_SH (a probe) rather than LOCK_EX (a holder).

    Returns:
        True when this descriptor now holds the lock.
    """
    op = (fcntl.LOCK_SH if shared else fcntl.LOCK_EX) | fcntl.LOCK_NB
    try:
        fcntl.flock(fd, op)
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
