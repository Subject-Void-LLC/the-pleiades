# pyright: strict
"""The backend a run or watch server reads: one live run, history and Actions.

The daemon has its own (daemonapp.py); this one serves a single run and
keeps no timeline, roadmap or security model.
"""

from __future__ import annotations

import os
import threading
import time
from typing import Iterator, List, Mapping, Optional, Sequence, Tuple, TypedDict

import actions
import binsize
import events
import history
import live
import memwatch
import prstate as ps
from jsonutil import JSONObject, get_str
from monitorconf import BINSIZE_LEDGER, MAX_APP_MEMORY, REPO_ROOT


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

    def broker(self) -> Optional[events.Broker]:
        """Returns the live run's event broker, or None with no live run.

        Returns:
            What /events and /wait subscribe to.
        """
        return self.live_run.broker if self.live_run is not None else None

    def select_broker(self, run_id: Optional[str], deadline: float) -> Tuple[Optional[events.Broker], Optional[str]]:
        """Returns the one run this server watches, whatever run_id says.

        Args:
            run_id: Unused: watch serves one run.
            deadline: Unused.

        Returns:
            (its broker, its id), or (None, None).
        """
        b = self.broker()
        return (b, self.run_id) if b is not None else (None, None)

    def waits_for_runs(self) -> bool:
        """Reports that this server watches one run and waits for no other.

        Returns:
            False.
        """
        return False

    def health(self) -> object:
        """Builds the /healthz document.

        Returns:
            That this is a watch server, not the daemon.
        """
        return {"ok": True, "protocol": 0, "mode": self.mode, "pid": os.getpid(), "run_id": self.run_id}

    def timeline_lines(self, since: float, kinds: Optional[List[str]], src: Optional[str]) -> Iterator[JSONObject]:
        """Returns no timeline: only the daemon keeps one.

        Args:
            since: Unused.
            kinds: Unused.
            src: Unused.

        Returns:
            An empty iterator.
        """
        return iter([])

    def roadmap(self, phases: Sequence[str], details: bool) -> object:
        """Returns no roadmap: only the daemon serves one.

        Args:
            phases: Unused.
            details: Unused.

        Returns:
            Why there is none.
        """
        return {"error": "the roadmap is served by the daemon (monitor.py ensure), not by watch"}

    def security(self) -> object:
        """Returns no security model: only the daemon serves one.

        Returns:
            Why there is none.
        """
        return {"error": "the security tab is served by the daemon (monitor.py ensure), not by watch"}

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
