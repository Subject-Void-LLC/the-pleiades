# Design note: the `gate` collection

Status: built (gate.run and gate.list, 2026-10-15). `gate.run` runs a project's own checks on a device and returns one typed
result per check; `gate.list` shows how the checks are classified without running anything.

## Methods

| Method | Does | Site, device |
|---|---|---|
| `gate.run` | Reads a gate manifest from the device, runs the selected checks with bounded parallelism, returns one result per check (PASS, FAIL or SKIP, with the reason, duration and a bounded tail of a failure's output). Any FAIL fails the task. | target, required |
| `gate.list` | Reads and validates the same manifest and returns each check's classification (name, kind, heavy, serial, timeout, where it runs). Runs nothing. | target, required |

## The manifest

A YAML file in the project on the device, named by the task's `manifest` parameter (bounded at 1 MiB, decoded
strictly: an unknown field or a duplicate key is refused).

```yaml
version: 1
kinds:                        # optional: kinds beyond the built-in pure, db, network, model
  - name: browser
    description: needs a headless browser
defaults:
  timeout: 30m                # required here or on every check: there is no implicit timeout
  parallelism: 2              # optional; 1 when absent
checks:
  - name: unit
    kind: pure
    command: [go, test, ./...]   # an argument vector, quoted by the platform
    dir: .                       # relative to the manifest's directory
    requires:                    # tested before the check starts; unmet means SKIP, naming which
      commands: [go]
      env: [GOFLAGS]             # presence only; a value is never read into a result
  - name: lint
    kind: pure
    shell: make lint             # or one shell string, run by sh -c, the project's own command
    skip_exit: [5]               # exit codes this check uses to mean "skipped"; none by default
  - name: migrations
    kind: db
    heavy: true                  # at most one heavy check runs at a time
    serial: true                 # runs with nothing else running
    timeout: 20m
    image: docker.io/library/postgres@sha256:...   # runs in a fresh container instead of dir
```

An entry with no `kind`, or with a kind neither built in nor declared, is a validation error, and so is a duplicate
or malformed `name`, an entry with both `command` and `shell` or neither, an entry with both `image` and `dir`, and a
check with no timeout of its own when the manifest sets no `defaults.timeout`. The whole manifest is refused before
anything runs.

`requires` takes `commands` (on the PATH of the shell the check runs in), `env` (variables set, by name), `files`
(paths that exist, relative to the check's directory), `os` (`linux`, `darwin`, `freebsd`), `engine: true` (the
device's container engine answers) and `tcp` (`host:port` endpoints that accept a connection, from the device).

## Parameters

`gate.run`: `manifest` (required), `kinds` (default: every kind), `checks` (names, default: all selected by kind),
`heavy` (`include`, `exclude`, `only`; default `include`), `parallelism` (overrides the manifest's; 1 when neither
sets it, at most 16; heavy checks always run one at a time and serial ones alone), `fail_on_skip` (default false). `gate.list` takes `manifest`, `kinds` and `checks`, and marks which checks a run would select.

## Results

Each check yields `{name, kind, status, reason, duration_ms, tail}`.

- **PASS:** exit status 0.
- **FAIL:** a non-zero exit, a timeout, a lost session, or an engine refusal, with the reason stated.
- **SKIP:** the check was not selected (the reason names the kind or the filter), a declared precondition was not
  met (the reason names it: "needs command psql", "needs env DATABASE_URL"), the check exited with a code its own
  `skip_exit` lists (the reason is its last output line, bounded and escaped), or the run was canceled or reached
  its deadline before the check started. No exit code means SKIP unless the check declares it: 77, the automake
  skip code, is also the sysexits "permission denied" code, and reading it as SKIP would hide a real failure.

A SKIP is reported as a SKIP in the result and the summary counts, never folded into PASS; with `fail_on_skip`
it fails the task. The tail is the last 40 lines and at most 8 KiB of a failing check's combined output, with
anything a terminal would act on escaped and the run's secrets masked, as every result is.

## Capabilities and transports

| Runs | Capability | Transport |
|---|---|---|
| `dir` checks | `CommandExecCapable` | ssh |
| `image` checks | `ContainerEngineCapable` | the engine port (socket, stdio or TLS, as onboarding found) |

Both methods require `CommandExecCapable`, since the manifest is read over SSH, so the device is one that runs
commands: a `linux_server` (onboarding records its container engine, when it runs one) or a generic SSH device. A
`container_host` is an engine reached over SSH, not a host that runs a project's commands, and cannot run either.
A manifest is validated against the device before anything runs, so a manifest with an `image` check on a device
with no engine is refused whole rather than failing that one check. The device's `timeout` command must take `-k`
and a duration (coreutils, or BusyBox 1.30 or later); `gate.run` tries it in that form before running anything.
An image check may require only what is tested from the device (`tcp`, `engine`); its commands, environment,
files and operating system are the image's.

## Check and Reversibility

| Method | Check | Reversible |
|---|---|---|
| `gate.run` | Reads and validates the manifest, resolves the selection and the device's capabilities, then answers **unchecked** (`CannotCheck`): a check is a command, and whether it passes is only known by running it. A manifest that would be refused fails the check. | No: it runs the project's checks and changes nothing of its own; anything a check writes is the check's doing, and the result says which commands ran. |
| `gate.list` | Same as Invoke (it only reads). | Not applicable: read-only. |

## Decisions, by edge case

- **Where checks run: the target device only.** Running on the machine that runs pleiades would read its own
  checkout and credentials, which the device model forbids; a project checked out on a device is reached over the
  device's transport like any other.
- **Every check has a timeout, and the manifest states it.** No default fits checks that run from seconds to over
  half an hour (this repository's own suites: 2 to 40 minutes), and the engine's task timeout is none unless a
  launch sets one, so an implicit default would either fail real suites or let a hang run forever. Each result
  records its duration, for choosing the number. Timeouts are enforced on the device, by wrapping each command in
  `timeout --kill-after=10s` (coreutils and busybox both have it), because closing an SSH session does not stop the
  process it started. The task's own deadline still bounds the run: a check still running when it passes is a FAIL
  naming the deadline, and checks not yet started are SKIP.
- **Serial unless the manifest or the task says otherwise.** Checks are often parallel inside (go test, pytest
  workers), memory rather than CPU is what runs out, and this repository's own load-induced failures came from
  running container work side by side; speed is one line once a project knows its checks' footprint.
- **SKIP comes from declared preconditions first.** That is how this repository's own tests skip (a missing
  Docker, Landlock, a tool or a live target's variables), and the gate states the reason itself rather than reading
  it from a command's output.
- **`image` checks need a digest-pinned image** and run with no network and a read-only root, unless the entry
  declares `network:` (a named network) or `writable: true`. The image is not pulled implicitly unless the entry
  says `pull: true`. This is the policy `container.run` follows, for the same reasons.
- **No secret reaches a check.** Checks get no environment from the platform and no credential; a check that needs
  one reads it the way the project already does on that device.
- **Output is kept as a tail, not refused.** A check's output is streamed through a ring buffer of the last 40 lines
  and 8 KiB, so a chatty test suite neither fails the transport's bound nor fills memory; the full output stays on
  the device if the check writes it there.

## Reuse

`pkg/gate` (new) holds what `tools/testgate` and `gate.run` share and neither depends on go test for: the manifest
types, the result and summary model, the skip ledger (moved from `tools/internal/flakegate`'s `SkipLedger`), and
a bounded tail (generalized from flakegate's `FailureOutput`). `tools/testgate` keeps its `go test -json` parsing
and re-run-alone logic and calls the shared pieces. A fresh-container run (create, start, wait, logs, remove) is
added to `pkg/containerengine`, the one place a container is reached.

## Failure modes

| Failure | Answer |
|---|---|
| Manifest missing, unreadable, over 1 MiB, invalid YAML, an unknown field or duplicate key | Task fails before any check runs, naming the file and the first problem. |
| An entry with no kind, an unknown kind, a duplicate or malformed name | Validation error, whole manifest refused. |
| A selection that names an unknown kind or check, or selects nothing | Refused: a gate that runs nothing must not read as passed. |
| A check exceeds its timeout | Killed on the device; FAIL "timed out after 20m", with its tail. |
| The SSH session drops mid-check | That check FAILs naming the transport; the rest still run on a fresh session if the device answers. |
| The engine refuses or the image is absent and not pullable | That check FAILs naming the engine's answer. |
| The task is canceled | Running checks are stopped; FAIL for those, SKIP ("canceled") for the rest. |
| The run fails | The task's error carries each failing check's reason and its last 15 lines, then the skips, bounded at 16 KiB, and the task's returns (every check's result) are reported with the failure. |
| Output past the tail | Earlier lines dropped, counted in the result ("… 1,204 earlier lines left out"). |
