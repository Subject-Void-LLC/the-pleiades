# pyright: strict
"""The MCP server's tools, and the client each one reads the daemon through.

Every tool only reads: none starts, stops or signals a gate.
"""

from __future__ import annotations

import urllib.error
import urllib.parse
import urllib.request
from typing import Callable, Dict, List, Optional, Tuple

import daemon
from jsonutil import JSONObject, JSONValue, as_object, get_int, get_list, get_objects, get_str, loads
from mcpanswers import UNTRUSTED, actions_summary, cut, full_actions, roadmap_summary, stage_summary, timeline_line


# WAIT_MAX bounds gate_wait, so one call never outlives a client's patience by hours.
WAIT_MAX = 3600


class DaemonClient:
    """Reads the daemon over loopback HTTP, starting it once when it is down.

    Attributes:
        port: The daemon's port.
        state_dir: The state directory ensure uses, or None for the main clone's.
        tried_ensure: Whether ensure was already tried this session.
    """

    port: int
    state_dir: Optional[str]
    tried_ensure: bool

    def __init__(self, port: int, state_dir: Optional[str] = None) -> None:
        """Prepares a client.

        Args:
            port: The daemon's port.
            state_dir: The state directory ensure uses, or None for the main clone's.
        """
        self.port = port
        self.state_dir = state_dir
        self.tried_ensure = False

    def get(self, path: str, timeout: float = 15.0) -> Tuple[int, bytes]:
        """GETs a path, starting the daemon once if nothing answers.

        Args:
            path: The path and query.
            timeout: Seconds to wait for the answer.

        Returns:
            (status, body).

        Raises:
            OSError: The daemon cannot be reached even after ensure.
        """
        url = "http://127.0.0.1:%d%s" % (self.port, path)
        for attempt in range(2):
            try:
                req = urllib.request.Request(url, headers={"Host": "127.0.0.1:%d" % self.port})
                with urllib.request.urlopen(req, timeout=timeout) as resp:  # noqa: S310 (loopback only)
                    body: bytes = resp.read()
                    return int(resp.status), body
            except urllib.error.HTTPError as exc:
                body = exc.read()
                return int(exc.code), body
            except OSError:
                if attempt == 0 and not self.tried_ensure:
                    self.tried_ensure = True
                    daemon.ensure(self.port, quiet=True, state_dir=self.state_dir)
                    continue
                raise
        raise OSError("unreachable")

    def json(self, path: str, timeout: float = 15.0) -> JSONValue:
        """GETs a path and decodes its JSON.

        Args:
            path: The path and query.
            timeout: Seconds to wait.

        Returns:
            The decoded value (an error object stays an object).
        """
        _, body = self.get(path, timeout)
        return loads(body.decode("utf-8", errors="replace"))

    def jsonl(self, path: str) -> List[JSONValue]:
        """GETs a JSONL path.

        Args:
            path: The path and query.

        Returns:
            Each line decoded.
        """
        _, body = self.get(path)
        out: List[JSONValue] = []
        for line in body.decode("utf-8", errors="replace").splitlines():
            try:
                out.append(loads(line))
            except ValueError:
                continue
        return out


Tool = Callable[[DaemonClient, JSONObject], JSONValue]


def t_status(c: DaemonClient, args: JSONObject) -> JSONValue:
    """The one-call answer."""
    state = as_object(c.json("/state")) or {}
    health = as_object(c.json("/healthz")) or {}
    rm = as_object(c.json("/roadmap.json", timeout=90))
    memory = get_list(state, "memory")
    last = memory[-1] if memory else None
    return {
        "daemon": {k: health.get(k) for k in ("protocol", "pid", "started", "source", "state_dir")},
        "gate": dict(stage_summary(as_object(state.get("run"))), **(as_object(state.get("gate")) or {})),
        "actions": actions_summary(as_object(state.get("actions"))),
        "memory": {"available_mb": last[1] if isinstance(last, list) and len(last) == 2 else None,
                   "floor_mb": state.get("memory_floor_mb")},
        "roadmap": roadmap_summary(rm),
        "security_pending": (as_object(c.json("/security.json", timeout=90)) or {}).get("pending_total"),
        "page": "http://127.0.0.1:%d/" % c.port,
    }


def t_gate(c: DaemonClient, args: JSONObject) -> JSONValue:
    """The latest or a named run, every stage."""
    run_id = get_str(args, "run_id")
    if run_id:
        doc = as_object(c.json("/history/" + urllib.parse.quote(run_id, safe="")))
        run = doc if doc is not None and "error" not in doc else None
    else:
        state = as_object(c.json("/state")) or {}
        run = as_object(state.get("run"))
        run_id = get_str(state, "run_id")
    if run is None:
        return {"run_id": run_id or None, "run": None}
    stages: List[JSONValue] = []
    for s in get_objects(run, "stages"):
        stage: JSONObject = {k: s.get(k) for k in ("name", "group", "status", "start", "end", "elapsed", "done",
                                                   "total", "failed")}
        stage["untrusted"] = {"detail": cut(get_str(s, "detail"))}
        stages.append(stage)
    out = stage_summary(run)
    out["run_id"] = run_id
    out["stages"] = stages
    out["totals"] = run.get("totals")
    return out


def t_gate_wait(c: DaemonClient, args: JSONObject) -> JSONValue:
    """Blocks until the gate's first failure or end."""
    wanted = get_str(args, "for") or "fail,end"
    timeout = max(1, min(WAIT_MAX, get_int(args, "timeout") or 600))
    q: Dict[str, str] = {"for": wanted, "timeout": str(timeout)}
    for key in ("run_id", "since"):
        v = args.get(key)
        if v not in (None, ""):
            q["run" if key == "run_id" else key] = str(v)
    return c.json("/wait?" + urllib.parse.urlencode(q), timeout=timeout + 30)


def t_actions(c: DaemonClient, args: JSONObject) -> JSONValue:
    """The Actions poll, every job of the newest runs."""
    state = as_object(c.json("/state")) or {}
    acts = as_object(state.get("actions")) or {}
    titles = bool(args.get("titles"))
    if args.get("full"):
        return full_actions(acts, titles)
    return actions_summary(acts, titles)


def t_roadmap(c: DaemonClient, args: JSONObject) -> JSONValue:
    """Totals, or one phase with every item."""
    phase = get_str(args, "phase")
    if not phase:
        return roadmap_summary(as_object(c.json("/roadmap.json", timeout=90)))
    q = urllib.parse.urlencode({"phase": phase, "details": "1" if args.get("details") else "0"})
    doc = as_object(c.json("/roadmap.json?" + q, timeout=90)) or {}
    if "error" in doc:
        return {"error": doc.get("error")}
    if doc.get("configured") is False:
        return {"configured": False, "note": doc.get("note")}
    phases = [p for p in get_objects(doc, "phases") if get_str(p, "id") == phase]
    return phases[0] if phases else {"error": "no phase %s" % phase}


def t_timeline(c: DaemonClient, args: JSONObject) -> JSONValue:
    """The JSONL history, filtered."""
    q: Dict[str, str] = {"limit": str(max(1, min(5000, get_int(args, "limit") or 200)))}
    for key in ("kind", "src", "since"):
        v = args.get(key)
        if v not in (None, ""):
            q[key] = str(v)
    titles = bool(args.get("titles"))
    lines: List[JSONValue] = []
    for line in c.jsonl("/timeline?" + urllib.parse.urlencode(q)):
        obj = as_object(line)
        lines.append(timeline_line(obj, titles) if obj is not None else line)
    return lines


def t_security(c: DaemonClient, args: JSONObject) -> JSONValue:
    """Everything pending on security, with the attestation totals."""
    doc = as_object(c.json("/security.json", timeout=90)) or {}
    if "error" in doc:
        return {"error": doc.get("error")}
    att = as_object(doc.get("attestation")) or {}
    scans: JSONObject = {}
    for name, value in (as_object(doc.get("scans")) or {}).items():
        scan = as_object(value)
        if scan is None:
            scans[name] = value
            continue
        # The branch a scanner job ran on is GitHub's text, like every branch name.
        kept: JSONObject = {k: v for k, v in scan.items() if k != "branch"}
        kept["untrusted"] = {"branch": cut(scan.get("branch"))}
        scans[name] = kept
    out: JSONObject = {"pending_total": doc.get("pending_total"), "summary": att.get("summary"),
                       "next_review": att.get("next_review"), "scans": scans, "pins": doc.get("pins"),
                       "waivers": len(get_list(doc, "waivers")), "unreleased": len(get_list(doc, "unreleased"))}
    groups: List[JSONValue] = []
    for g in get_objects(doc, "pending"):
        items = get_list(g, "items")
        if get_str(g, "group") in ("in_progress", "release") and not args.get("all"):
            items = items[:5]
        groups.append({"group": g.get("group"), "title": g.get("title"), "count": g.get("count"), "items": items})
    out["pending"] = groups
    control = get_str(args, "control")
    if control:
        out["control"] = next((x for x in get_objects(att, "controls") if get_str(x, "id") == control), None)
    return out


def t_history(c: DaemonClient, args: JSONObject) -> JSONValue:
    """Saved gate runs, newest first."""
    doc = as_object(c.json("/history")) or {}
    limit = max(1, min(200, get_int(args, "limit") or 20))
    return get_list(doc, "runs")[:limit]


TOOLS: Dict[str, Tuple[Tool, str, JSONObject]] = {
    "status": (t_status, "Everything at once: the gate (verdict, failing and running stages), GitHub Actions "
               "(newest runs, failed jobs), MemAvailable, and the roadmap (release progress, next item). Call this first. "
               + UNTRUSTED,
               {"type": "object", "properties": {}}),
    "gate": (t_gate, "The latest pr-check gate run (or one saved run by run_id) with every stage's status and timing. "
             + UNTRUSTED,
             {"type": "object", "properties": {"run_id": {"type": "string", "description": "A saved run id; the latest when omitted"}}}),
    "gate_wait": (t_gate_wait, "Block until the gate's first failed stage or its end, then return that event. With no "
                  "run_id it waits on the running gate, or the next one to start. " + UNTRUSTED,
                  {"type": "object", "properties": {
                      "for": {"type": "string", "description": "Comma list of fail, end, stage, any (default fail,end)"},
                      "run_id": {"type": "string"},
                      "since": {"type": "integer", "description": "The last event seq already seen"},
                      "timeout": {"type": "integer", "description": "Seconds, 1 to 3600 (default 600)"}}}),
    "actions": (t_actions, "GitHub Actions for the branch the latest gate ran on: newest runs and failed jobs, or the "
                "whole poll with full=true; run titles only with titles=true. " + UNTRUSTED,
                {"type": "object", "properties": {"full": {"type": "boolean"},
                                                  "titles": {"type": "boolean", "description": "Include run titles"}}}),
    "roadmap": (t_roadmap, "The implementation roadmap: totals, current release, next item and work queue; or one phase "
                "(phase=\"41d\") with its items, and their full text with details=true. " + UNTRUSTED,
                {"type": "object", "properties": {"phase": {"type": "string"}, "details": {"type": "boolean"}}}),
    "timeline": (t_timeline, "Every past value the status page recorded (JSONL history): kind is one or a comma list of "
                 "daemon, gate.stage, gate.run, actions.run, actions.job, roadmap.summary, roadmap.phase, git.day, "
                 "memory "
                 "(a kind ending in '.' is a family); src narrows to one run id, phase id or Actions run; since is "
                 "epoch seconds; limit keeps the newest (default 200); titles=true keeps run titles. " + UNTRUSTED,
                 {"type": "object", "properties": {"kind": {"type": "string"}, "src": {"type": "string"},
                                                   "since": {"type": "number"}, "limit": {"type": "integer"},
                                                   "titles": {"type": "boolean", "description": "Include run titles"}}}),
    "security": (t_security, "Security posture and EVERYTHING PENDING, most urgent first: attestation problems, a failed "
                 "or stale govulncheck, controls awaiting sign-off, open controls, reviews due, unproven implementations, "
                 "stale gosec waivers, unreleased security changes. control=\"SSDF PS.2\" adds that control's statements; "
                 "all=true lists every item of the long groups. " + UNTRUSTED,
                 {"type": "object", "properties": {"control": {"type": "string"}, "all": {"type": "boolean"}}}),
    "history": (t_history, "Saved gate runs, newest first, with verdict and per-stage durations. " + UNTRUSTED,
                {"type": "object", "properties": {"limit": {"type": "integer"}}}),
}
