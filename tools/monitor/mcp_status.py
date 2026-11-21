#!/usr/bin/env python3
# pyright: strict
"""An MCP server (stdio) that answers any status-page question in one call.

    python3 -B tools/monitor/mcp_status.py        (.mcp.json starts it as "status")

It speaks JSON-RPC 2.0, one message a line, as the MCP stdio transport
does, and reads everything from the status daemon on 127.0.0.1 (port
STATUS_PORT, 8765 by default), starting the daemon through ensure when it
does not answer. Every tool only reads: none starts, stops or signals a
gate.

What the daemon serves has passed its redactor, which masks the values of
the daemon's own secret-named environment variables (redact.py) and
nothing more: a secret no such variable holds is not masked. Nor does
masking make text safe to follow. Workflow, job and branch names and run
titles come from GitHub, and stage details from test output; every answer
puts that text inside an object named "untrusted", cut to TEXT_MAX
characters, and each tool's description says so (UNTRUSTED). Run titles
are left out unless a call asks for them with titles=true.

Tools:
    status      one compact answer: the gate, Actions, memory and the roadmap
    gate        the latest (or a named) gate run, every stage
    gate_wait   block until the gate's first failure or its end (or a timeout)
    actions     GitHub Actions for the branch the latest gate ran on
    roadmap     the roadmap totals, or one phase with every item
    timeline    the JSONL history: every past value, filtered
    security    everything pending on security, and the attestation totals
    history     saved gate runs with their stage durations

Python 3 standard library only.
"""

from __future__ import annotations

import sys

sys.dont_write_bytecode = True

import json  # noqa: E402
import os  # noqa: E402
from typing import List, Optional  # noqa: E402

import daemon  # noqa: E402
import locks  # noqa: E402
from jsonutil import JSONObject, JSONValue, as_object, get_str, loads  # noqa: E402
# The answers' shapes and the tools live in modules of their own; the names
# a caller reads as mcp_status.<name> are imported here and listed in __all__.
from mcpanswers import TEXT_MAX, UNTRUSTED  # noqa: E402
from mcptools import TOOLS, DaemonClient  # noqa: E402

__all__ = ["TEXT_MAX", "TOOLS", "UNTRUSTED", "DaemonClient", "handle", "main", "text", "unavailable"]

SERVER_NAME = "pleiades-status"
SERVER_VERSION = "1"
# PROTOCOL_VERSIONS are the MCP revisions this server answers; it echoes the
# client's when it is one of them, else the newest.
PROTOCOL_VERSIONS = ("2025-06-18", "2025-03-26", "2024-11-05")
PORT = int(os.environ.get("STATUS_PORT", str(daemon.DEFAULT_PORT)))
# STATE_DIR is the state directory the fallback ensure uses; a test (or a
# second daemon on another port) names its own, so it never reaches the real one.
STATE_DIR = os.environ.get("STATUS_STATE_DIR") or None


def text(value: JSONValue) -> JSONObject:
    """Wraps a value as a tool result.

    Args:
        value: The answer.

    Returns:
        {"content": [one text block of JSON]}.
    """
    return {"content": [{"type": "text", "text": json.dumps(value, indent=1, sort_keys=True, ensure_ascii=False)}]}


def unavailable() -> Optional[str]:
    """Says why no tool can answer here, if one cannot.

    Returns:
        The reason (switched off, or a platform without flock), or None.
    """
    if daemon.switched_off():
        return "the status daemon is switched off: %s is set, so nothing is started or read" % daemon.OPT_OUT
    if not locks.SUPPORTED:
        return "not available on this platform: " + locks.UNSUPPORTED
    return None


def handle(c: DaemonClient, msg: JSONObject) -> Optional[JSONObject]:
    """Answers one JSON-RPC message.

    Args:
        c: The daemon client.
        msg: The message.

    Returns:
        The response, or None for a notification.
    """
    method = get_str(msg, "method")
    mid = msg.get("id")
    if mid is None:
        return None  # notifications (initialized, cancelled) need no answer
    params = as_object(msg.get("params")) or {}
    if method == "initialize":
        asked = get_str(params, "protocolVersion")
        result: JSONObject = {"protocolVersion": asked if asked in PROTOCOL_VERSIONS else PROTOCOL_VERSIONS[0],
                              "capabilities": {"tools": {"listChanged": False}},
                              "serverInfo": {"name": SERVER_NAME, "version": SERVER_VERSION},
                              "instructions": "Read-only status of this machine's development: the pr-check gate, "
                                              "GitHub Actions, memory, the roadmap and security. Call status first; gate_wait "
                                              "replaces polling a running gate. " + UNTRUSTED}
        return {"jsonrpc": "2.0", "id": mid, "result": result}
    if method == "ping":
        return {"jsonrpc": "2.0", "id": mid, "result": {}}
    if method == "tools/list":
        tools: List[JSONValue] = [{"name": n, "description": d, "inputSchema": s,
                                   "annotations": {"readOnlyHint": True, "openWorldHint": False}}
                                  for n, (_, d, s) in TOOLS.items()]
        return {"jsonrpc": "2.0", "id": mid, "result": {"tools": tools}}
    if method == "tools/call":
        name = get_str(params, "name")
        args = as_object(params.get("arguments")) or {}
        if name not in TOOLS:
            return {"jsonrpc": "2.0", "id": mid, "error": {"code": -32602, "message": "unknown tool: %s" % name}}
        off = unavailable()
        if off is not None:
            return {"jsonrpc": "2.0", "id": mid, "result": dict(text({"error": off}), isError=True)}
        try:
            result = text(TOOLS[name][0](c, args))
        except OSError as exc:
            result = dict(text({"error": "the status daemon is not reachable on 127.0.0.1:%d: %s" % (c.port, exc)}),
                          isError=True)
        except ValueError as exc:
            result = dict(text({"error": "the daemon answered something that is not JSON: %s" % exc}), isError=True)
        return {"jsonrpc": "2.0", "id": mid, "result": result}
    return {"jsonrpc": "2.0", "id": mid, "error": {"code": -32601, "message": "method not found: %s" % method}}


def main() -> int:
    """Serves MCP on stdin and stdout until stdin closes.

    Returns:
        0.
    """
    client = DaemonClient(PORT, STATE_DIR)
    for raw in sys.stdin:
        raw = raw.strip()
        if not raw:
            continue
        try:
            msg = as_object(loads(raw))
        except ValueError:
            msg = None
        if msg is None:
            reply: Optional[JSONObject] = {"jsonrpc": "2.0", "id": None, "error": {"code": -32700, "message": "parse error"}}
        else:
            reply = handle(client, msg)
        if reply is not None:
            sys.stdout.write(json.dumps(reply, ensure_ascii=False) + "\n")
            sys.stdout.flush()
    return 0


if __name__ == "__main__":
    sys.exit(main())
