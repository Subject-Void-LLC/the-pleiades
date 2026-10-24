# pyright: strict
"""The monitor's history: every local run and every Actions run it saw.

Everything lives under one directory, .IGNORE/ci/monitor/ in the repository,
which .gitignore already ignores (the `.[A-Z]*` pattern), since a run log
can name a host and a user. The directory is mode 0700 and every file in it
0600. Layout:

    runs/<run id>/run.json      the run's state (stages, memory, verdict)
    runs/<run id>/log.txt       the run's own log, secrets masked
    runs/<run id>/report.json   .coverage/pr/records/report.json, copied
    runs/<run id>/<leg>.txt     .coverage/pr/<leg>.log, copied
    actions.json                every Actions run seen, with its jobs

pr-check deletes .coverage/pr when it starts, so the copies are the only
lasting record of a run's report.
"""

from __future__ import annotations

import json
import os
import re
import time
from typing import Dict, List, Mapping, Optional, TextIO, TypedDict, Union

from jsonutil import JSONObject, JSONValue, as_object, get_float, get_objects, get_str, loads

# RUN_ID is what a run id must look like. It also keeps a requested id from
# naming a path outside runs/.
RUN_ID = re.compile(r"^[0-9]{8}T[0-9]{6}Z(?:-[0-9a-f]{1,12})?(?:-[0-9]+)?$")
# HISTORY_FILE is what a file saved beside run.json must be called.
HISTORY_FILE = re.compile(r"^[a-z0-9_-]+\.(?:txt|json)$")
# MAX_ACTIONS_RUNS bounds actions.json.
MAX_ACTIONS_RUNS = 300


class RunSummary(TypedDict):
    """One saved run, as the history table lists it.

    Attributes:
        id: The run id.
        started: When it began.
        ended: When it ended.
        seconds: Its length.
        verdict: pass, fail or stopped.
        commit: The commit tested.
        mode: run or watch.
        stopped_by: ctrl-c or watchdog, when the monitor stopped it.
        stages: Each ended stage's length in seconds.
    """

    id: str
    started: Optional[float]
    ended: Optional[float]
    seconds: Optional[float]
    verdict: str
    commit: str
    mode: str
    stopped_by: str
    stages: Dict[str, float]


def ensure_private_dir(path: str) -> None:
    """Creates a directory (and its parents) and makes it mode 0700.

    Args:
        path: The directory.
    """
    os.makedirs(path, mode=0o700, exist_ok=True)
    os.chmod(path, 0o700)


def write_private(path: str, data: Union[str, bytes]) -> None:
    """Writes a file as mode 0600, through a temp file renamed over it.

    Args:
        path: The file.
        data: Its contents; text is written as UTF-8.
    """
    raw = data.encode("utf-8") if isinstance(data, str) else data
    tmp = path + ".tmp"
    fd = os.open(tmp, os.O_WRONLY | os.O_CREAT | os.O_TRUNC, 0o600)
    try:
        os.write(fd, raw)
    finally:
        os.close(fd)
    os.chmod(tmp, 0o600)
    os.replace(tmp, path)


def write_json(path: str, value: object) -> None:
    """Writes a value as JSON, mode 0600.

    Args:
        path: The file.
        value: Anything json.dumps accepts.
    """
    write_private(path, json.dumps(value, indent=1, sort_keys=True) + "\n")


def read_json(path: str) -> JSONValue:
    """Reads a JSON file.

    Args:
        path: The file.

    Returns:
        The decoded value, or None when the file is missing or not JSON.
    """
    try:
        with open(path, encoding="utf-8") as f:
            return loads(f.read())
    except (OSError, ValueError):
        return None


def new_run_id(now: float, commit: str = "") -> str:
    """Builds a run id from a start time and a commit.

    Args:
        now: When the run began.
        commit: The commit tested, if known.

    Returns:
        An id such as 20261024T134930Z-ae89f745ba1c.
    """
    stamp = time.strftime("%Y%m%dT%H%M%SZ", time.gmtime(now))
    short = re.sub(r"[^0-9a-f]", "", commit.lower())[:12]
    return stamp + ("-" + short if short else "")


def stage_seconds(run: JSONObject) -> Dict[str, float]:
    """Measures each ended stage of a saved run.

    Args:
        run: A saved run.json.

    Returns:
        Each stage's length in seconds, skipped stages left out.
    """
    out: Dict[str, float] = {}
    for st in get_objects(run, "stages"):
        start, end = get_float(st, "start"), get_float(st, "end")
        if start is not None and end is not None and get_str(st, "status") != "skipped":
            out[get_str(st, "name")] = round(max(0.0, end - start), 1)
    return out


class HistoryStore:
    """Reads and writes the history directory.

    Attributes:
        root: The history directory.
        runs_dir: Its runs/ subdirectory.
    """

    root: str
    runs_dir: str

    def __init__(self, root: str) -> None:
        """Opens a history directory, creating it 0700.

        Args:
            root: The directory.
        """
        self.root = root
        self.runs_dir = os.path.join(root, "runs")
        ensure_private_dir(root)
        ensure_private_dir(self.runs_dir)

    def run_dir(self, run_id: str) -> str:
        """Finds a run's directory.

        Args:
            run_id: The run id.

        Returns:
            The directory's path.

        Raises:
            ValueError: run_id is not a run id.
        """
        if not RUN_ID.match(run_id):
            raise ValueError("not a run id: %r" % (run_id,))
        return os.path.join(self.runs_dir, run_id)

    def unique_run_id(self, now: float, commit: str = "") -> str:
        """Builds a run id no saved run has yet.

        Args:
            now: When the run began.
            commit: The commit tested, if known.

        Returns:
            The run id.
        """
        base = new_run_id(now, commit)
        run_id, n = base, 1
        while os.path.exists(os.path.join(self.runs_dir, run_id)):
            n += 1
            run_id = "%s-%d" % (base, n)
        return run_id

    def create_run(self, run_id: str) -> str:
        """Creates a run's directory, 0700.

        Args:
            run_id: The run id.

        Returns:
            The directory's path.
        """
        path = self.run_dir(run_id)
        ensure_private_dir(path)
        return path

    def open_log(self, run_id: str) -> TextIO:
        """Opens a run's log for appending, mode 0600, line buffered.

        Args:
            run_id: The run id.

        Returns:
            The open file.
        """
        path = os.path.join(self.create_run(run_id), "log.txt")
        fd = os.open(path, os.O_WRONLY | os.O_CREAT | os.O_APPEND, 0o600)
        os.chmod(path, 0o600)
        return os.fdopen(fd, "w", encoding="utf-8", buffering=1)

    def save_run(self, run_id: str, run: Mapping[str, object], meta: Mapping[str, object]) -> None:
        """Saves a run's state as run.json.

        Args:
            run_id: The run id.
            run: The run (prstate.RunDict).
            meta: Fields to add, such as the mode.
        """
        doc = dict(run)
        doc.update(meta)
        doc["id"] = run_id
        write_json(os.path.join(self.create_run(run_id), "run.json"), doc)

    def save_text(self, run_id: str, name: str, text: str) -> None:
        """Saves a text file beside a run's run.json.

        Args:
            run_id: The run id.
            name: The file's name, such as report.json or mac.txt.
            text: Its contents.

        Raises:
            ValueError: name is not a history file name.
        """
        if not HISTORY_FILE.match(name):
            raise ValueError("not a history file name: %r" % (name,))
        write_private(os.path.join(self.create_run(run_id), name), text)

    def load_run(self, run_id: str) -> Optional[JSONObject]:
        """Loads a saved run, with its copied report.

        Args:
            run_id: The run id.

        Returns:
            run.json with a "report" field, or None when there is no such run.
        """
        try:
            path = self.run_dir(run_id)
        except ValueError:
            return None
        run = as_object(read_json(os.path.join(path, "run.json")))
        if run is not None:
            run["report"] = read_json(os.path.join(path, "report.json"))
        return run

    def list_runs(self) -> List[RunSummary]:
        """Summarizes every saved run, newest first.

        Returns:
            One summary per run.
        """
        out: List[RunSummary] = []
        try:
            names = os.listdir(self.runs_dir)
        except OSError:
            return out
        for name in names:
            if not RUN_ID.match(name):
                continue
            run = as_object(read_json(os.path.join(self.runs_dir, name, "run.json")))
            if run is None:
                continue
            start = get_float(run, "started") or get_float(run, "created")
            end = get_float(run, "ended")
            out.append(RunSummary(
                id=name, started=start, ended=end,
                seconds=round(end - start, 1) if start is not None and end is not None else None,
                verdict=get_str(run, "verdict"), commit=get_str(run, "commit"), mode=get_str(run, "mode"),
                stopped_by=get_str(run, "stopped_by"), stages=stage_seconds(run),
            ))
        out.sort(key=lambda r: r["started"] or 0.0, reverse=True)
        return out

    def actions_path(self) -> str:
        """Names actions.json.

        Returns:
            Its path.
        """
        return os.path.join(self.root, "actions.json")

    def load_actions(self) -> Dict[str, JSONObject]:
        """Loads every Actions run seen.

        Returns:
            Each run by its id (as a string).
        """
        doc = as_object(read_json(self.actions_path()))
        if doc is None:
            return {}
        out: Dict[str, JSONObject] = {}
        for key, value in doc.items():
            run = as_object(value)
            if run is not None:
                out[key] = run
        return out

    def save_actions(self, runs: List[JSONObject]) -> Dict[str, JSONObject]:
        """Merges runs into actions.json.

        A newer sighting of a run replaces the older one; only the newest
        MAX_ACTIONS_RUNS runs are kept.

        Args:
            runs: Runs with their jobs (actions.ActionsRun, as JSON).

        Returns:
            Everything actions.json now holds.
        """
        seen = self.load_actions()
        for run in runs:
            seen[str(run.get("id"))] = run
        keep = sorted(seen.values(), key=lambda r: get_str(r, "created"), reverse=True)[:MAX_ACTIONS_RUNS]
        doc = {str(r.get("id")): r for r in keep}
        write_json(self.actions_path(), doc)
        return doc
