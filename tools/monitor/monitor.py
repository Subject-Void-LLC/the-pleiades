#!/usr/bin/env python3
# pyright: strict
"""A live dashboard of make pr-check, every stage, and the branch's GitHub Actions.

    monitor.py run   [--cap 4500M] [--parallelism 1] [--port 8765] [--linger 120]
    monitor.py watch [--log FILE] [--pr-dir DIR] [--pace SECONDS] [--no-save]
    monitor.py serve [--port 8765]

run starts `make --trace pr-check` itself (under ~/.local/bin/capped, in its
own process group), serves the dashboard at http://127.0.0.1:<port>/, stops
the run if MemAvailable drops under 700 MB, saves the run to history and
exits with pr-check's status. watch follows a log something else is writing
(or replays a finished one) plus .coverage/pr. serve shows only history and
Actions. Every file it writes is under .IGNORE/ci/monitor/ (see history.py).

Python 3 standard library only. Nothing in the Makefile or CI calls this.
"""

from __future__ import annotations

import sys

# Importing the sibling modules must not write __pycache__ into the tree.
sys.dont_write_bytecode = True

import argparse  # noqa: E402
import os  # noqa: E402
import re  # noqa: E402
import signal  # noqa: E402
import subprocess  # noqa: E402
import threading  # noqa: E402
import time  # noqa: E402
from dataclasses import dataclass  # noqa: E402
from types import FrameType  # noqa: E402
from typing import Callable, List, Mapping, Optional, Sequence, TextIO, TypedDict  # noqa: E402

import actions  # noqa: E402
import binsize  # noqa: E402
import history  # noqa: E402
import live  # noqa: E402
import prstate as ps  # noqa: E402
import redact  # noqa: E402
import server  # noqa: E402
import memwatch  # noqa: E402
from jsonutil import JSONObject, get_str  # noqa: E402

HERE = os.path.dirname(os.path.abspath(__file__))
# REPO_ROOT is two levels above tools/monitor.
REPO_ROOT = os.path.dirname(os.path.dirname(HERE))
# HISTORY_DIR is the one directory the monitor writes to.
HISTORY_DIR = os.path.join(REPO_ROOT, ".IGNORE", "ci", "monitor")
PR_DIR = os.path.join(REPO_ROOT, ".coverage", "pr")
# BINSIZE_LEDGER is tools/binsize's ledger, which the monitor only reads.
BINSIZE_LEDGER = os.path.join(REPO_ROOT, ".IGNORE", "ci", "binsize.jsonl")
CAPPED = os.path.expanduser("~/.local/bin/capped")
# SAVE_SECONDS is how often a run in progress is saved to history.
SAVE_SECONDS = 10
# MAX_APP_MEMORY bounds the memory samples kept for the page.
MAX_APP_MEMORY = 8640

SampleHook = Callable[[Optional[int], float], None]


@dataclass
class Options:
    """The command line, typed.

    Attributes:
        command: run, watch or serve.
        port: The port on 127.0.0.1.
        no_actions: Whether to skip polling GitHub Actions.
        cap: run: the memory cap handed to capped.
        parallelism: run: PR_CHECK_PARALLELISM.
        linger: run: seconds to keep serving after the run.
        log: watch: the log to follow.
        pr_dir: watch: the .coverage/pr directory to read.
        pace: watch: seconds between lines already in the log.
        no_save: watch: whether to skip saving the run.
    """

    command: str
    port: int = 8765
    no_actions: bool = False
    cap: str = "4500M"
    parallelism: int = 1
    linger: int = 120
    log: Optional[str] = None
    pr_dir: Optional[str] = None
    pace: float = 0.0
    no_save: bool = False


class StateDoc(TypedDict):
    """The /state document.

    Attributes:
        mode: run, watch or serve.
        now: The server's clock.
        run_id: The history id of the live run.
        run: The live run, or None.
        actions: The latest Actions poll.
        memory: Recent MemAvailable samples.
        memory_floor_mb: The watchdog's floor.
    """

    mode: str
    now: float
    run_id: Optional[str]
    run: Optional[ps.RunDict]
    actions: JSONObject
    memory: List[ps.MemorySample]
    memory_floor_mb: int


class App:
    """What the HTTP server reads: the live run, history and Actions.

    Attributes:
        mode: run, watch or serve.
        store: The history.
        live_run: The run being watched, or None.
        run_id: Its history id.
        poller: The Actions poller, or None.
        memory: Recent MemAvailable samples.
        lock: Guards memory.
    """

    mode: str
    store: history.HistoryStore
    live_run: Optional[live.LiveRun]
    run_id: Optional[str]
    poller: Optional[actions.ActionsPoller]
    memory: List[ps.MemorySample]
    lock: threading.Lock

    def __init__(self, mode: str, store: history.HistoryStore, live_run: Optional[live.LiveRun] = None,
                 run_id: Optional[str] = None, poller: Optional[actions.ActionsPoller] = None) -> None:
        """Builds the server's view.

        Args:
            mode: run, watch or serve.
            store: The history.
            live_run: The run being watched, or None.
            run_id: Its history id.
            poller: The Actions poller, or None.
        """
        self.mode = mode
        self.store = store
        self.live_run = live_run
        self.run_id = run_id
        self.poller = poller
        self.memory = []
        self.lock = threading.Lock()

    def add_memory(self, t: float, mb: float) -> None:
        """Keeps a memory sample for the page.

        Args:
            t: When it was taken.
            mb: MemAvailable in MB.
        """
        with self.lock:
            self.memory.append((t, mb))
            del self.memory[: max(0, len(self.memory) - MAX_APP_MEMORY)]

    def state(self) -> StateDoc:
        """Builds the /state document.

        Returns:
            The live run, Actions and memory.
        """
        with self.lock:
            memory = self.memory[-1500:]
        if self.poller is not None:
            acts = self.poller.snapshot()
        else:
            acts: JSONObject = {"branch": None, "runs": [], "polled": None,
                                "error": "Actions polling is off (--no-actions)"}
        return StateDoc(mode=self.mode, now=time.time(), run_id=self.run_id,
                        run=self.live_run.snapshot() if self.live_run is not None else None,
                        actions=acts, memory=memory, memory_floor_mb=memwatch.THRESHOLD_MB)

    def history(self) -> Mapping[str, object]:
        """Builds the /history document.

        Returns:
            {"runs": every saved run's summary}.
        """
        return {"runs": self.store.list_runs()}

    def history_run(self, run_id: str) -> Optional[JSONObject]:
        """Loads one saved run.

        Args:
            run_id: The run id.

        Returns:
            The run, or None.
        """
        return self.store.load_run(run_id)

    def actions_history(self) -> Mapping[str, object]:
        """Builds the /actions-history document.

        Returns:
            {"runs": every Actions run seen, newest first}.
        """
        runs = sorted(self.store.load_actions().values(), key=lambda r: get_str(r, "created"), reverse=True)
        return {"runs": runs}

    def binsize(self) -> binsize.LedgerDoc:
        """Builds the /binsize document from the ledger.

        Returns:
            The ledger's comparable groups, or why there are none.
        """
        doc = binsize.read_ledger(BINSIZE_LEDGER)
        doc["path"] = os.path.relpath(BINSIZE_LEDGER, REPO_ROOT)
        return doc


def git_head(root: str) -> str:
    """Reads HEAD's commit.

    Args:
        root: The repository.

    Returns:
        The full commit, or an empty string.
    """
    try:
        out = subprocess.run(["git", "rev-parse", "HEAD"], cwd=root, capture_output=True, text=True, timeout=10)
    except (OSError, subprocess.TimeoutExpired):
        return ""
    return out.stdout.strip() if out.returncode == 0 else ""


def start_thread(target: Callable[[], None], name: str) -> threading.Thread:
    """Runs a function in a daemon thread.

    Args:
        target: The function.
        name: The thread's name.

    Returns:
        The started thread.
    """
    t = threading.Thread(target=target, name=name, daemon=True)
    t.start()
    return t


def memory_loop(app: App, live_run: Optional[live.LiveRun], stop: threading.Event,
                on_sample: Optional[SampleHook] = None) -> None:
    """Samples MemAvailable every SAMPLE_SECONDS until stop is set.

    Args:
        app: Keeps samples for the page.
        live_run: Keeps samples with the run, until it ends; or None.
        stop: Ends the loop.
        on_sample: Called with each sample (the watchdog), or None.
    """
    while not stop.is_set():
        now, mb = time.time(), memwatch.read_available_mb()
        if mb is not None:
            app.add_memory(now, mb)
            if live_run is not None and not live_run.finished.is_set():
                live_run.add_memory(now, mb)
        if on_sample is not None:
            on_sample(mb, now)
        stop.wait(memwatch.SAMPLE_SECONDS)


def pr_dir_loop(live_run: live.LiveRun, stop: threading.Event) -> None:
    """Reads .coverage/pr into the run every two seconds until stop is set.

    Args:
        live_run: The run.
        stop: Ends the loop.
    """
    while not stop.is_set():
        try:
            live_run.poll_pr_dir()
        except Exception as exc:  # a bad poll must not end the monitor
            print("monitor: reading .coverage/pr failed: %s" % type(exc).__name__, file=sys.stderr)
        stop.wait(2)


def save_loop(store: history.HistoryStore, live_run: live.LiveRun, run_id: str,
              meta: Mapping[str, object], stop: threading.Event) -> None:
    """Saves the run to history every SAVE_SECONDS until stop is set.

    Args:
        store: The history.
        live_run: The run.
        run_id: Its history id.
        meta: Fields saved with it.
        stop: Ends the loop.
    """
    while not stop.wait(SAVE_SECONDS):
        store.save_run(run_id, live_run.full_snapshot(), meta)


def start_common(opts: Options, mode: str, store: history.HistoryStore, redactor: redact.Redactor,
                 live_run: Optional[live.LiveRun] = None,
                 run_id: Optional[str] = None) -> tuple[App, server.ThreadingHTTPServer]:
    """Starts Actions polling and the server.

    Args:
        opts: The command line.
        mode: run, watch or serve.
        store: The history.
        redactor: Masks Actions text.
        live_run: The run being watched, or None.
        run_id: Its history id.

    Returns:
        (the app, the running server). Exits with status 2 when the port is
        taken.
    """
    poller: Optional[actions.ActionsPoller] = None
    if not opts.no_actions:
        poller = actions.ActionsPoller(REPO_ROOT, store, redactor)
        poller.start()
    app = App(mode, store, live_run, run_id, poller)
    try:
        httpd = server.start_server(app, opts.port)
    except OSError as exc:
        print("monitor: cannot serve on 127.0.0.1:%d: %s" % (opts.port, exc.strerror or exc), file=sys.stderr)
        sys.exit(2)
    print("monitor: dashboard at http://127.0.0.1:%d/" % opts.port, flush=True)
    return app, httpd


def wait_for_ctrl_c(seconds: Optional[float] = None) -> None:
    """Blocks until Ctrl-C (or SIGTERM), or until seconds pass.

    Args:
        seconds: The longest wait, or None to wait for the signal alone.
    """
    done = threading.Event()

    def on_signal(signum: int, frame: Optional[FrameType]) -> None:
        """Ends the wait.

        Args:
            signum: The signal.
            frame: Unused.
        """
        done.set()

    signal.signal(signal.SIGINT, on_signal)
    signal.signal(signal.SIGTERM, on_signal)
    if seconds is None:
        while not done.wait(1):
            pass
    else:
        done.wait(seconds)


class Stopper:
    """Signals pr-check's whole process group, never by name.

    Attributes:
        proc: pr-check, a session (and process group) leader.
        run: The run, where the reason is noted.
        terminated_at: When SIGTERM was sent, or None.
        killed: Whether SIGKILL was sent.
    """

    proc: subprocess.Popen[bytes]
    run: live.LiveRun
    terminated_at: Optional[float]
    killed: bool

    def __init__(self, proc: subprocess.Popen[bytes], live_run: live.LiveRun) -> None:
        """Prepares to stop proc.

        Args:
            proc: pr-check, started with start_new_session.
            live_run: The run, where the reason is noted.
        """
        self.proc = proc
        self.run = live_run
        self.terminated_at = None
        self.killed = False

    def signal_group(self, sig: signal.Signals) -> None:
        """Sends a signal to the process group, ignoring a group already gone.

        Args:
            sig: The signal.
        """
        try:
            os.killpg(self.proc.pid, sig)
        except ProcessLookupError:
            pass

    def note(self, reason: str, text: str) -> None:
        """Records why the run is being stopped.

        Args:
            reason: ctrl-c or watchdog.
            text: A sentence for the page.
        """
        with self.run.lock:
            self.run.state.stopped_by = reason
            self.run.state.note(text)

    def term(self, reason: str, text: str) -> None:
        """Sends SIGTERM once.

        Args:
            reason: ctrl-c or watchdog.
            text: A sentence for the page and the terminal.
        """
        if self.terminated_at is not None or self.proc.poll() is not None:
            return
        self.terminated_at = time.time()
        self.note(reason, text)
        print("\nmonitor: %s; sending SIGTERM to pr-check's process group" % text, file=sys.stderr, flush=True)
        self.signal_group(signal.SIGTERM)

    def kill(self, reason: str, text: str) -> None:
        """Sends SIGKILL once.

        Args:
            reason: ctrl-c or watchdog.
            text: A sentence for the page and the terminal.
        """
        if self.killed or self.proc.poll() is not None:
            return
        self.killed = True
        self.note(reason, text)
        print("\nmonitor: %s; sending SIGKILL to pr-check's process group" % text, file=sys.stderr, flush=True)
        self.signal_group(signal.SIGKILL)


def check_run_options(opts: Options) -> Optional[str]:
    """Refuses run options that cannot work.

    Args:
        opts: The command line.

    Returns:
        A reason to refuse, or None.
    """
    if not re.match(r"^[1-9][0-9]*[MG]$", opts.cap):
        return "--cap is a size like 4500M or 4G, not %r" % opts.cap
    if opts.parallelism < 0:
        return "--parallelism is 0 or more"
    if not os.access(CAPPED, os.X_OK):
        return "%s is missing; pr-check is never run uncapped here" % CAPPED
    return None


def cmd_run(opts: Options) -> int:
    """Runs make --trace pr-check under the monitor.

    Args:
        opts: The command line.

    Returns:
        pr-check's exit status (128 plus the signal when one killed it), or
        2 when the options are refused.
    """
    refusal = check_run_options(opts)
    if refusal:
        print("monitor: " + refusal, file=sys.stderr)
        return 2
    redactor = redact.Redactor.from_environment()
    live_run = live.LiveRun(redactor, PR_DIR)
    commit = git_head(REPO_ROOT)
    live_run.state.commit = commit[:12]
    store = history.HistoryStore(HISTORY_DIR)
    run_id = store.unique_run_id(time.time(), commit)
    app, httpd = start_common(opts, "run", store, redactor, live_run, run_id)
    meta: Mapping[str, object] = {"mode": "run", "cap": opts.cap, "parallelism": opts.parallelism}

    log = store.open_log(run_id)
    cmd = ["env", "PR_CHECK_PARALLELISM=%d" % opts.parallelism, CAPPED, opts.cap, "make", "--trace", "pr-check"]
    print("monitor: running make --trace pr-check under capped %s (history %s)" % (opts.cap, run_id), flush=True)
    # start_new_session is setsid(): pr-check gets its own session and
    # process group, so Ctrl-C here does not reach it directly and a stop
    # signals exactly its group.
    proc = subprocess.Popen(cmd, cwd=REPO_ROOT, stdin=subprocess.DEVNULL, stdout=subprocess.PIPE,
                            stderr=subprocess.STDOUT, start_new_session=True)
    stopper = Stopper(proc, live_run)
    stdout = proc.stdout
    assert stdout is not None  # stdout=PIPE always gives one

    def read_output() -> None:
        """Tees pr-check's output to the terminal and the run log, masked."""
        for raw in iter(stdout.readline, b""):
            masked = live_run.feed_main(raw.decode("utf-8", errors="replace"))
            log.write(masked + "\n")
            sys.stdout.write(masked + "\n")
            sys.stdout.flush()

    def on_sample(mb: Optional[int], now: float) -> None:
        """Applies the watchdog to one memory sample.

        Args:
            mb: MemAvailable in MB, or None.
            now: When it was taken.
        """
        if proc.poll() is not None or mb is None:
            return
        if memwatch.should_kill(stopper.terminated_at, now, mb):
            stopper.kill("watchdog", "MemAvailable still %d MB a minute after SIGTERM" % mb)
        elif memwatch.should_terminate(mb):
            stopper.term("watchdog", "the watchdog stopped the run: MemAvailable %d MB, under %d MB"
                         % (mb, memwatch.THRESHOLD_MB))

    interrupts: List[int] = [0]

    def on_interrupt(signum: int, frame: Optional[FrameType]) -> None:
        """Stops pr-check: SIGTERM first (it puts its VMs back), SIGKILL on a second.

        Args:
            signum: The signal.
            frame: Unused.
        """
        interrupts[0] += 1
        if interrupts[0] == 1:
            stopper.term("ctrl-c", "stopped by Ctrl-C")
        else:
            stopper.kill("ctrl-c", "stopped by a second Ctrl-C; a lab VM may be left running")

    signal.signal(signal.SIGINT, on_interrupt)
    signal.signal(signal.SIGTERM, on_interrupt)
    stop = threading.Event()
    reader = start_thread(read_output, "reader")
    start_thread(lambda: memory_loop(app, live_run, stop, on_sample), "memory")
    start_thread(lambda: pr_dir_loop(live_run, stop), "pr-dir")
    start_thread(lambda: save_loop(store, live_run, run_id, meta, stop), "save")

    while proc.poll() is None:
        time.sleep(0.5)
    reader.join(timeout=10)
    rc = proc.returncode
    if rc < 0:
        rc = 128 - rc  # killed by signal -rc, as a shell reports it
    finish_run(live_run, store, run_id, meta, rc, log)
    stop.set()
    print("monitor: pr-check ended, verdict %s, exit %d; saved as %s"
          % (live_run.snapshot()["verdict"], rc, run_id), flush=True)
    if opts.linger > 0:
        print("monitor: the dashboard stays up %ds more (Ctrl-C ends it now)" % opts.linger, flush=True)
        wait_for_ctrl_c(opts.linger)
    httpd.shutdown()
    return rc


def finish_run(live_run: live.LiveRun, store: history.HistoryStore, run_id: str,
               meta: Mapping[str, object], rc: int, log: Optional[TextIO]) -> None:
    """Closes a run and saves it with its copied report and leg logs.

    Args:
        live_run: The run.
        store: The history.
        run_id: Its history id.
        meta: Fields saved with it.
        rc: Its exit status.
        log: Its open log, closed here; or None.
    """
    live_run.poll_pr_dir()
    live_run.copy_outputs(store, run_id)
    with live_run.lock:
        live_run.state.end_run(time.time(), rc)
    live_run.finished.set()
    store.save_run(run_id, live_run.full_snapshot(), meta)
    if log is not None:
        log.close()


def follow_log(path: str, live_run: live.LiveRun, store: history.HistoryStore, run_id: str,
               pace: float, save: bool) -> None:
    """Feeds a log into the run, following it until its summary and a quiet spell.

    A log that already holds a finished run is a replay and is never saved.

    Args:
        path: The log.
        live_run: The run.
        store: The history.
        run_id: The history id to save under.
        pace: Seconds between lines already in the log (0 for none).
        save: Whether to save a run that was still going at attach.
    """
    tail = live.FileTail(path)
    first = tail.read_lines()
    for line in first:
        live_run.feed_main(line)
        if pace > 0:
            time.sleep(pace)
    with live_run.lock:
        state = live_run.state
        state.attached_late = bool(first)
        finished_at_attach = state.verdict not in (None, "running")
        if first:
            state.note("attached to a log already %d line(s) long: those lines show the time they were read" % len(first))
        if finished_at_attach:
            state.note("replay of a finished log: times are when each line was read, except bars marked 'record'")
    save = save and not finished_at_attach
    meta: Mapping[str, object] = {"mode": "watch"}
    log = store.open_log(run_id) if save else None
    quiet_since, last_save = time.time(), 0.0
    while True:
        lines = tail.read_lines()
        for line in lines:
            masked = live_run.feed_main(line)
            if log is not None:
                log.write(masked + "\n")
        now = time.time()
        if lines:
            quiet_since = now
        if save and now - last_save > SAVE_SECONDS:
            store.save_run(run_id, live_run.full_snapshot(), meta)
            last_save = now
        with live_run.lock:
            verdict, code = live_run.state.verdict, live_run.state.exit_code
        if verdict not in (None, "running") and now - quiet_since > 3:
            break
        time.sleep(0.5)
    rc = code if code is not None else (0 if verdict == "pass" else 1)
    if save:
        finish_run(live_run, store, run_id, meta, rc, log)
        print("monitor: the run ended; saved as %s" % run_id, flush=True)
    else:
        live_run.poll_pr_dir()
        with live_run.lock:
            live_run.state.end_run(time.time(), rc)
        live_run.finished.set()


def cmd_watch(opts: Options) -> int:
    """Attaches to a run something else started, or replays a finished log.

    Args:
        opts: The command line.

    Returns:
        0 after Ctrl-C, or 2 when the log is missing.
    """
    if opts.log and not os.path.isfile(opts.log):
        print("monitor: no log at %s" % opts.log, file=sys.stderr)
        return 2
    redactor = redact.Redactor.from_environment()
    live_run = live.LiveRun(redactor, os.path.abspath(opts.pr_dir) if opts.pr_dir else PR_DIR)
    store = history.HistoryStore(HISTORY_DIR)
    run_id = store.unique_run_id(time.time(), git_head(REPO_ROOT))
    app, httpd = start_common(opts, "watch", store, redactor, live_run, run_id)
    stop = threading.Event()
    start_thread(lambda: memory_loop(app, live_run, stop), "memory")
    start_thread(lambda: pr_dir_loop(live_run, stop), "pr-dir")
    log = opts.log
    if log:
        start_thread(lambda: follow_log(log, live_run, store, run_id, opts.pace, not opts.no_save), "follow")
    else:
        with live_run.lock:
            live_run.state.records_valid = True
            live_run.state.note("no --log: only .coverage/pr is read, so the static checks and pass progress are not visible")
    wait_for_ctrl_c()
    stop.set()
    httpd.shutdown()
    return 0


def cmd_serve(opts: Options) -> int:
    """Serves history and Actions only.

    Args:
        opts: The command line.

    Returns:
        0 after Ctrl-C.
    """
    store = history.HistoryStore(HISTORY_DIR)
    app, httpd = start_common(opts, "serve", store, redact.Redactor.from_environment())
    stop = threading.Event()
    start_thread(lambda: memory_loop(app, None, stop), "memory")
    wait_for_ctrl_c()
    stop.set()
    httpd.shutdown()
    return 0


def parse_args(argv: Sequence[str]) -> Options:
    """Parses the command line.

    Args:
        argv: The arguments, without the program name.

    Returns:
        The options.
    """
    p = argparse.ArgumentParser(prog="monitor.py", description="Live dashboard of make pr-check and GitHub Actions.")
    sub = p.add_subparsers(dest="command", required=True)
    for name, helptext in (("run", "run make --trace pr-check and watch it"),
                           ("watch", "watch a run something else started, or replay a log"),
                           ("serve", "serve history and Actions only")):
        sp = sub.add_parser(name, help=helptext)
        sp.add_argument("--port", type=int, default=8765, help="port on 127.0.0.1 (default 8765)")
        sp.add_argument("--no-actions", action="store_true", help="do not poll GitHub Actions")
        if name == "run":
            sp.add_argument("--cap", default="4500M", help="memory cap for capped (default 4500M)")
            sp.add_argument("--parallelism", type=int, default=1, help="PR_CHECK_PARALLELISM (default 1)")
            sp.add_argument("--linger", type=int, default=120, help="seconds to keep serving after the run (default 120)")
        if name == "watch":
            sp.add_argument("--log", help="the make output to follow (make --trace pr-check 2>&1 | tee FILE)")
            sp.add_argument("--pr-dir", help="the .coverage/pr directory to read (default the repository's)")
            sp.add_argument("--pace", type=float, default=0.0, help="seconds between lines already in the log")
            sp.add_argument("--no-save", action="store_true", help="do not save the run to history")
    ns = vars(p.parse_args(list(argv)))
    return Options(
        command=str(ns["command"]), port=int(ns["port"]), no_actions=bool(ns["no_actions"]),
        cap=str(ns.get("cap", "4500M")), parallelism=int(ns.get("parallelism", 1)),
        linger=int(ns.get("linger", 120)), log=str(ns["log"]) if ns.get("log") else None,
        pr_dir=str(ns["pr_dir"]) if ns.get("pr_dir") else None, pace=float(ns.get("pace", 0.0)),
        no_save=bool(ns.get("no_save", False)),
    )


def main(argv: Optional[Sequence[str]] = None) -> int:
    """Runs the command line.

    Args:
        argv: The arguments, without the program name; sys.argv by default.

    Returns:
        The exit status.
    """
    opts = parse_args(sys.argv[1:] if argv is None else argv)
    commands = {"run": cmd_run, "watch": cmd_watch, "serve": cmd_serve}
    return commands[opts.command](opts)


if __name__ == "__main__":
    sys.exit(main())
