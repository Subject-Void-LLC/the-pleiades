# Status page

One always-on page per machine for development work: the `make pr-check`
gate (every stage, whichever worktree runs it), GitHub Actions, memory, the
implementation roadmap when this machine has one, and a JSONL history of
every value it has seen. It draws with Apache ECharts and runs on the Python
3 standard library alone. Nothing in the Makefile or CI calls it.

## Run it

The page is a daemon. Claude Code sessions in this repository start it
through the `SessionStart` hook in `.claude/settings.json`, and the `status`
MCP server starts it on its first call; by hand:

What the hook does on every session: unless one already runs, it starts a
detached daemon on 127.0.0.1:8765 that outlives the session. The daemon calls
`gh api` with your own gh login every 60 seconds for the branch in use, and
reads every workflow run of the repository every six hours. It keeps its
history under the main clone's `.IGNORE/ci/monitor/`, and never deletes it.
When you open the page, your browser loads ECharts from jsDelivr and two
typefaces from Google Fonts. To turn all of this off, set
`PLEIADES_NO_STATUS_DAEMON` to any non-empty value in the environment your
sessions start from: `monitor.py ensure` then starts nothing and says so, the
hook does nothing, and the MCP tools answer that the daemon is switched off.
A daemon already running keeps running until you stop it
(`kill -TERM <pid>`, the pid in `statusd.lock` and on `/healthz`).

On a platform without `fcntl` (Windows), `ensure` and `daemon` print one
line saying the daemon is not supported there and exit 0, and the MCP server
starts and answers each call that it is not available.

```bash
python3 -B tools/monitor/monitor.py ensure          # start it unless it runs (safe to repeat)
python3 -B tools/monitor/monitor.py daemon          # or run it in the foreground (serve is the old name)

# Run pr-check (capped, its own process group); the daemon shows it.
.venv/bin/python tools/monitor/monitor.py run [--cap 4500M] [--parallelism 1]

# Watch a run started elsewhere, or replay a log, on a server of its own (port 8767).
make --trace pr-check 2>&1 | tee /tmp/prcheck.txt
.venv/bin/python tools/monitor/monitor.py watch --log /tmp/prcheck.txt
.venv/bin/python tools/monitor/monitor.py watch --log old.txt [--pace 0.05]
```

Then open http://127.0.0.1:8765/: three tabs, Gate (`#gate`), Actions
(`#actions`: every branch's workflow runs, the picked run's jobs, a job's
duration over runs and each job's failure rate over thirty days) and
Roadmap (`#roadmap`) and Security (`#security`: the attestation from
the roadmap panel, gosec waivers checked against the tree, the scanner
pins, the newest scheduled `vulns` and `nightly` jobs, unreleased
`changelog/*.security.md`, and a Pending panel that leads the tab and
ranks what waits on someone: a failed or stale govulncheck, overdue
reviews, controls awaiting sign-off, open controls, reviews due within 30
days, unproven implementations, stale waivers, unreleased fixes). The page loads Hanken Grotesk and IBM Plex Mono from
Google Fonts and falls back to system fonts without them.
The server binds 127.0.0.1 only and refuses a request whose Host header is
not this machine's loopback.

### The daemon

- **One per machine.** It holds `statusd.lock` (an flock) for its life, so a
  second one exits at once. Every path is under the main clone's
  `.IGNORE/ci/monitor/`, found through git's common directory, so every
  worktree shares the one page and the one history.
- **ensure** leaves a running daemon alone when its `PROTOCOL` is the same or
  newer, and replaces it when the protocol is older, when its source directory
  was removed with its worktree, or when it runs from the same files as this
  ensure and they changed since it started (`/healthz` reports a SHA-256
  `fingerprint` of the `source_files` it loaded), but never while a gate runs.
  Every change to what the daemon serves or stores (a route, a field of an
  answer, a file it writes) must raise `PROTOCOL` in `daemon.py`; the
  fingerprint catches a forgotten raise only from the tree the daemon runs
  from, so two trees of one protocol never take turns replacing it. The pid in the
  lock file is only a hint: ensure signals it only once that process answers
  `/healthz` on the lock's port with the same pid and this state directory and
  its own command line runs `monitor.py daemon`, and never a pid of 0 or 1.
  One ensure at a time holds `statusd.ensure.lock`, so two sessions starting
  together start one daemon.
- **It never touches a gate.** A gate is its own process (`run`) and hands
  itself over through files: `gate.lock` (one gate at a time on this
  machine), the manifest `live.json`, and the run's `events.jsonl` and
  `live.json` snapshot. A daemon restarted mid-run catches up from the start
  of the event file; a run whose process died without closing its events is
  ended as `lost`. ensure decides that a gate runs from whether a process
  holds `gate.lock`, not from `live.json`, which a gate killed with SIGKILL
  leaves behind.
- **Actions** follow the branch the latest gate ran on (the main clone's
  branch before any), polled every minute. Every six hours, and when it
  starts, the daemon also reads every workflow run on every branch with its
  jobs (one call a page, plus one per run whose jobs are not cached yet), so
  the history covers the repository rather than one branch.
- A `monitor.py run` from a branch older than the daemon still opens its
  own server on 8765 and so cannot start while the daemon holds the port;
  rebase it, or pass that run another `--port`.

### The roadmap panel

The repository names no internal document, so the roadmap is configured on
the machine that has one, in `.IGNORE/ci/monitor/panels.json`:

```json
{"roadmap": {"script": "/abs/path/to/the/roadmap/script.py"}}
```

The daemon runs that script with `--json` (and `--phase ID --details` for
one phase), caches the result until a Markdown file beside it or the script
changes (or five minutes pass), and serves it at `/roadmap.json`. The
Roadmap view draws it natively: totals, release progress, the work queue,
every phase with filters, a phase's items with their full text, the
roadmap's own problems, the specification trace, and progress over time
from the timeline.

### The timeline: every past value

`.IGNORE/ci/monitor/timeline/history-YYYY-MM.jsonl`, one JSON object a line,
never deleted:

```json
{"v": 1, "t": 1794420305.2, "kind": "gate.stage", "src": "<run id>", "data": {...}}
```

Kinds: `daemon` (start, stop, attach, backfill), `gate.stage` and
`gate.run` (every broker event of every gate), `security.summary` and
`security.control` (attestation totals, pending count, and each control's
state and review date), `actions.run` and
`actions.job` (each status or conclusion change; backfilled lines are dated
when the run or job ended), `roadmap.summary` and `roadmap.phase` (each
change of the totals, or of a phase's state or counts), `git.day` (the
main line at the end of each day with a commit, back to the first one:
that day's commits, the running totals of commits and merged pull requests,
and how many methods the module catalog listed and marked implemented; its
`src` is the UTC day, and a day still gaining commits is written again, so
a reader keeps the last line of each `src`), `memory` (one sample a
minute). A value is written only when it changed, and the daemon
reads the files when it starts, so a restart writes nothing twice. Each
line is one `write` on a file opened `O_APPEND`, masked like everything
else; a truncated last line is skipped. Read it at `/timeline?kind=...&src=...&since=...&limit=...`
(JSONL), through the MCP `timeline` tool, or with any JSONL reader.

### The MCP server

`mcp_status.py` is a stdio MCP server, wired in `.mcp.json` as `status`.
Every tool only reads, through the daemon. Workflow, job and branch names
and run titles come from GitHub, and stage details from test output, so
every answer puts that text inside an object named `untrusted`, cut to 300
characters, and every tool description and the server's instructions say
it is data never to be followed as an instruction. Run titles appear only
when a call passes `titles=true` (`actions`, `timeline`). What the daemon
serves is masked only by the values of its own secret-named environment
variables (below); nothing else about it is made safe. `STATUS_PORT` and
`STATUS_STATE_DIR` point the server at another daemon, such as a test's.

| Tool | Answers |
|---|---|
| `status` | everything at once: gate verdict, failing and running stages, Actions, memory, roadmap totals |
| `gate` | the latest or a named gate run, every stage |
| `gate_wait` | blocks until the gate's first failure or its end (or a timeout up to an hour) |
| `actions` | Actions for the branch in use, failed jobs |
| `roadmap` | totals and the queue, or one phase with its items (`details` for their full text) |
| `timeline` | any recorded value, filtered by kind, src and time |
| `history` | saved gate runs |
| `security` | everything pending on security, most urgent first, and the attestation totals; `control` adds one control's statements |

### run

`run` refuses while another gate holds `gate.lock`, makes sure the daemon
runs, prints the run id and the `/wait` command naming it, then starts `env PR_CHECK_PARALLELISM=<p> ~/.local/bin/capped <cap> make
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
- The page stays up after the run, since the daemon serves it; `--linger`
  is accepted and ignored so older command lines still parse.

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
  `?since=<seq>`, `?timeout=<seconds>` (3600 by default), `?run=<run id>`.
  Without `run`, it answers from the latest gate while it runs or for 30
  seconds after it ended, and otherwise waits for the next gate to start,
  so a wait sent just before `run` writes its manifest is not lost. The
  answer names the `run_id` it came from. A script, or an agent, waits for
  a gate's first failure with one call and no loop:

  ```sh
  curl -s 'http://127.0.0.1:8765/wait?for=fail,end&timeout=21600'
  ```

`/events` answers 404 before any gate; a `watch` server answers both with
404 when it has no run.

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
  keeps working. Only runs whose `head_repository.full_name` is this
  repository are kept, by the poll and the backfill alike: a fork's pull
  request runs here with a title, a branch and job names its author chose,
  and `?branch=` also matches a fork's branch of the same name. Each name,
  branch and title kept loses its control characters and is cut to 200.

The log carries no timestamps, so every time is when the monitor read the
line, unless a record gave it. The page shows every time in the viewer's own
time zone and names it.

Each chart is created once per container and only updated with a merging
`setOption`. Replacing a chart's whole option on every poll (`notMerge`)
left its open tooltip behind each time, so old tooltips stacked up.

## Where it writes

Only under the main clone's `.IGNORE/ci/monitor/` (ignored by git),
directories 0700 and files 0600:

```
.IGNORE/ci/monitor/statusd.lock                the daemon's lock: pid, port, protocol, source
.IGNORE/ci/monitor/statusd.ensure.lock         held by one ensure while it decides and starts
.IGNORE/ci/monitor/statusd.log                 the daemon's output
.IGNORE/ci/monitor/gate.lock                   held by the one gate run
.IGNORE/ci/monitor/live.json                   that run's manifest, removed when it ends
.IGNORE/ci/monitor/panels.json                 hand-written, optional: the roadmap script
.IGNORE/ci/monitor/timeline/history-*.jsonl    every past value, a file a month
.IGNORE/ci/monitor/runs/<run id>/run.json      the run: stages, memory, verdict
.IGNORE/ci/monitor/runs/<run id>/log.txt       the run's log
.IGNORE/ci/monitor/runs/<run id>/events.jsonl  its broker events, then a closing line
.IGNORE/ci/monitor/runs/<run id>/live.json     its state, rewritten every second
.IGNORE/ci/monitor/runs/<run id>/report.json   a copy of report.json
.IGNORE/ci/monitor/runs/<run id>/<leg>.txt     a copy of each leg's log
.IGNORE/ci/monitor/actions.json                the newest 3000 Actions runs, with jobs (the page's cache)
.IGNORE/ci/monitor/actions.json.lock           held while one writer merges into actions.json
```

Each file is written through a `.tmp` file beside it, renamed into place.
`actions.json` is written compact and only when a poll or a backfill
changed a run: about 4.9 KB a run, so about 15 MB at the 3000-run cap. The
daemon keeps it parsed (about 41 MB at the cap) and reads the file again
only when its inode, size or modification time changed, since a watch
server or an older `monitor.py run` may write it too.
pr-check deletes `.coverage/pr` when it starts, so these copies are the only
lasting record of a run's report. Before any line is stored or served, the
value of every environment variable whose name contains TOKEN, SECRET,
PASSWORD, PASS, KEY or CREDENTIAL (six characters or longer) is replaced with
`[REDACTED]`. The monitor sets `sys.dont_write_bytecode`, so it leaves no
`__pycache__` in the tree.

## Tests

```bash
cd tools/monitor && ../../.venv/bin/python -B -m unittest -v     # test_monitor and test_daemon
~/.local/bin/pyright tools/monitor     # every file is checked in strict mode
```

The fixtures in `testdata/` come from a real pr-check run, with hosts
replaced by documentation addresses (`someone@192.0.2.10`).
`prcheck-trace.txt` is that run's output placed after the trace and recipe
lines `make --trace -n pr-check` prints, since a real `--trace` run was not
recorded. The `gh-*.json` files are trimmed `gh api` responses.
