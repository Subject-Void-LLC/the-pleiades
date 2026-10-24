# pyright: strict
"""Memory sampling and the watchdog's decisions.

The monitor reads MemAvailable from /proc/meminfo every few seconds. While
it runs pr-check itself, the watchdog stops the run when the machine runs
short, before the kernel (or WSL) has to: this machine has crashed from
memory pressure before. The decisions are plain functions so a test can
check them without a real run or a real shortage.
"""

from __future__ import annotations

import re
from typing import Optional

# THRESHOLD_MB is the MemAvailable floor: below it the run is stopped.
THRESHOLD_MB = 700
# SAMPLE_SECONDS is how often memory is sampled.
SAMPLE_SECONDS = 5
# KILL_GRACE_SECONDS is how long a SIGTERM gets to work before the watchdog
# sends SIGKILL, if memory is still short. pr-check's TERM trap stops its
# legs and puts the lab VMs back, which takes a while, so a SIGKILL comes
# only when the shortage lasts.
KILL_GRACE_SECONDS = 60

MEM_AVAILABLE = re.compile(r"^MemAvailable:\s+(\d+) kB$", re.MULTILINE)


def parse_meminfo(text: str) -> Optional[int]:
    """Reads MemAvailable from /proc/meminfo's text.

    Args:
        text: The file's contents.

    Returns:
        MemAvailable in MB, or None when the line is missing.
    """
    m = MEM_AVAILABLE.search(text)
    if not m:
        return None
    return int(m.group(1)) // 1024


def read_available_mb(path: str = "/proc/meminfo") -> Optional[int]:
    """Reads the machine's MemAvailable.

    Args:
        path: The meminfo file.

    Returns:
        MemAvailable in MB, or None where it cannot be read.
    """
    try:
        with open(path, encoding="ascii", errors="replace") as f:
            return parse_meminfo(f.read())
    except OSError:
        return None


def should_terminate(available_mb: Optional[float], threshold_mb: float = THRESHOLD_MB) -> bool:
    """Decides whether a sample is low enough to stop the run.

    An unreadable sample (None) never stops a run: no reading is not
    evidence of a shortage.

    Args:
        available_mb: MemAvailable in MB, or None.
        threshold_mb: The floor.

    Returns:
        True when the run should get SIGTERM.
    """
    return available_mb is not None and available_mb < threshold_mb


def should_kill(terminated_at: Optional[float], now: float, available_mb: Optional[float],
                threshold_mb: float = THRESHOLD_MB, grace: float = KILL_GRACE_SECONDS) -> bool:
    """Decides whether to follow a SIGTERM with SIGKILL.

    Only when the SIGTERM has had grace seconds and memory is still short.

    Args:
        terminated_at: When SIGTERM was sent, or None if it was not.
        now: The current time.
        available_mb: MemAvailable in MB, or None.
        threshold_mb: The floor.
        grace: Seconds SIGTERM gets first.

    Returns:
        True when the run should get SIGKILL.
    """
    if terminated_at is None:
        return False
    return now - terminated_at >= grace and should_terminate(available_mb, threshold_mb)
