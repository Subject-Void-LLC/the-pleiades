# pr-check monitor

A live dashboard of `make pr-check`, every stage, plus the branch's GitHub
Actions runs, with a history of both. It draws with Apache ECharts and runs
on the Python 3 standard library alone. Nothing in the Makefile or CI calls
it.

## Run it

From the repository root, with the repository's virtual environment:

```bash
# Run pr-check under the monitor (capped, its own process group).
.venv/bin/python tools/monitor/monitor.py run [--cap 4500M] [--parallelism 1] [--port 8765] [--linger 120]

# Watch a run started elsewhere: start it with --trace and tee its output.
make --trace pr-check 2>&1 | tee /tmp/prcheck.txt
.venv/bin/python tools/monitor/monitor.py watch --log /tmp/prcheck.txt

# Replay a finished log (not saved to history).
.venv/bin/python tools/monitor/monitor.py watch --log old.txt [--pace 0.05]

# History and Actions only.
.venv/bin/python tools/monitor/monitor.py serve
```

Then open http://127.0.0.1:8765/. The server binds 127.0.0.1 only and
refuses a request whose Host header is not this machine's loopback.

Every command takes `--port` and `--no-actions`.

### run

`run` starts `env PR_CHECK_PARALLELISM=<p> ~/.local/bin/capped <cap> make
--trace pr-check` from the repository root in a new session (setsid, through
`start_new_session`), tees its output to the terminal and to the run's log,
and ends with pr-check's own exit status (128 plus the signal number when a
signal killed it). It refuses to start without `capped`.

- **Ctrl-C** sends SIGTERM to pr-check's process group, never `pkill -f`.
  pr-check's own trap then stops its legs and puts the lab VMs back. A second
  Ctrl-C sends SIGKILL, which may leave a VM running.
- **The watchdog** samples MemAvailable every 5 seconds. Below 700 MB it
  sends SIGTERM to the group and marks the run as stopped by the watchdog.
  If memory is still short 60 seconds later, it sends SIGKILL.
- It also reads swap in use when the run starts, and sends the same SIGTERM
  once swap has grown 3 GB past that, logging each 1 GB of growth to the
  run's log. MemAvailable alone missed the 2026-10-25 crash: swap filled all
  8 GB, load reached 600 and the sampler itself was starved, while
  MemAvailable never went under 700 MB. Growth in swap does not escalate to
  SIGKILL, since swapped pages stay out after the run stops.
- After the run ends, the dashboard stays up for `--linger` seconds (120 by
  default) so the final state can be read, or until Ctrl-C.

### watch

`watch` follows a log something else is writing and reads `.coverage/pr`
(or `--pr-dir`). Lines already in the log when it attaches all carry the
attach time, and the page says so. A log that already ends in pr-check's
summary is a replay and is never saved. Without `--log`, only `.coverage/pr`
is read, so the static checks and pass progress are not visible.

## Subscribing instead of polling

The run is the subject and anything watching it is an observer: each change of
a stage's status, and the run's end with its verdict, is published as a
numbered event. Two routes subscribe to them, and the page itself is one of
their subscribers (it redraws the moment a stage changes):

- **`/events`** streams them as Server-Sent Events (`event: stage`,
  `event: run`, then `event: closed` when the run has ended). `?since=<seq>`
  starts after an event already seen, and a browser reconnecting sends
  `Last-Event-ID`, so nothing is missed.
- **`/wait`** holds one request until an event answers it, then returns that
  event as JSON: `?for=fail,end` (the default; `fail` is a stage that failed or
  was stopped, `end` the run's end, `stage` any stage change, `any` everything),
  `?since=<seq>`, `?timeout=<seconds>` (3600 by default). A script, or an
  agent, waits for a gate's first failure with one call and no loop:

  ```sh
  curl -s 'http://127.0.0.1:8765/wait?for=fail,end&timeout=21600'
  ```

Both answer 404 when the monitor has no live run (`serve`).

## What it reads

- **The make log.** With `--trace`, GNU make prints `Makefile:NNN: update
  target 'X' due to: ...` or `Makefile:NNN: target 'X' does not exist` before
  each target's recipe; those start each static stage. `--trace` also prints
  the recipe text, which holds the very markers pr-check echoes later, so any
  line ending in a backslash, or following one, is skipped. Without
  `--trace`, static stages are found by their first command, and `fmt`
  (which prints nothing) is marked passed once make reaches pr-check.
- **pr-check's markers**, in order: `prscope:`, `pr-check: N container-free
  and M container package(s) against BASE`. In the newer order the fast pass
  runs next (its first package line starts it, timed from the count line),
  then `coverage-fast`, the fast pass's own coverage floors; a failing one
  prints `pr-check: the container-free packages miss a coverage floor ...`
  and every part not yet begun is marked not-run. Then the Mac's prepare lines
  (`maccheck: ...`), `pr-check: the mac tests run beside ...`,
  `pr-check: the bsd win tests run one after another ...`, the testgate passes
  (fast, repeat, containers; each `ok`/`FAIL` package line counts toward the
  pass, which ends at `testgate: all tests passed` or `exit status N`),
  `coverage-check:` lines, `testgate: durations.json ...`, the sizes step
  (`binsize: ./cmd/pleiades at ...` and one line per platform), `pr-check:
  waiting for the X tests` (the legs' output follows), and prreport's summary
  `pr-check at SHA, DATE: VERDICT (exit N)` with one row per part.
- **`.coverage/pr/records/*.json`.** A pass or leg record carries its real
  start and length, which replace the times the monitor saw (outlined bars
  on the page). Step records carry a verdict. `report.json` names the
  expected parts and, at the end, the verdict. Records are read only once the
  log shows pr-check rewriting `.coverage/pr`, and only when their commit
  matches.
- **`.coverage/pr/{mac,bsd,win}.log`**, tailed for each leg's latest line
  and its step bar (box, reachable, ready, ship, test, put back; the Mac has
  no box). A leg's package total is its own (`N package(s) at ...` or its
  record), not the fast pass's. Package output arrives only when the leg ends.
- **`.IGNORE/ci/binsize.jsonl`**, read only, for the Binary size panel: each
  platform's size over commits, compared only within one package, Go release
  and set of flags. A missing or unreadable ledger is reported on the page.
- **`/proc/meminfo`** for MemAvailable.
- **GitHub Actions**, every 60 seconds, for the checked-out branch: `gh api
  repos/Subject-Void-LLC/the-pleiades/actions/runs?branch=...` and the jobs of
  the newest five runs. The repository is in every path, since `gh api` has
  no `-R`. When gh is missing or logged out, the panel says why and the rest
  keeps working.

The log carries no timestamps, so every time is when the monitor read the
line, unless a record gave it. The page shows every time in the viewer's own
time zone and names it.

Each chart is created once per container and only updated with a merging
`setOption`. Replacing a chart's whole option on every poll (`notMerge`)
left its open tooltip behind each time, so old tooltips stacked up.

## Where it writes

Only under `.IGNORE/ci/monitor/` (ignored by git), directories 0700 and files
0600:

```
.IGNORE/ci/monitor/runs/<run id>/run.json     the run: stages, memory, verdict
.IGNORE/ci/monitor/runs/<run id>/log.txt      the run's log
.IGNORE/ci/monitor/runs/<run id>/report.json  a copy of report.json
.IGNORE/ci/monitor/runs/<run id>/<leg>.txt    a copy of each leg's log
.IGNORE/ci/monitor/actions.json               every Actions run seen, with jobs
```

Each file is written through a `.tmp` file beside it, renamed into place.
pr-check deletes `.coverage/pr` when it starts, so these copies are the only
lasting record of a run's report. Before any line is stored or served, the
value of every environment variable whose name contains TOKEN, SECRET,
PASSWORD, PASS, KEY or CREDENTIAL (six characters or longer) is replaced with
`[REDACTED]`. The monitor sets `sys.dont_write_bytecode`, so it leaves no
`__pycache__` in the tree.

## Tests

```bash
cd tools/monitor && ../../.venv/bin/python -B -m unittest -v
~/.local/bin/pyright tools/monitor     # every file is checked in strict mode
```

The fixtures in `testdata/` come from a real pr-check run, with hosts
replaced by documentation addresses (`someone@192.0.2.10`).
`prcheck-trace.txt` is that run's output placed after the trace and recipe
lines `make --trace -n pr-check` prints, since a real `--trace` run was not
recorded. The `gh-*.json` files are trimmed `gh api` responses.
