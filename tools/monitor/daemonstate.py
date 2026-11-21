# pyright: strict
"""The daemon's state: what it holds, and the gate runs it follows through their files.

DaemonState is the base of DaemonApp (daemonapp.py): daemonserve.py adds the
documents the HTTP server reads from it, and daemonapp.py what is written to
the timeline.
"""

from __future__ import annotations

import os
import threading
import time
from typing import Callable, Dict, List, Optional, Tuple

import actions
import history
import paths
import prstate as ps
import redact
import roadmap
import runfile
import timeline
from daemonconf import KEEP_VIEWS
from fingerprint import loaded_sources, source_fingerprint
from jsonutil import JSONObject, get_str


class DaemonState:
    """What the daemon holds: its stores, the runs it follows and its own identity.

    Attributes:
        state_dir: The shared state directory.
        root: The main clone.
        port: The port served.
        store: The run history.
        timeline: The JSONL history.
        reader: Reads it for /timeline, parsing only what was appended.
        panel: The roadmap panel.
        poller: The Actions poller, or None.
        cond: Guards views, current and memory, and wakes /wait callers when a run appears.
        views: Each followed run by id.
        current: The latest run's id, or None.
        memory: Recent MemAvailable samples.
        started: When the daemon started.
        alive: Whether a run's process (by pid) still runs; a stand-in in tests.
        security_cache: The last /security.json and when it was built.
        git_seen: The main line's commit when its history was last recorded.
        git_catalog: The module catalog's counts already read, by commit.
        source_files: The monitor files this process loaded.
        fingerprint: Their digest as loaded, which /healthz reports.
    """

    state_dir: str
    root: str
    port: int
    store: history.HistoryStore
    timeline: timeline.Timeline
    reader: timeline.Reader
    panel: roadmap.RoadmapPanel
    poller: Optional[actions.ActionsPoller]
    cond: threading.Condition
    views: Dict[str, runfile.RunView]
    current: Optional[str]
    memory: List[ps.MemorySample]
    started: float
    alive: Callable[[int], bool]
    security_cache: Optional[Tuple[float, JSONObject]]
    git_seen: str
    git_catalog: Dict[str, Optional[Tuple[int, int]]]
    source_files: List[str]
    fingerprint: str

    def __init__(self, state_dir: str, root: str, port: int, redactor: redact.Redactor,
                 poller: Optional[actions.ActionsPoller] = None) -> None:
        """Builds the daemon's view.

        Args:
            state_dir: The shared state directory.
            root: The main clone.
            port: The port served.
            redactor: Masks what is written to the timeline.
            poller: The Actions poller, or None.
        """
        self.state_dir = state_dir
        self.root = root
        self.port = port
        self.store = history.HistoryStore(state_dir)
        self.timeline = timeline.Timeline(paths.timeline_dir(state_dir), redactor)
        self.reader = timeline.Reader(paths.timeline_dir(state_dir))
        self.panel = roadmap.RoadmapPanel(paths.panels(state_dir))
        self.poller = poller
        self.cond = threading.Condition()
        self.git_seen = ""
        self.git_catalog = {}
        self.views = {}
        self.current = None
        self.memory = []
        self.started = time.time()
        self.alive = runfile.process_alive
        self.security_cache = None
        # Every module is imported by now, so this is the code this process runs.
        self.source_files = loaded_sources()
        self.fingerprint = source_fingerprint(paths.HERE, self.source_files)


    def follow_manifest(self) -> Optional[runfile.RunView]:
        """Reads the manifest and follows a run it names that is new.

        Returns:
            The newly followed run, or None.
        """
        doc = runfile.read_manifest(self.state_dir)
        if doc is None:
            return None
        run_id = get_str(doc, "run_id")
        with self.cond:
            if run_id in self.views:
                return None
            view = runfile.RunView(self.store, doc)
            self.views[run_id] = view
            self.current = run_id
            ended = [v for v in self.views.values() if v.ended is not None]
            for old in sorted(ended, key=lambda v: v.attached)[: max(0, len(self.views) - KEEP_VIEWS)]:
                del self.views[old.run_id]
            self.cond.notify_all()
        self.timeline.append("daemon", "machine", {"event": "attach", "run_id": run_id,
                                                   "root": doc.get("root"), "branch": doc.get("branch"),
                                                   "commit": doc.get("commit")})
        return view

    def pump(self) -> None:
        """Reads every open run's new events and writes them to the timeline."""
        with self.cond:
            open_views = [v for v in self.views.values() if not v.broker.is_closed()]
        for view in open_views:
            for e in view.pump(self.alive(view.pid())):
                self.timeline.record_gate_event(view.run_id, e)
            if view.broker.is_closed():
                with self.cond:
                    self.cond.notify_all()

    def current_view(self) -> Optional[runfile.RunView]:
        """Returns the latest run followed.

        Returns:
            It, or None before any.
        """
        with self.cond:
            return self.views.get(self.current) if self.current else None

    def current_root(self) -> str:
        """Names the checkout the latest gate ran in, for the Actions poller.

        Returns:
            Its root, or the main clone.
        """
        view = self.current_view()
        root = get_str(view.manifest, "root") if view is not None else ""
        return root if root and os.path.isdir(root) else self.root
