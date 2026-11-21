# pyright: strict
"""The source fingerprint a daemon reports on /healthz, and how it is taken.

The list of files is never kept by hand: it is every module this process
imported from the monitor's directory (sys.modules), so a module added
later is covered as soon as anything imports it.
"""

from __future__ import annotations

import hashlib
import os
import re
import sys
from typing import List, Sequence

import paths


# SOURCE_NAME is what a fingerprinted file's name must look like, so a name
# read back from /healthz never reaches outside the monitor's directory.
SOURCE_NAME = re.compile(r"^[A-Za-z0-9_]+\.py$")


def loaded_sources(directory: str = paths.HERE) -> List[str]:
    """Names the files of the monitor this process has loaded.

    Args:
        directory: The monitor's directory.

    Returns:
        The file names of every module imported from it (monitor.py, run as
        __main__, included), sorted.
    """
    names: List[str] = []
    for mod in list(sys.modules.values()):
        path: object = getattr(mod, "__file__", None)
        if isinstance(path, str) and path.endswith(".py") and os.path.dirname(os.path.abspath(path)) == directory:
            names.append(os.path.basename(path))
    return sorted(set(names))


def source_fingerprint(directory: str, names: Sequence[str]) -> str:
    """Digests the named files as they are on disk now.

    Args:
        directory: Where they are.
        names: Their names; any that is not a plain .py name is skipped.

    Returns:
        The SHA-256 of each name and its content, in name order.
    """
    h = hashlib.sha256()
    for name in sorted(n for n in names if SOURCE_NAME.match(n)):
        h.update(name.encode("utf-8") + b"\0")
        try:
            with open(os.path.join(directory, name), "rb") as f:
                h.update(f.read())
        except OSError:
            h.update(b"\0missing")
        h.update(b"\0")
    return h.hexdigest()
