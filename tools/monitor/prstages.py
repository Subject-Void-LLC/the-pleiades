# pyright: strict
"""The parser's static targets, the scope line and the OS legs' steps.
"""

from __future__ import annotations

import re

import prstate as ps
from prpatterns import LEG_BOX, LEG_FAIL, LEG_PASS, LEG_PUT_BACK, LEG_READY, LEG_TESTING, TREE_SENT


def static_reached_prcheck(state: ps.RunState, now: float) -> None:
    """Marks the static checks passed once make has reached pr-check's recipe.

    make stops at the first failing prerequisite, so reaching the recipe
    means every one of them passed, including any the log never showed.

    Args:
        state: The run.
        now: The time make reached the recipe.
    """
    for name in ps.STATIC_TARGETS:
        st = state.stage(name)
        if st.status == ps.RUNNING:
            state.finish(name, now, ps.PASS)
        elif st.status == ps.PENDING and name not in ps.HIDDEN_UNTIL_SEEN:
            state.finish(name, now, ps.PASS)
            st.detail = "not visible in the log; make reached pr-check, so it passed"
    if state.main in ps.STATIC_TARGETS:
        state.main = None


def start_scope(state: ps.RunState, now: float) -> None:
    """Starts the scope step, which opens pr-check's own recipe.

    Args:
        state: The run.
        now: The time it began.
    """
    static_reached_prcheck(state, now)
    if state.stage("scope").status == ps.PENDING:
        state.start("scope", now, "prscope: which packages the branch changes")
        state.main = "scope"


def on_scope_count(state: ps.RunState, m: re.Match[str], now: float) -> None:
    """Reads pr-check's package count line, which ends the scope step.

    Args:
        state: The run.
        m: SCOPE_COUNT's match.
        now: The time it was read.
    """
    start_scope(state, now)
    state.finish("scope", now, ps.PASS)
    state.fast_total, state.docker_total, state.base = int(m.group(1)), int(m.group(2)), m.group(3)
    state.stage("scope").detail = "%s container-free and %s container package(s) against %s" % m.groups()[:3]
    for name in ("fast", "repeat"):
        state.stage(name).total = state.fast_total
        if state.fast_total == 0:
            state.set_verdict(name, ps.SKIPPED, "no container-free package changed")
    if state.fast_total == 0:
        state.set_verdict("coverage-fast", ps.SKIPPED, "no container-free package changed")
    state.stage("containers").total = state.docker_total
    if state.docker_total == 0:
        state.set_verdict("containers", ps.SKIPPED, "no container package changed")
        state.set_verdict("durations", ps.SKIPPED, "no container package changed")
    # pr-check deletes and rewrites .coverage/pr right after this line, so
    # its records belong to this run from here on.
    state.records_valid = True
    state.scope_done_at = now
    # What runs next depends on the Makefile's order: the Mac's prepare
    # phase (a maccheck line) or the fast pass (its first package line).
    state.main = None


def leg_step(st: ps.Stage, line: str) -> None:
    """Moves an OS leg's step bar by one of its own lines.

    Args:
        st: The leg's stage.
        line: A line the leg's check printed.
    """
    ready = LEG_READY.match(line)
    if LEG_BOX.match(line):
        st.reach("box")
    elif ready:
        st.reach("ready")
        # The leg's own package count, which can differ from the fast pass's.
        st.total = int(ready.group(1))
    elif TREE_SENT.match(line) or LEG_TESTING.match(line):
        st.reach("ship")
    elif LEG_PASS.match(line) or LEG_FAIL.match(line):
        st.reach("test")
    elif LEG_PUT_BACK.match(line) and st.step == len(st.steps) - 1:
        # Only a leg whose tests ran fills its bar; one that stopped early
        # keeps the step it reached, and its status says why.
        st.reach("put back")


def on_oscheck(state: ps.RunState, leg: str, line: str, now: float) -> None:
    """Reads an OS check's line printed in the main log (its prepare phase).

    Args:
        state: The run.
        leg: mac, bsd or win.
        line: The line.
        now: The time it was read.
    """
    if state.stage(leg).status == ps.PENDING:
        # The leg's prepare phase runs in the foreground: whatever ran
        # before it (coverage-fast, in the newer order) has ended.
        if state.main is not None and state.main not in ps.LEGS:
            state.close_main(now)
        state.start(leg, now, "preparing: checking the machine and sending the tree")
        state.main = leg
    state.stage(leg).last_line = line
    leg_step(state.stage(leg), line)
    if TREE_SENT.match(line):
        state.stage(leg).detail = "tree sent; its tests run on the machine"
        # The leg stays running: only its prepare phase is over.
        if state.main == leg:
            state.main = None


def on_trace(state: ps.RunState, target: str, now: float) -> None:
    """Reads a make --trace line: target's recipe is about to run.

    Args:
        state: The run.
        target: The target make names.
        now: The time it was read.
    """
    state.trace_seen = True
    if target == "pr-check":
        start_scope(state, now)
        return
    if state.main in ps.STATIC_TARGETS:
        state.finish(state.main, now, ps.PASS)
    state.start(target, now)
    state.main = target


def on_make_error(state: ps.RunState, target: str, now: float) -> None:
    """Reads make giving up on a target.

    Args:
        state: The run.
        target: The target that failed.
        now: The time it was read.
    """
    if target in state.stages and state.stage(target).group == "static":
        state.finish(target, now, ps.FAIL)
    elif state.main is not None:
        state.close_main(now, ps.FAIL)
    if state.verdict in (None, "running"):
        state.verdict = "fail"
