# pyright: strict
"""The Security tab's model: attestation, scanner exceptions, and what is pending.

build() gathers, read-only:

- the security attestation, from the roadmap panel's model (its `security`
  part: controls, lifecycle stages, frameworks), when a roadmap is configured;
- gosec-waivers.json: every waiver and skip, each checked against the tree
  (a waiver whose file is gone, or whose line is past the file's end, is
  stale);
- the scanner pins (GOSEC_VERSION, GOVULNCHECK_VERSION in the Makefile, the
  go and toolchain lines in go.mod);
- the newest GitHub Actions run of the vulns job (govulncheck on a schedule)
  and of nightly, from the runs the daemon has recorded;
- changelog/*.security.md: security changes not yet released, each dated by
  the commit that added it.

and derives `pending`: one entry per thing that waits on someone, grouped
and ranked, so the page and an agent read the same list. Nothing here
writes, runs a scanner, or reads a secret.
"""

from __future__ import annotations

import os
import re
import subprocess
import time
from typing import Dict, List, Optional, Tuple

from history import read_json
from jsonutil import JSONObject, JSONValue, as_object, get_int, get_list, get_objects, get_str

# REVIEW_SOON_DAYS is how close a control's review date must be to count as due soon.
REVIEW_SOON_DAYS = 30
# SCAN_STALE_HOURS is how old the newest vulns run may be before it is pending.
SCAN_STALE_HOURS = 48
# GROUP_ORDER ranks the pending groups, most urgent first.
GROUP_ORDER = ("problem", "scanner", "overdue", "signoff", "open", "due_soon", "unproven", "waiver", "release", "in_progress")
GROUP_TITLES = {
    "problem": "Problems the attestation reports",
    "scanner": "Vulnerability scanning",
    "overdue": "Reviews overdue",
    "signoff": "Awaiting your review and sign-off",
    "open": "Open controls: nothing done yet",
    "due_soon": "Reviews due within %d days" % REVIEW_SOON_DAYS,
    "unproven": "Implementation not proven",
    "waiver": "Stale scanner waivers",
    "release": "Security changes not yet released",
    "in_progress": "Controls in progress",
}


def parse_date(text: str) -> Optional[float]:
    """Reads a YYYY-MM-DD date as epoch seconds (UTC midnight).

    Args:
        text: The date.

    Returns:
        Its time, or None.
    """
    try:
        return time.mktime(time.strptime(text[:10], "%Y-%m-%d"))
    except (ValueError, OverflowError):
        return None


def tool_pins(root: str) -> JSONObject:
    """Reads the scanner and Go versions the repository pins.

    Args:
        root: The repository.

    Returns:
        gosec, govulncheck, go and toolchain, each "" when not found.
    """
    out: JSONObject = {"gosec": "", "govulncheck": "", "go": "", "toolchain": ""}
    try:
        with open(os.path.join(root, "Makefile"), encoding="utf-8") as f:
            make = f.read()
        for key, var in (("gosec", "GOSEC_VERSION"), ("govulncheck", "GOVULNCHECK_VERSION")):
            m = re.search(r"^%s\s*\??=\s*(\S+)" % var, make, re.M)
            if m:
                out[key] = m.group(1)
    except OSError:
        pass
    try:
        with open(os.path.join(root, "go.mod"), encoding="utf-8") as f:
            mod = f.read()
        for key, pat in (("go", r"^go\s+(\S+)"), ("toolchain", r"^toolchain\s+(\S+)")):
            m = re.search(pat, mod, re.M)
            if m:
                out[key] = m.group(1)
    except OSError:
        pass
    return out


def line_count(path: str) -> Optional[int]:
    """Counts a file's lines.

    Args:
        path: The file.

    Returns:
        The count, or None when it cannot be read.
    """
    try:
        with open(path, "rb") as f:
            return sum(1 for _ in f)
    except OSError:
        return None


def waivers(root: str) -> Tuple[List[JSONValue], List[JSONValue]]:
    """Reads gosec-waivers.json and checks each entry against the tree.

    Args:
        root: The repository.

    Returns:
        (waivers, skips), each entry with file, line, rule, a reason and
        `stale` naming why it no longer matches the tree, or "".
    """
    doc = as_object(read_json(os.path.join(root, "gosec-waivers.json"))) or {}
    counts: Dict[str, Optional[int]] = {}

    def check(entry: JSONObject) -> JSONObject:
        """Checks one entry."""
        rel = get_str(entry, "file")
        if rel not in counts:
            counts[rel] = line_count(os.path.join(root, rel)) if rel else None
        n = counts[rel]
        line = int(get_str(entry, "line") or 0) if get_str(entry, "line").isdigit() else get_int(entry, "line") or 0
        stale = ""
        if n is None:
            stale = "the file no longer exists"
        elif line and line > n:
            stale = "line %d is past the file's end (%d lines)" % (line, n)
        reason = get_str(entry, "reason")
        return {"file": rel, "line": line or None, "rule": get_str(entry, "rule_id"),
                "reason": reason, "short": reason.split(". ")[0][:220], "stale": stale}

    return ([check(w) for w in get_objects(doc, "waivers")], [check(s) for s in get_objects(doc, "skips")])


def unreleased(root: str) -> List[JSONValue]:
    """Lists changelog/*.security.md, each with its first sentence and the date its commit added it.

    Args:
        root: The repository.

    Returns:
        The fragments, oldest first.
    """
    d = os.path.join(root, "changelog")
    try:
        names = sorted(n for n in os.listdir(d) if n.endswith(".security.md"))
    except OSError:
        return []
    added: Dict[str, float] = {}
    try:
        out = subprocess.run(["git", "log", "--diff-filter=A", "--name-only", "--format=@%ct", "--", "changelog/"],
                             cwd=root, capture_output=True, text=True, timeout=20)
        when = 0.0
        for line in out.stdout.splitlines():
            if line.startswith("@"):
                when = float(line[1:] or 0)
            elif line.strip():
                added.setdefault(os.path.basename(line.strip()), when)
    except (OSError, subprocess.TimeoutExpired, ValueError):
        pass
    rows: List[JSONObject] = []
    for n in names:
        try:
            with open(os.path.join(d, n), encoding="utf-8") as f:
                text = " ".join(f.read().split())
        except OSError:
            continue
        first = re.split(r"(?<=[.!?])\s", text, maxsplit=1)[0]
        rows.append({"name": n[: -len(".security.md")], "summary": first[:300], "added": added.get(n)})
    rows.sort(key=lambda r: added_at(r))
    return list(rows)


def added_at(row: JSONObject) -> float:
    """Returns when a fragment was added, 0 when unknown.

    Args:
        row: The fragment.

    Returns:
        Epoch seconds.
    """
    v = row.get("added")
    return float(v) if isinstance(v, (int, float)) else 0.0


def scanner_runs(actions_runs: List[JSONObject]) -> JSONObject:
    """Finds the newest vulns and nightly job results among recorded Actions runs.

    Args:
        actions_runs: Recorded runs with their jobs.

    Returns:
        {"vulns": job or None, "nightly": job or None}, each with its run's url and branch.
    """
    out: JSONObject = {"vulns": None, "nightly": None}
    best: Dict[str, float] = {}
    for r in actions_runs:
        for j in get_objects(r, "jobs"):
            name = get_str(j, "name")
            if name not in ("vulns", "nightly") or get_str(j, "conclusion") in ("", "skipped"):
                continue
            end = j.get("end")
            t = float(end) if isinstance(end, (int, float)) else 0.0
            if t >= best.get(name, -1.0):
                best[name] = t
                out[name] = {"conclusion": get_str(j, "conclusion"), "status": get_str(j, "status"), "end": end,
                             "failed": bool(j.get("failed")), "url": get_str(j, "url") or get_str(r, "url"),
                             "branch": get_str(r, "branch"), "run": r.get("number")}
    return out


def pending(controls: List[JSONObject], problems: List[JSONValue], waived: List[JSONValue],
            fragments: List[JSONValue], scans: JSONObject, now: float) -> List[JSONValue]:
    """Derives everything that waits on someone.

    Args:
        controls: The attestation's controls.
        problems: The attestation's own problems.
        waived: Waivers and skips, checked.
        fragments: Unreleased security changelog fragments.
        scans: scanner_runs' result.
        now: The time to judge review dates by.

    Returns:
        Entries {group, title, detail, ref}, ranked by GROUP_ORDER.
    """
    out: List[JSONObject] = []
    for p in problems:
        po = as_object(p)
        out.append({"group": "problem", "title": get_str(po, "kind") or "problem", "detail": get_str(po, "detail") or str(p), "ref": get_str(po, "control")})
    for c in controls:
        cid, title, state = get_str(c, "id"), get_str(c, "title"), get_str(c, "state")
        done, total = get_int(c, "statements_done") or 0, get_int(c, "statements_total") or 0
        for p in get_list(c, "problems"):
            out.append({"group": "problem", "title": cid, "detail": str(p) if not isinstance(p, dict) else get_str(p, "detail"), "ref": cid})
        if state.startswith("drafted"):
            out.append({"group": "signoff", "title": cid + ": " + title,
                        "detail": "%d of %d statements done; drafted and waiting for an owner to review and sign" % (done, total), "ref": cid})
        elif state == "open":
            out.append({"group": "open", "title": cid + ": " + title, "detail": "0 of %d statements done" % total, "ref": cid})
        elif state == "in progress":
            out.append({"group": "in_progress", "title": cid + ": " + title, "detail": "%d of %d statements done" % (done, total), "ref": cid})
        due = parse_date(get_str(c, "review_due"))
        if due is not None:
            days = int((due - now) // 86400)
            if days < 0:
                out.append({"group": "overdue", "title": cid + ": " + title, "detail": "review was due %s (%d days ago)" % (get_str(c, "review_due"), -days), "ref": cid})
            elif days <= REVIEW_SOON_DAYS:
                out.append({"group": "due_soon", "title": cid + ": " + title, "detail": "review due %s (in %d days)" % (get_str(c, "review_due"), days), "ref": cid})
        for impl in get_objects(c, "implemented_by"):
            if not impl.get("proven") and get_str(impl, "status") == "done":
                out.append({"group": "unproven", "title": cid + ": Phase " + get_str(impl, "phase"),
                            "detail": "the phase is done but its implementation is not proven", "ref": cid})
    for w in waived:
        wo = as_object(w) or {}
        if get_str(wo, "stale"):
            out.append({"group": "waiver", "title": "%s %s:%s" % (get_str(wo, "rule") or "skip", get_str(wo, "file"), wo.get("line") or ""),
                        "detail": get_str(wo, "stale"), "ref": ""})
    for f in fragments:
        fo = as_object(f) or {}
        out.append({"group": "release", "title": get_str(fo, "name"), "detail": get_str(fo, "summary"), "ref": ""})
    vulns = as_object(scans.get("vulns"))
    if vulns is None:
        out.append({"group": "scanner", "title": "govulncheck", "detail": "no run of the vulns job is recorded yet", "ref": ""})
    else:
        end = vulns.get("end")
        age = (now - float(end)) / 3600 if isinstance(end, (int, float)) else None
        if vulns.get("failed"):
            out.append({"group": "scanner", "title": "govulncheck failed", "detail": "the newest vulns job concluded %s" % get_str(vulns, "conclusion"), "ref": get_str(vulns, "url")})
        elif age is not None and age > SCAN_STALE_HOURS:
            out.append({"group": "scanner", "title": "govulncheck is stale", "detail": "the newest vulns job ran %d hours ago" % int(age), "ref": get_str(vulns, "url")})
    rank = {g: i for i, g in enumerate(GROUP_ORDER)}
    out.sort(key=lambda e: rank.get(get_str(e, "group"), 99))
    return list(out)


def build(root: str, roadmap_model: Optional[JSONObject], actions_runs: List[JSONObject], now: Optional[float] = None) -> JSONObject:
    """Builds the /security.json document.

    Args:
        root: The repository (the main clone).
        roadmap_model: The roadmap panel's model, or None when there is none.
        actions_runs: Every recorded Actions run, with jobs.
        now: The time to judge dates by; now by default.

    Returns:
        The document; `attestation` is None when no roadmap is configured.
    """
    at = now if now is not None else time.time()
    sec = as_object(roadmap_model.get("security")) if roadmap_model is not None else None
    controls = get_objects(sec, "controls")
    waived, skips = waivers(root)
    fragments = unreleased(root)
    scans = scanner_runs(actions_runs)
    items = pending(controls, get_list(sec, "problems"), waived + skips, fragments, scans, at)
    groups: List[JSONValue] = []
    for g in GROUP_ORDER:
        members = [e for e in items if get_str(as_object(e), "group") == g]
        if members:
            groups.append({"group": g, "title": GROUP_TITLES[g], "count": len(members), "items": members})
    reviews = sorted((get_str(c, "review_due"), get_str(c, "id")) for c in controls if get_str(c, "review_due"))
    return {
        "generated": at,
        "attestation": None if sec is None else {
            "source": sec.get("source"), "summary": sec.get("summary"), "stages": sec.get("stages"),
            "frameworks": sec.get("frameworks"), "controls": list(controls),
            "next_review": {"date": reviews[0][0], "control": reviews[0][1]} if reviews else None,
        },
        "waivers": waived, "skips": skips, "pins": tool_pins(root), "scans": scans,
        "unreleased": fragments, "pending": groups,
        "pending_total": sum(get_int(as_object(g), "count") or 0 for g in groups if get_str(as_object(g), "group") != "in_progress"),
    }
