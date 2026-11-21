# pyright: strict
"""The repository's own history, a value a day, for the progress chart.

The roadmap is git-ignored, so its totals exist only from the day the
daemon began recording them. What git itself holds reaches back to the
first commit: how many commits and merged pull requests the main line had
by the end of each day, and how many methods the generated module catalog
listed and marked implemented.

days() reads all of it with `git log` and one `git show` for each day the
catalog changed. It reads; it never fetches, so it is as current as the
clone's last fetch.
"""

from __future__ import annotations

import subprocess
import time
from typing import Dict, List, Optional, Tuple

from jsonutil import JSONObject, as_object, loads

# CATALOG is the generated module catalog, the one tracked count of methods.
CATALOG = "docs/reference/schemas/module-catalog.json"
# REFS are tried in order: the remote's main line, then whatever is checked out.
REFS = ("origin/main", "main", "HEAD")
GIT_TIMEOUT = 60.0
# MERGE_PREFIX opens the subject GitHub gives a pull request's merge commit.
MERGE_PREFIX = "Merge pull request #"


def git(root: str, *args: str) -> Optional[str]:
    """Runs one git command in root.

    Args:
        root: The clone.
        *args: The command, without "git".

    Returns:
        Its output, or None when it failed or did not finish.
    """
    try:
        out = subprocess.run(["git", "--no-optional-locks", *args], cwd=root, capture_output=True,
                             timeout=GIT_TIMEOUT, stdin=subprocess.DEVNULL)
    except (OSError, subprocess.TimeoutExpired):
        return None
    if out.returncode != 0:
        return None
    return out.stdout.decode("utf-8", errors="replace")


def resolve(root: str) -> Optional[Tuple[str, str]]:
    """Finds the main line and the commit it is at.

    Args:
        root: The clone.

    Returns:
        (ref, commit), or None outside a repository.
    """
    for ref in REFS:
        out = git(root, "rev-parse", "--verify", "--quiet", ref + "^{commit}")
        if out is not None and out.strip():
            return ref, out.strip()
    return None


def day_of(t: float) -> str:
    """Names the UTC day an instant falls in.

    Args:
        t: Epoch seconds.

    Returns:
        YYYY-MM-DD.
    """
    return time.strftime("%Y-%m-%d", time.gmtime(t))


def count_methods(text: str) -> Optional[Tuple[int, int]]:
    """Counts one version of the module catalog.

    Args:
        text: The catalog file's contents.

    Returns:
        (methods listed, methods implemented), or None when it is not a catalog.
    """
    try:
        doc = as_object(loads(text))
    except ValueError:
        return None
    if doc is None:
        return None
    listed = implemented = 0
    for value in doc.values():
        entry = as_object(value)
        if entry is None or "status" not in entry:
            continue
        listed += 1
        if entry.get("status") == "implemented":
            implemented += 1
    return listed, implemented


def catalog_by_day(root: str, ref: str, cache: Dict[str, Optional[Tuple[int, int]]]) -> Dict[str, Tuple[int, int]]:
    """Reads the catalog's counts at the end of each day it changed.

    Args:
        root: The clone.
        ref: The main line.
        cache: Counts already read, by commit; filled in as more are read.

    Returns:
        Each such day's (listed, implemented).
    """
    out = git(root, "log", ref, "--format=%H %ct", "--", CATALOG)
    last: Dict[str, Tuple[int, str]] = {}
    for raw in (out or "").splitlines():
        sha, _, when = raw.partition(" ")
        if not when.isdigit():
            continue
        day = day_of(int(when))
        if day not in last or int(when) > last[day][0]:
            last[day] = (int(when), sha)
    counts: Dict[str, Tuple[int, int]] = {}
    for day, (_, sha) in last.items():
        if sha not in cache:
            text = git(root, "show", "%s:%s" % (sha, CATALOG))
            cache[sha] = count_methods(text) if text is not None else None
        got = cache[sha]
        if got is not None:
            counts[day] = got
    return counts


def days(root: str, ref: str, cache: Optional[Dict[str, Optional[Tuple[int, int]]]] = None) -> List[Tuple[str, float, JSONObject]]:
    """Reduces the main line's history to one value for each day with a commit.

    Args:
        root: The clone.
        ref: The main line.
        cache: Catalog counts already read, by commit.

    Returns:
        (day, when the day's last commit was made, value), oldest first. A
        value holds the day's commits, the running totals of commits and
        merged pull requests, and the catalog's counts as they stood, None
        before the catalog existed.
    """
    out = git(root, "log", ref, "--format=%ct%x09%s")
    if out is None:
        return []
    per_day: Dict[str, List[float]] = {}
    for raw in out.splitlines():
        when, _, subject = raw.partition("\t")
        if not when.isdigit():
            continue
        slot = per_day.setdefault(day_of(int(when)), [0.0, 0.0, 0.0])
        slot[0] += 1
        slot[1] += 1 if subject.startswith(MERGE_PREFIX) else 0
        slot[2] = max(slot[2], float(when))
    catalog = catalog_by_day(root, ref, cache if cache is not None else {})
    result: List[Tuple[str, float, JSONObject]] = []
    commits = merged = 0
    methods: Optional[Tuple[int, int]] = None
    for day in sorted(set(per_day) | set(catalog)):
        n, m, last = per_day.get(day, [0.0, 0.0, 0.0])
        commits += int(n)
        merged += int(m)
        methods = catalog.get(day, methods)
        if day not in per_day:
            continue
        result.append((day, last, {
            "commits": int(n), "commits_total": commits, "merged_prs_total": merged,
            "methods": methods[0] if methods else None, "methods_implemented": methods[1] if methods else None}))
    return result
