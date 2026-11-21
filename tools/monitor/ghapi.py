# pyright: strict
"""gh api, as the Actions poller calls it: one REST path in, decoded JSON or a short reason out.

It never asks gh for a token and never prints one.
"""

from __future__ import annotations

import os
import subprocess
from typing import Callable, Optional, Tuple

from jsonutil import JSONValue, loads


# ApiCall reads one API path: (decoded JSON, None) or (None, reason).
ApiCall = Callable[[str], Tuple[JSONValue, Optional[str]]]
# Runner is subprocess.run's shape, so a test can stand in for it.
Runner = Callable[..., "subprocess.CompletedProcess[str]"]


def explain_error(stderr: str) -> str:
    """Turns gh's error output into a short reason.

    Args:
        stderr: What gh printed on stderr.

    Returns:
        A one-line reason.
    """
    text = stderr.strip()
    low = text.lower()
    if "auth login" in low or "not logged" in low or "http 401" in low or "bad credentials" in low:
        return "gh is not logged in: run gh auth login"
    first = text.splitlines()[0] if text else "gh api failed with no message"
    return first[:200]


def gh_api(path: str, run: Runner = subprocess.run) -> Tuple[JSONValue, Optional[str]]:
    """Reads one API path with gh api.

    Args:
        path: The path, repository included.
        run: subprocess.run, or a stand-in.

    Returns:
        (decoded JSON, None), or (None, a reason) when it could not be read.
    """
    env = dict(os.environ)
    env.update({"GH_PROMPT_DISABLED": "1", "GH_NO_UPDATE_NOTIFIER": "1", "NO_COLOR": "1"})
    try:
        proc = run(["gh", "api", "-H", "Accept: application/vnd.github+json", path],
                   capture_output=True, text=True, timeout=30, env=env)
    except FileNotFoundError:
        return None, "gh is not installed"
    except subprocess.TimeoutExpired:
        return None, "gh api timed out"
    if proc.returncode != 0:
        return None, explain_error(proc.stderr or "")
    try:
        return loads(proc.stdout), None
    except ValueError:
        return None, "gh api returned something that is not JSON"


def current_branch(repo_root: str, run: Runner = subprocess.run) -> Optional[str]:
    """Names the branch checked out.

    Args:
        repo_root: The repository.
        run: subprocess.run, or a stand-in.

    Returns:
        The branch, or None for a detached HEAD or an error.
    """
    try:
        proc = run(["git", "rev-parse", "--abbrev-ref", "HEAD"], cwd=repo_root,
                   capture_output=True, text=True, timeout=10)
    except (OSError, subprocess.TimeoutExpired):
        return None
    branch = (proc.stdout or "").strip()
    return branch if proc.returncode == 0 and branch and branch != "HEAD" else None
