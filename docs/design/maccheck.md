# Design note: `make mac-check`, testing on a real Mac before a push

Status: built (tools/maccheck, 2026-10-16). A developer tool, and a piece of dogfooding: the Mac is driven through
The Pleiades itself, so what The Pleiades cannot yet do for it is written down below as a gap to build.

## Why

A Linux machine cannot show what a macOS (BSD) userland lacks: GNU's `timeout -k`, GNU's argument permutation
(`chmod 700 -- dir`), `stat -c`. CI's `fast-macos` job finds these, but only after a push, and three did reach it
(FAILURE_PATTERNS 420, 476, 494). The SSH test harness now runs commands with `POSIXLY_CORRECT`, which closes the
argument-order class on Linux; the rest needs a Mac.

## What it does

1. **The Mac** is `PLEIADES_MAC` in `tests/mac/.env` (gitignored, so never committed and never in the tree sent to
   the Mac; `tests/mac/.env.example` shows it): anything `ssh` accepts (`user@host`, `user@ip`, or a `Host` alias
   from `~/.ssh/config`). The same variable in the environment overrides the file, and `-mac` overrides both for
   one run. Deleting the file stops testing on a Mac.
2. **Liveness first.** `ssh -G` resolves the target as ssh would, and a TCP connection to its SSH port must open
   within five seconds. A Mac that is asleep, off the network or not listening is named at once, with what to
   check and how to change the Mac or stop using one.
3. **The Mac is a Pleiades device.** A private project under `.git/pleiades-maccheck` (this clone's, never
   committed) holds it as a `generic_ssh` device with the first of ssh's identity files as its credential, made
   with this branch's own `pleiades` (`init`, `add-host`, `add-credential`, `onboard`) and remade whenever the
   target changes. `net.ssh.ping` confirms Pleiades reaches it, and `exec.command` that it is a Mac with `go` and
   `caffeinate` on its PATH.
4. **The tree.** `git archive` of the commit (never the working tree) is unpacked into
   `~/.pleiades-maccheck/<commit>` on the Mac, over plain ssh (gap 2 below); the newest three are kept.
5. **The tests.** `exec.command` runs, under `caffeinate -dims` so the Mac does not sleep partway, the branch's
   container-free packages through `tools/testgate` exactly as `pr-check` does: the full pass, then three strict
   repeats.

`pr-check` runs the same check whenever a Mac is named, and says macOS was not checked when none is. A Mac that is
named but unreachable fails `pr-check`, with the same instructions.

## Gaps found by driving it through The Pleiades

Each is something a Pleiades user doing the same would meet. None is built here.

| Gap | What it costs here | What would close it |
|---|---|---|
| Onboarding grants a Darwin kernel shell exec only; the file methods are proven on FreeBSD, not macOS. | Nothing yet: the check needs only `exec.command`. | Prove `pkg/remotefile` against a real Mac (this one), as FreeBSD was, and grant POSIX file access for Darwin. |
| No method moves a local file or tree to a device: `file.copy` refuses `src`, since a Walk-tier Runner cannot see the controller's files. | The tree goes over plain ssh, the one step that is not a Pleiades method. | A transfer that works on both tiers, for instance an artifact the Controller hands the Runner. |
| No timeout: `exec.command` has no timeout parameter, and a runbook task has no timeout key. | A hung session is bounded only by `go test`'s own per-package timeout. | A task-level timeout, as Ansible's `timeout:` keyword. |
| A failed `exec.command` keeps only an excerpt of its output. | The Mac's script always exits 0 and ends its output with `maccheck-exit=<status>`, so a failing run's output survives. | Return `rc` and the full output with the failure, as Ansible does. |
| `pleiades run` and `adhoc` return a command's output only when it ends. | A run of ten to twenty minutes is silent until it ends. | Stream a running command's output. |

## Failure modes

| Failure | Answer |
|---|---|
| No Mac named | `make mac-check` refuses, saying how to name one; `pr-check` says macOS was not checked and carries on. |
| The SSH port does not answer in five seconds | Refused before anything else, naming the address, what to check, and how to change or remove the setting. |
| Pleiades cannot log in or the host key is unknown | Refused, with `ssh <mac> true` to accept the key, and the same instructions. |
| Not a Mac, or no `go` or `caffeinate` | Refused, naming which. |
| The tests fail | Exit 1, with the Mac's output. |
