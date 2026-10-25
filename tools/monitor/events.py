# pyright: strict
"""The observer half of the dashboard: a run publishes what changed, and any
number of subscribers wait on it, so the page and a script are told rather
than asking every few seconds.

An event is a dict with a sequence number:

    {"seq": 3, "type": "stage", "name": "win", "status": "fail", "at": ...}
    {"seq": 9, "type": "run", "verdict": "pass", "exit_code": 0, "at": ...}

A stage event is published each time a stage's status changes, and one run
event when the run ends, after which the broker is closed. A subscriber
passes the last seq it saw and is given everything after it, so one that
connects late, or reconnects, misses nothing the broker still holds.
"""

from __future__ import annotations

import threading
import time
from typing import Dict, Iterable, List, Optional

Event = Dict[str, object]

# MAX_EVENTS bounds what the broker keeps; a run has a few hundred at most.
MAX_EVENTS = 5000

# FAILED are the stage statuses a "fail" subscriber is told about.
FAILED = ("fail", "stopped")


class Broker:
    """Holds one run's events and wakes whoever waits on them.

    Attributes:
        cond: Guards the events and wakes waiters.
        events: The events kept, oldest first.
        next_seq: The seq the next event gets.
        closed: Whether the run ended; nothing more is published.
    """

    cond: threading.Condition
    events: List[Event]
    next_seq: int
    closed: bool

    def __init__(self) -> None:
        """Starts with no events."""
        self.cond = threading.Condition()
        self.events = []
        self.next_seq = 1
        self.closed = False

    def publish(self, kind: str, fields: Dict[str, object], now: Optional[float] = None) -> Optional[Event]:
        """Publishes one event and wakes every waiter.

        Args:
            kind: stage or run.
            fields: The event's other fields.
            now: When it happened; the current time by default.

        Returns:
            The event, or None once the broker is closed.
        """
        with self.cond:
            if self.closed:
                return None
            event: Event = {"seq": self.next_seq, "type": kind, "at": now if now is not None else time.time()}
            event.update(fields)
            self.next_seq += 1
            self.events.append(event)
            del self.events[: max(0, len(self.events) - MAX_EVENTS)]
            self.cond.notify_all()
            return event

    def close(self) -> None:
        """Ends the stream: waiters wake, and nothing more is published."""
        with self.cond:
            self.closed = True
            self.cond.notify_all()

    def since(self, seq: int) -> List[Event]:
        """Returns the events after seq.

        Args:
            seq: The last seq the caller saw; 0 for all.

        Returns:
            The events kept whose seq is greater, oldest first.
        """
        with self.cond:
            return [e for e in self.events if int(str(e["seq"])) > seq]

    def wait(self, seq: int, timeout: float) -> List[Event]:
        """Waits for events after seq.

        Args:
            seq: The last seq the caller saw.
            timeout: Seconds to wait at most.

        Returns:
            The events after seq, empty when timeout passed or the broker
            closed with nothing new.
        """
        deadline = time.monotonic() + timeout
        with self.cond:
            while True:
                fresh = [e for e in self.events if int(str(e["seq"])) > seq]
                left = deadline - time.monotonic()
                if fresh or self.closed or left <= 0:
                    return fresh
                self.cond.wait(left)

    def is_closed(self) -> bool:
        """Reports whether the run ended."""
        with self.cond:
            return self.closed


def matches(event: Event, wanted: Iterable[str]) -> bool:
    """Reports whether an event is one a subscriber asked for.

    Args:
        event: The event.
        wanted: fail (a stage that failed or was stopped), end (the run
            ended), stage (any stage change) or any (every event).

    Returns:
        True when the event answers one of wanted.
    """
    for w in wanted:
        if w == "any":
            return True
        if w == "end" and event.get("type") == "run":
            return True
        if w == "stage" and event.get("type") == "stage":
            return True
        if w == "fail" and event.get("type") == "stage" and event.get("status") in FAILED:
            return True
    return False
