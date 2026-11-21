# pyright: strict
"""DaemonApp: the daemon's backend, and what it writes to the timeline.

DaemonApp is ServedState (daemonserve.py) with the recording the periodic
jobs run: the roadmap, the main line's history and GitHub Actions.
"""

from __future__ import annotations

from typing import List

import actions
import githistory
import roadmap
from daemonserve import ServedState
from jsonutil import JSONObject, as_object, get_float, get_int, get_list, get_objects, get_str


class DaemonApp(ServedState):
    """The daemon's backend: ServedState plus what it records on the timeline."""

    def record_roadmap(self) -> None:
        """Writes the roadmap's totals and each phase that changed."""
        model = self.panel.model()
        if not roadmap.usable(model):
            return
        self.timeline.record_if_changed("roadmap.summary", "roadmap", roadmap.summary_value(model))
        for pid, value in roadmap.phase_values(model).items():
            self.timeline.record_if_changed("roadmap.phase", pid, value)
        doc = self.security()
        att = as_object(doc.get("attestation")) or {}
        self.timeline.record_if_changed("security.summary", "security", {
            "attestation": att.get("summary"), "pending": doc.get("pending_total"),
            "waivers": len(get_list(doc, "waivers")), "unreleased": len(get_list(doc, "unreleased"))})
        for c in get_objects(att, "controls"):
            self.timeline.record_if_changed("security.control", get_str(c, "id"), {
                k: c.get(k) for k in ("state", "statements_done", "statements_total", "review_due", "signed_on")})

    def record_git(self) -> None:
        """Writes the main line's history, a value a day, when it has moved.

        Every day is dated by its last commit, so the first call fills the
        timeline back to the first commit and a later one writes only the
        days that changed.
        """
        found = githistory.resolve(self.root)
        if found is None or found[1] == self.git_seen:
            return
        for day, when, value in githistory.days(self.root, found[0], self.git_catalog):
            self.timeline.record_if_changed("git.day", day, value, when)
        self.git_seen = found[1]

    def record_actions(self) -> None:
        """Writes each Actions run and job, of the latest poll, whose status or conclusion changed."""
        if self.poller is None:
            return
        self.record_action_runs(get_objects(self.poller.snapshot(), "runs"), historical=False)

    def record_action_runs(self, runs: List[JSONObject], historical: bool) -> None:
        """Writes each run and job whose status or conclusion changed.

        Args:
            runs: Runs with their jobs, as JSON.
            historical: Date each line by when the run or job ended (a
                backfill) rather than now (a poll).
        """
        for run in runs:
            rid = str(get_int(run, "id") or "")
            if not rid:
                continue
            when = (get_float(run, "end") or get_float(run, "start")) if historical else None
            self.timeline.record_if_changed("actions.run", rid, {
                k: run.get(k) for k in ("name", "title", "branch", "sha", "status", "conclusion", "event",
                                        "number", "attempt", "start", "end", "url")}, when)
            for job in get_objects(run, "jobs"):
                jwhen = (get_float(job, "end") or get_float(job, "start") or when) if historical else None
                self.timeline.record_if_changed("actions.job", "%s/%s" % (rid, get_str(job, "name")), {
                    k: job.get(k) for k in ("name", "status", "conclusion", "start", "end", "failed")}, jwhen)

    def backfill_actions(self) -> None:
        """Reads every workflow run on every branch into actions.json and the timeline."""
        if self.poller is None:
            return
        redactor = self.timeline.redactor

        def save(runs: List[actions.ActionsRun]) -> None:
            """Saves one batch as it lands.

            Args:
                runs: The batch.
            """
            docs = [as_object(redactor.mask_tree(actions.run_to_json(r))) or {} for r in runs]
            self.store.save_actions(docs)
            self.record_action_runs(docs, historical=True)

        runs, err = actions.backfill(self.store.load_actions(), self.poller.api, save=save)
        self.timeline.append("daemon", "machine", {"event": "actions-backfill", "runs": len(runs),
                                                   "error": redactor.mask(err) if err else None})
