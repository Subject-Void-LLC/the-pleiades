# A failed deploy repaired on a local model's pick, with rescue and rollback

A runbook starts a tiny web server in a throwaway BusyBox container and health-checks it. When a step fails, the
block's `rescue` looks at what the failure left behind and asks a local model, through `ai.chat`, which of a
fixed set of fixes applies. The run then takes the path the model named, checks the site again, and either ends
`rescued` or fails and leaves the container for you to look at. `pleiades rollback` removes it.

Everything runs on your own machine: Docker for the container and Ollama for the model. The runbook can also run
with no network at all (below).

## What is here

- `pleiades/inventory.yaml` names two devices. `dockerhost` is this machine's own Docker or Podman. `workstation`
  is a local Ollama tagged `residency: local`. Neither needs a stored credential.
- `pleiades/ai/prompts/diagnose.json` is the prompt bundle. It tells the model what each fix means and asks for
  one as JSON.
- `pleiades/runbooks/repair_web.yaml` is the runbook. `-e fault=<name>` breaks the deploy one of five ways:

  | `fault`      | What goes wrong                                    | The fix the model should name |
  | ------------ | -------------------------------------------------- | ----------------------------- |
  | `disk_full`  | `/dev/shm` is filled, so publishing the page fails | `free_space`                  |
  | `bad_perms`  | the document root is unreadable to the server user | `fix_perms`                   |
  | `port_taken` | another server already holds port 8080             | `change_port`                 |
  | `bad_config` | `/etc/app.conf` has an unterminated quote          | `regenerate_config`           |
  | `no_server`  | the `httpd` program is gone                        | `give_up` (no fix applies)    |

  Without `fault` the deploy succeeds and the model is never called.

## How the model's answer is bounded

- **It picks a value; it never writes a command.** The answer is held to a JSON schema whose `fix` is an `enum`
  of the five names above, and the run fails if the answer does not meet it. Each name is one fixed task in the
  runbook, gated with `when: result.diagnosis.json.fix == '...'`. A wrong pick runs the wrong one of those
  tasks, inside a disposable container, and the check that follows it fails.
- **It stays on this machine.** The task asks for `residency: local`, and a local use never reaches a hosted
  provider. The platform checks this before every call.
- **Its own uncertainty stops the run.** A `review` rule holds the answer for a person when the model's
  `confidence` is under 0.5. With nobody at the terminal the task fails as `held`; decide it with
  `pleiades ai review`, then run again. A model's confidence in itself is a weak signal, so this catches a model
  that says it is unsure, not one that is confidently wrong.
- **What the failure left behind is data.** The error and the diagnostic dump go to the model as `input`,
  placed after the prompt as one JSON string, and the prompt tells it to treat them as data. The model still
  reads them.

## Rescue and rollback

- **Rescue.** The block's `rescue` gathers diagnostics, asks the model, runs the fix it named, then publishes,
  starts and checks the site again. If the check passes, the run's outcome is `rescued` (exit 0). If the model
  answers `give_up`, or the check still fails after the fix, the run fails and the container is kept for you to
  inspect.
- **Rollback.** `pleiades rollback <run-id>` removes the container through the undo `container.run` recorded.
  Every other task carries `rollback: []`. That says undoing it means doing nothing: what each `container.exec`
  did lives inside the container that goes, and the model call changed no device. The rollback reports those
  tasks as `nothing to undo`.
- **Failed tasks are unknowns.** A task that failed during its action may have changed part of what it meant to,
  and the journal cannot say which. The rollback names each one as `unknown`, still runs, and ends incomplete
  (exit 3) unless you accept them by name with `--allow-unknown`.
- **Why not `rollback_on_failure`.** That key undoes a block before its rescue starts. Here the rescue needs the
  broken container to diagnose, so the undo is a separate step you run when you decide to.
- **One container per run.** A run expects to create the container itself. Roll back, or remove, one an earlier
  run left before running again; otherwise `container.run` finds it already running, records no undo, and the
  later rollback leaves it in place.

## Running it

```sh
ollama pull gemma4:e4b-it-qat
cd pleiades
pleiades onboard dockerhost && pleiades onboard workstation
pleiades validate runbooks/repair_web.yaml
pleiades run runbooks/repair_web.yaml --mode check      # starts nothing and calls no model; exits 3, since a
                                                        # command inside a container cannot be checked
pleiades run runbooks/repair_web.yaml -e fault=port_taken
pleiades rollback <run-id> --allow-unknown <each node it names as unknown>
```

To run with no registry and no network, load the image into the engine first (`docker load` from an archive
made with `docker save busybox:1.36`) and add `pull: never` under `container.run`. The engine then runs the
image it holds and contacts no registry. Ollama only needs the model already pulled.

## What this example has been run against

- **A real model.** On 2026-10-20, every fault ran against this machine's Docker 29.8 and a local
  `gemma4:e4b-it-qat`.
  - The four fixable faults were rescued with the fix the table names.
  - `no_server` got `give_up` and failed.
  - A rollback of a rescued run, and of the failed one, removed the container. A second rollback of the
    same run was refused with nothing left to undo.
- **How the prompt got here.** The first wording of the prompt led the model to answer `regenerate_config` for
  `port_taken` on three runs in a row; it read the other server's directory as a configuration error. Saying
  what each fix means, and adding the process list to the dump, fixed that on the runs since. Expect a
  different model, or a different wording, to need the same tuning.
- **The automated gate.** `cmd/pleiades`'s `TestCLI_AIRepairExampleRepairsAndRollsBack` copies this
  directory and points the model host at an HTTP server speaking Ollama's API. It then runs the following
  through the real binary against real Docker, with `pull: never`:
  - the check;
  - a `disk_full` run rescued by `free_space`;
  - a rollback that removes the container.

  That server answers what the test tells it to. So the gate proves the platform routes a pick to the right
  task, never that a real model picks well.
