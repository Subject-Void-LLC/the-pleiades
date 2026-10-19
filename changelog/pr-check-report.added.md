`make pr-check` now ends with one report of the whole run, written to `.coverage/pr/records/report.json` and printed as its last lines. It has:
- **A line for each part of the run.** Each Linux pass, each macOS, FreeBSD and Windows check, and each other step, with its verdict.
- **Each failure that counts, with its own output.** Tests that passed when re-run alone are named apart from them.
- **Where a machine's check stopped, and why**, when it never reached its tests. A check with no machine named is shown as unset, not failed.

`go run ./tools/prreport` prints the report again. Its flags:
- `-json` prints the same model as JSON.
- `-failures` prints only the failures that count.
- `-skips` prints every skipped test, grouped by reason.
- `-leg NAME` prints one part of the run.

The report holds pr-check's own exit status, so it never shows a pass for a run the gate failed.

To support it, `tools/testgate` and the three OS checks take `-report-out`, which writes a record of the run however it ends.
