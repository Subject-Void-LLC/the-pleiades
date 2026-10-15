# Image pins and lockfiles with the build methods

The build methods check what a build depends on before it builds: that every image it starts from is
named by both tag and digest, that those pins are moved forward deliberately, and that a lockfile covers
its manifest with an integrity hash for every entry, from sources you allow.

## What is here

- `pleiades/inventory.yaml` names one Linux server, `build-box`, holding your project at `/srv/app`.
  Onboard it so its container engine is recorded: tags are resolved through it, so through the machine's
  own network, proxy, mirrors and registry logins.
- `pleiades/runbooks/pins.yaml` checks the image pins in a Dockerfile and a Compose file, and verifies an
  npm lockfile. Both tasks only read.
- `pleiades/runbooks/refresh.yaml` moves every pin to what its tag names now, rewriting the files.

## Running it

```sh
cd pleiades
pleiades validate runbooks/pins.yaml runbooks/refresh.yaml
pleiades run runbooks/pins.yaml
pleiades run runbooks/refresh.yaml --mode check   # resolves every tag and lists the changes, writing nothing
pleiades run runbooks/refresh.yaml
pleiades rollback <run-id>                        # writes back exactly what the refresh changed
```

The refresh keeps the references it replaces in an undo record on `build-box`, in the login's
`~/.local/state/pleiades/build-digests` (private to it, removed when replayed or after 90 days), and the
rollback replays that record, writing each reference back only where the file still holds what the run wrote.

A check of `build.digests.refresh` resolves every tag as a run does and reports each change it would make;
`build.digests.check` and `build.lock.verify` only read, so their check is the task itself.

## What this example has been run against

`pleiades validate` accepts both runbooks in the repository's own tests. They target a machine of yours,
so they have not been run as written; `cmd/pleiades`'s build release gate runs the same methods through
the real binary against a real sshd, a real Docker engine and a real registry.
