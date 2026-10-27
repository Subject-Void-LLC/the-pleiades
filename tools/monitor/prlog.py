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

import datetime
import re
from typing import Dict, Optional

import prstate as ps
from jsonutil import JSONObject, get_float, get_int, get_str, get_strings

# TRACE is GNU make 4.3's --trace line, one per target before its recipe.
TRACE = re.compile(r"^\S*[Mm]akefile:\d+: (?:update target '([^']+)' due to: .*|target '([^']+)' does not exist)$")
# MAKE_ERROR is make giving up on a target.
MAKE_ERROR = re.compile(r"^make(?:\[\d+\])?: \*\*\* \[\S*?:\d+: ([^\]]+)\] (.+)$")
# COMMANDS names a static target by its first command, for a log made
# without --trace. fmt prints nothing without --trace and is never seen.
COMMANDS: Dict[str, str] = {
    "go build ./...": "build",
    "go build -tags devtools ./tools/...": "devtools",
    "go vet ./...": "vet",
    "go mod tidy -diff": "tidy-check",
    "go run ./tools/gosec-check": "gosec",
    "go run ./tools/docs-lint": "docs-lint",
    "go run ./tools/gendocs": "docs-gen-check",
    "go run ./tools/helm-lint": "helm-lint",
    "go tool templ generate ./internal/ui/render": "templ-gen-check",
    "actionlint": "workflow-lint",
    "GOOS=darwin go vet -tags integration ./...": "pr-check-vet",
}
SCOPE_COUNT = re.compile(r"^pr-check: (\d+) container-free and (\d+) container package\(s\) against ([^\s,]+)"
                         r"(?:, and (\d+) container package\(s\) on the Mac's Podman)?")
PRSCOPE = re.compile(r"^prscope: ")
OSCHECK = re.compile(r"^(mac|bsd|win|winarm)check: (.*)$")
TREE_SENT = re.compile(r"^(mac|bsd|win|winarm)check: the tree of \S+ is on ")
VULN_REMOTE_START = re.compile(r"^\w+check: govulncheck \S+ runs on ")
VULN_REMOTE_PASS = re.compile(r"^\w+check: govulncheck \S+ passed on ")
VULN_REMOTE_FAIL = re.compile(r"^\w+check: govulncheck \S+ failed on ")
VULN_HERE = re.compile(r"^pr-check: the \w+ check (?:could not run govulncheck|has no tree to scan), so govulncheck runs here")
LEG_BESIDE = re.compile(r"^pr-check: the (\w+) tests run beside the ones below")
LAB_BESIDE = re.compile(r"^pr-check: the ([\w ]+) tests run one after another beside")
# LEG_AT_START and LAB_AT_START are the same lines when the legs start at t0,
# before any pass: each starts its leg and leaves the recipe's own stage alone.
LEG_AT_START = re.compile(r"^pr-check: the (\w+) tests run from the start, beside")
LAB_AT_START = re.compile(r"^pr-check: the ([\w ]+) tests run one after another from the start, beside")
# PASS_BEGIN is the line pr-check prints as each local testgate pass begins.
PASS_BEGIN = re.compile(r"^pr-check: the (fast|repeat|containers) pass begins")
PACKAGE = re.compile(r"^(ok|FAIL|\?)\s+([\w.\-]+(?:/[\w.\-]+)+)(?:\s|$)")
PASS_OK = re.compile(r"^testgate: (?:all tests passed|passed \()")
EXIT_STATUS = re.compile(r"^exit status (\d+)$")
COVERAGE_LINE = re.compile(r"^coverage-check: ")
COVERAGE_DONE = re.compile(r"^coverage-check: \d+ package\(s\) measured")
DURATIONS_DONE = re.compile(r"^testgate: durations\.json ")
NO_DOCKER = re.compile(r"^pr-check: no Docker daemon answered")
WAITING = re.compile(r"^pr-check: waiting for the ([\w-]+) tests")
SUMMARY = re.compile(r"^pr-check(?: at (\S+))?, (.+?): ([A-Z][A-Z-]*)(?: \(exit (-?\d+)\))?$")
SUMMARY_ROW = re.compile(r"^  (" + "|".join(ps.REPORT_PARTS) + r")\s+(\S+)\s*(.*)$")
ALL_PASSED = re.compile(r"^pr-check: all checks passed")
LEG_PASS = re.compile(r"^(\w+)check: all tests passed on ")
LEG_FAIL = re.compile(r"^(\w+)check: the tests failed on ")
LEG_ERROR = re.compile(r"^(\w+)check: running the tests on ")
LEG_PUT_BACK = re.compile(r"^(\w+)check: put the VM \S+ back")
LEG_BOX = re.compile(r"^\w+check: starting the VM ")
LEG_READY = re.compile(r"^\w+check: (\d+) package\(s\) at \S+ on ")
LEG_TESTING = re.compile(r"^\w+check: running on the ")
FLOOR_STOP = re.compile(r"^pr-check: the container-free packages miss a coverage floor")
BINSIZE_HEAD = re.compile(r"^binsize: ")
BINSIZE_ROW = re.compile(r"^  ([\w.-]+/[\w.-]+)\s+([\d.]+ [KMG]iB)\s*(.*)$")
RFC3339 = re.compile(r"^(\d{4}-\d\d-\d\dT\d\d:\d\d:\d\d)(?:\.(\d+))?(Z|[+-]\d\d:\d\d)?$")

# LOGGED are the stages with a log of their own under .coverage/pr: each OS
# leg, and the container half on the Mac's Podman, which runs beside the
# Mac's own tests.
LOGGED = ps.LEGS + ["containers-mac"]

# STEP_ORDER is the serial order of what follows the testgate passes.
STEP_ORDER = ["images", "coverage", "durations"]


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
    if m.group(4) is not None:
        state.stage("scope").detail += ", and %s on the Mac's Podman" % m.group(4)
    for name in ("fast", "repeat"):
        state.stage(name).total = state.fast_total
        if state.fast_total == 0:
            state.set_verdict(name, ps.SKIPPED, "no container-free package changed")
    if state.fast_total == 0:
        state.set_verdict("coverage-fast", ps.SKIPPED, "no container-free package changed")
    state.stage("containers").total = state.docker_total
    if m.group(4) is not None:
        state.stage("containers-mac").total = int(m.group(4))
        if m.group(4) == "0":
            state.set_verdict("containers-mac", ps.SKIPPED, "no container package changed that runs on the Mac")
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
        state.stage(leg).detail = "tree sent; its tests start after govulncheck"
        # The leg stays running: only its prepare phase is over.
        if state.main == leg:
            state.main = None
        advance(state, "govulncheck", now)
    elif VULN_REMOTE_START.match(line):
        if state.main != "govulncheck":
            advance(state, "govulncheck", now)
        state.stage("govulncheck").detail = "on the %s machine" % leg
    elif VULN_REMOTE_PASS.match(line):
        state.finish("govulncheck", now, ps.PASS)
    elif VULN_REMOTE_FAIL.match(line):
        state.finish("govulncheck", now, ps.FAIL)


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
    return False


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
    if VULN_HERE.match(line):
        if state.main == "mac":
            # The Mac sent no tree, so it has no tests to run later.
            state.finish("mac", now, ps.ENDED)
            state.stage("mac").detail = "no tree on the machine, so its tests do not run"
            state.main = None
        advance(state, "govulncheck", now)
        state.stage("govulncheck").detail = "on this machine"
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
        leg: One of LOGGED: an OS leg, or containers-mac.
        line: The line.
        now: The time it was read.
    """
    line = line.rstrip("\r\n")
    if not line.strip():
        return
    if leg == "mac" and on_mac_vuln(state, line, now):
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


def on_mac_vuln(state: ps.RunState, line: str, now: float) -> bool:
    """Reads the govulncheck lines the Mac's log holds: the scan runs on the
    Mac, after its prepare phase and before its tests, or here when it cannot.

    Args:
        state: The run.
        line: A line of the Mac's log.
        now: The time it was read.

    Returns:
        Whether the line was one of them.
    """
    vuln = state.stage("govulncheck")
    if VULN_REMOTE_START.match(line) or VULN_HERE.match(line):
        if vuln.status == ps.PENDING:
            state.start("govulncheck", now)
        vuln.detail = "on the mac machine" if VULN_REMOTE_START.match(line) else "on this machine"
        return True
    if VULN_REMOTE_PASS.match(line):
        state.finish("govulncheck", now, ps.PASS)
        return True
    if VULN_REMOTE_FAIL.match(line):
        state.finish("govulncheck", now, ps.FAIL)
        return True
    return False


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
