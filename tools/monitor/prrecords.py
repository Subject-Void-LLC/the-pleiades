# pyright: strict
"""The JSON records pr-check writes under .coverage/pr/records, applied to a RunState.
"""

from __future__ import annotations

import datetime
from typing import Dict, Optional

import prstate as ps
from jsonutil import JSONObject, get_float, get_int, get_str, get_strings
from prpatterns import RFC3339


def parse_time(text: str) -> Optional[float]:
    """Reads an RFC 3339 time, nanoseconds allowed.

    Args:
        text: A time such as 2026-10-24T09:49:56.014583298-04:00.

    Returns:
        Epoch seconds, or None when text is not such a time.
    """
    m = RFC3339.match(text)
    if not m:
        return None
    frac = (m.group(2) or "0")[:6].ljust(6, "0")
    zone = m.group(3) or "Z"
    zone = "+00:00" if zone == "Z" else zone
    try:
        return datetime.datetime.fromisoformat("%s.%s%s" % (m.group(1), frac, zone)).timestamp()
    except ValueError:
        return None


def record_detail(rec: JSONObject) -> str:
    """Describes a record in one line.

    Args:
        rec: A pass, leg or step record.

    Returns:
        The description.
    """
    kind = get_str(rec, "kind")
    if kind == "leg":
        error = get_str(rec, "error")
        if error:
            return "%s: stopped at %s: %s" % (get_str(rec, "os"), get_str(rec, "step", "?"), error.split("\n")[0])
        return "%s on %s, %d package(s)" % (get_str(rec, "os"), get_str(rec, "target"), len(get_strings(rec, "packages")))
    if kind == "testgate":
        return "%d package(s), %.0fs" % (len(get_strings(rec, "packages")), get_float(rec, "seconds") or 0.0)
    if get_str(rec, "reason"):
        return get_str(rec, "reason")
    code = get_int(rec, "exit_code")
    return "exit %s" % ("?" if code is None else code)


def apply_report(state: ps.RunState, report: JSONObject) -> None:
    """Folds report.json into the run: its commit, expected parts and verdict.

    Args:
        state: The run.
        report: report.json, decoded.
    """
    commit = get_str(report, "commit")
    if commit and not state.commit:
        state.commit = commit[:12]
    expected = get_strings(report, "expected")
    if expected:
        for name in ps.REPORT_PARTS:
            if name not in expected and state.stage(name).status == ps.PENDING:
                state.set_verdict(name, ps.SKIPPED, "not expected by this run")
    verdict = get_str(report, "verdict")
    if verdict and verdict != "running":
        state.verdict = verdict
        code = get_int(report, "exit_code")
        if code is not None:
            state.exit_code = code


def apply_records(state: ps.RunState, records: Dict[str, JSONObject],
                  report: Optional[JSONObject], now: float) -> None:
    """Folds pr-check's records into the run.

    A pass or leg record carries real start times and lengths, which replace
    the times the monitor saw (source "record"); a step record carries only
    its verdict. state.main is left alone: the log's own end line for a
    stage still has to arrive to move the recipe to the next one.

    Args:
        state: The run.
        records: Each part's record by name (report.json excluded).
        report: report.json, decoded, or None.
        now: The time they were read.
    """
    if report is not None:
        apply_report(state, report)
    for name, rec in records.items():
        if name not in ps.REPORT_PARTS:
            continue
        status = ps.VERDICT_STATUS.get(get_str(rec, "verdict"))
        if status is None:
            continue
        st = state.stage(name)
        start = parse_time(get_str(rec, "started"))
        seconds = get_float(rec, "seconds")
        if start is not None and seconds is not None:
            st.start, st.end, st.source = start, start + seconds, "record"
        elif st.status == ps.RUNNING:
            st.end = now
        elif st.start is None:
            st.start = st.end = now
        st.status = status
        st.detail = record_detail(rec)
        packages = get_strings(rec, "packages")
        if packages:
            st.total = len(packages)
        if get_str(rec, "kind") == "testgate" and status == ps.PASS:
            st.packages.update(packages)
