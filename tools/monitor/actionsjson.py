# pyright: strict
"""Actions runs and jobs as the monitor keeps them: parsed from GitHub's JSON, and back.

Only runs whose head repository is REPO are kept, and every free-text field
is cut to TEXT_MAX characters and loses its control characters before it
is stored (actions.py says why).
"""

from __future__ import annotations

import calendar
import time
import unicodedata
from typing import List, Optional, TypedDict

from jsonutil import JSONObject, JSONValue, as_object, get_float, get_int, get_objects, get_str


REPO = "Subject-Void-LLC/the-pleiades"


# FAILED_CONCLUSIONS are the job conclusions the page marks as failed.
FAILED_CONCLUSIONS = ("failure", "timed_out", "startup_failure")
# TEXT_MAX bounds every free-text field read from GitHub (workflow, job and
# branch names, run titles), in characters.
TEXT_MAX = 200


class ActionsJob(TypedDict):
    """One job of a workflow run.

    Attributes:
        id: GitHub's job id.
        name: The job's name ("containers (3)").
        status: queued, in_progress or completed.
        conclusion: success, failure, skipped and so on, once completed.
        start: When it began, epoch seconds.
        end: When it ended, epoch seconds.
        url: Its page on github.com.
        failed: Whether its conclusion is a failure.
        steps_done: How many of its steps have completed.
        steps_total: How many steps it has (0 before GitHub lists them).
    """

    id: int
    name: str
    status: str
    conclusion: str
    start: Optional[float]
    end: Optional[float]
    url: str
    failed: bool
    steps_done: int
    steps_total: int


class ActionsRun(TypedDict):
    """One workflow run and its jobs.

    Attributes:
        id: GitHub's run id.
        name: The workflow's name.
        title: The run's title (the pull request's, for example).
        branch: The branch it ran for.
        sha: Its commit, shortened to 12 characters.
        status: queued, in_progress or completed.
        conclusion: success, failure and so on, once completed.
        event: What started it (push, pull_request).
        number: The workflow's run number.
        attempt: Which attempt this is.
        url: Its page on github.com.
        created: When GitHub created it (RFC 3339).
        start: When it began, epoch seconds.
        end: When it ended, epoch seconds, once completed.
        jobs: Its jobs, oldest first.
        jobs_error: Why its jobs could not be read, if they could not.
    """

    id: int
    name: str
    title: str
    branch: str
    sha: str
    status: str
    conclusion: str
    event: str
    number: int
    attempt: int
    url: str
    created: str
    start: Optional[float]
    end: Optional[float]
    jobs: List[ActionsJob]
    jobs_error: str


def parse_time(text: str) -> Optional[float]:
    """Reads a GitHub UTC time such as 2026-10-24T06:49:10Z.

    Args:
        text: The time, or an empty string.

    Returns:
        Epoch seconds, or None.
    """
    if not text:
        return None
    try:
        return float(calendar.timegm(time.strptime(text, "%Y-%m-%dT%H:%M:%SZ")))
    except ValueError:
        return None


def clean_text(text: str, limit: int = TEXT_MAX) -> str:
    """Makes text from GitHub safe to keep: no control or format characters, and short.

    A control character (a newline, an escape) becomes a space; a format
    character (a zero-width space, a bidirectional override) is dropped.

    Args:
        text: The text.
        limit: The most characters kept.

    Returns:
        The text, ending in "..." when it was cut.
    """
    kept: List[str] = []
    for ch in text:
        cat = unicodedata.category(ch)
        if cat == "Cc":
            kept.append(" ")
        elif cat != "Cf":
            kept.append(ch)
    out = "".join(kept)
    return out if len(out) <= limit else out[: limit - 3] + "..."


def from_this_repository(run: JSONObject) -> bool:
    """Reports whether a run's head is this repository rather than a fork.

    A fork's pull request run reports the fork as head_repository.full_name
    (and fork: true); a run whose head repository was deleted reports none.

    Args:
        run: One run as the API lists it.

    Returns:
        True only when head_repository.full_name is REPO.
    """
    return get_str(as_object(run.get("head_repository")), "full_name") == REPO


def parse_runs(doc: JSONValue) -> List[ActionsRun]:
    """Reads the workflow runs in a runs API response, this repository's only.

    Args:
        doc: The decoded response.

    Returns:
        Each run headed by REPO, its jobs empty; a fork's runs are left out.
    """
    out: List[ActionsRun] = []
    for r in get_objects(as_object(doc), "workflow_runs"):
        run_id = get_int(r, "id")
        if run_id is None or not from_this_repository(r):
            continue
        status = get_str(r, "status")
        out.append(ActionsRun(
            id=run_id, name=clean_text(get_str(r, "name")), title=clean_text(get_str(r, "display_title")),
            branch=clean_text(get_str(r, "head_branch")), sha=get_str(r, "head_sha")[:12], status=status,
            conclusion=get_str(r, "conclusion"), event=get_str(r, "event"),
            number=get_int(r, "run_number") or 0, attempt=get_int(r, "run_attempt") or 0,
            url=get_str(r, "html_url"), created=get_str(r, "created_at"),
            start=parse_time(get_str(r, "run_started_at") or get_str(r, "created_at")),
            end=parse_time(get_str(r, "updated_at")) if status == "completed" else None,
            jobs=[], jobs_error="",
        ))
    return out


def job_from_json(j: JSONObject) -> ActionsJob:
    """Reads one job, from the API or from actions.json.

    A job GitHub skipped reports a completion time before its start; its end
    is clamped to its start so it draws as a point, not a negative bar.

    Args:
        j: The job object. API fields (started_at) and the monitor's own
            (start) are both understood.

    Returns:
        The job.
    """
    status = get_str(j, "status")
    start = get_float(j, "start")
    if start is None:
        start = parse_time(get_str(j, "started_at"))
    end = get_float(j, "end")
    if end is None and status == "completed":
        end = parse_time(get_str(j, "completed_at"))
    if start is not None and end is not None and end < start:
        end = start
    conclusion = get_str(j, "conclusion")
    steps = get_objects(j, "steps")
    if steps:
        steps_total = len(steps)
        steps_done = sum(1 for st in steps if get_str(st, "status") == "completed")
    else:
        # A job saved by the monitor keeps only the counts.
        steps_total = get_int(j, "steps_total") or 0
        steps_done = get_int(j, "steps_done") or 0
    return ActionsJob(id=get_int(j, "id") or 0, name=clean_text(get_str(j, "name")), status=status,
                      conclusion=conclusion, start=start, end=end, url=get_str(j, "html_url") or get_str(j, "url"),
                      failed=conclusion in FAILED_CONCLUSIONS, steps_done=steps_done, steps_total=steps_total)


def parse_jobs(doc: JSONValue) -> List[ActionsJob]:
    """Reads the jobs in a jobs API response.

    Args:
        doc: The decoded response.

    Returns:
        Each job, oldest start first.
    """
    out = [job_from_json(j) for j in get_objects(as_object(doc), "jobs")]
    out.sort(key=lambda j: (j["start"] or 0.0, j["name"]))
    return out


def job_to_json(job: ActionsJob) -> JSONObject:
    """Converts a job to plain JSON.

    Args:
        job: The job.

    Returns:
        The same fields as a JSON object.
    """
    return {"id": job["id"], "name": job["name"], "status": job["status"], "conclusion": job["conclusion"],
            "start": job["start"], "end": job["end"], "url": job["url"], "failed": job["failed"],
            "steps_done": job["steps_done"], "steps_total": job["steps_total"]}


def run_to_json(run: ActionsRun) -> JSONObject:
    """Converts a run, with its jobs, to plain JSON.

    Args:
        run: The run.

    Returns:
        The same fields as a JSON object.
    """
    jobs: List[JSONValue] = [job_to_json(j) for j in run["jobs"]]
    return {"id": run["id"], "name": run["name"], "title": run["title"], "branch": run["branch"],
            "sha": run["sha"], "status": run["status"], "conclusion": run["conclusion"],
            "event": run["event"], "number": run["number"], "attempt": run["attempt"], "url": run["url"],
            "created": run["created"], "start": run["start"], "end": run["end"], "jobs": jobs,
            "jobs_error": run["jobs_error"]}
