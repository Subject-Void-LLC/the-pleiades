# pyright: strict
"""Memory sampling and the watchdog's decisions.

The monitor reads MemAvailable from /proc/meminfo every few seconds. While
it runs pr-check itself, the watchdog stops the run when the machine runs
short (MemAvailable under its floor, or swap grown past its limit since the
run began), before the kernel (or WSL) has to: this machine has crashed from
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

# SWAP_GROWTH_MB is how much more swap the machine may use than it did when
# the run began before the run is stopped. MemAvailable alone missed the
# 2026-10-25 crash: the kernel paged into swap until all 8 GB were gone and
# load reached 600, MemAvailable never went under the floor, and this
# sampler was itself starved for minutes at a time. Swap grows before that
# point, while there is still time to stop the run.
SWAP_GROWTH_MB = 3072
# SWAP_STEP_MB is the growth at which each step is logged, so a run leaves
# evidence of how close it came even when nothing stops it.
SWAP_STEP_MB = 1024

MEM_AVAILABLE = re.compile(r"^MemAvailable:\s+(\d+) kB$", re.MULTILINE)
SWAP_TOTAL = re.compile(r"^SwapTotal:\s+(\d+) kB$", re.MULTILINE)
SWAP_FREE = re.compile(r"^SwapFree:\s+(\d+) kB$", re.MULTILINE)


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


def parse_swap_used(text: str) -> Optional[int]:
    """Reads the swap in use from /proc/meminfo's text.

    Args:
        text: The file's contents.

    Returns:
        SwapTotal minus SwapFree in MB, or None when either line is missing.
    """
    total, free = SWAP_TOTAL.search(text), SWAP_FREE.search(text)
    if not total or not free:
        return None
    return (int(total.group(1)) - int(free.group(1))) // 1024


def read_swap_used_mb(path: str = "/proc/meminfo") -> Optional[int]:
    """Reads the machine's swap in use.

    Args:
        path: The meminfo file.

    Returns:
        Swap in use in MB, or None where it cannot be read.
    """
    try:
        with open(path, encoding="ascii", errors="replace") as f:
            return parse_swap_used(f.read())
    except OSError:
        return None


def swap_grown(used_mb: Optional[float], baseline_mb: Optional[float], limit_mb: float = SWAP_GROWTH_MB) -> bool:
    """Decides whether swap has grown enough since the run began to stop it.

    A missing reading or baseline never stops a run.

    Args:
        used_mb: Swap in use now, in MB, or None.
        baseline_mb: Swap in use when the run began, in MB, or None.
        limit_mb: The growth allowed.

    Returns:
        True when the run should get SIGTERM.
    """
    return used_mb is not None and baseline_mb is not None and used_mb - baseline_mb >= limit_mb


def swap_step(used_mb: Optional[float], baseline_mb: Optional[float], step_mb: float = SWAP_STEP_MB) -> int:
    """Says how many whole steps swap has grown since the run began.

    Args:
        used_mb: Swap in use now, in MB, or None.
        baseline_mb: Swap in use when the run began, in MB, or None.
        step_mb: One step.

    Returns:
        The number of steps, 0 when it has not grown or cannot be read.
    """
    if used_mb is None or baseline_mb is None or used_mb <= baseline_mb:
        return 0
    return int((used_mb - baseline_mb) // step_mb)


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
