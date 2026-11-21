#!/usr/bin/env python3
# pyright: strict
"""Runs the monitor's unit tests twice, and fails when a run leaves a daemon behind.

    python3 -B tools/monitor/runtests.py        (make monitor-test)

The first run is the ordinary one. The second hides fcntl from every Python
process it starts, through a sitecustomize on PYTHONPATH, as on Windows:
the tests of the daemon itself skip, saying why, and the rest must pass.
Each run gets a temporary directory of its own as TMPDIR, so every state
directory a test creates is under it. When the run ends, by passing,
failing, timing out or being interrupted, any status daemon whose command
line names that directory was left behind: it is stopped and the run fails,
naming it.

Python 3.8 or newer, standard library only; no virtual environment.
"""

from __future__ import annotations

import os
import shutil
import signal
import subprocess
import sys
import tempfile
import time
from typing import Dict, List, Optional

HERE = os.path.dirname(os.path.abspath(__file__))
# RUN_SECONDS bounds one run of the suite; it takes about ten seconds alone.
RUN_SECONDS = 600
# HIDE_FCNTL is the sitecustomize that makes fcntl unimportable.
HIDE_FCNTL = "import sys\nsys.modules['fcntl'] = None  # as on Windows: the status daemon is not supported\n"


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


def alive(pid: int) -> bool:
    """Reports whether a pid still runs.

    Args:
        pid: The process.

    Returns:
        True while it does.
    """
    try:
        os.kill(pid, 0)
    except ProcessLookupError:
        return False
    except OSError:
        return True
    return True


def run_suite(label: str, hide_fcntl: bool) -> int:
    """Runs the suite once in a temporary directory of its own, then looks for leftovers.

    Args:
        label: What the run is called in its lines.
        hide_fcntl: Whether fcntl is made unimportable for every Python it starts.

    Returns:
        0 when the suite passed and left nothing running, else 1.
    """
    tmp = tempfile.mkdtemp(prefix="monitor-test-")
    env: Dict[str, str] = dict(os.environ, TMPDIR=tmp, TEMP=tmp, TMP=tmp)
    if hide_fcntl:
        site = os.path.join(tmp, "site")
        os.mkdir(site)
        with open(os.path.join(site, "sitecustomize.py"), "w", encoding="utf-8") as f:
            f.write(HIDE_FCNTL)
        env["PYTHONPATH"] = os.pathsep.join([site] + ([env["PYTHONPATH"]] if env.get("PYTHONPATH") else []))
    cmd = [sys.executable, "-B", "-m", "unittest", "discover", "-s", HERE, "-t", HERE, "-p", "test_*.py"]
    print("monitor-test: %s: %s" % (label, " ".join(cmd[1:])), flush=True)
    code: Optional[int] = None
    try:
        code = subprocess.run(cmd, cwd=HERE, env=env, timeout=RUN_SECONDS).returncode
    except subprocess.TimeoutExpired:
        print("monitor-test: %s: the suite ran past %d seconds and was killed" % (label, RUN_SECONDS), flush=True)
    finally:
        # Every exit path, an interrupt included, looks for what the run left.
        found = leftovers(tmp)
        deadline = time.monotonic() + 5  # a daemon whose owner just died stops within a second or two
        while found and time.monotonic() < deadline:
            time.sleep(0.2)
            found = leftovers(tmp)
        if found:
            print("monitor-test: %s: the suite left status daemon(s) running: pid %s; stopping them"
                  % (label, ", ".join(map(str, found))), flush=True)
            stop(found)
            code = 1
        shutil.rmtree(tmp, ignore_errors=True)
    return 0 if code == 0 else 1


def main() -> int:
    """Runs both passes.

    Returns:
        0 when both passed and left nothing behind.
    """
    status = run_suite("with fcntl", hide_fcntl=False)
    status |= run_suite("without fcntl, as on Windows", hide_fcntl=True)
    print("monitor-test: %s" % ("all tests passed" if status == 0 else "FAILED"), flush=True)
    return status


if __name__ == "__main__":
    sys.exit(main())
