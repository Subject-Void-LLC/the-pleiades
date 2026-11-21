# pyright: strict
"""Every past value the status page has seen, kept as JSONL, never deleted.

One file a month, timeline/history-YYYY-MM.jsonl, one JSON object a line:

    {"v": 1, "t": 1794420305.2, "kind": "gate.stage", "src": "<run id>", "data": {...}}

v is the line's schema version (docs/reference/schemas would be wrong for a
git-ignored file, so the schema is timeline.schema.json beside this module).
kind is one of KINDS; src says what the value belongs to (a run id, an
Actions run id, a phase, "machine"); data is the value itself, masked.

Only the daemon writes, and each line is one os.write on a file opened
O_APPEND, so a line is never interleaved with another and a crash leaves at
worst one truncated last line, which every reader skips.

Values that did not change are not written again: record_if_changed keeps
the last value of each (kind, src) and the daemon seeds it from the files
when it starts, so a restart writes nothing it already holds.

The files only grow, so the daemon's Reader remembers, for each query and
file, how far it has parsed and what matched, and on the next call parses
only the bytes appended since: an open page's three calls a minute cost the
new lines, not every line back to the first month.
"""

from __future__ import annotations

import json
import os
import threading
import time
from collections import OrderedDict
from typing import Dict, Iterable, Iterator, List, Mapping, Optional, Tuple

from history import ensure_private_dir
from jsonutil import JSONObject, JSONValue, as_object, get_str, loads
from redact import Redactor

VERSION = 1
# KINDS are the kinds of line written. A reader skips a kind it does not know.
KINDS = (
    "daemon",          # the daemon started or stopped, or attached to a run
    "gate.stage",      # one stage of a gate run changed status (its broker event)
    "gate.run",        # a gate run ended, with its verdict
    "actions.run",     # an Actions run's status or conclusion changed
    "actions.job",     # one of its jobs' status or conclusion changed
    "roadmap.summary", # the roadmap's totals, release or next item changed
    "roadmap.phase",   # one phase's state or counts changed
    "security.summary", # the attestation totals, pending count, waivers or unreleased changes changed
    "security.control", # one control's state, statement counts or review date changed
    "git.day",         # the main line at the end of one day: commits, merged pull requests, catalog methods
    "memory",          # MemAvailable and swap in use, one sample a minute
)


def month_file(directory: str, t: float) -> str:
    """Names the file a line written at t goes to.

    Args:
        directory: The timeline directory.
        t: Epoch seconds.

    Returns:
        <directory>/history-YYYY-MM.jsonl, in UTC.
    """
    return os.path.join(directory, time.strftime("history-%Y-%m.jsonl", time.gmtime(t)))


def files(directory: str) -> List[str]:
    """Lists the timeline's files, oldest first.

    Args:
        directory: The timeline directory.

    Returns:
        Their paths.
    """
    try:
        names = os.listdir(directory)
    except OSError:
        return []
    return [os.path.join(directory, n) for n in sorted(names) if n.startswith("history-") and n.endswith(".jsonl")]


def parse_line(raw: str) -> Optional[JSONObject]:
    """Decodes one line, or None for a truncated or foreign one.

    Args:
        raw: The line.

    Returns:
        The object, when it is a whole line of a version this reads.
    """
    raw = raw.strip()
    if not raw:
        return None
    try:
        obj = as_object(loads(raw))
    except ValueError:
        return None
    if obj is None or obj.get("v") != VERSION or not isinstance(obj.get("t"), (int, float)):
        return None
    return obj


def matches(obj: JSONObject, wanted: Optional[Iterable[str]], src: Optional[str]) -> bool:
    """Reports whether a line is of a wanted kind and src.

    Args:
        obj: The line.
        wanted: Kinds (one ending in "." names a family), or None for all.
        src: The src, or None for all.

    Returns:
        True when it matches both.
    """
    kind = get_str(obj, "kind")
    if wanted is not None and not any(kind == w or (w.endswith(".") and kind.startswith(w)) for w in wanted):
        return False
    return src is None or get_str(obj, "src") == src


def first_file(since: float) -> str:
    """Names the earliest month file a query from since can need.

    Args:
        since: Epoch seconds, or 0 for all.

    Returns:
        Its base name, or "" for every file.
    """
    return time.strftime("history-%Y-%m.jsonl", time.gmtime(since)) if since > 0 else ""


def read(directory: str, since: float = 0.0, kinds: Optional[Iterable[str]] = None,
         src: Optional[str] = None) -> Iterator[JSONObject]:
    """Reads the timeline in order.

    Args:
        directory: The timeline directory.
        since: Only lines with t greater than this.
        kinds: Only these kinds (a kind ending in "." names a family, as "gate."); all when None.
        src: Only this src; all when None.

    Yields:
        Each matching line's object.
    """
    wanted = list(kinds) if kinds is not None else None
    first_month = first_file(since)
    for path in files(directory):
        if os.path.basename(path) < first_month:
            continue
        try:
            with open(path, encoding="utf-8", errors="replace") as f:
                for raw in f:
                    obj = parse_line(raw)
                    if obj is None:
                        continue
                    t = obj.get("t")
                    if not isinstance(t, (int, float)) or t <= since or not matches(obj, wanted, src):
                        continue
                    yield obj
        except OSError:
            continue


# READER_ENTRIES bounds how many (query, file) results a Reader keeps.
READER_ENTRIES = 64
# READER_LINES bounds one kept result; a query matching more (every memory
# sample of a month) is answered by parsing again rather than kept.
READER_LINES = 20000


class ReadEntry:
    """What one query matched in one file, and how far the file was parsed.

    Attributes:
        inode: The file's inode when parsing began; another inode starts over.
        offset: The bytes parsed, always ending at a whole line.
        lines: The lines that matched, in order.
    """

    inode: int
    offset: int
    lines: List[JSONObject]

    def __init__(self, inode: int) -> None:
        """Starts an entry at a file's beginning.

        Args:
            inode: The file's inode.
        """
        self.inode = inode
        self.offset = 0
        self.lines = []


class Reader:
    """Reads the timeline as read() does, parsing each file's new bytes only.

    Attributes:
        directory: The timeline directory.
        lock: Serializes reads and guards entries.
        entries: Each (file, kinds, src) query's entry, least recently used first.
        parsed_bytes: How many bytes have been parsed in all (for tests and the curious).
    """

    directory: str
    lock: threading.Lock
    entries: "OrderedDict[Tuple[str, Optional[Tuple[str, ...]], Optional[str]], ReadEntry]"
    parsed_bytes: int

    def __init__(self, directory: str) -> None:
        """Prepares a reader.

        Args:
            directory: The timeline directory.
        """
        self.directory = directory
        self.lock = threading.Lock()
        self.entries = OrderedDict()
        self.parsed_bytes = 0

    def read(self, since: float = 0.0, kinds: Optional[Iterable[str]] = None,
             src: Optional[str] = None) -> List[JSONObject]:
        """Reads the timeline in order, as read() would.

        Args:
            since: Only lines with t greater than this.
            kinds: Only these kinds (a kind ending in "." names a family); all when None.
            src: Only this src; all when None.

        Returns:
            Each matching line's object.
        """
        wanted = tuple(sorted(set(kinds))) if kinds is not None else None
        first_month = first_file(since)
        out: List[JSONObject] = []
        with self.lock:
            for path in files(self.directory):
                if os.path.basename(path) < first_month:
                    continue
                for obj in self.file_lines(path, wanted, src):
                    t = obj.get("t")
                    if isinstance(t, (int, float)) and t > since:
                        out.append(obj)
        return out

    def file_lines(self, path: str, wanted: Optional[Tuple[str, ...]], src: Optional[str]) -> List[JSONObject]:
        """Returns one file's matching lines, parsing only what was appended since the last call. The caller holds lock.

        Args:
            path: The file.
            wanted: The kinds, or None.
            src: The src, or None.

        Returns:
            The lines, in order.
        """
        key = (path, wanted, src)
        entry = self.entries.pop(key, None)
        try:
            with open(path, "rb") as f:
                st = os.fstat(f.fileno())
                if entry is None or entry.inode != st.st_ino or st.st_size < entry.offset:
                    entry = ReadEntry(st.st_ino)  # new, replaced or cut short: start over
                if st.st_size > entry.offset:
                    f.seek(entry.offset)
                    chunk = f.read(st.st_size - entry.offset)
                    end = chunk.rfind(b"\n") + 1  # a last line still being written waits for the next call
                    for raw in chunk[:end].split(b"\n"):
                        obj = parse_line(raw.decode("utf-8", errors="replace"))
                        if obj is not None and matches(obj, wanted, src):
                            entry.lines.append(obj)
                    entry.offset += end
                    self.parsed_bytes += end
        except OSError:
            return []
        if len(entry.lines) <= READER_LINES:
            self.entries[key] = entry
            while len(self.entries) > READER_ENTRIES:
                self.entries.popitem(last=False)
        return list(entry.lines)


def fingerprint(data: JSONValue) -> str:
    """Reduces a value to a string that is equal exactly when the values are.

    Args:
        data: The value.

    Returns:
        Its canonical JSON.
    """
    return json.dumps(data, sort_keys=True, separators=(",", ":"))


class Timeline:
    """The writer: appends lines, and remembers the last value of each key.

    Attributes:
        directory: Where the files are.
        redactor: Masks every value before it is written.
        lock: Serializes writes and guards last.
        last: The fingerprint of the last value written for each (kind, src).
        gate_seq: The highest gate event seq written for each run.
    """

    directory: str
    redactor: Redactor
    lock: threading.Lock
    last: Dict[Tuple[str, str], str]
    gate_seq: Dict[str, int]

    def __init__(self, directory: str, redactor: Redactor) -> None:
        """Opens a timeline; seed() reads what it already holds.

        Args:
            directory: Where the files are.
            redactor: Masks every value before it is written.
        """
        self.directory = directory
        self.redactor = redactor
        self.lock = threading.Lock()
        self.last = {}
        self.gate_seq = {}

    def seed(self) -> int:
        """Reads every line already written, so nothing held is written twice.

        Returns:
            How many lines were read.
        """
        n = 0
        last: Dict[Tuple[str, str], str] = {}
        gate_seq: Dict[str, int] = {}
        for obj in read(self.directory):
            n += 1
            kind, src = get_str(obj, "kind"), get_str(obj, "src")
            data = obj.get("data")
            last[(kind, src)] = fingerprint(data)
            if kind.startswith("gate."):
                ev = as_object(data) if data is not None else None
                s = ev.get("seq") if ev is not None else None
                if isinstance(s, int):
                    gate_seq[src] = max(gate_seq.get(src, 0), s)
        with self.lock:
            self.last.update(last)
            for k, v in gate_seq.items():
                self.gate_seq[k] = max(self.gate_seq.get(k, 0), v)
        return n

    def append(self, kind: str, src: str, data: JSONValue, t: Optional[float] = None) -> JSONObject:
        """Writes one line.

        Args:
            kind: One of KINDS.
            src: What the value belongs to.
            data: The value; masked before it is written.
            t: When it was true; now by default.

        Returns:
            The line written.
        """
        at = t if t is not None else time.time()
        masked = self.redactor.mask_tree(data)
        line: JSONObject = {"v": VERSION, "t": round(at, 3), "kind": kind, "src": self.redactor.mask(src),
                            "data": masked}
        raw = (json.dumps(line, sort_keys=True, separators=(",", ":"), ensure_ascii=False) + "\n").encode("utf-8")
        with self.lock:
            ensure_private_dir(self.directory)
            fd = os.open(month_file(self.directory, at), os.O_WRONLY | os.O_CREAT | os.O_APPEND, 0o600)
            try:
                os.write(fd, raw)
            finally:
                os.close(fd)
            self.last[(kind, src)] = fingerprint(masked)
        return line

    def record_if_changed(self, kind: str, src: str, data: JSONValue, t: Optional[float] = None) -> bool:
        """Writes a value only when it differs from the last one for (kind, src).

        Args:
            kind: One of KINDS.
            src: What the value belongs to.
            data: The value.
            t: When it was true; now by default.

        Returns:
            Whether a line was written.
        """
        fp = fingerprint(self.redactor.mask_tree(data))
        with self.lock:
            if self.last.get((kind, src)) == fp:
                return False
        self.append(kind, src, data, t)
        return True

    def record_gate_event(self, run_id: str, event: Mapping[str, object]) -> bool:
        """Writes one gate broker event, once, however often it is replayed.

        Args:
            run_id: The run.
            event: The event, with its seq and type.

        Returns:
            Whether a line was written.
        """
        seq = event.get("seq")
        if not isinstance(seq, int):
            return False
        with self.lock:
            if seq <= self.gate_seq.get(run_id, 0):
                return False
            self.gate_seq[run_id] = seq
        at = event.get("at")
        kind = "gate.run" if event.get("type") == "run" else "gate.stage"
        data: JSONValue = loads(json.dumps(dict(event), default=str))
        self.append(kind, run_id, data, float(at) if isinstance(at, (int, float)) else None)
        return True
