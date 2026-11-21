# pyright: strict
"""Where the status daemon and every gate run keep their files.

There is one status page per machine, whichever worktree a gate runs in, so
every path here is under the MAIN clone's .IGNORE/ci/monitor, found through
git's common directory (a worktree's .git file points at it). A copy of the
monitor outside any repository falls back to the tree it sits in.

    .IGNORE/ci/monitor/
        statusd.lock        held by the daemon for its life; its pid, port, protocol
        statusd.ensure.lock held by one ensure while it decides and starts
        statusd.log         the daemon's own output
        gate.lock           held by the one gate run in progress
        live.json           that run's manifest, removed when it ends
        panels.json         optional, hand-written: extra panels (the roadmap)
        timeline/           every past value, one JSON object a line, a file a month
        runs/<id>/          one run: log.txt, run.json, events.jsonl, live.json
        actions.json        every Actions run seen
"""

from __future__ import annotations

import os
import subprocess

HERE = os.path.dirname(os.path.abspath(__file__))
# TREE_ROOT is the checkout this copy of the monitor sits in.
TREE_ROOT = os.path.dirname(os.path.dirname(HERE))


def main_root(tree: str = TREE_ROOT) -> str:
    """Finds the main clone of the repository a tree belongs to.

    Args:
        tree: A checkout, main or a worktree.

    Returns:
        The main clone's directory, or tree itself when git cannot say.
    """
    try:
        out = subprocess.run(["git", "rev-parse", "--path-format=absolute", "--git-common-dir"], cwd=tree,
                             capture_output=True, text=True, timeout=10)
    except (OSError, subprocess.TimeoutExpired):
        return tree
    common = out.stdout.strip()
    if out.returncode != 0 or not common or os.path.basename(common) != ".git":
        return tree
    return os.path.dirname(common)


def state_dir(tree: str = TREE_ROOT) -> str:
    """Names the one directory the monitor writes to on this machine.

    Args:
        tree: A checkout, main or a worktree.

    Returns:
        <main clone>/.IGNORE/ci/monitor.
    """
    return os.path.join(main_root(tree), ".IGNORE", "ci", "monitor")


def daemon_lock(state: str) -> str:
    """Names the daemon's lock file.

    Args:
        state: The state directory.

    Returns:
        Its path.
    """
    return os.path.join(state, "statusd.lock")


def ensure_lock(state: str) -> str:
    """Names the lock one ensure holds while it decides and starts, so two at once start one daemon.

    Args:
        state: The state directory.

    Returns:
        Its path.
    """
    return os.path.join(state, "statusd.ensure.lock")


def daemon_log(state: str) -> str:
    """Names the daemon's output file.

    Args:
        state: The state directory.

    Returns:
        Its path.
    """
    return os.path.join(state, "statusd.log")


def gate_lock(state: str) -> str:
    """Names the lock the one gate run holds.

    Args:
        state: The state directory.

    Returns:
        Its path.
    """
    return os.path.join(state, "gate.lock")


def manifest(state: str) -> str:
    """Names the running gate's manifest.

    Args:
        state: The state directory.

    Returns:
        Its path.
    """
    return os.path.join(state, "live.json")


def panels(state: str) -> str:
    """Names the optional panel configuration.

    Args:
        state: The state directory.

    Returns:
        Its path.
    """
    return os.path.join(state, "panels.json")


def timeline_dir(state: str) -> str:
    """Names the directory of the JSONL history.

    Args:
        state: The state directory.

    Returns:
        Its path.
    """
    return os.path.join(state, "timeline")
