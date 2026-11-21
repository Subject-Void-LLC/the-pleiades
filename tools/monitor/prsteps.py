# pyright: strict
"""The parser's passes and the serial steps after them: which stage runs next, and its lines.
"""

from __future__ import annotations

from typing import Optional

import prstate as ps
from prpatterns import (BINSIZE_HEAD, BINSIZE_ROW, COVERAGE_DONE, COVERAGE_LINE, DURATIONS_DONE, EXIT_STATUS, FLOOR_STOP,
                        NO_DOCKER, PASS_OK, RESOURCES_DONE, RESOURCES_LINE, STEP_ORDER)


def next_after_pass(state: ps.RunState, name: str) -> Optional[str]:
    """Names the stage that runs after a testgate pass.

    Args:
        state: The run.
        name: The pass that just ended.

    Returns:
        The next stage's name, or None when the next one starts on a marker
        of its own.
    """
    if name == "fast":
        if state.fast_first:
            # The newer order: coverage-fast, then the legs; repeat starts
            # once pr-check says the lab legs run beside it.
            return None if state.stage("coverage-fast").status == ps.SKIPPED else "coverage-fast"
        return "repeat"
    if name == "repeat":
        return "images" if state.docker_total == 0 else "containers"
    return "images"


def first_pass(state: ps.RunState) -> str:
    """Names the first testgate pass that has not begun.

    Args:
        state: The run.

    Returns:
        fast, repeat or containers, or images when every pass is done or
        not in this run.
    """
    for name in ps.PASSES:
        if state.stage(name).status == ps.PENDING:
            return name
    return "images"


def advance(state: ps.RunState, name: Optional[str], now: float) -> None:
    """Closes the recipe's current stage and starts another in its place.

    A skipped stage is passed over to the step after it: durations after
    coverage, say, when no container package changed.

    Args:
        state: The run.
        name: The stage to start, or None to start nothing.
        now: The time of the change.
    """
    state.close_main(now)
    if name is None:
        return
    if state.stage(name).status == ps.SKIPPED:
        if name in STEP_ORDER and STEP_ORDER.index(name) + 1 < len(STEP_ORDER):
            advance(state, STEP_ORDER[STEP_ORDER.index(name) + 1], now)
        return
    state.start(name, now)
    state.main = name


def current_pass(state: ps.RunState, now: float) -> Optional[str]:
    """Names the testgate pass a package line belongs to, starting it if needed.

    In the newer order the fast pass begins right after the package count
    line, so its first output starts it, timed from the count line. Whether
    an OS leg has begun does not matter: since the legs start at t0 one is
    always running by then (a run before PASS_BEGIN existed showed the fast
    pass pending while it ran).

    Args:
        state: The run.
        now: The time the line was read.

    Returns:
        The pass, or None when the line belongs to no pass.
    """
    if state.main in ps.PASSES:
        return state.main
    if state.main is not None or state.tail is not None or state.scope_done_at is None:
        return None
    if state.stage("fast").status == ps.PENDING and state.fast_total != 0:
        state.fast_first = True
        state.start("fast", state.scope_done_at)
        state.main = "fast"
        return "fast"
    return None


def on_floor_stop(state: ps.RunState, now: float) -> None:
    """Reads pr-check stopping because the fast pass missed a coverage floor.

    Everything it expected and had not begun will never run.

    Args:
        state: The run.
        now: The time it was read.
    """
    state.close_main(now, ps.FAIL)
    state.finish("coverage-fast", now, ps.FAIL)
    for name in ps.REPORT_PARTS:
        st = state.stage(name)
        if st.status == ps.PENDING:
            st.status = ps.NOT_RUN
            st.detail = "not run: the container-free packages missed a coverage floor"


def on_binsize(state: ps.RunState, line: str, now: float) -> bool:
    """Reads tools/binsize's lines, which make up the sizes step.

    Args:
        state: The run.
        line: The line.
        now: The time it was read.

    Returns:
        Whether the line was one of them.
    """
    if BINSIZE_HEAD.match(line):
        advance(state, "sizes", now)
        state.stage("sizes").detail = line
        return True
    m = BINSIZE_ROW.match(line)
    if m and state.main == "sizes":
        st = state.stage("sizes")
        st.packages.add(m.group(1))
        st.detail = "%d platform(s); %s %s %s" % (len(st.packages), m.group(1), m.group(2), m.group(3))
        return True
    return False


def on_step_line(state: ps.RunState, line: str, now: float) -> bool:
    """Reads the lines that end testgate passes and the steps after them.

    Args:
        state: The run.
        line: The line.
        now: The time it was read.

    Returns:
        Whether the line was one of them.
    """
    if FLOOR_STOP.match(line):
        on_floor_stop(state, now)
        return True
    if PASS_OK.match(line):
        current_pass(state, now)
    main = state.main
    if main == "coverage-fast" and (COVERAGE_LINE.match(line) or EXIT_STATUS.match(line)):
        # The fast pass's own floor check: its lines are not the coverage step's.
        if EXIT_STATUS.match(line):
            state.finish(main, now, ps.FAIL)
        return True
    if main == "sizes" and EXIT_STATUS.match(line):
        state.close_main(now, ps.FAIL)
        return True
    if PASS_OK.match(line) and main in ps.PASSES:
        state.finish(main, now, ps.PASS)
        advance(state, next_after_pass(state, main), now)
        return True
    if EXIT_STATUS.match(line):
        if main in ps.PASSES:
            state.finish(main, now, ps.FAIL)
            advance(state, next_after_pass(state, main), now)
        elif main in STEP_ORDER:
            state.finish(main, now, ps.FAIL)
            i = STEP_ORDER.index(main)
            advance(state, STEP_ORDER[i + 1] if i + 1 < len(STEP_ORDER) else None, now)
        return True
    if NO_DOCKER.match(line):
        state.finish("images", now, ps.NOT_RUN)
        state.stage("images").detail = "no Docker daemon answered"
        advance(state, "coverage", now)
        return True
    if COVERAGE_DONE.match(line):
        if main != "coverage":
            advance(state, "coverage", now)
        advance(state, "durations", now)
        return True
    if COVERAGE_LINE.match(line):
        if main != "coverage":
            advance(state, "coverage", now)
        return True
    if DURATIONS_DONE.match(line):
        if main != "durations":
            advance(state, "durations", now)
        state.close_main(now, ps.PASS)
        return True
    if RESOURCES_DONE.match(line):
        if main != "resources":
            advance(state, "resources", now)
        state.close_main(now, ps.PASS)
        return True
    if RESOURCES_LINE.match(line):
        if main != "resources":
            advance(state, "resources", now)
        return True
    return False
