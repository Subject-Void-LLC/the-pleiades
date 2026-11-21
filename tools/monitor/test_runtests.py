# pyright: strict
"""Tests of runtests.py, the runner make monitor-test calls, and of the run it made.

    cd tools/monitor && python3 -B -m unittest -v test_runtests
"""

from __future__ import annotations

import sys

sys.dont_write_bytecode = True

import os  # noqa: E402
import unittest  # noqa: E402

import locks  # noqa: E402
import runtests  # noqa: E402

OK_RUN = ["....", "Ran 4 tests in 0.1s", "", "OK (skipped=1)"]


class MarkerTest(unittest.TestCase):
    """The run is the one runtests.py says it is."""

    @unittest.skipUnless(os.environ.get(runtests.MARKER), "MONITOR_TEST_FCNTL is set by runtests.py only")
    def test_fcntl_is_there_exactly_when_the_run_says(self) -> None:
        """present means flock works here; hidden or absent means it does not,
        so a hide that did not take fails the second run."""
        marker = os.environ.get(runtests.MARKER)
        self.assertIn(marker, ("present", "hidden", "absent"))
        self.assertEqual(locks.SUPPORTED, marker == "present", "%s=%s" % (runtests.MARKER, marker))


class RunnerRulesTest(unittest.TestCase):
    """How runtests.py plans its runs and judges each one."""

    def test_one_run_where_there_is_no_fcntl(self) -> None:
        """Two runs where fcntl exists (one hiding it), one where it does not."""
        self.assertEqual([m for _, m in runtests.plan(True)], ["present", "hidden"])
        self.assertEqual([m for _, m in runtests.plan(False)], ["absent"])

    def test_a_pass_needs_status_0_and_the_ok_line(self) -> None:
        """Status 0 with unittest's OK line passes; a failing status, a killed
        child, or a last line other than OK each fail the run."""
        self.assertEqual(runtests.problems(0, OK_RUN, [], "present"), [])
        self.assertEqual(runtests.problems(0, ["Ran 4 tests", "OK"], [], "hidden"), [])
        self.assertTrue(runtests.problems(1, OK_RUN, [], "present"))
        self.assertTrue(runtests.problems(None, OK_RUN, [], "present"))
        self.assertTrue(runtests.problems(0, ["Ran 4 tests in 0.1s", "", "FAILED (failures=1)"], [], "present"))
        self.assertTrue(runtests.problems(0, [], [], "present"))

    def test_a_skip_for_fcntl_fails_where_fcntl_is_present(self) -> None:
        """A test skipped for lacking flock fails the run that has fcntl, and
        not the runs without it; other skips fail neither."""
        lacking = [("test_daemon.X.test_y", "needs fcntl flock; not supported")]
        other = [("test_daemon.X.test_z", "needs POSIX signals")]
        self.assertTrue(runtests.problems(0, OK_RUN, lacking, "present"))
        self.assertEqual(runtests.problems(0, OK_RUN, lacking, "hidden"), [])
        self.assertEqual(runtests.problems(0, OK_RUN, lacking, "absent"), [])
        self.assertEqual(runtests.problems(0, OK_RUN, other, "present"), [])


if __name__ == "__main__":
    unittest.main()
