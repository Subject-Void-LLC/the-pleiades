#!/usr/bin/env python3
# pyright: strict
"""A live dashboard of make pr-check, every stage, GitHub Actions and the roadmap.

    monitor.py ensure [--port 8765] [--quiet]
    monitor.py daemon [--port 8765] [--no-actions]
    monitor.py run    [--cap 4500M] [--parallelism 1] [--port 8765]
    monitor.py watch  [--log FILE] [--pr-dir DIR] [--pace SECONDS] [--no-save] [--port 8767]

The page is one always-on daemon per machine (daemon.py): ensure starts it
when it is not running, and is what the session hook calls. run starts
`make --trace pr-check` itself (under ~/.local/bin/capped, in its own process
group), refuses while another gate runs on this machine, stops the run if
MemAvailable drops under 700 MB, saves the run to history, hands it to the
daemon through files (runfile.py) and exits with pr-check's status. watch
follows a log something else is writing (or replays a finished one) on a
server of its own. serve is the old name of daemon. Every file is under the
main clone's .IGNORE/ci/monitor/ (paths.py).

Python 3 standard library only. Nothing in the Makefile or CI calls this.
"""

from __future__ import annotations

import sys

# Importing the sibling modules must not write __pycache__ into the tree.
sys.dont_write_bytecode = True

import os  # noqa: E402
from typing import Optional, Sequence  # noqa: E402

import daemon  # noqa: E402
# Each command lives in a module of its own; the names a caller reads as
# monitor.<name> are imported here and listed in __all__.
from gaterun import check_run_options, cmd_run  # noqa: E402
from monitorcli import Options, parse_args  # noqa: E402
from watchapp import App  # noqa: E402
from watchlog import cmd_watch  # noqa: E402

__all__ = ["App", "Options", "check_run_options", "cmd_daemon", "cmd_ensure", "cmd_run", "cmd_watch", "main",
           "parse_args"]

HERE = os.path.dirname(os.path.abspath(__file__))


def cmd_daemon(opts: Options) -> int:
    """Runs the status daemon in the foreground (serve is its old name).

    Args:
        opts: The command line.

    Returns:
        0 after SIGTERM or Ctrl-C, or when one already runs.
    """
    return daemon.run_daemon(opts.port, opts.no_actions, opts.state_dir)


def cmd_ensure(opts: Options) -> int:
    """Starts the status daemon unless one of this protocol or newer runs.

    Args:
        opts: The command line.

    Returns:
        0 when one runs, 1 when it could not be started.
    """
    return daemon.ensure(opts.port, opts.quiet, opts.state_dir, no_actions=opts.no_actions)


def main(argv: Optional[Sequence[str]] = None) -> int:
    """Runs the command line.

    Args:
        argv: The arguments, without the program name; sys.argv by default.

    Returns:
        The exit status.
    """
    opts = parse_args(sys.argv[1:] if argv is None else argv)
    commands = {"run": cmd_run, "watch": cmd_watch, "serve": cmd_daemon, "daemon": cmd_daemon,
                "ensure": cmd_ensure}
    return commands[opts.command](opts)


if __name__ == "__main__":
    sys.exit(main())
