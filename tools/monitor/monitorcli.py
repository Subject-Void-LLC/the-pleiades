# pyright: strict
"""monitor.py's command line: its options, typed, and the parser that fills them.
"""

from __future__ import annotations

import argparse
from dataclasses import dataclass
from typing import Optional, Sequence

import daemon


@dataclass
class Options:
    """The command line, typed.

    Attributes:
        command: ensure, daemon, serve, run or watch.
        port: The port on 127.0.0.1.
        no_actions: Whether to skip polling GitHub Actions.
        cap: run: the memory cap handed to capped.
        parallelism: run: PR_CHECK_PARALLELISM.
        linger: run: ignored, kept so old command lines still parse.
        log: watch: the log to follow.
        pr_dir: watch: the .coverage/pr directory to read.
        pace: watch: seconds between lines already in the log.
        no_save: watch: whether to skip saving the run.
        quiet: ensure: print only problems.
        state_dir: ensure, daemon: the state directory, for tests.
    """

    command: str
    port: int = 8765
    no_actions: bool = False
    cap: str = "4500M"
    parallelism: int = 1
    linger: int = 0
    log: Optional[str] = None
    pr_dir: Optional[str] = None
    pace: float = 0.0
    no_save: bool = False
    quiet: bool = False
    state_dir: Optional[str] = None


def parse_args(argv: Sequence[str]) -> Options:
    """Parses the command line.

    Args:
        argv: The arguments, without the program name.

    Returns:
        The options.
    """
    p = argparse.ArgumentParser(prog="monitor.py", description="Live status page: pr-check, GitHub Actions, roadmap.")
    sub = p.add_subparsers(dest="command", required=True)
    for name, helptext in (("ensure", "start the status daemon unless it runs (an opt-in session hook may call this)"),
                           ("daemon", "run the status daemon in the foreground"),
                           ("serve", "the old name of daemon"),
                           ("run", "run make --trace pr-check and hand it to the daemon"),
                           ("watch", "watch a run something else started, or replay a log, on its own server")):
        sp = sub.add_parser(name, help=helptext)
        default_port = 8767 if name == "watch" else daemon.DEFAULT_PORT
        sp.add_argument("--port", type=int, default=default_port,
                        help="port on 127.0.0.1 (default %d)" % default_port)
        sp.add_argument("--no-actions", action="store_true", help="do not poll GitHub Actions")
        if name in ("ensure", "daemon", "serve"):
            sp.add_argument("--state-dir", help="the state directory (default the main clone's .IGNORE/ci/monitor; for tests)")
        if name == "ensure":
            sp.add_argument("--quiet", action="store_true", help="print only problems")
        if name == "run":
            sp.add_argument("--cap", default="4500M", help="memory cap for capped (default 4500M)")
            sp.add_argument("--parallelism", type=int, default=1, help="PR_CHECK_PARALLELISM (default 1)")
            sp.add_argument("--linger", type=int, default=0,
                            help="ignored: the daemon keeps the page up after the run")
        if name == "watch":
            sp.add_argument("--log", help="the make output to follow (make --trace pr-check 2>&1 | tee FILE)")
            sp.add_argument("--pr-dir", help="the .coverage/pr directory to read (default the repository's)")
            sp.add_argument("--pace", type=float, default=0.0, help="seconds between lines already in the log")
            sp.add_argument("--no-save", action="store_true", help="do not save the run to history")
    ns = vars(p.parse_args(list(argv)))
    return Options(
        command=str(ns["command"]), port=int(ns["port"]), no_actions=bool(ns["no_actions"]),
        cap=str(ns.get("cap", "4500M")), parallelism=int(ns.get("parallelism", 1)),
        linger=int(ns.get("linger", 0)), log=str(ns["log"]) if ns.get("log") else None,
        pr_dir=str(ns["pr_dir"]) if ns.get("pr_dir") else None, pace=float(ns.get("pace", 0.0)),
        no_save=bool(ns.get("no_save", False)), quiet=bool(ns.get("quiet", False)),
        state_dir=str(ns["state_dir"]) if ns.get("state_dir") else None,
    )
