# pyright: strict
"""Where monitor.py run and watch read and write: the checkout, history and the ledgers.
"""

from __future__ import annotations

import os
from typing import Callable, Optional

import paths


# REPO_ROOT is the checkout this copy sits in (a worktree, perhaps): where pr-check runs.
REPO_ROOT = paths.TREE_ROOT
# HISTORY_DIR is the one directory the monitor writes to, shared by every worktree.
HISTORY_DIR = paths.state_dir()
PR_DIR = os.path.join(REPO_ROOT, ".coverage", "pr")
# BINSIZE_LEDGER is tools/binsize's ledger, which the monitor only reads.
BINSIZE_LEDGER = os.path.join(REPO_ROOT, ".IGNORE", "ci", "binsize.jsonl")
CAPPED = os.path.expanduser("~/.local/bin/capped")
# SAVE_SECONDS is how often a run in progress is saved to history.
SAVE_SECONDS = 10
# MAX_APP_MEMORY bounds the memory samples kept for the page.
MAX_APP_MEMORY = 8640

SampleHook = Callable[[Optional[int], float], None]
