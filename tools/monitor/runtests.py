#!/usr/bin/env python3
# pyright: strict
"""Runs the monitor's unit tests, and fails when a run leaves a daemon behind.

    python3 -B tools/monitor/runtests.py        (make monitor-test)

Where this platform has fcntl, the suite runs twice: as it is, and with
fcntl hidden from every Python process the run starts, through a
sitecustomize on PYTHONPATH, as on Windows. Where it has none (Windows),
it runs once. Each run's child gets MONITOR_TEST_FCNTL (present, hidden or
absent), and a test fails when locks.SUPPORTED disagrees with it, so a
hide that did not take fails the run. A run passes only when unittest
exits 0, its last line is OK, and, where fcntl is present, no test skipped
for lacking it. Every skipped test is printed with its reason.

Each run gets a temporary directory of its own as TMPDIR, so every state
directory a test creates is under it. However the run ends (a pass, a
failure, RUN_SECONDS, Ctrl-C or SIGTERM), any status daemon whose command
line names that directory is stopped, and the run fails, naming it. The
runs are not retried, unlike the Go tests under tools/testgate.

The grammar and run-time constructs hold Python 3.8 (OldestPythonTest);
it has been run on 3.10 only here, and CI's interpreters are the first
others. Standard library only; no virtual environment.
"""

from __future__ import annotations

import importlib.util
import json
import os
import re
import shutil
import signal
import subprocess
import sys
import tempfile
import threading
import time
from types import FrameType
from typing import Dict, List, Optional, Tuple

from jsonutil import as_object, get_list, loads

HERE = os.path.dirname(os.path.abspath(__file__))
# RUN_SECONDS bounds one run of the suite; it takes about ten seconds alone.
RUN_SECONDS = 600
# HIDE_FCNTL is the sitecustomize that makes fcntl unimportable.
HIDE_FCNTL = "import sys\nsys.modules['fcntl'] = None  # as on Windows: the status daemon is not supported\n"
# MARKER tells the suite whether fcntl should be importable in it.
MARKER = "MONITOR_TEST_FCNTL"
# FCNTL_SKIP starts the reason of every test skipped for lacking flock.
FCNTL_SKIP = "needs fcntl flock"
# OK_LINE is unittest's last line on a pass.
OK_LINE = re.compile(r"^OK(?: \(.*\))?$")
# POSIX says whether os.kill(pid, 0) only asks; on Windows it ends the process.
POSIX = os.name == "posix"


class Interrupted(Exception):
    """Raised by the SIGTERM handler, so a run's cleanup still happens."""


def plan(fcntl_here: bool) -> List[Tuple[str, str]]:
    """Names the runs to make.

    Args:
        fcntl_here: Whether this platform has fcntl.

    Returns:
        (label, marker) for each run: two where fcntl exists, one where it does not.
    """
    if fcntl_here:
        return [("with fcntl", "present"), ("without fcntl, as on Windows", "hidden")]
    return [("without fcntl (this platform has none)", "absent")]


def problems(code: Optional[int], lines: List[str], skips: List[Tuple[str, str]], marker: str) -> List[str]:
    """Judges one run.

    Args:
        code: The child's exit status, or None when it was killed.
        lines: Its output.
        skips: Each skipped test and its reason.
        marker: The run's MONITOR_TEST_FCNTL.

    Returns:
        Why the run failed; empty when it passed.
    """
    out: List[str] = []
    if code != 0:
        out.append("the suite exited with status %s" % code)
    last = next((x.strip() for x in reversed(lines) if x.strip()), "")
    if not OK_LINE.match(last):
        out.append("unittest's last line is %r, not OK" % last)
    lacking = [t for t, why in skips if why.startswith(FCNTL_SKIP)]
    if marker == "present" and lacking:
        out.append("%d test(s) skipped for lacking fcntl where fcntl is present: %s" % (len(lacking), ", ".join(lacking)))
    return out


def leftovers(tmp: str) -> List[int]:
    """Finds the status daemons whose command line names a directory under tmp.

    Args:
        tmp: The run's temporary directory.

    Returns:
        Their pids; none where ps is missing (Windows, where no daemon runs).
    """
    try:
        out = subprocess.run(["ps", "-eo", "pid=,args="], capture_output=True, text=True, timeout=10)
    except (OSError, subprocess.TimeoutExpired):
        return []
    pids: List[int] = []
    for line in out.stdout.splitlines():
        pid, _, args = line.strip().partition(" ")
        if pid.isdigit() and "monitor.py" in args and " daemon " in args + " " and tmp in args:
            pids.append(int(pid))
    return pids


def alive(pid: int) -> bool:
    """Reports whether a pid still runs.

    Args:
        pid: The process.

    Returns:
        True while it does. Off POSIX it is always True, since os.kill(pid, 0)
        would end the process there; leftovers finds none there anyway.
    """
    if not POSIX:
        return True
    try:
        os.kill(pid, 0)
    except ProcessLookupError:
        return False
    except OSError:
        return True
    return True


def stop(pids: List[int]) -> None:
    """Stops left-behind daemons: SIGTERM, then SIGKILL after five seconds.

    Args:
        pids: Daemons leftovers found, each proven by its command line.
    """
    for pid in pids:
        try:
            os.kill(pid, signal.SIGTERM)
        except OSError:
            pass
    deadline = time.monotonic() + 5
    while time.monotonic() < deadline and any(alive(p) for p in pids):
        time.sleep(0.1)
    for pid in pids:
        if alive(pid):
            try:
                os.kill(pid, getattr(signal, "SIGKILL", signal.SIGTERM))
            except OSError:
                pass


def read_skips(report: str) -> List[Tuple[str, str]]:
    """Reads the skips a child wrote.

    Args:
        report: The child's report file.

    Returns:
        (test id, reason) for each skip; none when the child wrote no report.
    """
    try:
        with open(report, encoding="utf-8") as f:
            doc = as_object(loads(f.read())) or {}
    except (OSError, ValueError):
        return []
    out: List[Tuple[str, str]] = []
    for row in get_list(doc, "skipped"):
        if isinstance(row, list) and len(row) == 2:
            out.append((str(row[0]), str(row[1])))
    return out


def run_suite(label: str, marker: str) -> int:
    """Runs the suite once in a temporary directory of its own, then looks for leftovers.

    Args:
        label: What the run is called in its lines.
        marker: present, hidden (fcntl hidden here) or absent.

    Returns:
        0 when the run passed and left nothing running, else 1.
    """
    tmp = tempfile.mkdtemp(prefix="monitor-test-")
    report = os.path.join(tmp, "report.json")
    env: Dict[str, str] = dict(os.environ, TMPDIR=tmp, TEMP=tmp, TMP=tmp, **{MARKER: marker})
    if marker == "hidden":
        site = os.path.join(tmp, "site")
        os.mkdir(site)
        with open(os.path.join(site, "sitecustomize.py"), "w", encoding="utf-8") as f:
            f.write(HIDE_FCNTL)
        env["PYTHONPATH"] = os.pathsep.join([site] + ([env["PYTHONPATH"]] if env.get("PYTHONPATH") else []))
    cmd = [sys.executable, "-B", os.path.abspath(__file__), "--child", report]
    print("monitor-test: %s (%s=%s)" % (label, MARKER, marker), flush=True)
    lines: List[str] = []
    code: Optional[int] = None
    proc = subprocess.Popen(cmd, cwd=HERE, env=env, stdout=subprocess.PIPE, stderr=subprocess.STDOUT, text=True)
    stdout = proc.stdout
    assert stdout is not None  # stdout=PIPE always gives one

    def pump() -> None:
        """Copies the child's output through, keeping it for the verdict."""
        for line in stdout:
            lines.append(line.rstrip("\n"))
            print(line, end="", flush=True)

    reader = threading.Thread(target=pump, daemon=True)
    reader.start()
    found: List[int] = []
    try:
        code = proc.wait(timeout=RUN_SECONDS)
    except subprocess.TimeoutExpired:
        print("monitor-test: %s: the suite ran past %d seconds and was killed" % (label, RUN_SECONDS), flush=True)
    finally:
        # Every exit path, an interrupt included, ends the child and looks for what the run left.
        if proc.poll() is None:
            proc.kill()
        proc.wait()
        reader.join(10)
        found = leftovers(tmp)
        deadline = time.monotonic() + 5  # a daemon whose owner just died stops within a second or two
        while found and time.monotonic() < deadline:
            time.sleep(0.2)
            found = leftovers(tmp)
        if found:
            print("monitor-test: %s: the suite left status daemon(s) running: pid %s; stopping them"
                  % (label, ", ".join(map(str, found))), flush=True)
            stop(found)
        skips = read_skips(report)
        shutil.rmtree(tmp, ignore_errors=True)
    for test, why in skips:
        print("monitor-test: %s: skipped %s: %s" % (label, test, why), flush=True)
    bad = problems(code, lines, skips, marker) + (["it left a daemon running"] if found else [])
    for why in bad:
        print("monitor-test: %s: FAILED: %s" % (label, why), flush=True)
    return 1 if bad else 0


def child(report: str) -> int:
    """Runs the suite in this process and writes its skips to report.

    Args:
        report: Where to write {"skipped": [[test id, reason], ...]}.

    Returns:
        0 when every test passed or skipped.
    """
    import unittest
    suite = unittest.defaultTestLoader.discover(HERE, "test_*.py", HERE)
    result = unittest.TextTestRunner(verbosity=1).run(suite)
    with open(report, "w", encoding="utf-8") as f:
        json.dump({"skipped": [[t.id(), why] for t, why in result.skipped]}, f)
    return 0 if result.wasSuccessful() else 1


def main(argv: List[str]) -> int:
    """Runs every planned run, or one suite as a child.

    Args:
        argv: The arguments, without the program name.

    Returns:
        0 when every run passed and left nothing behind.
    """
    if argv[:1] == ["--child"] and len(argv) == 2:
        return child(argv[1])

    def on_term(signum: int, frame: Optional[FrameType]) -> None:
        """Turns SIGTERM into an exception, so the current run's cleanup runs.

        Args:
            signum: The signal.
            frame: Unused.
        """
        raise Interrupted()

    signal.signal(signal.SIGTERM, on_term)
    status = 0
    try:
        for label, marker in plan(importlib.util.find_spec("fcntl") is not None):
            status |= run_suite(label, marker)
    except (Interrupted, KeyboardInterrupt):
        print("\nmonitor-test: interrupted; the run in progress was ended and its daemons looked for", flush=True)
        return 1
    print("monitor-test: %s" % ("all tests passed" if status == 0 else "FAILED"), flush=True)
    return status


if __name__ == "__main__":
    sys.exit(main(sys.argv[1:]))
