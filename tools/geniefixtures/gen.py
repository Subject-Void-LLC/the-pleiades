#!/usr/bin/env python3
"""Copy genieparser's golden fixtures for pkg/iosparse into its testdata.

pkg/iosparse ports a chosen set of Cisco genieparser show-command parsers to
Go. Each port is held to every one of genieparser's own recorded device
outputs and expected results for its parser class. This script copies those
fixtures out of a genieparser checkout pinned at PINNED_COMMIT into
pkg/iosparse/testdata/<Class>/<os>/{equal,empty}/, converting each
*_expected.py (a Python module holding one dict literal named
expected_output) into *_expected.json.

The testdata is COMMITTED. Python is deliberately NOT a build or CI
dependency: `make ci` never runs this file, and nothing in the Go build
references it. Regenerating is a manual step, only needed when the pinned
commit or the list of parsers changes:

    git clone https://github.com/CiscoTestAutomation/genieparser
    git -C genieparser checkout <PINNED_COMMIT>
    python3 -I tools/geniefixtures/gen.py --src genieparser

The checkout is third-party input and none of its code is run. An expected
file is read with ast and converted with ast.literal_eval, which evaluates
literals only, so a fixture holding anything but a literal is refused
rather than executed. Run it with python3 -I so neither the current
directory nor the checkout can put a module in front of the standard
library.
"""

import argparse
import ast
import json
import os
import shutil
import subprocess
import sys

# PINNED_COMMIT is the genieparser commit the Go ports were written against.
PINNED_COMMIT = "e62f9467a383997e190e04679d769c5c009e7460"

# PARSERS names each ported class and the operating system folders whose
# fixtures it is held to. An ios folder is listed only where ios's class of
# the same name is iosxe's parser unchanged (a subclass adding nothing but
# an exclude list, which affects Genie's Ops diffing and never parsing).
PARSERS = [
    ("ShowVersion", ["iosxe", "ios"]),
    ("ShowInventory", ["iosxe"]),
    ("ShowIpInterfaceBrief", ["iosxe"]),
    ("ShowInterfaces", ["iosxe", "ios"]),
    ("ShowIpRoute", ["iosxe"]),
    ("ShowIpOspfNeighbor", ["iosxe"]),
    ("ShowIpBgpSummary", ["iosxe"]),
    ("ShowBgpSummary", ["iosxe", "ios"]),
    ("ShowVlan", ["iosxe", "ios"]),
    ("ShowCdpNeighbors", ["iosxe", "ios"]),
    ("ShowLldpNeighbors", ["iosxe", "ios"]),
    ("ShowArchiveConfigDifferences", ["iosxe", "ios"]),
]

# PARSER_ROOT is where genieparser keeps its parsers, under the checkout.
PARSER_ROOT = os.path.join("src", "genie", "libs", "parser")


def expected_literal(path):
    """Return the expected_output literal from one *_expected.py file.

    The file is parsed, never imported: the one assignment to
    expected_output is found in the syntax tree and its value converted
    with ast.literal_eval, which refuses anything but a literal.
    """
    with open(path, "r", encoding="utf-8") as f:
        tree = ast.parse(f.read(), filename=path)
    for node in tree.body:
        if isinstance(node, ast.Assign) and any(
            isinstance(t, ast.Name) and t.id == "expected_output" for t in node.targets
        ):
            return ast.literal_eval(node.value)
    raise SystemExit(f"{path}: no expected_output assignment")


def check_keys(value, path):
    """Refuse a dict key JSON would write ambiguously.

    Genie's expected dicts key by string, except a few that key by an int
    index (ShowIpRoute's next_hop_list). JSON writes an int key as its
    decimal string, which is exactly the key the Go port uses, since a Go
    map keyed by string is the only shape JSON can carry. A bool, float or
    None key would be written as a string nothing could tell apart from a
    real one, so it is refused.
    """
    if isinstance(value, dict):
        for k, v in value.items():
            if isinstance(k, bool) or not isinstance(k, (str, int)):
                raise SystemExit(f"{path}: key {k!r} is neither a string nor an int")
            check_keys(v, path)
    elif isinstance(value, (list, tuple)):
        for v in value:
            check_keys(v, path)


def copy_folder(src, dst):
    """Copy one equal or empty folder, converting expected files to JSON.

    Returns the number of *_output.txt fixtures it copied.
    """
    count = 0
    os.makedirs(dst, exist_ok=True)
    for name in sorted(os.listdir(src)):
        source = os.path.join(src, name)
        # genieparser's harness globs *_output.txt, which never matches a
        # hidden file, so a hidden fixture is one it does not run.
        if name.startswith("."):
            continue
        if name.endswith("_output.txt") or name.endswith("_arguments.json"):
            # Copied byte for byte: the Go test reads them exactly as
            # genieparser's harness does (text mode, universal newlines).
            shutil.copyfile(source, os.path.join(dst, name))
            if name.endswith("_output.txt"):
                count += 1
        elif name.endswith("_expected.py"):
            value = expected_literal(source)
            check_keys(value, source)
            target = os.path.join(dst, name[: -len(".py")] + ".json")
            with open(target, "w", encoding="utf-8") as f:
                json.dump(value, f, indent=1, sort_keys=True, ensure_ascii=False)
                f.write("\n")
        else:
            raise SystemExit(f"{source}: unexpected file in a fixture folder")
    return count


def main():
    """Copy every listed parser's fixtures and write the testdata README."""
    parser = argparse.ArgumentParser(description=__doc__.splitlines()[0])
    parser.add_argument("--src", required=True, help="genieparser checkout")
    parser.add_argument(
        "--out",
        default=os.path.join(os.path.dirname(os.path.abspath(__file__)), "..", "..", "pkg", "iosparse", "testdata"),
        help="pkg/iosparse/testdata",
    )
    args = parser.parse_args()

    head = subprocess.run(
        ["git", "-C", args.src, "rev-parse", "HEAD"], check=True, capture_output=True, text=True
    ).stdout.strip()
    if head != PINNED_COMMIT:
        raise SystemExit(f"checkout is at {head}, want {PINNED_COMMIT}")

    out = os.path.abspath(args.out)
    lines = []
    for cls, systems in PARSERS:
        shutil.rmtree(os.path.join(out, cls), ignore_errors=True)
        for osname in systems:
            base = os.path.join(args.src, PARSER_ROOT, osname, "tests", cls, "cli")
            counts = []
            for kind in ("equal", "empty"):
                n = copy_folder(os.path.join(base, kind), os.path.join(out, cls, osname, kind))
                counts.append(n)
            lines.append(f"| {cls} | {osname} | {counts[0]} | {counts[1]} |")

    with open(os.path.join(out, "README.md"), "w", encoding="utf-8") as f:
        f.write(
            "# genieparser fixtures\n\n"
            "Copied by `tools/geniefixtures/gen.py` from Cisco's genieparser "
            f"(https://github.com/CiscoTestAutomation/genieparser) at commit `{PINNED_COMMIT}`. "
            "Do not edit by hand: regenerate.\n\n"
            "Copyright (c) 2021 Cisco Systems, Inc. and/or its affiliates. Licensed under the "
            "Apache License, Version 2.0; see `../LICENSE.genieparser` and `../NOTICE.genieparser`. "
            "The fixtures are genieparser's, unchanged except that each expected result was "
            "converted from a Python literal to JSON.\n\n"
            "`skips.json` is not genieparser's: it lists, with a reason each, the fixtures "
            "the port does not pass.\n\n"
            "Each `*_output.txt` and `*_arguments.json` is genieparser's own file, "
            "unchanged. Each `*_expected.json` is the `expected_output` literal of "
            "genieparser's `*_expected.py`, converted to JSON; an int dict key "
            "(ShowIpRoute's `next_hop_list` index) becomes its decimal string, as "
            "JSON writes it.\n\n"
            "| Parser | OS | equal | empty |\n|---|---|---|---|\n" + "\n".join(lines) + "\n"
        )
    print("\n".join(lines))


if __name__ == "__main__":
    sys.exit(main())
