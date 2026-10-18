# Setting one value in a configuration file, and checking the result

`file.json` and `file.yaml` set one value at a key path in a configuration file on a server, and leave the rest
of the file as it was: JSON's key order, and its numbers and strings exactly as written; YAML's comments. A key
that already holds the value is no change, and nothing is written. `http.check` then asks whether the
application answers as it should. It never keeps the body it read, only its size and digest.

## What is here

- `pleiades/inventory.yaml` names one Linux server, `app-1`.
- `pleiades/runbooks/config.yaml` does four things:
  - moves the app to port 8443 in `/etc/app/config.json`;
  - removes a deprecated flag whose key holds a dot (the list form of `key` reaches it);
  - sets the worker pool's size in `/etc/app/workers.yaml`;
  - checks `https://app.example.com/healthz` answers with `"status":"ok"`.

## Running it

```sh
cd pleiades
pleiades validate runbooks/config.yaml
pleiades run runbooks/config.yaml --mode check   # each edit computed and shown as a diff of the key; nothing written
pleiades run runbooks/config.yaml
pleiades rollback <run-id>                        # needs each edit's rollback: list: the earlier text is not journaled
```

A changed file's undo is a `file.copy` of the text it held before. That text is withheld from the run journal,
since a configuration file can hold a secret, so `pleiades rollback` replays it only from a `rollback:` list
written on the task.

## What this example has been run against

`pleiades validate` accepts the runbook in the repository's own tests. `cmd/pleiades`'s config release gate runs
the same methods through the real binary, against a real SSH server and a real HTTPS server: a check writes
nothing, and the run edits both files, keeping the rest of each.
