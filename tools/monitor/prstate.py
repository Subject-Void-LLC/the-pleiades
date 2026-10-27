# pyright: strict
"""The state model of one make pr-check run, as the monitor sees it.

A run is a list of stages (the static checks, the scope step, the three
testgate passes, the three OS legs and the four recorded steps), each with a
status and the times the monitor saw it start and end. Times are wall clock
seconds (time.time()). The make log carries no timestamps, so a time is when
the monitor read the line that marks it, unless a record pr-check wrote gives
a better one (source "record").

The parser that moves a run through these states lives in prlog.py; this
module only holds the states and the rules for changing them.
"""

from __future__ import annotations

from dataclasses import dataclass, field
from typing import Dict, List, Optional, Set, Tuple, TypedDict

# The statuses a stage can have.
PENDING = "pending"
RUNNING = "running"
PASS = "pass"
FAIL = "fail"
NOT_RUN = "not-run"
# ENDED is a stage that finished but whose verdict no line or record has
# given yet (govulncheck prints no verdict line of its own, for example).
ENDED = "ended"
# SKIPPED is a stage this run does not have (no container package changed).
SKIPPED = "skipped"
# STOPPED is a stage cut off by Ctrl-C, the watchdog or make giving up.
STOPPED = "stopped"

TERMINAL = frozenset({PASS, FAIL, NOT_RUN, ENDED, SKIPPED, STOPPED})

# The prerequisites of pr-check, in the order make runs them. tools and
# templ-gen are prerequisites of prerequisites; --trace names them too.
STATIC_TARGETS = [
    "build", "devtools", "vet", "fmt", "tidy-check", "tools", "gosec", "docs-lint",
    "docs-gen-check", "helm-lint", "templ-gen", "templ-gen-check", "workflow-lint", "pr-check-vet",
]
# HIDDEN_UNTIL_SEEN are targets only --trace shows; they stay off the page
# until something happens to them.
HIDDEN_UNTIL_SEEN = ("tools", "templ-gen")
# containers-mac is the container half pr-check runs on the Mac against its Podman (maccheck -containers).
PASSES = ["fast", "repeat", "containers", "containers-mac"]
LEGS = ["mac", "bsd", "win", "winarm"]
# coverage-fast checks the fast pass's coverage floors before anything else
# runs; sizes records the binary sizes (tools/binsize) after durations.
STEPS = ["coverage-fast", "govulncheck", "images", "coverage", "durations", "sizes"]
# The parts report.json can list as expected.
REPORT_PARTS = PASSES + LEGS + STEPS
# The order the dashboard lists stages in.
DISPLAY_ORDER = (STATIC_TARGETS + ["scope", "fast", "coverage-fast", "repeat", "containers", "containers-mac"] + LEGS
                 + ["govulncheck", "images", "coverage", "durations", "sizes"])

# A verdict word from a record or prreport's summary, as a stage status.
VERDICT_STATUS: Dict[str, str] = {
    "pass": PASS,
    "fail": FAIL,
    "error": FAIL,
    "not-run": NOT_RUN,
    "unset": NOT_RUN,
    "missing": NOT_RUN,
    "interrupted": STOPPED,
}

# LAB_LEG_STEPS and MAC_LEG_STEPS are the steps an OS leg goes through, in order. A lab VM is
# started first and put back last; the Mac is always up.
LAB_LEG_STEPS = ["box", "reachable", "ready", "ship", "test", "put back"]
MAC_LEG_STEPS = ["reachable", "ready", "ship", "test"]

# MAX_MEMORY_POINTS bounds how many memory samples one state keeps. At one
# sample every 5 seconds it is about 12 hours.
MAX_MEMORY_POINTS = 8640

# MemorySample is one MemAvailable reading: (epoch seconds, MB).
MemorySample = Tuple[float, float]


class StageDict(TypedDict):
    """A stage as the page and history see it.

    Attributes:
        name: The stage's name ("fast", "gosec").
        group: static, scope, pass, leg or step.
        status: One of the status constants.
        start: When it began, epoch seconds, or None.
        end: When it ended, epoch seconds, or None.
        elapsed: Seconds from start to end (or to now while it runs).
        source: "seen" or "record".
        detail: One line about it.
        done: Packages reported so far.
        total: Packages it runs, when known.
        failed: Packages that failed.
        last_line: An OS leg's latest line of its own.
        steps: An OS leg's steps, in order (empty for other stages).
        step: How many of those steps are done.
    """

    name: str
    group: str
    status: str
    start: Optional[float]
    end: Optional[float]
    elapsed: Optional[float]
    source: str
    detail: str
    done: int
    total: Optional[int]
    failed: int
    last_line: str
    steps: List[str]
    step: int


class TotalsDict(TypedDict):
    """How many packages each tier runs.

    Attributes:
        fast: Container-free packages, when known.
        docker: Container packages, when known.
    """

    fast: Optional[int]
    docker: Optional[int]


class RunDict(TypedDict):
    """A run as the page and history see it.

    Attributes:
        created: When the monitor began watching.
        started: When the first stage began.
        ended: When the run ended.
        verdict: pass, fail, stopped, or None while running.
        exit_code: pr-check's exit status, once known.
        commit: The commit tested (short).
        base: What the branch was compared against.
        attached_late: Whether the log already had lines at attach.
        trace: Whether make --trace lines were seen.
        notes: Remarks for the page.
        stopped_by: ctrl-c or watchdog, when the monitor stopped it.
        totals: Package counts per tier.
        stages: Every stage, in display order.
        memory: MemAvailable samples.
        lines: Main-log lines read.
    """

    created: float
    started: Optional[float]
    ended: Optional[float]
    verdict: Optional[str]
    exit_code: Optional[int]
    commit: str
    base: str
    attached_late: bool
    trace: bool
    notes: List[str]
    stopped_by: Optional[str]
    totals: TotalsDict
    stages: List[StageDict]
    memory: List[MemorySample]
    lines: int


def group_of(name: str) -> str:
    """Names the dashboard group a stage belongs to.

    Args:
        name: A stage name.

    Returns:
        pass, leg, step, scope or static.
    """
    if name in PASSES:
        return "pass"
    if name in LEGS:
        return "leg"
    if name in STEPS:
        return "step"
    if name == "scope":
        return "scope"
    return "static"


@dataclass
class Stage:
    """One step of a run: its status, times and progress.

    Attributes:
        name: The stage's name.
        group: Its dashboard group (group_of).
        status: One of the status constants.
        start: When it began, epoch seconds.
        end: When it ended, epoch seconds.
        source: "seen" (the monitor read a line) or "record" (a record
            pr-check wrote gave the times).
        detail: One line about it.
        total: Packages it runs, when known.
        packages: Packages reported so far.
        failed: Packages that failed.
        last_line: An OS leg's latest line of its own.
        steps: An OS leg's steps, in order (empty for other stages).
        step: How many of those steps are done.
    """

    name: str
    group: str = ""
    status: str = PENDING
    start: Optional[float] = None
    end: Optional[float] = None
    source: str = "seen"
    detail: str = ""
    total: Optional[int] = None
    packages: Set[str] = field(default_factory=lambda: set[str]())
    failed: Set[str] = field(default_factory=lambda: set[str]())
    last_line: str = ""
    steps: List[str] = field(default_factory=lambda: list[str]())
    step: int = 0

    def __post_init__(self) -> None:
        """Fills in the group from the name, and an OS leg's steps."""
        if not self.group:
            self.group = group_of(self.name)
        if self.group == "leg" and not self.steps:
            self.steps = list(MAC_LEG_STEPS if self.name == "mac" else LAB_LEG_STEPS)

    def reach(self, step: str) -> None:
        """Marks an OS leg's step, and every step before it, done.

        Args:
            step: One of the leg's steps; an unknown name is ignored.
        """
        if step in self.steps:
            self.step = max(self.step, self.steps.index(step) + 1)

    def to_dict(self, now: float) -> StageDict:
        """Converts the stage for the page.

        Args:
            now: The time an open stage's elapsed time runs to.

        Returns:
            The stage as a StageDict.
        """
        elapsed: Optional[float] = None
        if self.start is not None:
            elapsed = max(0.0, (self.end if self.end is not None else now) - self.start)
        return StageDict(
            name=self.name, group=self.group, status=self.status, start=self.start, end=self.end,
            elapsed=elapsed, source=self.source, detail=self.detail, done=len(self.packages),
            total=self.total, failed=len(self.failed), last_line=self.last_line,
            steps=list(self.steps), step=self.step,
        )


class RunState:
    """The whole run: its stages, verdict, memory samples and parser position.

    Attributes:
        stages: Every stage by name, in display order.
        created: When the monitor began watching.
        started: When the first stage began.
        ended: When the run ended.
        verdict: pass, fail, stopped, or None while running.
        exit_code: pr-check's exit status, once known.
        commit: The commit tested (short).
        base: What the branch was compared against.
        attached_late: Whether the log already had lines at attach.
        notes: Remarks for the page.
        stopped_by: ctrl-c or watchdog, when the monitor stopped it.
        fast_total: Container-free packages, when known.
        docker_total: Container packages, when known.
        memory: MemAvailable samples.
        main: The stage pr-check's recipe is in now (prlog.py).
        tail: Whose output the end of the log is ("mac", "lab"), once
            pr-check prints its legs' logs.
        lab_leg: Which lab leg that output belongs to now.
        in_recipe: Whether the line before was recipe text ending in a
            backslash.
        trace_seen: Whether a make --trace line was seen.
        in_summary: Whether prreport's closing summary has begun.
        records_valid: Whether .coverage/pr holds this run's records.
        lines: Main-log lines read.
        scope_done_at: When the package count line was read.
        fast_first: Whether the fast pass ran before the legs began (the
            order with coverage-fast), rather than beside them.
    """

    stages: Dict[str, Stage]
    created: float
    started: Optional[float]
    ended: Optional[float]
    verdict: Optional[str]
    exit_code: Optional[int]
    commit: str
    base: str
    attached_late: bool
    notes: List[str]
    stopped_by: Optional[str]
    fast_total: Optional[int]
    docker_total: Optional[int]
    memory: List[MemorySample]
    main: Optional[str]
    tail: Optional[str]
    lab_leg: Optional[str]
    in_recipe: bool
    trace_seen: bool
    in_summary: bool
    records_valid: bool
    lines: int
    scope_done_at: Optional[float]
    fast_first: bool

    def __init__(self, now: float) -> None:
        """Creates a run with every known stage pending.

        Args:
            now: When the monitor began watching.
        """
        self.stages = {name: Stage(name) for name in DISPLAY_ORDER}
        self.created = now
        self.started = None
        self.ended = None
        self.verdict = None
        self.exit_code = None
        self.commit = ""
        self.base = ""
        self.attached_late = False
        self.notes = []
        self.stopped_by = None
        self.fast_total = None
        self.docker_total = None
        self.memory = []
        self.main = None
        self.tail = None
        self.lab_leg = None
        self.in_recipe = False
        self.trace_seen = False
        self.in_summary = False
        self.records_valid = False
        self.lines = 0
        self.scope_done_at = None
        self.fast_first = False

    def stage(self, name: str) -> Stage:
        """Finds a stage, adding it when it is new.

        Args:
            name: The stage's name.

        Returns:
            The stage.
        """
        if name not in self.stages:
            self.stages[name] = Stage(name)
        return self.stages[name]

    def start(self, name: str, now: float, detail: Optional[str] = None) -> Stage:
        """Marks a stage running from now, unless it already began.

        Args:
            name: The stage's name.
            now: The time it began.
            detail: A new detail line, when given.

        Returns:
            The stage.
        """
        st = self.stage(name)
        if st.status == PENDING:
            st.status = RUNNING
            st.start = now
        if detail is not None:
            st.detail = detail
        if self.started is None:
            self.started = now
        return st

    def finish(self, name: str, now: float, status: str) -> Stage:
        """Ends a stage.

        A stage that never started gets now as both its start and end, so it
        shows as a point rather than vanishing. A stage that already ended
        keeps its times; its status changes only from ENDED, which means its
        verdict was not known yet.

        Args:
            name: The stage's name.
            now: The time it ended.
            status: Its status from here on.

        Returns:
            The stage.
        """
        st = self.stage(name)
        if st.status in TERMINAL:
            if st.status == ENDED and status != ENDED:
                st.status = status
            return st
        if st.start is None:
            st.start = now
        st.end = now
        st.status = status
        return st

    def set_verdict(self, name: str, status: str, detail: Optional[str] = None,
                    now: Optional[float] = None) -> Stage:
        """Sets a stage's verdict from an authority (a record or the summary).

        Unlike finish, this overrides a status the parser guessed, since a
        record knows better than a line.

        Args:
            name: The stage's name.
            status: Its status from here on.
            detail: A new detail line, when given.
            now: When given, a running stage is ended at this time.

        Returns:
            The stage.
        """
        st = self.stage(name)
        if st.status == RUNNING and now is not None:
            st.end = now
        st.status = status
        if detail:
            st.detail = detail
        return st

    def close_main(self, now: float, status: str = ENDED) -> None:
        """Ends the stage the recipe was in, if it is still running.

        Args:
            now: The time it ended.
            status: Its status, ENDED unless the caller knows better.
        """
        if self.main is not None and self.stage(self.main).status == RUNNING:
            self.finish(self.main, now, status)
        self.main = None

    def add_memory(self, t: float, available_mb: float) -> None:
        """Records one MemAvailable sample.

        Args:
            t: When it was taken.
            available_mb: MemAvailable in MB.
        """
        self.memory.append((t, available_mb))
        if len(self.memory) > MAX_MEMORY_POINTS:
            del self.memory[: len(self.memory) - MAX_MEMORY_POINTS]

    def note(self, text: str) -> None:
        """Adds a note for the page, once.

        Args:
            text: The note.
        """
        if text not in self.notes:
            self.notes.append(text)

    def end_run(self, now: float, exit_code: int, stopped_by: Optional[str] = None) -> None:
        """Closes the run: its exit code, its verdict and any stage still open.

        A stage still pending never ran: it is marked not-run, so nothing
        stays pending once the run is over.

        Args:
            now: When it ended.
            exit_code: pr-check's exit status.
            stopped_by: ctrl-c or watchdog, when the monitor stopped it.
        """
        self.ended = now
        self.exit_code = exit_code
        if stopped_by:
            self.stopped_by = stopped_by
        if self.verdict is None or self.verdict == "running":
            if self.stopped_by:
                self.verdict = "stopped"
            else:
                self.verdict = "pass" if exit_code == 0 else "fail"
        for st in self.stages.values():
            if st.status == RUNNING:
                if self.stopped_by:
                    self.finish(st.name, now, STOPPED)
                else:
                    self.finish(st.name, now, ENDED if exit_code == 0 else FAIL)
            elif st.status == PENDING and st.name not in HIDDEN_UNTIL_SEEN:
                st.status = NOT_RUN
                st.detail = st.detail or "did not run"

    def to_dict(self, now: float, max_points: int = 1500) -> RunDict:
        """Converts the run for the page or for history.

        Args:
            now: The time open stages run to.
            max_points: The most memory samples to include, thinned evenly;
                0 keeps every one.

        Returns:
            The run as a RunDict.
        """
        memory = self.memory
        if max_points and len(memory) > max_points:
            stride = len(memory) / float(max_points)
            memory = [memory[int(i * stride)] for i in range(max_points)] + [memory[-1]]
        order = [n for n in DISPLAY_ORDER if n in self.stages]
        order += [n for n in self.stages if n not in DISPLAY_ORDER]
        stages = [self.stages[n].to_dict(now) for n in order
                  if not (n in HIDDEN_UNTIL_SEEN and self.stages[n].status == PENDING)]
        return RunDict(
            created=self.created, started=self.started, ended=self.ended, verdict=self.verdict,
            exit_code=self.exit_code, commit=self.commit, base=self.base,
            attached_late=self.attached_late, trace=self.trace_seen, notes=list(self.notes),
            stopped_by=self.stopped_by, totals=TotalsDict(fast=self.fast_total, docker=self.docker_total),
            stages=stages, memory=list(memory), lines=self.lines,
        )
