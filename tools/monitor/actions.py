# pyright: strict
"""GitHub Actions runs and jobs for the current branch, through gh api.

The machine's gh is 2.4.0, whose `gh run` commands lack most --json fields,
so this reads the REST API through `gh api` with the repository written into
every path. gh api has no -R flag; an explicit repos/<owner>/<repo> path does
the same job, since it never falls back to the current directory's remote.
It never asks gh for a token and never prints one. When gh is missing or
logged out, poll returns the reason and the rest of the monitor goes on
without Actions.

The repository is public, so a fork's pull request runs here too, and its
author chooses its title, its branch name and (through the workflow file in
the pull request) its job names. Only runs whose head repository is REPO
are kept: the branch filter alone also matches a fork's branch of the same
name. Every free-text field that is kept is cut to TEXT_MAX characters and
loses its control characters before it is stored.
"""

from __future__ import annotations

import threading
import time
import urllib.parse
from concurrent.futures import ThreadPoolExecutor
from typing import Callable, Dict, List, Optional, Protocol, Tuple

# The parsing of runs and jobs, and gh api itself, live in modules of their
# own; the names a caller reads as actions.<name> are imported here and listed
# in __all__.
from actionsjson import (REPO, TEXT_MAX, ActionsJob, ActionsRun, job_from_json, job_to_json, parse_jobs, parse_runs,
                         run_to_json)
from ghapi import ApiCall, current_branch, explain_error, gh_api
from jsonutil import JSONObject, JSONValue, as_object, get_objects, get_str
from redact import Redactor

__all__ = ["BACKFILL_PAGE", "REPO", "TEXT_MAX", "ActionsJob", "ActionsPoller", "ActionsRun", "ActionsStore", "ApiCall",
           "backfill", "current_branch", "explain_error", "gh_api", "job_from_json", "job_to_json", "parse_jobs",
           "parse_runs", "poll", "run_to_json"]

# POLL_SECONDS is how often the branch's runs are read.
POLL_SECONDS = 60
# RUNS_WITH_JOBS is how many of the newest runs get their jobs read.
RUNS_WITH_JOBS = 5
# BACKFILL_PAGE is how many runs one page of the backfill lists (GitHub's most).
BACKFILL_PAGE = 100
# BACKFILL_MAX_PAGES bounds one backfill: 100 pages is 10,000 runs.
BACKFILL_MAX_PAGES = 100
# BACKFILL_WORKERS is how many job lists the backfill reads at once.
BACKFILL_WORKERS = 4


def runs_path(branch: str) -> str:
    """Builds the API path listing a branch's newest ten workflow runs.

    Args:
        branch: The branch name.

    Returns:
        The path for gh api.
    """
    return "repos/%s/actions/runs?branch=%s&per_page=10" % (REPO, urllib.parse.quote(branch, safe=""))


def all_runs_path(page: int) -> str:
    """Builds the API path listing one page of every branch's workflow runs.

    Args:
        page: The page, from 1.

    Returns:
        The path for gh api.
    """
    return "repos/%s/actions/runs?per_page=%d&page=%d" % (REPO, BACKFILL_PAGE, int(page))


def jobs_path(run_id: int) -> str:
    """Builds the API path listing a run's jobs.

    Args:
        run_id: The run id.

    Returns:
        The path for gh api.
    """
    return "repos/%s/actions/runs/%d/jobs?per_page=100" % (REPO, int(run_id))


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


def backfill(cache: Dict[str, JSONObject], api: ApiCall = gh_api, pause: Callable[[float], None] = time.sleep,
             save: Optional[Callable[[List[ActionsRun]], None]] = None, every: int = 25,
             gap: float = 0.5) -> Tuple[List[ActionsRun], Optional[str]]:
    """Reads every workflow run on every branch, with the jobs of each.

    A completed run whose jobs are already cached is not read again, so a
    second backfill costs one call a page. Jobs are read BACKFILL_WORKERS
    runs at a time, each reader pausing gap seconds between calls, and save
    is called with each batch of `every` runs, so the page fills in while a
    first backfill works through hundreds of runs.

    Args:
        cache: Runs read before, by id (actions.json).
        api: gh_api, or a stand-in.
        pause: time.sleep, or a stand-in.
        save: Called with the runs completed so far, or None.
        every: How many runs between saves.
        gap: Seconds between job reads.

    Returns:
        (every run, newest first, None), or (the runs read, a reason) when a page failed.
    """
    runs: List[ActionsRun] = []
    err: Optional[str] = None
    for page in range(1, BACKFILL_MAX_PAGES + 1):
        doc, err = api(all_runs_path(page))
        if err:
            break
        got = parse_runs(doc)
        runs += got
        if len(got) < BACKFILL_PAGE:
            break
    def read_jobs(run: ActionsRun) -> ActionsRun:
        """Fills one run's jobs, from the cache when it was already complete.

        Args:
            run: The run.

        Returns:
            The same run.
        """
        old = cache.get(str(run["id"]))
        old_jobs = [job_from_json(j) for j in get_objects(old, "jobs")]
        if old_jobs and get_str(old, "status") == "completed" and run["status"] == "completed":
            run["jobs"] = old_jobs
            return run
        jdoc, jerr = api(jobs_path(run["id"]))
        if jerr:
            run["jobs_error"] = jerr
            run["jobs"] = old_jobs
        else:
            run["jobs"] = parse_jobs(jdoc)
        pause(gap)
        return run

    # A few readers at once: each gh call is a process start and a round trip,
    # and one at a time took most of an hour for five hundred runs.
    with ThreadPoolExecutor(max_workers=BACKFILL_WORKERS) as pool:
        for start in range(0, len(runs), every):
            batch = list(pool.map(read_jobs, runs[start:start + every]))
            if save is not None:
                save(batch)
    return runs, err


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
        root_fn: Names the checkout whose branch to poll, when set; the
            daemon follows whichever worktree the latest gate ran in.
    """

    repo_root: str
    root_fn: Optional[Callable[[], str]]
    store: Optional[ActionsStore]
    redactor: Redactor
    api: ApiCall
    lock: threading.Lock
    state: JSONObject
    stop: threading.Event

    def __init__(self, repo_root: str, store: Optional[ActionsStore], redactor: Redactor,
                 api: ApiCall = gh_api, root_fn: Optional[Callable[[], str]] = None) -> None:
        """Prepares a poller; start() begins polling.

        Args:
            repo_root: The repository whose branch is polled.
            store: Where runs seen are saved, or None.
            redactor: Masks every string before it is kept.
            api: gh_api, or a stand-in.
            root_fn: Names the checkout to poll instead of repo_root, when set.
        """
        self.repo_root = repo_root
        self.root_fn = root_fn
        self.store = store
        self.redactor = redactor
        self.api = api
        self.lock = threading.Lock()
        self.state = {"branch": None, "runs": [], "error": None, "polled": None}
        self.stop = threading.Event()

    def poll_once(self) -> None:
        """Polls once and keeps the result."""
        branch = current_branch(self.root_fn() if self.root_fn is not None else self.repo_root)
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
