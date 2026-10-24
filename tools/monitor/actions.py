# pyright: strict
"""GitHub Actions runs and jobs for the current branch, through gh api.

The machine's gh is 2.4.0, whose `gh run` commands lack most --json fields,
so this reads the REST API through `gh api` with the repository written into
every path. gh api has no -R flag; an explicit repos/<owner>/<repo> path does
the same job, since it never falls back to the current directory's remote.
It never asks gh for a token and never prints one. When gh is missing or
logged out, poll returns the reason and the rest of the monitor goes on
without Actions.
"""

from __future__ import annotations

import calendar
import os
import subprocess
import threading
import time
import urllib.parse
from typing import Callable, Dict, List, Optional, Protocol, Tuple, TypedDict

from jsonutil import (JSONObject, JSONValue, as_object, get_float, get_int, get_objects, get_str, loads)
from redact import Redactor

REPO = "Subject-Void-LLC/the-pleiades"
# POLL_SECONDS is how often the branch's runs are read.
POLL_SECONDS = 60
# RUNS_WITH_JOBS is how many of the newest runs get their jobs read.
RUNS_WITH_JOBS = 5
# FAILED_CONCLUSIONS are the job conclusions the page marks as failed.
FAILED_CONCLUSIONS = ("failure", "timed_out", "startup_failure")

# ApiCall reads one API path: (decoded JSON, None) or (None, reason).
ApiCall = Callable[[str], Tuple[JSONValue, Optional[str]]]
# Runner is subprocess.run's shape, so a test can stand in for it.
Runner = Callable[..., "subprocess.CompletedProcess[str]"]


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


def runs_path(branch: str) -> str:
    """Builds the API path listing a branch's newest ten workflow runs.

    Args:
        branch: The branch name.

    Returns:
        The path for gh api.
    """
    return "repos/%s/actions/runs?branch=%s&per_page=10" % (REPO, urllib.parse.quote(branch, safe=""))


def jobs_path(run_id: int) -> str:
    """Builds the API path listing a run's jobs.

    Args:
        run_id: The run id.

    Returns:
        The path for gh api.
    """
    return "repos/%s/actions/runs/%d/jobs?per_page=100" % (REPO, int(run_id))


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


def parse_runs(doc: JSONValue) -> List[ActionsRun]:
    """Reads the workflow runs in a runs API response.

    Args:
        doc: The decoded response.

    Returns:
        Each run, its jobs empty.
    """
    out: List[ActionsRun] = []
    for r in get_objects(as_object(doc), "workflow_runs"):
        run_id = get_int(r, "id")
        if run_id is None:
            continue
        status = get_str(r, "status")
        out.append(ActionsRun(
            id=run_id, name=get_str(r, "name"), title=get_str(r, "display_title"),
            branch=get_str(r, "head_branch"), sha=get_str(r, "head_sha")[:12], status=status,
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
    return ActionsJob(id=get_int(j, "id") or 0, name=get_str(j, "name"), status=status,
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


def explain_error(stderr: str) -> str:
    """Turns gh's error output into a short reason.

    Args:
        stderr: What gh printed on stderr.

    Returns:
        A one-line reason.
    """
    text = stderr.strip()
    low = text.lower()
    if "auth login" in low or "not logged" in low or "http 401" in low or "bad credentials" in low:
        return "gh is not logged in: run gh auth login"
    first = text.splitlines()[0] if text else "gh api failed with no message"
    return first[:200]


def gh_api(path: str, run: Runner = subprocess.run) -> Tuple[JSONValue, Optional[str]]:
    """Reads one API path with gh api.

    Args:
        path: The path, repository included.
        run: subprocess.run, or a stand-in.

    Returns:
        (decoded JSON, None), or (None, a reason) when it could not be read.
    """
    env = dict(os.environ)
    env.update({"GH_PROMPT_DISABLED": "1", "GH_NO_UPDATE_NOTIFIER": "1", "NO_COLOR": "1"})
    try:
        proc = run(["gh", "api", "-H", "Accept: application/vnd.github+json", path],
                   capture_output=True, text=True, timeout=30, env=env)
    except FileNotFoundError:
        return None, "gh is not installed"
    except subprocess.TimeoutExpired:
        return None, "gh api timed out"
    if proc.returncode != 0:
        return None, explain_error(proc.stderr or "")
    try:
        return loads(proc.stdout), None
    except ValueError:
        return None, "gh api returned something that is not JSON"


def current_branch(repo_root: str, run: Runner = subprocess.run) -> Optional[str]:
    """Names the branch checked out.

    Args:
        repo_root: The repository.
        run: subprocess.run, or a stand-in.

    Returns:
        The branch, or None for a detached HEAD or an error.
    """
    try:
        proc = run(["git", "rev-parse", "--abbrev-ref", "HEAD"], cwd=repo_root,
                   capture_output=True, text=True, timeout=10)
    except (OSError, subprocess.TimeoutExpired):
        return None
    branch = (proc.stdout or "").strip()
    return branch if proc.returncode == 0 and branch and branch != "HEAD" else None


def poll(branch: str, cache: Dict[str, JSONObject], api: ApiCall = gh_api) -> Tuple[List[ActionsRun], Optional[str]]:
    """Reads a branch's newest runs and the jobs of the newest few.

    A completed run whose jobs were already read while it was complete is
    not read again.

    Args:
        branch: The branch.
        cache: Runs read before, by id (actions.json).
        api: gh_api, or a stand-in.

    Returns:
        (runs newest first, None), or ([], a reason) when nothing could be read.
    """
    doc, err = api(runs_path(branch))
    if err:
        return [], err
    runs = parse_runs(doc)
    for i, run in enumerate(runs):
        old = cache.get(str(run["id"]))
        old_jobs = [job_from_json(j) for j in get_objects(old, "jobs")]
        if old_jobs and get_str(old, "status") == "completed" and run["status"] == "completed":
            run["jobs"] = old_jobs
            continue
        if i >= RUNS_WITH_JOBS:
            run["jobs"] = old_jobs
            continue
        jdoc, jerr = api(jobs_path(run["id"]))
        if jerr:
            run["jobs_error"] = jerr
            continue
        run["jobs"] = parse_jobs(jdoc)
    return runs, None


class ActionsStore(Protocol):
    """The part of history.HistoryStore the poller uses.

    A structural type, so this module does not depend on history.
    """

    def load_actions(self) -> Dict[str, JSONObject]:
        """Loads every Actions run seen.

        Returns:
            Each run by its id.
        """
        ...

    def save_actions(self, runs: List[JSONObject]) -> Dict[str, JSONObject]:
        """Merges runs into the saved ones.

        Args:
            runs: Runs as JSON.

        Returns:
            Everything saved.
        """
        ...



class ActionsPoller:
    """Reads the branch's runs every POLL_SECONDS in a thread.

    Attributes:
        repo_root: The repository whose branch is polled.
        store: Where runs seen are saved, or None.
        redactor: Masks every string before it is kept.
        api: gh_api, or a stand-in.
        lock: Guards state.
        state: The latest poll: branch, runs, error and when.
        stop: Set to end the polling thread.
    """

    repo_root: str
    store: Optional[ActionsStore]
    redactor: Redactor
    api: ApiCall
    lock: threading.Lock
    state: JSONObject
    stop: threading.Event

    def __init__(self, repo_root: str, store: Optional[ActionsStore], redactor: Redactor,
                 api: ApiCall = gh_api) -> None:
        """Prepares a poller; start() begins polling.

        Args:
            repo_root: The repository whose branch is polled.
            store: Where runs seen are saved, or None.
            redactor: Masks every string before it is kept.
            api: gh_api, or a stand-in.
        """
        self.repo_root = repo_root
        self.store = store
        self.redactor = redactor
        self.api = api
        self.lock = threading.Lock()
        self.state = {"branch": None, "runs": [], "error": None, "polled": None}
        self.stop = threading.Event()

    def poll_once(self) -> None:
        """Polls once and keeps the result."""
        branch = current_branch(self.repo_root)
        runs: List[ActionsRun] = []
        err: Optional[str]
        if branch is None:
            err = "no branch is checked out"
        else:
            cache = self.store.load_actions() if self.store else {}
            runs, err = poll(branch, cache, self.api)
        docs = [as_object(self.redactor.mask_tree(run_to_json(r))) or {} for r in runs]
        if docs and self.store:
            self.store.save_actions(docs)
        listed: List[JSONValue] = list(docs)
        state: JSONObject = {"branch": branch, "runs": listed, "error": self.redactor.mask(err) if err else None,
                             "polled": time.time()}
        with self.lock:
            self.state = state

    def snapshot(self) -> JSONObject:
        """Returns the latest poll's result.

        Returns:
            branch, runs, error and polled.
        """
        with self.lock:
            return dict(self.state)

    def loop(self) -> None:
        """Polls until stop is set; a failed poll is reported, never raised."""
        while not self.stop.is_set():
            try:
                self.poll_once()
            except Exception as exc:  # a poll must never take the monitor down
                with self.lock:
                    self.state = dict(self.state, error="polling failed: %s" % type(exc).__name__)
            self.stop.wait(POLL_SECONDS)

    def start(self) -> None:
        """Starts polling in a daemon thread."""
        threading.Thread(target=self.loop, name="actions", daemon=True).start()
