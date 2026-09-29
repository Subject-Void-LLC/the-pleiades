#!/usr/bin/env python3
# ServiceNo!: a local mock of the ServiceNow Table API.
#
# It answers the part of the Table API a ticket runbook uses, shaped the way
# ServiceNow shapes it, so The Pleiades can be tested against "ServiceNow"
# without an instance: list and read records with an encoded query, create,
# update and delete them, reference fields (cmdb_ci) and choice fields
# (priority, state) in their three display modes, and work_notes and comments
# as journal fields written to sys_journal_field. Anything else ServiceNow
# does, it says no to, which is where the name comes from.
#
# Standard library only, Python 3.9 or later. Everything is held in memory
# and lost when it stops. Run it locally:
#
#     python3 serviceno.py --port 8443 --user admin --password s3cret \
#         --tls-cert cert.pem --tls-key key.pem
#
# The Pleiades' own release gate runs it in a python:3-alpine container
# (cmd/pleiades/servicenow_runbook_release_gate_test.go), so Python is never
# a build or CI dependency of the repository.
#
# SPDX-License-Identifier: GPL-3.0-or-later

import argparse
import base64
import hmac
import json
import re
import secrets
import ssl
import sys
import threading
from datetime import datetime, timezone
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
from urllib.parse import parse_qs, unquote, urlparse

# The largest request body accepted, far above any ticket update.
MAX_BODY = 1 << 20

# Choice fields: stored value to display value, as a stock instance has them.
CHOICES = {
    "incident": {
        "priority": {"1": "1 - Critical", "2": "2 - High", "3": "3 - Moderate", "4": "4 - Low", "5": "5 - Planning"},
        "impact": {"1": "1 - High", "2": "2 - Medium", "3": "3 - Low"},
        "urgency": {"1": "1 - High", "2": "2 - Medium", "3": "3 - Low"},
        "state": {"1": "New", "2": "In Progress", "3": "On Hold", "6": "Resolved", "7": "Closed", "8": "Canceled"},
    },
}

# Reference fields: field to the table it points at. A reference's display
# value is the target record's name (or number, for a task table).
REFERENCES = {
    "incident": {"cmdb_ci": "cmdb_ci", "assigned_to": "sys_user"},
}

# Journal fields: written through the record, stored in sys_journal_field,
# and read back from the record as empty, as the Table API does by default.
JOURNAL_FIELDS = {"work_notes", "comments"}

# The tables this mock knows. Any other name is ServiceNow's "Invalid table".
TABLES = ("incident", "cmdb_ci", "sys_user", "sys_journal_field")

# Numbered tables: the prefix a new record's number takes.
NUMBER_PREFIX = {"incident": "INC"}


def now():
    """Returns the current time in ServiceNow's "YYYY-MM-DD HH:MM:SS" form."""
    return datetime.now(timezone.utc).strftime("%Y-%m-%d %H:%M:%S")


class Store:
    """Every table's records, keyed by sys_id, behind one lock."""

    def __init__(self):
        self.lock = threading.Lock()
        self.tables = {name: {} for name in TABLES}
        self.counters = {name: 10000 for name in NUMBER_PREFIX}

    def create(self, table, fields):
        """Inserts a record and returns it, filling the system fields."""
        with self.lock:
            record = {k: v for k, v in fields.items() if k not in JOURNAL_FIELDS}
            record["sys_id"] = secrets.token_hex(16)
            record["sys_created_on"] = record["sys_updated_on"] = now()
            record["sys_mod_count"] = "0"
            if table in NUMBER_PREFIX and not record.get("number"):
                self.counters[table] += 1
                record["number"] = "%s%07d" % (NUMBER_PREFIX[table], self.counters[table])
            if table == "incident":
                record.setdefault("state", "1")
                record.setdefault("priority", "4")
            self.tables[table][record["sys_id"]] = record
            self._journal(table, record["sys_id"], fields)
            return dict(record)

    def update(self, table, sys_id, fields):
        """Applies fields to a record, or returns None when it does not exist."""
        with self.lock:
            record = self.tables[table].get(sys_id)
            if record is None:
                return None
            for k, v in fields.items():
                if k in JOURNAL_FIELDS or k.startswith("sys_"):
                    continue
                record[k] = v
            record["sys_updated_on"] = now()
            record["sys_mod_count"] = str(int(record.get("sys_mod_count", "0")) + 1)
            self._journal(table, sys_id, fields)
            return dict(record)

    def delete(self, table, sys_id):
        """Removes a record, reporting whether it existed."""
        with self.lock:
            return self.tables[table].pop(sys_id, None) is not None

    def get(self, table, sys_id):
        """Returns a copy of one record, or None."""
        with self.lock:
            record = self.tables[table].get(sys_id)
            return dict(record) if record is not None else None

    def all(self, table):
        """Returns a copy of every record in a table, oldest first."""
        with self.lock:
            return [dict(r) for r in self.tables[table].values()]

    def _journal(self, table, sys_id, fields):
        """Writes each journal field in fields as a sys_journal_field entry."""
        for element in sorted(JOURNAL_FIELDS):
            value = fields.get(element)
            if value in (None, ""):
                continue
            entry = {
                "sys_id": secrets.token_hex(16),
                "name": table,
                "element": element,
                "element_id": sys_id,
                "value": str(value),
                "sys_created_on": now(),
            }
            self.tables["sys_journal_field"][entry["sys_id"]] = entry


def parse_query(query):
    """Parses an encoded query into (conditions, order).

    Conditions are ANDed with ^; each is field=value, field!=value,
    fieldLIKEvalue or fieldSTARTSWITHvalue. ^OR, ^NQ and anything else are
    refused, since silently matching everything would be worse than saying
    no. ORDERBYfield and ORDERBYDESCfield set the order.
    """
    conditions, order = [], None
    if not query:
        return conditions, order
    for term in query.split("^"):
        if not term:
            continue
        if term.startswith("ORDERBYDESC"):
            order = (term[len("ORDERBYDESC"):], True)
            continue
        if term.startswith("ORDERBY"):
            order = (term[len("ORDERBY"):], False)
            continue
        if term.startswith("OR") or term.startswith("NQ"):
            raise ValueError("ServiceNo! does not support %s in an encoded query" % term[:2])
        m = re.match(r"^([A-Za-z0-9_.]+)(!=|=|LIKE|STARTSWITH)(.*)$", term)
        if not m:
            raise ValueError("ServiceNo! cannot read the query term %r" % term)
        conditions.append((m.group(1), m.group(2), m.group(3)))
    return conditions, order


def matches(record, conditions):
    """Reports whether a record satisfies every condition."""
    for field, op, value in conditions:
        actual = str(record.get(field, ""))
        if op == "=" and actual != value:
            return False
        if op == "!=" and actual == value:
            return False
        if op == "LIKE" and value.lower() not in actual.lower():
            return False
        if op == "STARTSWITH" and not actual.startswith(value):
            return False
    return True


class Handler(BaseHTTPRequestHandler):
    """Serves the Table API from the server's Store."""

    server_version = "ServiceNo!/1.0"
    protocol_version = "HTTP/1.1"

    # --- plumbing ---------------------------------------------------------

    def log_message(self, fmt, *args):
        sys.stderr.write("ServiceNo! %s %s\n" % (self.address_string(), fmt % args))

    def send_json(self, status, body, headers=None):
        """Writes a JSON response in ServiceNow's content type."""
        raw = json.dumps(body).encode("utf-8")
        self.send_response(status)
        self.send_header("Content-Type", "application/json;charset=UTF-8")
        self.send_header("Content-Length", str(len(raw)))
        for k, v in (headers or {}).items():
            self.send_header(k, v)
        self.end_headers()
        self.wfile.write(raw)

    def send_error_json(self, status, message, detail=None):
        """Writes ServiceNow's error envelope."""
        self.send_json(status, {"error": {"message": message, "detail": detail}, "status": "failure"})

    def authorized(self):
        """Checks basic auth, or a bearer token when one is configured."""
        header = self.headers.get("Authorization", "")
        cfg = self.server.config
        if header.startswith("Basic "):
            try:
                user, _, password = base64.b64decode(header[6:]).decode("utf-8").partition(":")
            except (ValueError, UnicodeDecodeError):
                return False
            return hmac.compare_digest(user, cfg.user) and hmac.compare_digest(password, cfg.password)
        if header.startswith("Bearer ") and cfg.token:
            return hmac.compare_digest(header[7:], cfg.token)
        return False

    def read_body(self):
        """Reads a JSON object body, or returns None after answering 400."""
        length = int(self.headers.get("Content-Length") or 0)
        if length > MAX_BODY:
            self.send_error_json(400, "Request body too large")
            return None
        raw = self.rfile.read(length) if length else b"{}"
        try:
            body = json.loads(raw or b"{}")
        except ValueError:
            self.send_error_json(400, "Exception while reading request", "The payload is not valid JSON.")
            return None
        if not isinstance(body, dict):
            self.send_error_json(400, "Exception while reading request", "The payload must be a JSON object.")
            return None
        return {k: ("" if v is None else str(v) if not isinstance(v, str) else v) for k, v in body.items()}

    def route(self):
        """Splits the path into (table, sys_id, query), or answers and returns None."""
        parsed = urlparse(self.path)
        parts = [unquote(p) for p in parsed.path.split("/") if p]
        if len(parts) < 4 or parts[:3] != ["api", "now", "table"] or len(parts) > 5:
            self.send_error_json(400, "Requested URI does not represent any resource", parsed.path)
            return None
        table = parts[3]
        if table not in TABLES:
            self.send_error_json(400, "Invalid table %s" % table)
            return None
        sys_id = parts[4] if len(parts) == 5 else None
        query = {k: v[-1] for k, v in parse_qs(parsed.query, keep_blank_values=True).items()}
        return table, sys_id, query

    # --- presentation -----------------------------------------------------

    def present(self, table, record, query):
        """Shapes a stored record for a response: fields, display values, references."""
        mode = query.get("sysparm_display_value", "false").lower()
        exclude_link = query.get("sysparm_exclude_reference_link", "false").lower() == "true"
        wanted = [f for f in query.get("sysparm_fields", "").split(",") if f]
        out = {}
        for field, value in record.items():
            if wanted and field not in wanted:
                continue
            out[field] = self.present_field(table, field, value, mode, exclude_link)
        if table == "incident" and not wanted:
            for element in JOURNAL_FIELDS:
                out.setdefault(element, "")
        return out

    def present_field(self, table, field, value, mode, exclude_link):
        """Shapes one field value for the requested display mode."""
        ref_table = REFERENCES.get(table, {}).get(field)
        if ref_table and value:
            target = self.server.store.get(ref_table, value) or {}
            display = target.get("name") or target.get("number") or ""
            link = "%s/api/now/table/%s/%s" % (self.server.config.base, ref_table, value)
            if mode == "true":
                return display if exclude_link else {"display_value": display, "link": link}
            if mode == "all":
                ref = {"display_value": display, "value": value}
                if not exclude_link:
                    ref["link"] = link
                return ref
            return value if exclude_link else {"link": link, "value": value}
        choices = CHOICES.get(table, {}).get(field)
        if choices is not None:
            display = choices.get(value, value)
            if mode == "true":
                return display
            if mode == "all":
                return {"display_value": display, "value": value}
        if mode == "all":
            return {"display_value": value, "value": value}
        return value

    # --- verbs ------------------------------------------------------------

    def do_GET(self):
        if not self.authorized():
            return self.send_error_json(401, "User Not Authenticated", "Required to provide Auth information")
        routed = self.route()
        if routed is None:
            return
        table, sys_id, query = routed
        if sys_id is not None:
            record = self.server.store.get(table, sys_id)
            if record is None:
                return self.send_error_json(404, "No Record found", "Record doesn't exist or ACL restricts the record retrieval")
            return self.send_json(200, {"result": self.present(table, record, query)})
        try:
            conditions, order = parse_query(query.get("sysparm_query", ""))
            limit = int(query.get("sysparm_limit", "10000"))
            offset = int(query.get("sysparm_offset", "0"))
        except ValueError as e:
            return self.send_error_json(400, "Invalid query", str(e))
        rows = [r for r in self.server.store.all(table) if matches(r, conditions)]
        if order:
            rows.sort(key=lambda r: str(r.get(order[0], "")), reverse=order[1])
        total = len(rows)
        rows = rows[offset:offset + limit]
        self.send_json(200, {"result": [self.present(table, r, query) for r in rows]}, {"X-Total-Count": str(total)})

    def do_POST(self):
        if not self.authorized():
            return self.send_error_json(401, "User Not Authenticated", "Required to provide Auth information")
        routed = self.route()
        if routed is None:
            return
        table, sys_id, query = routed
        if sys_id is not None:
            return self.send_error_json(405, "Method not Supported", "POST method not supported for API")
        body = self.read_body()
        if body is None:
            return
        record = self.server.store.create(table, body)
        location = "%s/api/now/table/%s/%s" % (self.server.config.base, table, record["sys_id"])
        self.send_json(201, {"result": self.present(table, record, query)}, {"Location": location})

    def do_PATCH(self):
        self.update()

    def do_PUT(self):
        self.update()

    def update(self):
        """Applies a PATCH or PUT to one record."""
        if not self.authorized():
            return self.send_error_json(401, "User Not Authenticated", "Required to provide Auth information")
        routed = self.route()
        if routed is None:
            return
        table, sys_id, query = routed
        if sys_id is None:
            return self.send_error_json(405, "Method not Supported", "%s method not supported for API" % self.command)
        body = self.read_body()
        if body is None:
            return
        record = self.server.store.update(table, sys_id, body)
        if record is None:
            return self.send_error_json(404, "No Record found", "Record doesn't exist or ACL restricts the record retrieval")
        self.send_json(200, {"result": self.present(table, record, query)})

    def do_DELETE(self):
        if not self.authorized():
            return self.send_error_json(401, "User Not Authenticated", "Required to provide Auth information")
        routed = self.route()
        if routed is None:
            return
        table, sys_id, _ = routed
        if sys_id is None or not self.server.store.delete(table, sys_id):
            return self.send_error_json(404, "No Record found", "Record doesn't exist or ACL restricts the record retrieval")
        self.send_response(204)
        self.send_header("Content-Length", "0")
        self.end_headers()


def main(argv=None):
    parser = argparse.ArgumentParser(prog="serviceno", description="ServiceNo!: a local mock of the ServiceNow Table API.")
    parser.add_argument("--host", default="0.0.0.0", help="address to listen on")
    parser.add_argument("--port", type=int, default=8443, help="port to listen on")
    parser.add_argument("--user", default="admin", help="the basic-auth user")
    parser.add_argument("--password", required=True, help="the basic-auth password")
    parser.add_argument("--token", default="", help="also accept this bearer token")
    parser.add_argument("--tls-cert", help="PEM certificate chain; with --tls-key, serve HTTPS")
    parser.add_argument("--tls-key", help="PEM private key for --tls-cert")
    parser.add_argument("--base", default="", help="the instance URL references link to (default: derived from the listener)")
    parser.add_argument("--seed", help="a JSON file of {table: [record, ...]} to load at start")
    cfg = parser.parse_args(argv)

    scheme = "https" if cfg.tls_cert else "http"
    if not cfg.base:
        cfg.base = "%s://localhost:%d" % (scheme, cfg.port)
    server = ThreadingHTTPServer((cfg.host, cfg.port), Handler)
    server.config = cfg
    server.store = Store()
    if cfg.seed:
        with open(cfg.seed, encoding="utf-8") as f:
            for table, records in json.load(f).items():
                for record in records:
                    server.store.create(table, record)
    if cfg.tls_cert:
        context = ssl.SSLContext(ssl.PROTOCOL_TLS_SERVER)
        context.minimum_version = ssl.TLSVersion.TLSv1_2
        context.load_cert_chain(cfg.tls_cert, cfg.tls_key)
        server.socket = context.wrap_socket(server.socket, server_side=True)
    sys.stderr.write("ServiceNo! listening on %s://%s:%d (the ServiceNow Table API, mostly)\n" % (scheme, cfg.host, cfg.port))
    sys.stderr.flush()
    try:
        server.serve_forever()
    except KeyboardInterrupt:
        pass


if __name__ == "__main__":
    main()
