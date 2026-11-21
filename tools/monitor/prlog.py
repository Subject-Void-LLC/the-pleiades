# pyright: strict
"""The parser that reads make pr-check's output into a RunState.

feed_line takes one line of the main log (make's own output, ideally from
`make --trace pr-check`), feed_leg_line one line of an OS leg's log
(.coverage/pr/<mac|bsd|win>.log), and apply_records the JSON records pr-check
writes under .coverage/pr/records. Each moves the stages in prstate.py.

The markers it reads, in the order pr-check prints them, are listed in
README.md. The parser never trusts recipe text: --trace prints each recipe
before running it, and the pr-check recipe holds the very strings it later
echoes, so a line that ends in a backslash, or follows one, is skipped.
"""

from __future__ import annotations

import prstate as ps
# The patterns, the handlers each line is given to and the records live in
# modules of their own; the names a caller reads as prlog.<name> are imported
# here and listed in __all__.
from prpatterns import (ALL_PASSED, COMMANDS, LAB_AT_START, LAB_BESIDE, LEG_AT_START, LEG_BESIDE, LEG_ERROR, LEG_FAIL,
                        LEG_PASS, LEG_PUT_BACK, LOGGED, MAKE_ERROR, OSCHECK, PACKAGE, PASS_BEGIN, PRSCOPE, SCOPE_COUNT,
                        SUMMARY, SUMMARY_ROW, TRACE, WAITING)
from prrecords import apply_records, apply_report, parse_time, record_detail
from prstages import leg_step, on_make_error, on_oscheck, on_scope_count, on_trace, start_scope
from prsteps import advance, current_pass, first_pass, on_binsize, on_step_line

__all__ = ["LOGGED", "OSCHECK", "TRACE", "apply_records", "apply_report", "feed_leg_line", "feed_line", "feed_summary",
           "feed_tail", "parse_time", "record_detail"]


def feed_line(state: ps.RunState, line: str, now: float) -> None:
    """Moves the run by one line of the main log.

    Args:
        state: The run.
        line: The line, with or without its newline.
        now: The time it was read.
    """
    line = line.rstrip("\r\n")
    state.lines += 1
    # Recipe text that --trace prints: skip it, whatever it says.
    continued = state.in_recipe
    state.in_recipe = line.endswith("\\")
    if continued or state.in_recipe:
        return

    m = TRACE.match(line)
    if m:
        on_trace(state, m.group(1) or m.group(2), now)
        return
    m = MAKE_ERROR.match(line)
    if m:
        on_make_error(state, m.group(1), now)
        return
    if not state.trace_seen and line in COMMANDS:
        target = COMMANDS[line]
        if state.main in ps.STATIC_TARGETS and state.main != target:
            state.finish(state.main, now, ps.PASS)
        state.start(target, now)
        state.main = target
        return
    if PRSCOPE.match(line):
        start_scope(state, now)
        return
    m = SCOPE_COUNT.match(line)
    if m:
        on_scope_count(state, m, now)
        return
    if state.tail is not None:
        feed_tail(state, line, now)
        return
    m = OSCHECK.match(line)
    if m:
        on_oscheck(state, m.group(1), line, now)
        return
    m = PASS_BEGIN.match(line)
    if m:
        if m.group(1) == "fast":
            state.fast_first = True
        if state.main != m.group(1):
            advance(state, m.group(1), now)
        return
    m = LEG_AT_START.match(line)
    if m:
        if m.group(1) in ps.LEGS and state.stage(m.group(1)).status == ps.PENDING:
            state.start(m.group(1), now, "starting")
        return
    m = LAB_AT_START.match(line)
    if m:
        first = m.group(1).split()[0]
        if first in ps.LEGS and state.stage(first).status == ps.PENDING:
            state.start(first, now, "starting its VM")
        return
    m = LEG_BESIDE.match(line)
    if m:
        state.close_main(now)
        if m.group(1) in ps.LEGS:
            state.start(m.group(1), now, "testing on the machine")
        return
    if LAB_BESIDE.match(line):
        state.close_main(now)
        state.start("bsd", now, "starting its VM")
        advance(state, first_pass(state), now)
        return
    m = PACKAGE.match(line)
    if m:
        name = current_pass(state, now)
        if name is not None:
            st = state.stage(name)
            st.packages.add(m.group(2))
            if m.group(1) == "FAIL":
                st.failed.add(m.group(2))
        return
    if on_step_line(state, line, now) or on_binsize(state, line, now):
        return
    m = WAITING.match(line)
    if m:
        state.close_main(now)
        state.tail = m.group(1)
        state.lab_leg = "bsd" if state.tail == "lab" else state.tail
        return
    feed_summary(state, line, now)


def feed_tail(state: ps.RunState, line: str, now: float) -> None:
    """Reads a line of the leg output pr-check prints once its passes end.

    Args:
        state: The run.
        line: The line.
        now: The time it was read.
    """
    m = WAITING.match(line)
    if m:
        state.tail = m.group(1)
        state.lab_leg = "bsd" if state.tail == "lab" else state.tail
        return
    if SUMMARY.match(line):
        state.tail = None
        feed_summary(state, line, now)
        return
    m = OSCHECK.match(line)
    if m and state.tail in ("lab", "mac"):
        # The lab's output is bsd's then win's; a run before winarm had a
        # leg of its own printed its log after the Mac's.
        state.lab_leg = m.group(1)
    if state.lab_leg in LOGGED:
        feed_leg_line(state, state.lab_leg, line, now)


def feed_summary(state: ps.RunState, line: str, now: float) -> None:
    """Reads prreport's closing summary: the verdict and one row per part.

    Args:
        state: The run.
        line: The line.
        now: The time it was read.
    """
    m = SUMMARY.match(line)
    if m:
        state.in_summary = True
        state.tail = None
        if m.group(1):
            state.commit = m.group(1)
        state.verdict = m.group(3).lower()
        if m.group(4) is not None:
            state.exit_code = int(m.group(4))
        state.close_main(now)
        return
    if ALL_PASSED.match(line):
        state.verdict = "pass"
        return
    if state.in_summary:
        m = SUMMARY_ROW.match(line)
        if m and m.group(2) in ps.VERDICT_STATUS:
            state.set_verdict(m.group(1), ps.VERDICT_STATUS[m.group(2)], m.group(3), now)


def feed_leg_line(state: ps.RunState, leg: str, line: str, now: float) -> None:
    """Moves an OS leg by one line of that leg's own log.

    Args:
        state: The run.
        leg: One of LOGGED: an OS leg.
        line: The line.
        now: The time it was read.
    """
    line = line.rstrip("\r\n")
    if not line.strip():
        return
    st = state.stage(leg)
    # The Windows leg begins when the FreeBSD one ends: they share the lab.
    if leg == "win" and state.stage("bsd").status == ps.RUNNING:
        state.finish("bsd", now, ps.ENDED)
    if st.status == ps.PENDING:
        state.start(leg, now)
    m = PACKAGE.match(line)
    if m:
        st.packages.add(m.group(2))
        if m.group(1) == "FAIL":
            st.failed.add(m.group(2))
        return
    if OSCHECK.match(line):
        st.last_line = line
        leg_step(st, line)
    if LEG_PASS.match(line):
        state.finish(leg, now, ps.PASS)
    elif LEG_FAIL.match(line) or LEG_ERROR.match(line):
        state.finish(leg, now, ps.FAIL)
    elif LEG_PUT_BACK.match(line):
        state.finish(leg, now, ps.ENDED)


