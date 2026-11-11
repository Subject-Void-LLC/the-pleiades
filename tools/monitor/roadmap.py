# pyright: strict
"""The roadmap panel: a git-ignored roadmap's --json model, read on demand.

The repository names no internal document, so the panel is configured on
the machine that has one, in the state directory's panels.json:

    {"roadmap": {"script": "/abs/path/to/the/roadmap/script.py"}}

The script is run as `python3 <script> --json [--phase ID ...] [--details]`,
the read contract it already offers, and its output is cached until any
file beside it (the roadmap, its specification, its attestation, the script
itself) changes, or CACHE_SECONDS pass, since the model also reads the
repository's tests and files. A configured but failing script is reported
on the page, never raised.
"""

from __future__ import annotations

import os
import subprocess
import sys
import threading
import time
from typing import Dict, List, Optional, Sequence, Tuple

from history import read_json
from jsonutil import JSONObject, JSONValue, as_object, get_objects, get_str, loads

# CACHE_SECONDS bounds how stale an unchanged roadmap's model may be.
CACHE_SECONDS = 300.0
# RUN_TIMEOUT bounds one run of the script.
RUN_TIMEOUT = 60.0
# MAX_PHASES bounds how many phases one request may name.
MAX_PHASES = 20


def load_config(panels_path: str) -> Optional[str]:
    """Reads the roadmap script's path from panels.json.

    Args:
        panels_path: panels.json.

    Returns:
        The script, when one is configured and exists.
    """
    doc = as_object(read_json(panels_path))
    rm = as_object(doc.get("roadmap")) if doc is not None else None
    script = get_str(rm, "script")
    return script if script and os.path.isabs(script) and os.path.isfile(script) else None


def watched_mtimes(script: str) -> Tuple[float, ...]:
    """Reads the modification times the cache is keyed on.

    Args:
        script: The roadmap script.

    Returns:
        The script's and every Markdown file's beside it, in name order.
    """
    d = os.path.dirname(script)
    out: List[float] = []
    try:
        names = sorted(n for n in os.listdir(d) if n.endswith(".md") or n == os.path.basename(script))
    except OSError:
        return ()
    for n in names:
        try:
            out.append(os.stat(os.path.join(d, n)).st_mtime)
        except OSError:
            continue
    return tuple(out)


def compact(model: JSONObject) -> JSONObject:
    """Drops what the overview never shows: each item's long details.

    Args:
        model: The script's model.

    Returns:
        A copy without items' details.
    """
    phases: List[JSONValue] = []
    for p in get_objects(model, "phases"):
        items: List[JSONValue] = []
        for item in get_objects(p, "items"):
            items.append({k: v for k, v in item.items() if k != "details"})
        phases.append(dict(p, items=items))
    return dict(model, phases=phases)


def summary_value(model: JSONObject) -> JSONObject:
    """Picks the roadmap totals the timeline keeps.

    Args:
        model: The roadmap model.

    Returns:
        The summary without its long lists, plus the spec and security totals.
    """
    s = as_object(model.get("summary")) or {}
    keep = ("work_items", "work_items_done", "percent_done", "open_items", "items_citing_missing_tests",
            "items_citing_missing_files", "current_release", "release_line")
    out: JSONObject = {k: s[k] for k in keep if k in s}
    nxt = as_object(s.get("next_item"))
    if nxt is not None:
        out["next_item"] = {"phase": nxt.get("phase"), "title": nxt.get("title")}
    for part in ("spec", "security"):
        doc = as_object(model.get(part))
        if doc is not None and "summary" in doc:
            out[part] = doc["summary"]
    return out


def phase_values(model: JSONObject) -> Dict[str, JSONObject]:
    """Picks each phase's state and counts for the timeline.

    Args:
        model: The roadmap model.

    Returns:
        Each phase id's value.
    """
    out: Dict[str, JSONObject] = {}
    for p in get_objects(model, "phases"):
        pid = get_str(p, "id")
        if pid:
            out[pid] = {"state": p.get("state"), "done": p.get("done"), "total": p.get("total"),
                        "version": p.get("version")}
    return out


class RoadmapPanel:
    """Runs the roadmap script and caches its model.

    Attributes:
        panels_path: panels.json, reread on every request so an edit needs no restart.
        lock: Serializes runs of the script and guards cache.
        cache: Each request key's (mtimes, when, model or error).
    """

    panels_path: str
    lock: threading.Lock
    cache: Dict[str, Tuple[Tuple[float, ...], float, JSONObject]]

    def __init__(self, panels_path: str) -> None:
        """Prepares the panel.

        Args:
            panels_path: panels.json.
        """
        self.panels_path = panels_path
        self.lock = threading.Lock()
        self.cache = {}

    def configured(self) -> bool:
        """Reports whether a roadmap script is configured.

        Returns:
            True when panels.json names one that exists.
        """
        return load_config(self.panels_path) is not None

    def model(self, phases: Optional[Sequence[str]] = None, details: bool = False) -> JSONObject:
        """Returns the roadmap model, from cache when nothing changed.

        Args:
            phases: Only these phases (the script's --phase), or all.
            details: Whether items keep their details; the overview drops them.

        Returns:
            The model, or {"error": why} when there is none.
        """
        script = load_config(self.panels_path)
        if script is None:
            return {"error": "no roadmap panel: panels.json in the monitor's state directory names no script",
                    "configure": '{"roadmap": {"script": "/abs/path/implementation.py"}}'}
        wanted = sorted(set(phases or []))[:MAX_PHASES]
        key = "%s|%s|%d" % (script, ",".join(wanted), int(details))
        mtimes = watched_mtimes(script)
        now = time.time()
        with self.lock:
            hit = self.cache.get(key)
            if hit is not None and hit[0] == mtimes and now - hit[1] < CACHE_SECONDS:
                return hit[2]
            doc = self.run(script, wanted, details)
            self.cache[key] = (mtimes, now, doc)
            # Keep the cache small: the overview plus a few phases' details.
            while len(self.cache) > 32:
                oldest = min(self.cache.items(), key=lambda kv: kv[1][1])[0]
                del self.cache[oldest]
            return doc

    def run(self, script: str, phases: Sequence[str], details: bool) -> JSONObject:
        """Runs the script once.

        Args:
            script: The script.
            phases: --phase values.
            details: Whether to pass --details.

        Returns:
            Its model, compacted without details, or {"error": why}.
        """
        cmd = [sys.executable, "-B", script, "--json"]
        for p in phases:
            cmd += ["--phase", p]
        # The overview drops details itself; asking for them keeps item titles whole.
        if details or not phases:
            cmd.append("--details")
        try:
            out = subprocess.run(cmd, cwd=os.path.dirname(script), capture_output=True, timeout=RUN_TIMEOUT,
                                 stdin=subprocess.DEVNULL)
        except (OSError, subprocess.TimeoutExpired) as exc:
            return {"error": "the roadmap script did not run: %s" % type(exc).__name__}
        if out.returncode != 0:
            tail = out.stderr.decode("utf-8", errors="replace").strip().splitlines()[-1:] or [""]
            return {"error": "the roadmap script exited %d: %s" % (out.returncode, tail[0][:300])}
        try:
            doc = as_object(loads(out.stdout.decode("utf-8", errors="replace")))
        except ValueError:
            return {"error": "the roadmap script printed something that is not JSON"}
        if doc is None:
            return {"error": "the roadmap script printed JSON that is not an object"}
        return doc if details else compact(doc)
