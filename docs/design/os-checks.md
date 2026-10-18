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
runs them without the race detector, which needs cgo and a C toolchain there (`testgate -race=false`). Each
checks out the committed tree (never the working tree) into a directory of its own on the machine, and keeps the
newest three.

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
| Ready means | Darwin, with `go`, `git` and `caffeinate` | FreeBSD, with `go`, `git` and `cc` | Windows, with `go`, `git` and `tar.exe` |
| Tree sent by | `file.copy`, unpacked by `archive.extract` | `file.copy`, unpacked by `archive.extract` | `win.file.copy`, unpacked by `tar.exe` through `exec.winrm.shell` |
| Tests run by | `exec.command`, under `caffeinate -dims` | `exec.command` | `exec.winrm.shell`, each pass under `cmd.exe` so its streams join in order |
| Stopped by | the run's process group, written on the machine | the run's process group, written on the machine | `taskkill /T` of the run's process, written on the machine |

Every value a Windows script needs reaches PowerShell through `exec.winrm.shell`'s `env`, never inside a script's
text, so each script is a constant.

The VirtualBox lab's two boxes, both made with Pleiades runbooks:

- **`win-check`:** Windows Server 2025 Core, one CPU and 2 GB. Go is at `C:\go` (fetched with `win.file.download`
  against go.dev's digest) and MinGit at `C:\mingit` (fetched against its release's digest).
- **`bsd-check`:** FreeBSD 15.1, one CPU and 1 GB, cloned from the never-booted base. Its first boot installs
  `git` and `sudo` (`virt.vbox.vm.clone`'s `packages`, which FreeBSD's nuageinit reads as cloud-init does). Go is
  in the login's home, sent with `file.copy` and unpacked with `archive.extract`, so it needs no root.

## In pr-check

Each machine runs beside the Linux passes, not after them:

- **Prepare, one machine at a time.** `-phase prepare` checks the machine, builds `pleiades` and sends the tree.
  This is the only part that uses this machine, which is why the machines go one after another.
- **Test, in the background.** `-phase test` runs on the machine's own CPU and memory, while the Linux passes run
  here. Its output goes to `.coverage/pr/<mac|bsd|win>.log`.
- **Report.** Each log is printed whole once the Linux passes end. Each result counts like any other step's, and a
  machine that is not named is reported as not checked.
- **Interrupt.** An interrupt, or pr-check ending early, stops every check, and each check stops its tests on its
  machine too.
- **Parallelism.** The Mac runs `PR_CHECK_PARALLELISM` packages at once. The other two use go test's default for
  their own CPUs (`BSD_CHECK_PARALLELISM`, `WIN_CHECK_PARALLELISM`).

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
| No package manager on Windows or FreeBSD. | Open: `pkg.*` covers APT and DNF only, so the boxes' tools are fetched against published digests instead. Winget is not even present on Windows Server 2025 Core. |
| A FreeBSD login had no way to root. | Closed for the lab 2026-10-18: a clone's first boot can install `sudo`, which then honors the rule the seed always wrote. |

## Failure modes

| Failure | Answer |
|---|---|
| No machine named | `make <os>-check` refuses, saying how to name one. `pr-check` says that system was not checked and carries on. |
| A setting is half set, misspelled, names a device pleiades would misread, or names a directory that is no project | Refused before anything runs, naming the setting and how to change or remove it. |
| The machine does not answer | Refused within seconds, before anything else: the Mac by its SSH port, a lab machine by `wait.connection`. |
| The Mac refuses this machine's key, or its host key is unknown or changed | Refused by a plain `ssh` login before Pleiades is involved, with the command that fixes it. |
| Not the right system, or a tool missing | Refused, naming which and how to install it. |
| The tests fail | Exit 1, with the machine's output. |
| The run outlives its timeout | The command is sent `SIGTERM` and the task fails, saying the timeout ran out. |
| Interrupted while the tests run | The local `pleiades` is ended and the run is stopped on the machine. Exit 2, without advice to change the machine, since nothing about it was wrong. |
| `-phase test` with no `prepare` before it | Refused: the worktree has no `pleiades` for the machine yet. A tree that is not on the machine ends the run with status 2. |
