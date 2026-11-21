# pyright: strict
"""The lines pr-check prints that the parser (prlog.py) knows, as patterns.

The markers, in the order pr-check prints them, are listed in README.md.
"""

from __future__ import annotations

import re
from typing import Dict

import prstate as ps


# TRACE is GNU make 4.3's --trace line, one per target before its recipe.
TRACE = re.compile(r"^\S*[Mm]akefile:\d+: (?:update target '([^']+)' due to: .*|target '([^']+)' does not exist)$")
# MAKE_ERROR is make giving up on a target.
MAKE_ERROR = re.compile(r"^make(?:\[\d+\])?: \*\*\* \[\S*?:\d+: ([^\]]+)\] (.+)$")
# COMMANDS names a static target by its first command, for a log made
# without --trace. fmt prints nothing without --trace and is never seen.
COMMANDS: Dict[str, str] = {
    "go build ./...": "build",
    "go build -tags devtools ./tools/...": "devtools",
    "go vet ./...": "vet",
    "go mod tidy -diff": "tidy-check",
    "go run ./tools/gosec-check": "gosec",
    "go run ./tools/docs-lint": "docs-lint",
    "go run ./tools/gendocs": "docs-gen-check",
    "go run ./tools/schemacompat": "schema-compat",
    "go run ./tools/helm-lint": "helm-lint",
    "go tool templ generate ./internal/ui/render": "templ-gen-check",
    "actionlint": "workflow-lint",
    "GOOS=darwin go vet -tags integration ./...": "pr-check-vet",
}
SCOPE_COUNT = re.compile(r"^pr-check: (\d+) container-free and (\d+) container package\(s\) against ([^\s,]+)")
PRSCOPE = re.compile(r"^prscope: ")
OSCHECK = re.compile(r"^(mac|bsd|win)check: (.*)$")
TREE_SENT = re.compile(r"^(mac|bsd|win)check: the tree of \S+ is on ")
LEG_BESIDE = re.compile(r"^pr-check: the (\w+) tests run beside the ones below")
LAB_BESIDE = re.compile(r"^pr-check: the ([\w ]+) tests run one after another beside")
# LEG_AT_START and LAB_AT_START are the same lines when the legs start at t0,
# before any pass: each starts its leg and leaves the recipe's own stage alone.
LEG_AT_START = re.compile(r"^pr-check: the (\w+) tests run from the start, beside")
LAB_AT_START = re.compile(r"^pr-check: the ([\w ]+) tests run one after another from the start, beside")
# PASS_BEGIN is the line pr-check prints as each local testgate pass begins.
PASS_BEGIN = re.compile(r"^pr-check: the (fast|repeat|containers) pass begins")
PACKAGE = re.compile(r"^(ok|FAIL|\?)\s+([\w.\-]+(?:/[\w.\-]+)+)(?:\s|$)")
PASS_OK = re.compile(r"^testgate: (?:all tests passed|passed \()")
EXIT_STATUS = re.compile(r"^exit status (\d+)$")
COVERAGE_LINE = re.compile(r"^coverage-check: ")
COVERAGE_DONE = re.compile(r"^coverage-check: \d+ package\(s\) measured")
DURATIONS_DONE = re.compile(r"^testgate: durations\.json ")
# RESOURCES_LINE is any line of the resources step (testgate -resources-check
# names tools/testgate/resources.json in each), and RESOURCES_DONE its last.
RESOURCES_LINE = re.compile(r"^testgate: .*tools/testgate/resources\.json")
RESOURCES_DONE = re.compile(r"^testgate: tools/testgate/resources\.json holds ")
NO_DOCKER = re.compile(r"^pr-check: no Docker daemon answered")
WAITING = re.compile(r"^pr-check: waiting for the ([\w-]+) tests")
SUMMARY = re.compile(r"^pr-check(?: at (\S+))?, (.+?): ([A-Z][A-Z-]*)(?: \(exit (-?\d+)\))?$")
SUMMARY_ROW = re.compile(r"^  (" + "|".join(ps.REPORT_PARTS) + r")\s+(\S+)\s*(.*)$")
ALL_PASSED = re.compile(r"^pr-check: all checks passed")
LEG_PASS = re.compile(r"^(\w+)check: all tests passed on ")
LEG_FAIL = re.compile(r"^(\w+)check: the tests failed on ")
LEG_ERROR = re.compile(r"^(\w+)check: running the tests on ")
LEG_PUT_BACK = re.compile(r"^(\w+)check: put the VM \S+ back")
LEG_BOX = re.compile(r"^\w+check: starting the VM ")
LEG_READY = re.compile(r"^\w+check: (\d+) package\(s\) at \S+ on ")
LEG_TESTING = re.compile(r"^\w+check: running on the ")
FLOOR_STOP = re.compile(r"^pr-check: the container-free packages miss a coverage floor")
BINSIZE_HEAD = re.compile(r"^binsize: ")
BINSIZE_ROW = re.compile(r"^  ([\w.-]+/[\w.-]+)\s+([\d.]+ [KMG]iB)\s*(.*)$")
RFC3339 = re.compile(r"^(\d{4}-\d\d-\d\dT\d\d:\d\d:\d\d)(?:\.(\d+))?(Z|[+-]\d\d:\d\d)?$")

# LOGGED are the stages with a log of their own under .coverage/pr: each OS
# leg.
LOGGED = list(ps.LEGS)

# STEP_ORDER is the serial order of what follows the testgate passes.
STEP_ORDER = ["images", "coverage", "durations", "resources"]
