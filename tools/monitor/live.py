# pyright: strict
"""The live side of the monitor: one run's state behind a lock, and its feeds.

LiveRun owns a RunState and the lock every thread takes before touching it:
the reader of the main log, the poller of .coverage/pr (records and leg
logs), the memory sampler and the HTTP server. Every line is masked by the
redactor before it reaches the state, so nothing served or saved can carry a
secret-named environment value.
"""

from __future__ import annotations

import os
import threading
import time
from typing import Dict, List, Optional, Tuple

import prlog
import prstate as ps
from history import HistoryStore
from jsonutil import JSONObject, as_object, get_str, loads
from redact import Redactor

# LEG_LOG_LIMIT bounds how much of a leg log is copied into history.
LEG_LOG_LIMIT = 4 * 1024 * 1024


class FileTail:
    """Reads whole new lines from a file that may grow or be replaced.

    Attributes:
        path: The file followed.
        offset: How many bytes have been read.
        inode: The file's inode when last read, to notice a new file.
        partial: The end of the last read that had no newline yet.
    """

    path: str
    offset: int
    inode: Optional[int]
    partial: str

    def __init__(self, path: str) -> None:
        """Follows a file from its start.

        Args:
            path: The file.
        """
        self.path = path
        self.offset = 0
        self.inode = None
        self.partial = ""

    def read_lines(self) -> List[str]:
        """Reads the complete lines added since the last call.

        A file that was replaced (new inode) or truncated (shorter than what
        was read) is read again from its start: pr-check deletes .coverage/pr
        when it begins.

        Returns:
            The new lines, without newlines.
        """
        try:
            st = os.stat(self.path)
        except OSError:
            return []
        if self.inode != st.st_ino or st.st_size < self.offset:
            self.inode, self.offset, self.partial = st.st_ino, 0, ""
        if st.st_size == self.offset:
            return []
        try:
            with open(self.path, "rb") as f:
                f.seek(self.offset)
                data = f.read(st.st_size - self.offset)
        except OSError:
            return []
        self.offset += len(data)
        lines = (self.partial + data.decode("utf-8", errors="replace")).split("\n")
        self.partial = lines.pop()
        return lines


def read_records(records_dir: str) -> Tuple[Dict[str, JSONObject], Optional[JSONObject]]:
    """Reads every record in a records directory.

    Args:
        records_dir: .coverage/pr/records or a copy of it.

    Returns:
        (each part's record by name, report.json or None). A record being
        rewritten at that moment is left out; the next poll reads it.
    """
    records: Dict[str, JSONObject] = {}
    report: Optional[JSONObject] = None
    try:
        names = os.listdir(records_dir)
    except OSError:
        return records, report
    for name in names:
        if not name.endswith(".json"):
            continue
        try:
            with open(os.path.join(records_dir, name), encoding="utf-8") as f:
                doc = as_object(loads(f.read()))
        except (OSError, ValueError):
            continue
        if doc is None:
            continue
        if name == "report.json":
            report = doc
        else:
            records[name[: -len(".json")]] = doc
    return records, report


def report_matches(state: ps.RunState, report: Optional[JSONObject]) -> bool:
    """Decides whether .coverage/pr belongs to the run the state describes.

    Compared by commit when both are known (the log names a short commit);
    otherwise trusted only once the log has shown pr-check rewriting
    .coverage/pr (state.records_valid).

    Args:
        state: The run.
        report: report.json, or None.

    Returns:
        True when the records are this run's.
    """
    if not state.records_valid:
        return False
    commit = get_str(report, "commit")
    if state.commit and commit:
        return commit.startswith(state.commit) or state.commit.startswith(commit)
    return True


class LiveRun:
    """One run being watched, shared by the monitor's threads.

    Attributes:
        redactor: Masks every line before it reaches the state.
        pr_dir: The run's .coverage/pr directory.
        lock: Guards state.
        state: The run.
        tails: Each OS leg's log, followed.
        finished: Set once the run has ended.
    """

    redactor: Redactor
    pr_dir: str
    lock: threading.Lock
    state: ps.RunState
    tails: Dict[str, FileTail]
    finished: threading.Event

    def __init__(self, redactor: Redactor, pr_dir: str, now: Optional[float] = None) -> None:
        """Watches a run.

        Args:
            redactor: Masks every line before it reaches the state.
            pr_dir: The run's .coverage/pr directory.
            now: When watching began; the current time by default.
        """
        self.redactor = redactor
        self.pr_dir = pr_dir
        self.lock = threading.Lock()
        self.state = ps.RunState(now if now is not None else time.time())
        self.tails = {leg: FileTail(os.path.join(pr_dir, leg + ".log")) for leg in ps.LEGS}
        self.finished = threading.Event()

    def feed_main(self, line: str, now: Optional[float] = None) -> str:
        """Feeds one main-log line, masked first.

        Args:
            line: The line.
            now: When it was read; the current time by default.

        Returns:
            The masked line, for the caller to store or print.
        """
        masked = self.redactor.mask(line.rstrip("\r\n"))
        with self.lock:
            prlog.feed_line(self.state, masked, now if now is not None else time.time())
        return masked

    def poll_pr_dir(self, now: Optional[float] = None) -> None:
        """Reads new leg-log lines and the records under pr_dir into the state.

        Both are read only once they are this run's (report_matches), so an
        earlier run's leftovers are never mistaken for its own.

        Args:
            now: When they were read; the current time by default.
        """
        at = now if now is not None else time.time()
        records, report = read_records(os.path.join(self.pr_dir, "records"))
        masked_records: Dict[str, JSONObject] = {}
        for name, rec in records.items():
            masked_records[name] = as_object(self.redactor.mask_tree(rec)) or {}
        masked_report = as_object(self.redactor.mask_tree(report)) if report is not None else None
        with self.lock:
            if not report_matches(self.state, report):
                return
            for leg, tail in self.tails.items():
                for line in tail.read_lines():
                    prlog.feed_leg_line(self.state, leg, self.redactor.mask(line), at)
            prlog.apply_records(self.state, masked_records, masked_report, at)

    def add_memory(self, t: float, mb: float) -> None:
        """Records a memory sample.

        Args:
            t: When it was taken.
            mb: MemAvailable in MB.
        """
        with self.lock:
            self.state.add_memory(t, mb)

    def snapshot(self, now: Optional[float] = None) -> ps.RunDict:
        """Converts the state for the page.

        Args:
            now: The time open stages run to; the current time by default.

        Returns:
            The run, memory thinned.
        """
        with self.lock:
            return self.state.to_dict(now if now is not None else time.time())

    def full_snapshot(self, now: Optional[float] = None) -> ps.RunDict:
        """Converts the state for history, keeping every memory sample.

        Args:
            now: The time open stages run to; the current time by default.

        Returns:
            The run.
        """
        with self.lock:
            return self.state.to_dict(now if now is not None else time.time(), max_points=0)

    def copy_outputs(self, store: HistoryStore, run_id: str) -> None:
        """Copies report.json and the leg logs into the run's history, masked.

        Nothing is copied unless .coverage/pr is this run's.

        Args:
            store: The history.
            run_id: The run id.
        """
        _, report = read_records(os.path.join(self.pr_dir, "records"))
        with self.lock:
            ours = report_matches(self.state, report)
        if not ours:
            return
        sources = [(os.path.join(self.pr_dir, "records", "report.json"), "report.json")]
        sources += [(os.path.join(self.pr_dir, leg + ".log"), leg + ".txt") for leg in ps.LEGS]
        for src, name in sources:
            try:
                with open(src, "rb") as f:
                    text = f.read(LEG_LOG_LIMIT).decode("utf-8", errors="replace")
            except OSError:
                continue
            store.save_text(run_id, name, self.redactor.mask(text))
