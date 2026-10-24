# pyright: strict
"""Tests for the pr-check monitor, run from tools/monitor:

    python3 -B -m unittest -v

The parser is tested on real pr-check output (testdata/prcheck-run.txt, a
real run's log with hosts replaced by documentation addresses) and on a
--trace log assembled from `make --trace -n pr-check` and that run's output
(testdata/prcheck-trace.txt). The Actions parser is tested on recorded
`gh api` responses. Nothing here runs make, go or gh.
"""

from __future__ import annotations

import sys

sys.dont_write_bytecode = True

import http.client  # noqa: E402
import json  # noqa: E402
import os  # noqa: E402
import socket  # noqa: E402
import stat  # noqa: E402
import tempfile  # noqa: E402
import unittest  # noqa: E402
from typing import Dict, List, Mapping, Optional, Tuple  # noqa: E402

import actions  # noqa: E402
import history  # noqa: E402
import live  # noqa: E402
import memwatch  # noqa: E402
import monitor  # noqa: E402
import prlog  # noqa: E402
import prstate as ps  # noqa: E402
import redact  # noqa: E402
import server  # noqa: E402
from jsonutil import JSONObject, JSONValue, as_object, loads  # noqa: E402

HERE = os.path.dirname(os.path.abspath(__file__))
TESTDATA = os.path.join(HERE, "testdata")


def fixture(name: str) -> str:
    """Reads a fixture file.

    Args:
        name: Its name under testdata/.

    Returns:
        Its text.
    """
    with open(os.path.join(TESTDATA, name), encoding="utf-8") as f:
        return f.read()


def fixture_json(name: str) -> JSONValue:
    """Reads a JSON fixture.

    Args:
        name: Its name under testdata/.

    Returns:
        The decoded value.
    """
    return loads(fixture(name))


def replay(name: str) -> ps.RunState:
    """Feeds a whole log fixture to a new run, one second per line.

    Args:
        name: The fixture.

    Returns:
        The run after the last line.
    """
    state = ps.RunState(0.0)
    for i, line in enumerate(fixture(name).splitlines()):
        prlog.feed_line(state, line, float(i))
    return state


def statuses(state: ps.RunState) -> Dict[str, str]:
    """Lists each stage's status.

    Args:
        state: The run.

    Returns:
        Each stage's status by name.
    """
    return {name: st.status for name, st in state.stages.items()}


def feed(state: ps.RunState, lines: List[str], start: float = 0.0) -> None:
    """Feeds lines to a run, one second apart.

    Args:
        state: The run.
        lines: The lines.
        start: The first line's time.
    """
    for i, line in enumerate(lines):
        prlog.feed_line(state, line, start + i)


PKG = "github.com/Subject-Void-LLC/the-pleiades/"


class RealLogTest(unittest.TestCase):
    """The parser on a real pr-check log made without --trace."""

    def test_verdict_and_counts(self) -> None:
        """Reads the verdict, exit code, commit and package counts."""
        state = replay("prcheck-run.txt")
        self.assertEqual(state.verdict, "fail")
        self.assertEqual(state.exit_code, 1)
        self.assertEqual(state.commit, "ae89f745ba1c")
        self.assertEqual((state.fast_total, state.docker_total, state.base), (36, 10, "origin/main"))
        self.assertFalse(state.trace_seen)

    def test_every_stage_status(self) -> None:
        """Ends every stage where the run's own summary says it ended."""
        got = statuses(replay("prcheck-run.txt"))
        for name in ("build", "devtools", "vet", "fmt", "gosec", "pr-check-vet", "scope",
                     "fast", "repeat", "containers", "govulncheck", "images", "coverage", "durations"):
            self.assertEqual(got[name], ps.PASS, name)
        self.assertEqual(got["mac"], ps.FAIL)
        self.assertEqual(got["bsd"], ps.NOT_RUN)
        self.assertEqual(got["win"], ps.FAIL)
        self.assertEqual(got["tools"], ps.PENDING)  # only --trace shows it

    def test_pass_progress_ignores_devtools_output(self) -> None:
        """Counts only each pass's own package lines, not devtools' go test."""
        state = replay("prcheck-run.txt")
        self.assertEqual(len(state.stage("fast").packages), 34)
        self.assertEqual(state.stage("fast").total, 36)
        self.assertEqual(len(state.stage("repeat").packages), 34)
        self.assertEqual(len(state.stage("containers").packages), 10)
        self.assertFalse(any("tools/breakglass" in p for p in state.stage("fast").packages))

    def test_legs_from_the_printed_tail(self) -> None:
        """Reads the leg output pr-check prints at the end."""
        state = replay("prcheck-run.txt")
        mac, win = state.stage("mac"), state.stage("win")
        self.assertEqual(mac.failed, {PKG + "tools/failures"})
        self.assertEqual(win.failed, {PKG + "tools/failures"})
        self.assertIn("put the VM win-check back", win.last_line)
        self.assertIn("someone@192.0.2.10", mac.detail)

    def test_stage_order_in_time(self) -> None:
        """Puts the serial stages one after another."""
        state = replay("prcheck-run.txt")
        order = ["scope", "govulncheck", "fast", "repeat", "containers", "images", "coverage", "durations"]
        starts = [state.stage(n).start for n in order]
        self.assertTrue(all(s is not None for s in starts))
        nums = [s for s in starts if s is not None]
        self.assertEqual(nums, sorted(nums))


class TraceLogTest(unittest.TestCase):
    """The parser on a --trace log."""

    def test_trace_targets_become_stages(self) -> None:
        """Starts a stage at each --trace line, tools and templ-gen included."""
        state = replay("prcheck-trace.txt")
        self.assertTrue(state.trace_seen)
        got = statuses(state)
        for name in ("build", "devtools", "fmt", "tools", "templ-gen", "templ-gen-check", "pr-check-vet"):
            self.assertEqual(got[name], ps.PASS, name)
        self.assertEqual(state.stage("fmt").detail, "")  # seen, not inferred
        self.assertLess(state.stage("build").start or 0.0, state.stage("devtools").start or 0.0)

    def test_recipe_text_is_not_read(self) -> None:
        """Ignores the recipe --trace prints, though it holds the markers."""
        state = replay("prcheck-trace.txt")
        self.assertEqual(state.stage("scope").detail, "36 container-free and 10 container package(s) against origin/main")
        self.assertEqual(state.verdict, "fail")
        self.assertEqual(statuses(state)["fast"], ps.PASS)

    def test_trace_line_forms(self) -> None:
        """Matches both forms make 4.3 prints."""
        a = prlog.TRACE.match("Makefile:193: target 'build' does not exist")
        b = prlog.TRACE.match("Makefile:909: update target 'gosec' due to: tools")
        self.assertIsNotNone(a)
        self.assertIsNotNone(b)
        self.assertEqual(a.group(2) if a else None, "build")
        self.assertEqual(b.group(1) if b else None, "gosec")

    def test_backslash_lines_are_skipped(self) -> None:
        """Skips a continued recipe line and the line after it."""
        state = ps.RunState(0.0)
        feed(state, ['echo "pr-check: waiting for the mac tests"; \\', 'pr-check: waiting for the mac tests'])
        self.assertIsNone(state.tail)
        feed(state, ["pr-check: waiting for the mac tests"])
        self.assertEqual(state.tail, "mac")


class TransitionTest(unittest.TestCase):
    """The state model's transitions, line by line."""

    def test_a_run_step_by_step(self) -> None:
        """Moves each stage through running to its end."""
        state = ps.RunState(0.0)
        feed(state, ["Makefile:193: target 'build' does not exist", "go build ./..."])
        self.assertEqual(state.stage("build").status, ps.RUNNING)
        feed(state, ["Makefile:737: update target 'pr-check' due to: build"], 10)
        self.assertEqual(state.stage("build").status, ps.PASS)
        self.assertEqual(state.stage("scope").status, ps.RUNNING)
        feed(state, ["pr-check: 2 container-free and 1 container package(s) against origin/main"], 20)
        self.assertEqual(state.stage("scope").status, ps.PASS)
        self.assertEqual(state.stage("mac").status, ps.RUNNING)
        self.assertTrue(state.records_valid)
        feed(state, ["maccheck: the tree of abc is on someone@192.0.2.10; -phase test runs its tests"], 30)
        self.assertEqual(state.stage("mac").status, ps.RUNNING)
        self.assertEqual(state.stage("govulncheck").status, ps.RUNNING)
        feed(state, ["pr-check: the mac tests run beside the ones below; their output follows them",
                     "pr-check: the bsd win tests run one after another beside the ones below"], 40)
        self.assertEqual(state.stage("govulncheck").status, ps.ENDED)
        self.assertEqual(state.stage("fast").status, ps.RUNNING)
        self.assertEqual(state.stage("bsd").status, ps.RUNNING)
        feed(state, ["ok  \t" + PKG + "a\t1.0s", "FAIL\t" + PKG + "b", "exit status 1"], 50)
        fast = state.stage("fast")
        self.assertEqual((len(fast.packages), len(fast.failed), fast.status), (2, 1, ps.FAIL))
        self.assertEqual(state.stage("repeat").status, ps.RUNNING)
        feed(state, ["testgate: all tests passed"], 60)
        self.assertEqual(state.stage("repeat").status, ps.PASS)
        self.assertEqual(state.stage("containers").status, ps.RUNNING)
        feed(state, ["testgate: all tests passed", "coverage-check: 3 package(s) measured, none below"], 70)
        self.assertEqual(state.stage("images").status, ps.ENDED)
        self.assertEqual(state.stage("coverage").status, ps.ENDED)
        self.assertEqual(state.stage("durations").status, ps.RUNNING)
        feed(state, ["testgate: durations.json holds a time for each"], 80)
        self.assertEqual(state.stage("durations").status, ps.PASS)
        feed(state, ["pr-check at abc123, 2026-10-24 14:24 UTC: PASS (exit 0)",
                     "  govulncheck pass     exit 0", "  bsd         not-run  FreeBSD: stopped"], 90)
        self.assertEqual((state.verdict, state.exit_code), ("pass", 0))
        self.assertEqual(state.stage("govulncheck").status, ps.PASS)
        self.assertEqual(state.stage("bsd").status, ps.NOT_RUN)

    def test_no_container_packages_skip_their_stages(self) -> None:
        """Skips containers and durations when no container package changed."""
        state = ps.RunState(0.0)
        feed(state, ["pr-check: 3 container-free and 0 container package(s) against origin/main",
                     "pr-check: the bsd win tests run one after another beside",
                     "testgate: all tests passed", "testgate: all tests passed"])
        self.assertEqual(state.stage("containers").status, ps.SKIPPED)
        self.assertEqual(state.stage("images").status, ps.RUNNING)
        feed(state, ["coverage-check: 3 package(s) measured"], 10)
        self.assertIsNone(state.main)
        self.assertEqual(state.stage("durations").status, ps.SKIPPED)

    def test_make_error_fails_its_target(self) -> None:
        """Fails the static target make gave up on."""
        state = ps.RunState(0.0)
        feed(state, ["Makefile:909: update target 'gosec' due to: tools", "make: *** [Makefile:909: gosec] Error 1"])
        self.assertEqual(state.stage("gosec").status, ps.FAIL)
        self.assertEqual(state.verdict, "fail")

    def test_end_run_stops_open_stages(self) -> None:
        """Marks open stages stopped when the monitor stopped the run."""
        state = ps.RunState(0.0)
        feed(state, ["pr-check: 3 container-free and 0 container package(s) against origin/main"])
        state.end_run(5.0, 143, stopped_by="watchdog")
        self.assertEqual((state.verdict, state.stage("mac").status), ("stopped", ps.STOPPED))

    def test_finish_keeps_the_first_end(self) -> None:
        """Keeps an ended stage's time; only an unknown verdict may change."""
        state = ps.RunState(0.0)
        state.start("images", 1.0)
        state.finish("images", 2.0, ps.ENDED)
        state.finish("images", 9.0, ps.PASS)
        self.assertEqual((state.stage("images").end, state.stage("images").status), (2.0, ps.PASS))
        state.finish("images", 9.0, ps.FAIL)
        self.assertEqual(state.stage("images").status, ps.PASS)

    def test_leg_log_lines(self) -> None:
        """Reads an OS leg's own log, and starts win when bsd ends."""
        state = ps.RunState(0.0)
        state.start("bsd", 0.0)
        for i, line in enumerate(fixture("win.txt").splitlines()):
            prlog.feed_leg_line(state, "win", line, 10.0 + i)
        self.assertEqual(state.stage("bsd").status, ps.ENDED)
        win = state.stage("win")
        self.assertEqual(win.status, ps.FAIL)
        self.assertEqual(win.failed, {PKG + "tools/failures"})


class ProgressTest(unittest.TestCase):
    """Step bars for the OS legs and the Actions jobs."""

    def test_leg_steps_from_real_logs(self) -> None:
        """Counts each leg's steps from its own lines; an early stop keeps its step."""
        state = replay("prcheck-run.txt")
        mac, bsd, win = state.stage("mac"), state.stage("bsd"), state.stage("win")
        self.assertEqual((mac.steps, mac.step), (ps.MAC_LEG_STEPS, 4))
        self.assertEqual(bsd.steps, ps.LAB_LEG_STEPS)
        self.assertEqual(bsd.step, 1)  # started its box, then stopped at ready
        self.assertEqual(win.step, 6)
        self.assertEqual(state.to_dict(0.0)["stages"][-1]["steps"], [])

    def test_leg_steps_one_by_one(self) -> None:
        """Moves the bar line by line through the leg's own log."""
        state = ps.RunState(0.0)
        expected = [1, 3, 4, 4, 5, 6]
        for i, line in enumerate(fixture("win.txt").splitlines()[:3] + [
                "wincheck: gcc found, so cgo and the race detector are on",
                "wincheck: the tests failed on win-check (Windows) in 19m12s",
                "wincheck: put the VM win-check back on labhost, its state saved"]):
            prlog.feed_leg_line(state, "win", line, float(i))
            self.assertEqual(state.stage("win").step, expected[i], line)

    def test_job_steps(self) -> None:
        """Counts each job's completed steps, and keeps the counts once saved."""
        jobs = actions.parse_jobs(fixture_json("gh-jobs.json"))
        ci = next(j for j in jobs if j["name"] == "ci (ubuntu-24.04, true)")
        self.assertEqual((ci["steps_done"], ci["steps_total"]), (10, 10))
        nightly = next(j for j in jobs if j["name"] == "nightly")
        self.assertEqual(nightly["steps_total"], 0)
        again = actions.job_from_json(actions.job_to_json(ci))
        self.assertEqual((again["steps_done"], again["steps_total"]), (10, 10))


class RecordsTest(unittest.TestCase):
    """Folding pr-check's records into the run."""

    def load(self) -> Tuple[Dict[str, JSONObject], Optional[JSONObject]]:
        """Reads the fixture records.

        Returns:
            The records and report.json.
        """
        return live.read_records(os.path.join(TESTDATA, "records"))

    def test_records_give_real_times(self) -> None:
        """Replaces seen times with a record's start and length."""
        records, report = self.load()
        state = replay("prcheck-run.txt")
        prlog.apply_records(state, records, report, 1000.0)
        fast = state.stage("fast")
        self.assertEqual(fast.source, "record")
        start = prlog.parse_time("2026-10-24T09:49:56.014583298-04:00")
        self.assertIsNotNone(start)
        self.assertAlmostEqual(fast.start or 0.0, start or 0.0, places=3)
        self.assertAlmostEqual((fast.end or 0.0) - (fast.start or 0.0), 297.3, places=3)
        self.assertEqual(state.stage("bsd").status, ps.NOT_RUN)
        self.assertIn("stopped at ready", state.stage("bsd").detail)

    def test_parse_time(self) -> None:
        """Reads RFC 3339 times with nanoseconds and zones."""
        self.assertEqual(prlog.parse_time("1970-01-01T00:00:10Z"), 10.0)
        self.assertEqual(prlog.parse_time("1970-01-01T01:00:10.5+01:00"), 10.5)
        self.assertIsNone(prlog.parse_time("yesterday"))

    def test_parts_not_expected_are_skipped(self) -> None:
        """Skips a part report.json does not expect."""
        state = ps.RunState(0.0)
        prlog.apply_records(state, {}, {"commit": "abc", "verdict": "running", "expected": ["fast", "mac"]}, 1.0)
        self.assertEqual(state.stage("containers").status, ps.SKIPPED)
        self.assertEqual(state.stage("fast").status, ps.PENDING)
        self.assertIsNone(state.verdict)

    def test_records_of_another_run_are_refused(self) -> None:
        """Refuses records before the log shows them rewritten, or for another commit."""
        state = ps.RunState(0.0)
        self.assertFalse(live.report_matches(state, {"commit": "ae89f745ba1c3948"}))
        state.records_valid = True
        state.commit = "ae89f745ba1c"
        self.assertTrue(live.report_matches(state, {"commit": "ae89f745ba1c3948"}))
        self.assertFalse(live.report_matches(state, {"commit": "0123456789ab"}))


class FileTailTest(unittest.TestCase):
    """Following a file that grows and is replaced."""

    def test_partial_lines_and_replacement(self) -> None:
        """Returns whole lines only, and starts over on a new file."""
        with tempfile.TemporaryDirectory() as d:
            path = os.path.join(d, "mac.log")
            tail = live.FileTail(path)
            self.assertEqual(tail.read_lines(), [])
            with open(path, "w", encoding="utf-8") as f:
                f.write("one\ntw")
            self.assertEqual(tail.read_lines(), ["one"])
            with open(path, "a", encoding="utf-8") as f:
                f.write("o\n")
            self.assertEqual(tail.read_lines(), ["two"])
            os.remove(path)
            with open(path, "w", encoding="utf-8") as f:
                f.write("fresh\n")
            self.assertEqual(tail.read_lines(), ["fresh"])


class HistoryTest(unittest.TestCase):
    """The history store."""

    def test_saves_privately_and_lists(self) -> None:
        """Writes 0700 directories and 0600 files, and lists runs newest first."""
        with tempfile.TemporaryDirectory() as d:
            root = os.path.join(d, "monitor")
            store = history.HistoryStore(root)
            self.assertEqual(stat.S_IMODE(os.stat(root).st_mode), 0o700)
            state = replay("prcheck-run.txt")
            state.started, state.ended = 100.0, 200.0
            old = store.unique_run_id(1000.0, "ae89f745ba1c3948")
            store.save_run(old, state.to_dict(200.0, max_points=0), {"mode": "run"})
            state.started = 300.0
            state.ended = 450.0
            new = store.unique_run_id(2000.0, "ae89f745ba1c")
            store.save_run(new, state.to_dict(450.0), {"mode": "watch"})
            store.save_text(new, "report.json", "{}")
            with store.open_log(new) as log:
                log.write("a line\n")
            run_dir = store.run_dir(new)
            self.assertEqual(stat.S_IMODE(os.stat(run_dir).st_mode), 0o700)
            for name in ("run.json", "report.json", "log.txt"):
                self.assertEqual(stat.S_IMODE(os.stat(os.path.join(run_dir, name)).st_mode), 0o600, name)
            runs = store.list_runs()
            self.assertEqual([r["id"] for r in runs], [new, old])
            self.assertEqual(runs[0]["seconds"], 150.0)
            self.assertEqual(runs[0]["verdict"], "fail")
            self.assertIn("fast", runs[0]["stages"])
            loaded = store.load_run(new)
            self.assertIsNotNone(loaded)
            self.assertEqual(loaded.get("report") if loaded else None, {})

    def test_unique_ids_and_refused_names(self) -> None:
        """Never reuses an id, and refuses ids and names that leave the run."""
        with tempfile.TemporaryDirectory() as d:
            store = history.HistoryStore(d)
            first = store.unique_run_id(0.0, "abc")
            store.create_run(first)
            self.assertEqual(store.unique_run_id(0.0, "abc"), first + "-2")
            self.assertIsNone(store.load_run("../../etc"))
            with self.assertRaises(ValueError):
                store.run_dir("../x")
            with self.assertRaises(ValueError):
                store.save_text(first, "../run.json", "x")

    def test_actions_merge(self) -> None:
        """Replaces a run seen again and keeps the newest MAX_ACTIONS_RUNS."""
        with tempfile.TemporaryDirectory() as d:
            store = history.HistoryStore(d)
            store.save_actions([{"id": 1, "created": "2026-10-01T00:00:00Z", "status": "in_progress"}])
            doc = store.save_actions([{"id": 1, "created": "2026-10-01T00:00:00Z", "status": "completed"}])
            self.assertEqual(doc["1"]["status"], "completed")
            many: List[JSONObject] = [{"id": i, "created": "2026-10-%02dT%02d:00:00Z" % (1 + i // 24, i % 24)}
                                      for i in range(history.MAX_ACTIONS_RUNS + 20)]
            self.assertEqual(len(store.save_actions(many)), history.MAX_ACTIONS_RUNS)
            self.assertEqual(stat.S_IMODE(os.stat(store.actions_path()).st_mode), 0o600)


class FakeApi:
    """A stand-in for gh api that serves the recorded responses.

    Attributes:
        calls: Every path asked for.
        fail: A reason to return for every call, or None.
    """

    calls: List[str]
    fail: Optional[str]

    def __init__(self, fail: Optional[str] = None) -> None:
        """Builds the fake.

        Args:
            fail: A reason to return for every call, or None.
        """
        self.calls = []
        self.fail = fail

    def __call__(self, path: str) -> Tuple[JSONValue, Optional[str]]:
        """Answers one path.

        Args:
            path: The API path.

        Returns:
            The recorded response, or the failure.
        """
        self.calls.append(path)
        if self.fail:
            return None, self.fail
        if path.endswith("/jobs?per_page=100"):
            return fixture_json("gh-jobs.json"), None
        return fixture_json("gh-runs.json"), None


class ActionsTest(unittest.TestCase):
    """The Actions parser on recorded gh api output."""

    def test_parse_runs(self) -> None:
        """Reads each run's fields and times."""
        runs = actions.parse_runs(fixture_json("gh-runs.json"))
        self.assertEqual(len(runs), 2)
        self.assertEqual((runs[0]["id"], runs[0]["name"], runs[0]["conclusion"]), (40071700770, "CI", "failure"))
        self.assertTrue(runs[0]["sha"].startswith("b1909bb3"))
        self.assertEqual(len(runs[0]["sha"]), 12)
        start, end = runs[0]["start"], runs[0]["end"]
        self.assertIsNotNone(start)
        self.assertEqual((end or 0.0) - (start or 0.0), 22 * 60 + 13)

    def test_parse_jobs(self) -> None:
        """Marks failed jobs and clamps a skipped job's end to its start."""
        jobs = actions.parse_jobs(fixture_json("gh-jobs.json"))
        self.assertEqual(len(jobs), 15)
        self.assertEqual(sorted(j["name"] for j in jobs if j["failed"]), ["coverage", "fast"])
        nightly = next(j for j in jobs if j["name"] == "nightly")
        self.assertEqual(nightly["conclusion"], "skipped")
        self.assertEqual(nightly["end"], nightly["start"])
        self.assertEqual(jobs[0]["name"], "nightly")  # sorted by start

    def test_poll_reads_jobs_once_per_completed_run(self) -> None:
        """Reads jobs for new runs, then reuses them once a run is complete."""
        api = FakeApi()
        runs, err = actions.poll("feat/lab-importers", {}, api)
        self.assertIsNone(err)
        self.assertEqual(len(api.calls), 3)
        self.assertIn("branch=feat%2Flab-importers", api.calls[0])
        self.assertTrue(api.calls[0].startswith("repos/Subject-Void-LLC/the-pleiades/"))
        cache = {str(r["id"]): actions.run_to_json(r) for r in runs}
        again = FakeApi()
        runs2, _ = actions.poll("feat/lab-importers", cache, again)
        self.assertEqual(len(again.calls), 1)
        self.assertEqual(len(runs2[0]["jobs"]), 15)

    def test_poll_reports_why_it_failed(self) -> None:
        """Returns the reason and no runs when gh cannot answer."""
        runs, err = actions.poll("main", {}, FakeApi(fail="gh is not logged in: run gh auth login"))
        self.assertEqual((runs, err), ([], "gh is not logged in: run gh auth login"))

    def test_explain_error(self) -> None:
        """Turns gh's stderr into a short reason."""
        self.assertEqual(actions.explain_error("To get started with GitHub CLI, please run:  gh auth login"),
                         "gh is not logged in: run gh auth login")
        self.assertEqual(actions.explain_error("HTTP 404: Not Found\nmore"), "HTTP 404: Not Found")

    def test_gh_missing(self) -> None:
        """Says gh is not installed when it cannot be run."""
        def missing(*args: object, **kwargs: object) -> "actions.subprocess.CompletedProcess[str]":
            """Raises as subprocess.run does for a missing program."""
            raise FileNotFoundError("gh")
        self.assertEqual(actions.gh_api("repos/x", run=missing), (None, "gh is not installed"))


class MemwatchTest(unittest.TestCase):
    """The watchdog's decisions."""

    def test_parse_meminfo(self) -> None:
        """Reads MemAvailable in MB."""
        text = "MemTotal:       10171756 kB\nMemFree:  100 kB\nMemAvailable:    7340032 kB\n"
        self.assertEqual(memwatch.parse_meminfo(text), 7168)
        self.assertIsNone(memwatch.parse_meminfo("MemTotal: 1 kB\n"))

    def test_terminate_below_the_floor_only(self) -> None:
        """Stops only below the floor, and never on a missing sample."""
        self.assertTrue(memwatch.should_terminate(699))
        self.assertFalse(memwatch.should_terminate(700))
        self.assertFalse(memwatch.should_terminate(None))

    def test_kill_after_grace_while_still_short(self) -> None:
        """Escalates to SIGKILL only after the grace period, still short."""
        self.assertFalse(memwatch.should_kill(None, 1000.0, 100))
        self.assertFalse(memwatch.should_kill(1000.0, 1059.0, 100))
        self.assertTrue(memwatch.should_kill(1000.0, 1060.0, 100))
        self.assertFalse(memwatch.should_kill(1000.0, 1100.0, 900))


class RedactTest(unittest.TestCase):
    """Masking secrets."""

    def test_secret_values(self) -> None:
        """Takes secret-named values of six characters or more, longest first."""
        env = {"GH_TOKEN": "abcdef123", "DB_PASSWORD": "hunter22", "API_KEY": "short", "HOME": "/home/x",
               "PLEIADES_WINRM_PASS": "abcdef12345"}
        self.assertEqual(redact.secret_values(env), ["abcdef12345", "abcdef123", "hunter22"])

    def test_mask_line_and_tree(self) -> None:
        """Masks a secret anywhere in a line or a JSON value."""
        r = redact.Redactor(["hunter22", "abcdef123"])
        self.assertEqual(r.mask("login hunter22 then abcdef1234"), "login [REDACTED] then [REDACTED]4")
        self.assertEqual(r.mask_tree({"a": ["x hunter22"], "b": 3}), {"a": ["x [REDACTED]"], "b": 3})

    def test_live_run_masks_what_it_keeps(self) -> None:
        """Masks a secret before the state or the log sees it."""
        run = live.LiveRun(redact.Redactor(["s3cr3tvalue"]), "/nonexistent")
        masked = run.feed_main("pr-check: waiting for the mac tests\n")
        self.assertEqual(masked, "pr-check: waiting for the mac tests")
        run.feed_main("maccheck: token s3cr3tvalue leaked")
        self.assertEqual(run.state.stage("mac").last_line, "maccheck: token [REDACTED] leaked")


def free_port() -> int:
    """Finds a free port on 127.0.0.1.

    Returns:
        The port.
    """
    with socket.socket() as s:
        s.bind(("127.0.0.1", 0))
        port: int = s.getsockname()[1]
        return port


class ServerTest(unittest.TestCase):
    """The HTTP server, on a real socket."""

    def get(self, port: int, path: str, host: Optional[str] = None) -> Tuple[int, Mapping[str, str], bytes]:
        """Makes one GET.

        Args:
            port: The server's port.
            path: The path.
            host: A Host header to send instead of the right one.

        Returns:
            (status, headers, body).
        """
        conn = http.client.HTTPConnection("127.0.0.1", port, timeout=5)
        headers = {"Host": host} if host else {}
        conn.request("GET", path, headers=headers)
        res = conn.getresponse()
        out = (res.status, {k.lower(): v for k, v in res.getheaders()}, res.read())
        conn.close()
        return out

    def test_routes(self) -> None:
        """Serves the page, the state and history, and refuses a foreign Host."""
        with tempfile.TemporaryDirectory() as d:
            store = history.HistoryStore(d)
            run = live.LiveRun(redact.Redactor([]), os.path.join(TESTDATA))
            for line in fixture("prcheck-run.txt").splitlines():
                run.feed_main(line)
            app = monitor.App("watch", store, run, "20261024T134930Z-ae89f745ba1c")
            port = free_port()
            httpd = server.start_server(app, port)
            try:
                code, headers, body = self.get(port, "/")
                self.assertEqual(code, 200)
                self.assertIn(b"echarts@5.5.1", body)
                self.assertIn("script-src 'self' https://cdn.jsdelivr.net", headers["content-security-policy"])
                code, _, body = self.get(port, "/state")
                state = as_object(loads(body.decode("utf-8")))
                self.assertEqual(code, 200)
                run_doc = as_object(state.get("run")) if state else None
                self.assertEqual(run_doc.get("verdict") if run_doc else None, "fail")
                code, _, _ = self.get(port, "/state", host="evil.example:%d" % port)
                self.assertEqual(code, 403)
                self.assertEqual(self.get(port, "/history")[0], 200)
                self.assertEqual(self.get(port, "/history/20990101T000000Z")[0], 404)
                self.assertEqual(self.get(port, "/../monitor.py")[0], 404)
            finally:
                httpd.shutdown()
                httpd.server_close()


class PageTest(unittest.TestCase):
    """The page's own rules."""

    def test_no_innerhtml_and_pinned_echarts(self) -> None:
        """Never writes HTML from a string, and pins ECharts by version and hash."""
        js = fixture(os.path.join("..", "dashboard.js"))
        html = fixture(os.path.join("..", "dashboard.html"))
        self.assertNotIn("innerHTML", js)
        self.assertNotIn("insertAdjacentHTML", js)
        self.assertIn('integrity="sha384-', html)
        self.assertIn("echarts@5.5.1/dist/echarts.min.js", html)

    def test_no_em_dash(self) -> None:
        """Keeps every file free of the em dash character."""
        dash = chr(0x2014)
        for root, _, files in os.walk(HERE):
            for name in files:
                if name.endswith((".py", ".js", ".html", ".md", ".txt", ".json")):
                    with open(os.path.join(root, name), encoding="utf-8") as f:
                        self.assertNotIn(dash, f.read(), name)


class CommandLineTest(unittest.TestCase):
    """The command line, short of starting anything."""

    def test_parse_args(self) -> None:
        """Reads each command's options with their defaults."""
        run = monitor.parse_args(["run"])
        self.assertEqual((run.command, run.cap, run.parallelism, run.port, run.linger), ("run", "4500M", 1, 8765, 120))
        watch = monitor.parse_args(["watch", "--log", "x.txt", "--pace", "0.5", "--no-save"])
        self.assertEqual((watch.log, watch.pace, watch.no_save, watch.pr_dir), ("x.txt", 0.5, True, None))
        self.assertEqual(monitor.parse_args(["serve", "--port", "9000"]).port, 9000)

    def test_run_refuses_bad_options(self) -> None:
        """Refuses a malformed cap or a negative parallelism before running anything."""
        self.assertIn("--cap", monitor.check_run_options(monitor.Options(command="run", cap="lots")) or "")
        self.assertIn("--parallelism", monitor.check_run_options(monitor.Options(command="run", parallelism=-1)) or "")
        self.assertEqual(monitor.cmd_run(monitor.Options(command="run", cap="0M")), 2)


class StateJsonTest(unittest.TestCase):
    """The state's JSON form."""

    def test_round_trip(self) -> None:
        """Converts to JSON and back, hiding --trace-only targets until seen."""
        state = replay("prcheck-run.txt")
        doc = json.loads(json.dumps(state.to_dict(1000.0)))
        names = [s["name"] for s in doc["stages"]]
        self.assertNotIn("tools", names)
        self.assertEqual(names[0], "build")
        self.assertEqual(doc["totals"], {"fast": 36, "docker": 10})


if __name__ == "__main__":
    unittest.main()
