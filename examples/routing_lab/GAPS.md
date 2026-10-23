# Gaps found while building this lab

This is the record of what a first build of this lab met, kept as it was found. Fixed in the same
change that added the example: G1, G2 and G3 (`net.frr.config` checks the run directory and
`frr.conf` before any change, in a check too, and names frr-reload's own error), G4 (docs/16's
Running Batfish), G5 (`container.run` takes `memory`), G7 (`import-containerlab
--trust-first-connect` refuses without a lab `PLEIADES_KNOWN_HOSTS`), G9 (`net.frr.facts` returns
`config_sha256`), G15 (the start page) and parts of G17 (the import's JSON lists capabilities;
docs/16 names `domainname localdomain` and `service_effecting`). Fixed afterwards, on the branch that added the lab importers: G6 (`http.wait`, and validation refusing an SSH method on a `container_host` with no `host`), G8 (a stopped run reports the tasks it never ran as `not_run`), G10 (`pleiades.builtin.project.write`), G11 (`validate -e`, and a missing variable refused before anything runs), G12 (`rollback --json` gives each problem), G13 (device names in labels), G14 (a run warns about another run on the same devices), G16 (`journal` says a run undid itself) and G17's last bullet (`net.trace` with `container`). The entries below are kept as they were found.

Each entry is something The Pleiades did not do, did wrong, or documented wrong or not at all,
met while following its own documentation (`docs/01-start-here.md`, `docs/16-network-verification.md`,
`pleiades doc`) and the skill `pleiades doc --create-skill` wrote. Binary: `pleiades
0.0.0-dev+2861d447d5aa`. Each gives the command, what it printed, and what was done instead.
Evidence files are under `captures/` (the first attempt under `captures/attempt1/`).

Ordered by how much each one would hurt a real change.

## G1. The documented router account cannot apply a configuration: `net.frr.config` needs `/var/run/frr` writable

`docs/16-network-verification.md` says an FRR router is reached "as an unprivileged account allowed to
use `vtysh` (the `frrvty` group)". That account can read and check, but not apply.

```
$ pleiades run runbooks/change_r3.yaml -e candidate=configs/r3.good.frr.conf --json   # captures/attempt1/10-good-candidate.json
net.frr.config: python3 exited 1: 2026-10-23 02:24:40,920  INFO: Called via "Namespace(input=None, reload=True, ...
```

Running the same reload by hand inside r3, as `netops`, shows the cause Pleiades did not report:

```
$ docker exec -u netops clab-frrlab-r3 python3 /usr/lib/frr/frr-reload.py --reload --stdout /tmp/diag.conf
PermissionError: [Errno 13] Permission denied: '/var/run/frr/reload-U81GG8.txt'
```

frr-reload.py writes its reload files into the run directory, which FRR's image creates as
`drwxr-sr-x frr frr`. Instead: the router image's start script runs `chgrp frrvty /var/run/frr; chmod 2775
/var/run/frr` (`images/frr/start.sh`). The docs should name this requirement, and see G3 for why onboarding
did not catch it.

## G2. A failed `net.frr.config` hides its reason, leaves the router half-changed, and its undo fails the same way

Same run as G1. Three problems in one:

1. The error is only the first line frr-reload printed (`INFO: Called via "Namespace(...)"`); the
   `PermissionError` that explains it is cut off. Nothing in the report or the journal would have
   told a user why.
2. frr-reload applies deletions before additions, and only the additions failed. The task reported
   `lines_deleted: ["interface eth3", " no description"]`, and r3 was left with eth3's description removed:
   neither the baseline nor the candidate.

   ```
   $ pleiades adhoc r3 net.frr.command 'command=show running-config' --json
   interface eth3
    ip address 10.3.3.1/24
    ip ospf area 0
    ip ospf passive
   exit
   ```

3. The block's undo (`net.frr.config restore=...`) failed with the same truncated error, so
   `rollback_on_failure` did not restore r3. Statuses: `["ok","ok","ok","failed","failed"]`. The run's
   outcome says only `execution failed`; nothing says that r3 is now in a third state.

Instead: after fixing G1 on the running routers, `pleiades rollback 19ff58fe-...` restored r3 to the baseline
digest (`captures/attempt1/12-rollback-failed-apply.json`). Its planner correctly ignored the failed undo and
replayed the original backup.

## G3. Onboarding grants `FRRCapable` on `show version`, and a check passes a change the account cannot make

`pleiades onboard` proves `vtysh -c 'show version'` as the account (docs/16). With G1's account, the check
of the corrected change was clean:

```
$ pleiades run runbooks/change_r3.yaml -e candidate=configs/r3.good.frr.conf --mode check --allow-unchecked wait.routes --json
{"status":"complete","message":"check complete: nothing was changed (...)","exit_code":0}      # captures/attempt1/09-good-candidate-check.json
```

and `net.frr.config` predicted `would_change`, because `frr-reload.py --test` needs no write access. The real
run then failed (G1, G2). Neither onboarding nor a check proves that the account can apply a configuration,
so the rehearsal a check is meant to be passed a change that could not be made. Instead: fixed the
permission in the image (G1).

## G4. Nothing in the docs or the skill says how to run the Batfish service

The skill and docs/16 say how to add the device (`pleiades add-host batfish --type batfish --set
api_base=http://127.0.0.1:9996`) and that an agent must not start Batfish without say-so, but not how to
start it, which image, what memory it needs, or that the API port is 9996 only. It is possible through
Pleiades: `runbooks/batfish_up.yaml` runs it with `container.run` on a `container_host` with no `host` (this
machine's Docker socket), and `runbooks/batfish_down.yaml` removes it with `container.remove`. A worked runbook
like this one belongs in docs/16.

## G5. `container.run` has no memory limit, and the Batfish image takes 80% of the machine

`pleiades doc container.run` lists no `memory`, `memory_swap` or `cpus` parameter. The Batfish image's command
is `java ... -XX:+UseContainerSupport -XX:MaxRAMPercentage=80 ...`, so with no container limit its heap may grow
to 80% of this 9.7 GiB machine. Instead: `JAVA_TOOL_OPTIONS: "-Xmx1536m -XX:MaxMetaspaceSize=256m"` in the
task's `env`, and `lab/batfish-cap.sh` (`docker update --memory 2g --memory-swap 2g frrlab-batfish`) straight
after the run. Result: `frrlab-batfish 209.3MiB / 2GiB`. The container runs uncapped for the second between the
two.

## G6. Nothing waits for a service on this machine; `wait.port` validates against a socket-only host and fails with a credential error

After `batfish_up`, nothing in Pleiades can wait until Batfish's API answers: `http.check` makes one request,
and `pleiades.builtin.wait.port` runs over SSH on the device. `pleiades validate` accepted `wait.port` on
`dockerhost` (a `container_host` with no `host` and no login), and the run failed with a misleading message:

```
$ pleiades adhoc dockerhost pleiades.builtin.wait.port port:=9996 timeout:=3 --json     # captures/attempt1/00-waitport-on-container-host.json
pleiades.builtin.wait.port: device "dockerhost": no usable authentication method: supply a password or a private key for this device
```

The device has no SSH at all; a credential would not help. Instead: a shell loop around
`pleiades adhoc batfish http.check url=http://127.0.0.1:9996/v2/version status_code:=200`, repeated until it
exits 0.

## G7. Host keys of an ephemeral lab go into `~/.ssh/known_hosts` by default

The import command in docs/16 and the skill, run as written, wrote the lab's keys into the user's own file:

```
$ pleiades import-containerlab --topology lab/clab-frrlab/topology-data.json --lab frrlab ... --trust-first-connect --onboard
~/.ssh/known_hosts: 3 key(s) added, 0 already trusted, 0 replaced          (once per node)
```

A containerlab lab makes new host keys on every deploy at the same `127.0.0.1` ports, so the next deploy is
refused as a changed key, or needs `--replace` against the user's real file. The skill mentions `PLEIADES_KNOWN_HOSTS` only as an alternative to `$HOME/.ssh/known_hosts`; neither docs/16 nor the
import's help mentions `PLEIADES_KNOWN_HOSTS`, and there is no per-project setting, so it must be exported in
every shell. Instead: every command after that ran with `PLEIADES_KNOWN_HOSTS=$PWD/lab/known_hosts`.
**Still to clean up:** the first import left 12 lines for `[127.0.0.1]:2201`, `:2202`, `:2203` and `:2211` at
lines 28 to 39 of `~/.ssh/known_hosts`; I was not permitted to edit that file, so they are still there.

## G8. A refused run's report leaves out the tasks that never ran

`captures/08-bad-candidate.json` has three entries in `tasks` (`ok, ok, failed`). The block's eight tasks,
`net.frr.config` among them, appear only in `plan`, with no status such as `skipped` or `not_run`. The skill's
list of task statuses has `skipped`, so a reader (or an agent) checking "did the apply run?" must diff `plan`
against `tasks`. Instead: proved it from the journal (`3 task(s), 0 changed`) and by reading the digests again.

## G9. `net.frr.facts` returns no digest of the running configuration

The experiment's baseline is "each router's running-configuration digest", and `net.frr.facts` returns the
configuration text but no SHA-256. `net.frr.config` returns one only for what it applied. Instead: the
baseline runbook passes each `running_config` to `net.batfish.check`, whose `sha256` per device is the digest
(cross-checked: it equals `sha256sum` of each `running_config` in the report).

## G10. No way to keep a router's running configuration as a project file

A candidate is "a whole configuration file in the project". The natural start is the running configuration,
and no method writes it into the project (`net.frr.facts` puts it in the report only). Instead:
`jq -j '.tasks[2].stats.running_config' captures/07-baseline.json > configs/r3.running.frr.conf`, then edits.

## G11. `pleiades validate` takes no `--extra-vars`, and a missing variable fails only mid-run

`change_r3.yaml` takes the candidate as `-e candidate=...`. `pleiades validate runbooks/change_r3.yaml` (it has
no `-e` flag) says `no issues found`, and a run without `-e` reads r1 and r2 first and then fails:

```
$ pleiades run runbooks/change_r3.yaml --mode check --allow-unchecked wait.routes --json
failed to render the parameters of tasks[2] (...): parameter devices: item 2: parameter src: render: no value supplied for "vars.candidate"
```

The variable is known to be missing before anything runs. Instead: always pass `-e`.

## G12. `rollback --json` refuses without saying why

```
$ pleiades rollback 7bc10005-e273-44ae-ada4-25c7cd960dd5 --mode check --json
{"outcome":{"status":"invalid","message":"rollback of 7bc10005-... refused: 1 problem(s)","exit_code":1}, "validation": null, ...}
```

The problem itself ("every change this run made has already been undone ...") is printed only by the text
view (`captures/14-rollback-after-self-undo.txt`). The skill tells an agent to read `--json`. Instead: ran
it again without `--json`.

## G13. Device IDs where a user expects names

- `--allow-unchecked`'s outcome: `"allowed_unchecked":["tasks[3].block[1] [cc0f9733-f9ce-44e5-9a62-43c8e0c90dcf]", ...]`
  (that is r1).
- `pleiades validate runbooks/batfish_up.yaml`: `[route] node "tasks[0]" on f20aa2a4-7087-4831-95bc-0e001bfed36e: engine_local, the docker engine`.
- Every task's `device` in a report is a UUID (`"device": "3d97c945-..."`), with the name only in `host`.

Instead: looked the IDs up in `inventory.yaml`.

## G14. Nothing stops or notices a second run changing the same network during a change

In step 5 a separate `pleiades adhoc r1 net.frr.interface name=eth2 enabled:=false` ran, and changed r1, while
`change_r3` was verifying its change; nothing refused or warned, and the change run's report does not mention
it. Here that was the point, but the same thing happens by accident. docs/10 says `.pleiades/run.lock` is held
only while a rollback plans and runs. Not worked around (it is what step 5 needed); worth saying in docs/16.

## G15. The start-here page is out of date for network work

`docs/01-start-here.md` says "178 registered methods across 23 namespaces" and "`net` 13". The binary has 184
(`SKILL.md`: "The 184 methods built into this binary") and 21 in `net`: `net.frr.*`, `net.batfish.check`,
`net.ovs.*`, `net.trace` and `wait.routes` are not mentioned there, and the page does not link
`docs/16-network-verification.md`. A new user starting where the docs say to start would not learn that this
work is supported.

## G16. The journal is unclear about a run that undid itself

`pleiades journal list` says `7bc10005-...  change_r3  6 task(s), 2 changed, 1 failed`, while runs undone by a
rollback say `rolled back by ...`; nothing says this one undid its change itself. `pleiades journal show` lists
an `undo:` line under the block's own undo step (`tasks[3].block[0].undo[0] ... undo: net.frr.config
restore=...`), an undo of the undo that rollback then ignores. Instead: read `journal show` and the refused
rollback (G12) together.

## G17. Smaller things

- `service_effecting` is `false` in the report of a runbook that changes routing. It is authored metadata
  (`docs/reference/task-keys.md`), and neither docs/16's example nor the skill mentions setting it. Left unset in
  these runbooks, so the reports match what docs/16 shows.
- FRR in a container always adds `domainname localdomain` to its running configuration, and Batfish does not
  recognise it, so every gate needs the same `accept_unrecognized` entry. docs/16 does not say this; the
  `net.batfish.check` example happens to include it. There is no project-wide accept.
- `wait.routes` returns a `diff` whose `before` and `after` are identical (`captures/11-good-candidate.json`),
  which says nothing about what it waited for.
- `import-containerlab --json` reports per node only `trusted` and `onboarded`; the capabilities onboarding
  proved are on standard error only.
- `net.trace` can only run from a managed Linux device with `tracepath`, so tracing from h1 or h2 needs them to
  be SSH devices too. Traced from r1 instead (`includes: [10.0.13.2]`).
