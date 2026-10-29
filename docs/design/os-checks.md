# Design note: testing on macOS, FreeBSD and Windows before a push

Status: built. `make mac-check` (tools/maccheck) on 2026-10-16; `make bsd-check` (tools/bsdcheck) and
`make win-check` (tools/wincheck) on 2026-10-18. These are developer tools, and dogfooding too: every machine is
driven through The Pleiades itself, so what The Pleiades cannot yet do for one is written down below as a gap to
build.

## Why

A Linux machine cannot show what another operating system lacks or does differently:

- **macOS.** Its BSD userland lacks GNU-only tools and flags: GNU's `timeout -k`, GNU's argument permutation
  (`chmod 700 -- dir`), `stat -c`. CI's `fast-macos` job finds these, but only after a push, and three did reach
  it (FAILURE_PATTERNS 420, 476, 494).
- **FreeBSD.** Its userland, kernel and file system are FreeBSD's own, and no CI job runs FreeBSD at all.
- **Windows.** Paths, files that cannot be removed while open, and a missing POSIX tool. CI's `ci` job builds and
  vets for Windows but runs no tests there.

The first run on Windows found a real one: testgate's own live tests asked for `-race` unconditionally, which
cannot build on a Windows machine with no C compiler (`-race requires cgo`).

## What runs

All three run the same packages: the branch's container-free packages, the ones `make pr-check` runs on Linux.
Each runs them through `tools/testgate` as pr-check does, the full pass and then three strict repeats. Windows
runs them with the race detector only when the machine has `gcc`, which the race detector and cgo both need
there; without one it runs `testgate -race=false`. Each
checks out the committed tree (never the working tree) into a directory of its own on the machine, and keeps the
newest three.

The lab's win-check has WinLibs' MinGW-w64 GCC at `C:\mingw64` (`examples/virtualbox_lab`'s
`check-windows-02-tools.yaml`, fetched against the release asset's digest), so its runs have cgo and the race detector on. Where `go env
CGO_ENABLED` is `0` (Go turns cgo off on a machine with no C compiler), go-sqlite3 links a stub, and
every test that opens a SQLite database fails on its error ("go-sqlite3 requires cgo to work. This is a stub")
before it checks anything. testgate reports such a test as a skip, `needs cgo: ...`, in the skip ledger and the
record's `missing`, never as a pass: a test that failed for any other reason, or beside a subtest that did, still
fails, and a package passes only when every failure in it was the stub. The production binaries that open SQLite
(the Controller and the Runner) are built with cgo on, in their Dockerfiles; the `pleiades` CLI links the driver
but never opens a database.

What the three share is in `tools/internal/oscheck`:

- the settings (the environment, or a gitignored file in this worktree or the clone's main worktree);
- the commit;
- driving the machine with this branch's own `pleiades`, built under the worktree's git directory;
- the flow of a run in phases.

## The machines

| | Mac | FreeBSD | Windows |
|---|---|---|---|
| Named by | `PLEIADES_MAC` in `tests/mac/.env`: an ssh target | `PLEIADES_BSD_PROJECT` and `PLEIADES_BSD_DEVICE` in `tests/bsd/.env` | `PLEIADES_WINRM_PROJECT` and `PLEIADES_WINRM_DEVICE` in `tests/win/.env` (the WinRM release gates' names) |
| As a device | `generic_ssh`, in a private project under `.git`, credential from ssh's identity files | a device of a project that already manages it (the VirtualBox lab's `bsd-check`), with that project's credential | a device of a project that already manages it (the lab's `win-check`), with that project's credential |
| Liveness | a TCP probe of its SSH port, five seconds | `wait.connection`, twenty seconds | `wait.connection`, twenty seconds |
| Power | always up | a VM, started when a run needs it and saved afterwards (`PLEIADES_BSD_VM_HOST`) | a VM, started when a run needs it and saved afterwards (`PLEIADES_WIN_VM_HOST`) |
| Ready means | Darwin, with `go`, `git` and `caffeinate` | FreeBSD, with `go`, `git` and `cc` | Windows, with `go`, `git` and `tar.exe` |
| Tree sent by | `file.copy`, unpacked by `archive.extract` | `file.copy`, unpacked by `archive.extract` | `win.file.copy`, unpacked by `tar.exe` through `exec.winrm.shell` |
| Tests run by | `exec.command`, under `caffeinate -dims` | `exec.command` | `exec.winrm.shell`, each pass under `cmd.exe` so its streams join in order |
| Stopped by | the run's process group and process tree (`pgrep -P`), written on the machine | the run's process group and process tree, written on the machine | `taskkill /T` of the run's process, written on the machine |

Every value a Windows script needs reaches PowerShell through `exec.winrm.shell`'s `env`, never inside a script's
text, so each script is a constant. On the Mac and FreeBSD the tests run as a job of their own, which their shells'
job control puts in a process group; a stop ends that group and then every process under the run, read with
`pgrep -P`, which also covers a shell with no job control (Linux's dash, without a terminal), where the scripts'
own tests run. The run's pid file is removed when it ends, so a later stop cannot reach a process that reused the
number.

The VirtualBox lab's two boxes, both made with Pleiades runbooks:

- **`win-check`:** Windows Server 2025 Core, one CPU and 2 GB. Go is at `C:\go` (fetched with `win.file.download`
  against go.dev's digest), MinGit at `C:\mingit` and WinLibs' GCC at `C:\mingw64` (each fetched against its
  release's digest).
- **`bsd-check`:** FreeBSD 15.1, one CPU and 4 GB, cloned from the never-booted base. It started at 1 GB, which
  could not compile the AWS SDK's `ec2` package (3.2 GB at its peak, 3.7 GB with `-race`): with a 2 GB swap file
  added it paged for 54 minutes and FreeBSD still killed `compile`. Its first boot installs `git` and `sudo`
  (`virt.vbox.vm.clone`'s `packages`, which FreeBSD's nuageinit reads as cloud-init does). Go is in the login's
  home, sent with `file.copy` and unpacked with `archive.extract`, so it needs no root.

## A box's power

The lab's boxes are VMs on one workstation, which cannot hold every lab VM's memory at once (`bsd-check` alone
takes 4 GB). So a check whose machine is a VM, named by `PLEIADES_BSD_VM_HOST` or `PLEIADES_WIN_VM_HOST` (the
VirtualBox host in the same project) and optionally `PLEIADES_BSD_VM` or `PLEIADES_WIN_VM` (the VM's name there,
the device's by default), manages its power:

- **Before the run** it reads the VM with `virt.vbox.vm.info`; one that is not running is started with
  `virt.vbox.vm.start` and waited for with `wait.connection`, up to ten minutes. A shut-down VM boots cold, about
  two minutes at four CPUs.
- **When the run ends**, passed, failed or interrupted, a VM this run started is put back with `virt.vbox.vm.stop`
  `mode: acpi` (five minutes for the guest to shut down on its power button), then `mode: poweroff` if it did not:
  the host has its memory back, and a powered-off VM's disks can be compacted, which a saved one's cannot (owner,
  2026-10-29). A VM that was already running is left running.
- **A second disk for variable data.** Each box keeps every run's trees, Go build and module caches and temporary
  files on a data disk, apart from its system disk: `virt.vbox.vm.attach_disk` gives the VM the disk, a guest-side
  runbook formats and mounts it (`/data` on FreeBSD, `D:` on Windows), and `PLEIADES_BSD_DATA` or
  `PLEIADES_WINRM_DATA` names the directory to the check, which then builds, caches and writes temporary files
  there and wipes its leftovers before each run. The system disk stays small, and the data disk can be compacted
  or thrown away and made again (`virt.vbox.vm.detach_disk` with `delete`) with the VM shut down.
- **Split phases** hand the box over in the ready file: a `-phase prepare` that started it leaves it running and
  says so there, and the `-phase test` given that file puts it back.
- `virt.vbox.vm.start` refuses a VM the host has no room for, keeping 1 GB for itself, and says to stop another.

## In pr-check

Each machine runs beside the Linux passes, not after them:

- **The Mac: prepare here, test in the background.** `-phase prepare` checks it, builds `pleiades` and sends the
  tree; `-phase test` then runs on the Mac's own CPU and memory while the Linux passes run here.
- **One policy for memory and disk on every machine.** Each check's readiness step makes room from what is the
  check's own and refuses a machine under 4 GB free; then the `testgate` pass on the machine applies the same
  policy every environment runs under (`tools/internal/headroom`): `-p` lowered to what the machine's free
  memory fits, the run refused on a disk under 4 GB, the run followed and stopped before the machine runs out,
  and each package's cost written into the record (`headroom`) the check reads back.
- **What a check no longer runs.** govulncheck on the Mac (`-phase vuln`), the Mac's share of the container
  packages on its Podman (`-containers`) and the Windows ARM VM's leg were cut from pr-check on 2026-10-29: the
  gate holds to the Crawl and Walk tiers, and `make ci` and CI's `ci` job run govulncheck on every pull request.
  `go run ./tools/wincheck -arm64` still runs the ARM check by hand.
- **The VMs: one at a time, in the background.** FreeBSD's check runs whole, its box started and put back, then
  Windows', so at most one box holds the host's memory (`LAB_CHECKS`).
- Each machine's output goes to `.coverage/pr/<mac|bsd|win>.log`.
- **Report.** Each log is printed whole once the Linux passes end. Each result counts like any other step's, and a
  machine that is not named is reported as not checked.
- **Interrupt.** An interrupt, or pr-check ending early, stops every check, and each check stops its tests on its
  machine too and puts back a box it started.
- **Parallelism.** The Mac runs `PR_CHECK_PARALLELISM` packages at once. The other two use go test's default for
  their own CPUs (`BSD_CHECK_PARALLELISM`, `WIN_CHECK_PARALLELISM`).

## The record of a run

A pr-check run printed several logs one after another, and reading one meant finding each pass's verdict, each
failure's output and each machine's outcome in that text. Each part of the run now also leaves a record in
`.coverage/pr/records`, and `tools/prreport` merges them into `report.json` there:

- **A testgate pass** (`-report-out`) records its arguments, packages, time and verdict, each failure with its
  outcome (failed, contention or not re-run) and the end of its own output, masked, and every skip with its reason.
  It writes one however the run ends; a pass whose record cannot be written fails.
- **A machine's check** (`-report-out`) records its leg: the machine, the commit, how it ended and, when it did not
  pass, the step it stopped at (settings, box, commit, reachable, ready, ship or test) and why. It reads back the
  records the machine's own two testgate passes wrote and carries them. A check with no machine named records that,
  at the settings step, which the report shows as unset rather than failed.
- **Every other step** (the served-images check, the coverage floors, the durations check) records its exit status,
  or why it did not run (`prreport step`).

The merge is given pr-check's own exit status, so a report never passes a run the gate failed: a failure no record
explains is named as one. It prints a line for each pass, leg and step, then each failure that counts with its
output, the tests that passed when re-run alone, and how many were skipped.

```sh
go run ./tools/prreport                  # the run again, as text
go run ./tools/prreport -json            # the same model as JSON
go run ./tools/prreport -failures        # only the failures that count
go run ./tools/prreport -skips           # every skip, grouped by reason
go run ./tools/prreport -leg win -json   # one pass, leg or step
```

A report for a commit other than HEAD says so. The report decides nothing: pr-check's exit status is still the
gate.

### The report as a contract

The report is meant to be read by tools as well as people: a dashboard, or an agent diagnosing a failed run. Its
shape is a contract, published as `docs/reference/schemas/pr-check-report.json` and generated from the Go model's
own comments (`tools/internal/prreport`), so it cannot drift from the code.

- **Versioning.** `schema` is the major version, now 2. It goes up only for a change an existing reader could
  misread: a field removed or renamed, or given another type or meaning. Within a major version the producer only
  adds optional fields, and new values to the open sets (verdicts, outcomes, steps, problem codes).
- **What a reader does.** It ignores a field it does not know. It treats a verdict, outcome or step it does not know
  as not a pass, and shows it as it is. It refuses a major version it does not know. The schema is open to match:
  objects take properties it does not declare, and known values are published as examples, not as a closed enum.
- **Verdicts.**
  - `pass`: every expected part has a record, every record was read, every value in them is known, and pr-check
    exited 0.
  - `fail`: a part failed, could not finish, or never ran, or pr-check failed. Two cases are said rather than
    failed: a check with no machine named, and a step this machine could not make.
  - `incomplete`: nothing failed, but something cannot be read as a pass.
  - `running`: written as pr-check begins. It is not a result, and one still saying so after its run is gone is an
    abandoned run.
  - `interrupted`: the run stopped before its end, by a signal or an early exit.
- **Problems.** Each problem has a stable `code` for a tool to act on, the `record` it is about, and a `detail` for
  a person. The codes:
  - `unreadable`, `unknown-kind`, `unknown-schema` and `unknown-value`: a record the merge could not read as it is;
  - `missing`: an expected part with no record;
  - `gate-failed` and `gate-disagrees`: pr-check's own exit status disagrees with the records;
  - `interrupted`: the run stopped before its end.
- **Missing parts.** pr-check begins by writing a `running` report naming every pass, leg and step it will run
  (`prreport begin`). The merge names each one with no record in `missing`. So a leg that died without writing
  anything shows as missing, never as absent and therefore passing.

Each of these cases has a test that drives it through the real producers. The schema test checks a complete report
against the schema strictly: every property it declares must appear with a value, and none it does not declare.

## Gaps found by driving them through The Pleiades

Each is something a Pleiades user doing the same would meet.

| Gap | Status |
|---|---|
| Onboarding granted a Darwin kernel shell exec only. | Closed 2026-10-16: a Darwin kernel is granted POSIX file access. |
| No method moved a local file or tree to a device. | Closed on the command line 2026-10-16 (`file.copy`'s `src`) and for Windows 2026-10-18 (`win.file.copy`). The Walk tier still refuses `src`, since a Runner cannot see the project. |
| No timeout: `exec.command` had no timeout parameter, and a runbook task had no timeout key. | Closed 2026-10-18: a task's `timeout:` key, Ansible's, and `pleiades adhoc --timeout`. An SSH command is sent `SIGTERM` when the timeout runs out, so the work stops on the device too. The FreeBSD and Mac test runs are bounded at four hours. |
| A failed `exec.command` kept only an excerpt of its output, so each check's script always exited 0 and printed its status after a marker. | Closed 2026-10-18: a failed task keeps the exit status and output its method recorded, on both tiers and through the external Collection protocol, and each script now exits with the tests' own status. |
| `pleiades run` and `adhoc` return a command's output only when it ends. | Open: a run of ten to twenty minutes is silent until it ends. |
| No method unpacks an archive on Windows. | Open: `tar.exe` through `exec.winrm.shell` unpacks the tree and the lab's Go and MinGit. |
| No package manager on Windows or FreeBSD. | Open: `pkg.*` covers APT and DNF only, so the boxes' tools are fetched against published digests instead. Winget is absent on Windows Server 2025 Core, and Microsoft supports it there in no form: with the application compatibility feature installed, winget run from its extracted bundle installed, listed and removed a package over WinRM, but the owner chose not to rely on what Microsoft calls unsupported (2026-10-18). Core needs another way. |
| No method installed a feature on demand, and none restarted a host. | Closed 2026-10-18: `win.capability.install` and `win.capability.remove` (DISM as SYSTEM, since Windows Update refuses a WinRM logon) and `win.reboot`, `win_reboot`'s counterpart. |
| `win.schtask` had never reached a host: its script did not fit the command line. | Closed 2026-10-18: found while trying to run DISM as SYSTEM; its tests now hold the script to the command line's limit. |
| A box held the host's memory whether or not a check was running. | Closed 2026-10-18: a check starts its VM and saves its state afterwards (`virt.vbox.vm.stop`'s new `mode: save`). |
| A saved box's disks could never be compacted, and every run's caches grew on its system disk. | Closed 2026-10-29: a check shuts its VM down afterwards (`mode: acpi`, then `poweroff`), and each run's variable data lives on a second disk (`virt.vbox.vm.attach_disk`, `PLEIADES_BSD_DATA`, `PLEIADES_WINRM_DATA`). |
| A FreeBSD login had no way to root. | Closed for the lab 2026-10-18: a clone's first boot can install `sudo`, which then honors the rule the seed always wrote. |

## Failure modes

| Failure | Answer |
|---|---|
| No machine named | `make <os>-check` refuses, saying how to name one. `pr-check` says that system was not checked and carries on. |
| A setting is half set, misspelled, names a device pleiades would misread, or names a directory that is no project | Refused before anything runs, naming the setting and how to change or remove it. |
| The machine does not answer | Refused within seconds, before anything else: the Mac by its SSH port, a lab machine by `wait.connection`. A VM the check started has ten minutes to answer, and is put back if it does not. |
| The host has no room for the VM | `virt.vbox.vm.start` refuses, naming what it needs and what is free, and nothing runs. |
| The VM cannot be put back | The run fails saying so, even when its tests passed, since the box still holds the host's memory; `virt.vbox.vm.stop` with `mode: acpi` (or `poweroff`) puts it away. |
| The Mac refuses this machine's key, or its host key is unknown or changed | Refused by a plain `ssh` login before Pleiades is involved, with the command that fixes it. |
| Not the right system, or a tool missing | Refused, naming which and how to install it. |
| The tests fail | Exit 1, with the machine's output. |
| The run outlives its timeout | The script is sent `SIGTERM`, and its trap stops the tests' process group and tree before it exits; the task fails, saying the timeout ran out. On Windows, `exec.winrm.shell`'s own timeout stops the command. |
| Interrupted while the tests run | The local `pleiades` is ended and the run is stopped on the machine. Exit 2, without advice to change the machine, since nothing about it was wrong. |
| `-phase test` with no `prepare` before it | Refused: the worktree has no `pleiades` for the machine yet. A tree that is not on the machine ends the run with status 2. |
