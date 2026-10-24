# pyright: strict
"""The binary size ledger tools/binsize keeps, read for the dashboard.

tools/binsize appends one JSON line per platform to .IGNORE/ci/binsize.jsonl
each time pr-check's sizes step runs:

    {"schema": 1, "commit": "...", "dirty": false, "recorded_at": "...",
     "package": "./cmd/pleiades", "platform": "linux/amd64",
     "go_version": "go1.26.10", "flags": "-trimpath", "bytes": 44178432}

Sizes are only comparable when the package, the Go release and the build
flags are the same, so entries are grouped by those three and each group is
charted on its own. The monitor only reads this file, and a missing or
unreadable file is reported, not raised.
"""

from __future__ import annotations

from typing import Dict, List, Optional, Tuple, TypedDict

from jsonutil import JSONValue, as_object, get_int, get_str, loads

# SCHEMA is the ledger schema this reader understands.
SCHEMA = 1


class SizeEntry(TypedDict):
    """One recorded size.

    Attributes:
        commit: The commit built.
        dirty: Whether the tree had uncommitted changes.
        recorded_at: When it was recorded (RFC 3339).
        bytes: The binary's size.
    """

    commit: str
    dirty: bool
    recorded_at: str
    bytes: int


class SizeGroup(TypedDict):
    """Every size recorded with one package, Go release and set of flags.

    Attributes:
        key: The three joined, to name the group.
        package: The package built.
        go_version: The Go release.
        flags: The build flags, as one string.
        latest: The newest entry's recorded_at.
        platforms: Each platform's entries, oldest first.
    """

    key: str
    package: str
    go_version: str
    flags: str
    latest: str
    platforms: Dict[str, List[SizeEntry]]


class LedgerDoc(TypedDict):
    """The ledger as the page reads it.

    Attributes:
        path: The ledger's file name, for messages.
        error: Why it could not be read, or None.
        skipped: Lines left out (not JSON, another schema, a missing field).
        groups: The comparable groups, newest first.
    """

    path: str
    error: Optional[str]
    skipped: int
    groups: List[SizeGroup]


def flags_text(value: JSONValue) -> str:
    """Turns a flags field into one string.

    Args:
        value: A string, or a list of strings.

    Returns:
        The flags, space separated.
    """
    if isinstance(value, str):
        return value
    if isinstance(value, list):
        return " ".join(v for v in value if isinstance(v, str))
    return ""


def parse_line(line: str) -> Optional[Tuple[Tuple[str, str, str], str, SizeEntry]]:
    """Reads one ledger line.

    Args:
        line: The line.

    Returns:
        ((package, go_version, flags), platform, entry), or None when the
        line is not a schema 1 entry with every field it needs.
    """
    try:
        obj = as_object(loads(line))
    except ValueError:
        return None
    if obj is None or get_int(obj, "schema") != SCHEMA:
        return None
    size = get_int(obj, "bytes")
    platform, package = get_str(obj, "platform"), get_str(obj, "package")
    if size is None or not platform or not package:
        return None
    dirty = obj.get("dirty")
    entry = SizeEntry(commit=get_str(obj, "commit"), dirty=dirty is True,
                      recorded_at=get_str(obj, "recorded_at"), bytes=size)
    return (package, get_str(obj, "go_version"), flags_text(obj.get("flags"))), platform, entry


def group_entries(text: str) -> Tuple[List[SizeGroup], int]:
    """Groups the ledger's lines by package, Go release and flags.

    Args:
        text: The whole ledger.

    Returns:
        (the groups, newest first; how many non-empty lines were left out).
    """
    groups: Dict[Tuple[str, str, str], SizeGroup] = {}
    skipped = 0
    for line in text.splitlines():
        if not line.strip():
            continue
        parsed = parse_line(line)
        if parsed is None:
            skipped += 1
            continue
        (package, go_version, flags), platform, entry = parsed
        key = (package, go_version, flags)
        if key not in groups:
            groups[key] = SizeGroup(key="%s, %s, %s" % (package, go_version, flags or "no flags"),
                                    package=package, go_version=go_version, flags=flags, latest="", platforms={})
        group = groups[key]
        group["platforms"].setdefault(platform, []).append(entry)
        group["latest"] = max(group["latest"], entry["recorded_at"])
    for group in groups.values():
        for entries in group["platforms"].values():
            entries.sort(key=lambda e: e["recorded_at"])
    ordered = sorted(groups.values(), key=lambda g: g["latest"], reverse=True)
    return ordered, skipped


def read_ledger(path: str) -> LedgerDoc:
    """Reads the ledger, never raising.

    Args:
        path: The ledger file.

    Returns:
        Its groups, or an error saying why there are none.
    """
    try:
        with open(path, encoding="utf-8", errors="replace") as f:
            text = f.read()
    except FileNotFoundError:
        return LedgerDoc(path=path, error="no ledger yet: pr-check's sizes step writes one", skipped=0, groups=[])
    except OSError as exc:
        return LedgerDoc(path=path, error="cannot read the ledger: %s" % (exc.strerror or type(exc).__name__),
                         skipped=0, groups=[])
    groups, skipped = group_entries(text)
    error = None if groups else "the ledger holds no entry this monitor can read"
    return LedgerDoc(path=path, error=error, skipped=skipped, groups=groups)
