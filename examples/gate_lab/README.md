# Running a project's checks with gate.run

`gate.run` reads a gate manifest from a device and runs the project's own checks there, one result each:
PASS, FAIL or SKIP, with the reason, how long it took, and the end of a failing check's output. Any FAIL
fails the task. `gate.list` validates the same manifest and shows how each check is classified, running
nothing.

## What is here

- `pleiades/inventory.yaml` names one Linux server, `build-box`. Point it at a machine you can reach over
  SSH that holds your project, add its credential, and `pleiades onboard build-box`.
- `gates.yaml` is a sample manifest. Copy it into the project on that machine as `/srv/app/gates.yaml`
  and change the commands to your project's own. An image check needs the image pinned by digest
  (`name@sha256:...`), so replace the placeholder with the digest you reviewed.
- `pleiades/runbooks/gate.yaml` lists the checks, then runs the `pure` and `db` ones.

## Running it

```sh
cd pleiades
pleiades validate runbooks/gate.yaml
pleiades run runbooks/gate.yaml --mode check --allow-unchecked gate.run   # reads and validates the manifest, runs nothing
pleiades run runbooks/gate.yaml
```

A check of `gate.run` reads and validates the manifest and names the checks that would run, then reports
the task unchecked, since whether a check passes is known only by running it; `--allow-unchecked gate.run`
accepts that gap by name. `gate.list` is checked for real, since it runs nothing.

## What this example has been run against

`pleiades validate` accepts this runbook in the repository's own tests. The runbook itself targets a
machine of yours, so it has not been run as written; `cmd/pleiades`'s gate release gate runs the same two
methods, through the real binary, against a real sshd and a real Docker engine onboarded as a
`linux_server`.
