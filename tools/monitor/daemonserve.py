# pyright: strict
"""The documents the daemon serves: server.Backend over DaemonState.

ServedState answers /state, /history, /healthz, /timeline, the roadmap and
security from what DaemonState holds; DaemonApp (daemonapp.py) adds the
timeline's recording on top.
"""

from __future__ import annotations

import os
import time
from typing import Iterator, List, Optional, Sequence, Tuple

import binsize
import events
import memwatch
import paths
import roadmap
import security
from daemonconf import MAX_APP_MEMORY, PROTOCOL, RECENT_SECONDS, SECURITY_SECONDS
from daemonstate import DaemonState
from jsonutil import JSONObject, get_objects, get_str


class ServedState(DaemonState):
    """What the HTTP server reads, in the daemon (server.Backend)."""

    def add_memory(self, t: float, mb: float) -> None:
        """Keeps a memory sample for the page.

        Args:
            t: When it was taken.
            mb: MemAvailable in MB.
        """
        with self.cond:
            self.memory.append((t, mb))
            del self.memory[: max(0, len(self.memory) - MAX_APP_MEMORY)]

    def state(self) -> object:
        """Builds the /state document.

        Returns:
            The latest run as it last wrote itself, Actions and memory.
        """
        view = self.current_view()
        with self.cond:
            memory = self.memory[-1500:]
        acts: JSONObject = self.poller.snapshot() if self.poller is not None else {
            "branch": None, "runs": [], "polled": None, "error": "Actions polling is off (--no-actions)"}
        run = view.snapshot() if view is not None else None
        gate: Optional[JSONObject] = None
        if view is not None:
            gate = {"run_id": view.run_id, "live": not view.broker.is_closed(), "root": view.manifest.get("root"),
                    "branch": view.manifest.get("branch"), "pid": view.pid()}
        return {"mode": "daemon", "now": time.time(), "run_id": view.run_id if view else None, "run": run,
                "gate": gate, "actions": acts, "memory": memory, "memory_floor_mb": memwatch.THRESHOLD_MB}

    def history(self) -> object:
        """Builds the /history document.

        Returns:
            {"runs": every saved run's summary}.
        """
        return {"runs": self.store.list_runs()}

    def history_run(self, run_id: str) -> Optional[object]:
        """Loads one saved run.

        Args:
            run_id: The run id.

        Returns:
            The run, or None.
        """
        return self.store.load_run(run_id)

    def actions_history(self) -> object:
        """Builds the /actions-history document.

        Returns:
            {"runs": every Actions run seen, newest first}.
        """
        runs = sorted(self.store.load_actions().values(), key=lambda r: get_str(r, "created"), reverse=True)
        return {"runs": runs}

    def binsize(self) -> object:
        """Builds the /binsize document from the main clone's ledger.

        Returns:
            The ledger's comparable groups, or why there are none.
        """
        ledger = os.path.join(self.root, ".IGNORE", "ci", "binsize.jsonl")
        doc = binsize.read_ledger(ledger)
        doc["path"] = os.path.relpath(ledger, self.root)
        return doc

    def broker(self) -> Optional[events.Broker]:
        """Returns the latest run's broker, for /events.

        Returns:
            It, or None before any run.
        """
        view = self.current_view()
        return view.broker if view is not None else None

    def select_broker(self, run_id: Optional[str], deadline: float) -> Tuple[Optional[events.Broker], Optional[str]]:
        """Chooses the run a /wait answers from, waiting for one when there is none yet.

        Naming a run waits for that run to be followed. Naming none takes the
        latest run while it is live or ended under RECENT_SECONDS ago, and
        otherwise waits for the next run to start: the gate an agent just
        launched may not have written its manifest when its /wait arrives.

        Args:
            run_id: The run named by ?run=, or None.
            deadline: time.monotonic() to give up at.

        Returns:
            (its broker, its id), or (None, None) when none came in time.
        """
        with self.cond:
            if run_id:
                while run_id not in self.views:
                    left = deadline - time.monotonic()
                    if left <= 0:
                        return None, None
                    self.cond.wait(min(left, 1.0))
                return self.views[run_id].broker, run_id
            seen = self.current
            while True:
                view = self.views.get(self.current) if self.current else None
                if view is not None:
                    if self.current != seen or not view.broker.is_closed():
                        return view.broker, view.run_id
                    if view.ended is not None and time.time() - view.ended < RECENT_SECONDS:
                        return view.broker, view.run_id
                left = deadline - time.monotonic()
                if left <= 0:
                    return None, None
                self.cond.wait(min(left, 1.0))

    def waits_for_runs(self) -> bool:
        """Reports that a /wait with no run waits for the next one.

        Returns:
            True.
        """
        return True

    def health(self) -> object:
        """Builds the /healthz document.

        Returns:
            Who this daemon is.
        """
        view = self.current_view()
        return {"ok": True, "protocol": PROTOCOL, "pid": os.getpid(), "port": self.port, "started": self.started,
                "source": paths.HERE, "state_dir": self.state_dir, "fingerprint": self.fingerprint,
                "source_files": list(self.source_files),
                "run_id": view.run_id if view is not None else None,
                "gate_live": view is not None and not view.broker.is_closed(),
                "roadmap": self.panel.configured()}

    def timeline_lines(self, since: float, kinds: Optional[List[str]], src: Optional[str]) -> Iterator[JSONObject]:
        """Reads the timeline.

        Args:
            since: Only lines after this epoch time.
            kinds: Only these kinds, or all.
            src: Only this src, or all.

        Returns:
            The lines, oldest first.
        """
        return iter(self.reader.read(since, kinds, src))

    def roadmap(self, phases: Sequence[str], details: bool) -> object:
        """Returns the roadmap model.

        Args:
            phases: Only these phases, or all.
            details: Whether items keep their details.

        Returns:
            The model, or {"error": why}.
        """
        return self.panel.model(phases or None, details)

    def security(self) -> JSONObject:
        """Builds the /security.json document, at most every SECURITY_SECONDS.

        Returns:
            The attestation, scanner exceptions and everything pending.
        """
        with self.cond:
            cached = self.security_cache
        if cached is not None and time.time() - cached[0] < SECURITY_SECONDS:
            return cached[1]
        model = self.panel.model()
        runs = list(self.store.load_actions().values())
        if self.poller is not None:
            runs += get_objects(self.poller.snapshot(), "runs")
        doc = security.build(self.root, model if roadmap.usable(model) else None, runs)
        with self.cond:
            self.security_cache = (time.time(), doc)
        return doc
