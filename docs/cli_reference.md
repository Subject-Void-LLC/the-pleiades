# Pleiades CLI Reference

`pleiades` is the Walk-tier composition root (`cmd/pleiades`): a single, self-contained
binary that scaffolds a project, manages a static YAML inventory, stores encrypted SSH
credentials, and validates and runs runbooks. It links the inventory, engine, and
validation packages directly. There is no Controller, database, or message broker at
this tier (PLAN.md Section 7), so every command works fully offline against files in
the current project directory.

Build it from the repository root:

```bash
go build -o pleiades ./cmd/pleiades
```

## Synopsis

```
pleiades <command> [flags]
```

Running `pleiades` with no arguments, or `pleiades -h` / `--help` / `help`, prints:

```
usage: pleiades <command> [flags]

commands:
  init            scaffold a new project in the current directory
  add-host        add a host to the static inventory
  add-credential  store an encrypted SSH credential for a device
  validate        check a runbook against the inventory
  run             build, validate, and print the plan for a runbook

Walk tier: no server, no database, no broker. See PLAN.md Section 7.
```

Every command accepts `--dir <path>` (default `.`) to point at a project directory
other than the current one. `--dir` governs the inventory path, and `validate`'s
*default* runbook path when none is given; an explicitly-supplied runbook path
(`validate [runbook.yaml]`, `run <runbook.yaml>`) is used as-is, resolved against the
process's real working directory, not against `--dir`.

Because `validate` and `run` use the stdlib `flag` package, which stops parsing flags
at the first non-flag argument, `--dir` must come **before** the runbook path on the
command line: `pleiades run --dir proj runbooks/sample.yaml` works,
`pleiades run runbooks/sample.yaml --dir proj` does not. `add-host` and
`add-credential` do not have this restriction: their positional argument (the host or
device name) can appear anywhere.

### Exit codes

| Code | Meaning |
|------|---------|
| `0`  | Success |
| `1`  | The command ran but failed (bad input, validation errors, execution failure) |
| `2`  | No command given, or an unknown command name |

Errors from a failed or unknown command are printed to stderr, prefixed `pleiades: `.
Running `pleiades` with no arguments at all prints the bare usage block (no
`pleiades: ` prefix, since there is no error text to prefix).

## Project layout

A scaffolded project directory looks like this once you have run `init`, `add-host`,
and `add-credential`:

```
.
├── inventory.yaml            # static inventory (created by init, appended by add-host)
├── README.md                 # created by init
├── runbooks/
│   └── sample.yaml           # starter runbook (created by init)
└── .pleiades/
    ├── master.key            # AES-256 master key (created on the first credential lookup:
    │                         #   add-credential, or a run whose task actually needs one)
    └── credentials.yaml      # AES-256-GCM encrypted credentials
```

`.pleiades/` is created lazily: a runbook that never uses `ssh_exec` never touches it,
and `run` will not create it just because the command executed.

## Commands

### `pleiades init`

```
pleiades init [--dir <path>]
```

Scaffolds a new Walk-tier project: `inventory.yaml` (empty `hosts: []` list),
`runbooks/sample.yaml` (a one-task `noop` starter runbook), and `README.md`.

`init` never overwrites a file that already exists, so re-running it in an existing
project is safe. It only creates whatever is still missing, and prints
`project already initialized, nothing to do` if there is nothing left to create.

### `pleiades add-host`

```
pleiades add-host <name> (--type <type> | --classify a,b,c) [--set key=value ...] [--tags a,b,c] [--dir <path>]
```

Appends one host to `inventory.yaml`. The host name is a positional argument and can
appear anywhere on the command line relative to the flags.

| Flag | Required | Description |
|------|----------|--------------|
| `--type` | exactly one of `--type`/`--classify` | Device type, e.g. `linux_server`, `cisco_router` |
| `--classify` | exactly one of `--type`/`--classify` | Comma-separated classification path (e.g. `linux_server,debian_family,ubuntu`), resolved against the built-in classification rules (PLAN.md Section 6d) into a concrete type |
| `--set key=value` | no, repeatable | A device property. May be given multiple times |
| `--tags` | no | Comma-separated tags, e.g. `--tags prod,web` |

`--set` infers a value's type the same way the YAML inventory file itself would:
exactly `true`/`false` becomes a boolean, a string that parses entirely as a base-10
integer becomes an int, and everything else (including version strings like `15.2`)
stays a string. This matters because typed accessors like `Properties.Int("port")`
only recognize a value already stored as that Go type: passing `--set port=2222` as a
plain string would leave `Int("port")` unable to read it.

`--classify` resolves the path eagerly, at add-host time, against the built-in
classification rule tree and fails loudly on a path matching no rule at any level,
rather than writing a host that would only fail later at `validate` or `run`. Both
the resolved type and the original classify path are written to `inventory.yaml`; a
host entry may also be hand-written with only a `classify:` list (no `type:`), and it
resolves the same way at load time.

`add-host` requires `inventory.yaml` to already exist (run `init` first) and rejects a
duplicate host name outright rather than creating a second entry. Each host gets a
freshly generated UUID as its stable ID, independent of its (renameable) name.

Example:

```bash
pleiades add-host webserver1 --type linux_server --set host=10.0.0.5 --tags prod,web
# added host "webserver1" (linux_server) to inventory.yaml

pleiades add-host webserver2 --classify linux_server,debian_family,ubuntu
# added host "webserver2" (linux_server) to inventory.yaml
```

### `pleiades add-credential`

```
pleiades add-credential <device> --username <user> [--password <password> | --key <path> [--passphrase]] [--dir <path>]
```

Stores one device's SSH credential, encrypted at rest with AES-256-GCM, in
`.pleiades/credentials.yaml`. Unlike `inventory.yaml`, this file is ciphertext and is
not meant to be hand-edited; `add-credential` is the only supported way to populate it.
Re-running it for a device that already has a stored credential updates that entry
rather than adding a duplicate.

Exactly one authentication method is used per invocation:

| Flag | Description |
|------|--------------|
| `--username` | required: the account to authenticate as |
| `--password` | a literal password value. If omitted (and `--key` is also absent), you are prompted interactively with echo disabled |
| `--key` | path to a PEM private key file |
| `--passphrase` | prompts (no echo) for the private key's passphrase; only valid alongside `--key` |

`--password` and `--key` are mutually exclusive: a device authenticates one way at a
time. Prompting for a secret by default, instead of requiring it as a bare flag value,
is deliberate: a flag's value is visible in shell history and in this process's
argument list to any other user on the same machine for as long as it runs.

Examples:

```bash
# Prompts for the password interactively (no echo):
pleiades add-credential webserver1 --username deploy

# Non-interactive, e.g. from a script or CI:
pleiades add-credential webserver1 --username deploy --password "$SECRET"

# Key-based auth, with a passphrase prompt:
pleiades add-credential router1 --username admin --key ~/.ssh/id_ed25519 --passphrase
```

#### Master key resolution

Encryption uses a 32-byte AES-256 key resolved in this order:

1. The `PLEIADES_MASTER_KEY` environment variable, if set: its value is base64-decoded
   and used directly. This lets CI or a secrets manager supply the key without ever
   writing it to disk.
2. `<dir>/.pleiades/master.key`, if it already exists: also base64-decoded.
3. Otherwise, a fresh random key is generated, base64-encoded, and written to
   `<dir>/.pleiades/master.key` (creating the `.pleiades/` directory if needed).

Losing `master.key` (or the value behind `PLEIADES_MASTER_KEY`) with no backup makes
every stored credential permanently undecryptable.

### `pleiades validate`

```
pleiades validate [--dir <path>] [runbook.yaml]
```

Loads the inventory and a runbook, then runs the shared validation core
(`internal/validate`) against them: capability checks (does the runbook target a
device that actually supports the action it asks for), and anything else that core
implements. The runbook argument is optional and defaults to
`<dir>/runbooks/sample.yaml`; passing more than one positional argument is an error.

The validation report is printed to stdout. `validate` exits non-zero (returning
`validation failed`) if the report contains any errors; a clean run prints a report
ending in `no issues found`.

```bash
pleiades validate                        # validates runbooks/sample.yaml
pleiades validate runbooks/upgrade.yaml  # validates a specific runbook
pleiades validate --dir proj proj/runbooks/upgrade.yaml  # --dir must come first;
                                                          # the runbook path is still
                                                          # resolved against the real
                                                          # working directory, not --dir
```

### `pleiades run`

```
pleiades run [--dir <path>] <runbook.yaml>
```

Loads the inventory and a runbook, validates it (aborting with `validation failed, not
executing` and no execution at all if validation fails), prints the plan, then
executes it for real.

**The plan**, printed before anything runs:

- `plan for <runbook> (<N> nodes, <M> inventory hosts loaded):`
- `service-effecting: <bool>` (from the runbook's `metadata.service_effecting`)
- `blast radius: <N> devices[, tiers: ...]` (`internal/validate`'s blast-radius
  calculation)
- The authored `pretasks:` / `tasks:` / `posttasks:` tree, each section printed only
  if non-empty. Nested `block:` tasks recurse one level deeper, with `rescue:` and
  `always:` printed as their own labeled sub-sections when present, mirroring how
  Ansible authors already read a block/rescue/always task.

**Execution**, printed after `executing:`, runs through a real in-process engine, with
no server, database, or broker involved:

- An in-process lock manager and event bus back the run.
- A task whose `fqcn` is `ssh_exec` dispatches over a real SSH connection
  (`internal/transport/ssh`), authenticated with whatever `add-credential` stored for
  the resolved target device. The credential store is resolved lazily: a runbook with
  no `ssh_exec` tasks never touches `.pleiades/` at all.
- Every other `fqcn` (including `noop`) runs through the builtin, transport-free
  executor.

Each node's result is printed as one line, `<node-id>[ [<device>]]: <status>`. When a
task has a `target`, `<device>` is the resolved device's opaque ID (`InventoryItem.
ID()`), not its human-readable name: `tasks[0] [258b5838-85b7-4fb7-8f4f-3793dc40c891]:
ok`, not `tasks[0] [webserver1]: ok`. `<status>` is one of:

| Status | Meaning |
|--------|---------|
| `FAILED: <error>` | The task errored |
| `skipped (condition evaluated false)` | A `when`/`when_or`/`when_cel` condition was false |
| `changed` | The task ran and reported changed state |
| `ok` | The task ran and reported no change |

`run` exits non-zero (`execution failed`) if any node failed, and prints `run
complete` on success.

```bash
pleiades run runbooks/sample.yaml
```

## Runbook YAML

A runbook is authored the same way an Ansible playbook is: an ordered
pretasks/tasks/posttasks list, not a hand-wired graph of nodes and edges.

```yaml
id: my-runbook
metadata:
  service_effecting: false   # optional; defaults to false
pretasks:                    # optional
  - name: setup
    fqcn: noop
tasks:                       # required
  - name: patch webservers
    fqcn: ssh_exec
    params:
      target: webserver1     # a device name, or a tag matching one or more devices
posttasks:                   # optional
  - name: teardown
    fqcn: noop
```

Key fields, per task:

- **`name`**: a free-form human label, purely for readability. It has no uniqueness
  requirement.
- **`fqcn`**: the action to run, e.g. `ssh_exec` or `noop`. There is no
  collection/namespace resolution: this is a bare action name, not a real Ansible
  fully-qualified collection name.
- **`params`**: arguments to the action. The `target` key, if present, names either an
  exact inventory device name or a tag: a tag resolves to every device carrying it, and
  the task runs once per resolved device. A task with no `target` runs once,
  controller-side, against no device.
- **`register`**: stores this task's result under this name, readable from a later
  task's `when_cel` as `stat.<name>[<device>]`, keyed by the resolved device's opaque
  ID for a targeted task, or by `""` for a controller-side task with no `target`.
- **`block`** / **`rescue`** / **`always`**: nested task lists on a block task, mirroring
  Ansible's `block:`/`rescue:`/`always:`. `rescue` runs if a task in `block` fails;
  `always` always runs.
- **`when`**, **`when_or`**, **`when_cel`**: conditionals. `when` is one expression, or a
  list of expressions ANDed together, matching Ansible's `when:`. `when_or` is the
  same shape but ORed (no Ansible equivalent). `when_cel` is a single raw
  [CEL](https://github.com/google/cel-spec) expression, used verbatim.

### Conditionals and registered stats

`register` on one task makes its result available to a later task's `when_cel`. Since
`stat.<name>` is keyed by device ID, this is simplest to use with a controller-side
task (no `target`), where the key is always the empty string:

```yaml
tasks:
  - name: precheck
    fqcn: noop
    register: precheck
    params:
      needs_reboot: true
  - name: reboot
    fqcn: noop
    when_cel: 'stat.precheck[""].needs_reboot == true'
  - name: skip-me
    fqcn: noop
    when_cel: 'stat.precheck[""].needs_reboot == false'
```

Running this prints `reboot` as `changed` and `skip-me` as `skipped (condition
evaluated false)`.

## Full walkthrough

```bash
pleiades init
pleiades add-host webserver1 --type linux_server --set host=10.0.0.5
pleiades add-credential webserver1 --username deploy   # prompts for a password
pleiades validate
pleiades run runbooks/sample.yaml
```
