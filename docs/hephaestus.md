---
status: beta
---

# The Forge

Pleiades is the execution engine. The Forge is everything a user does
*before* a runbook runs: authoring, converting, checking, and extending.
It is a CLI namespace (`pleiades forge`) and an SDK surface (`pkg/`), not
a separate runtime subsystem.

The internal design rationale, including the bootstrap principle and the
full catalog mapping tables, is in
[design/hephaestus.md](../design/hephaestus.md). This page covers what a
user needs to know.

## The boundary

Two things in this project involve Ansible. They are different jobs.

| Concept | Input | Output | Where it lives |
|---------|-------|--------|----------------|
| **Legacy interoperability** | An unconverted playbook, marked `type: ansible` | A running job, executed as Ansible inside a container | [Running an unconverted playbook](03-migrating-from-ansible.md#running-an-unconverted-playbook) |
| **The Forge** | Ansible source files on disk | Native Pleiades runbooks and Collections | This document |

Run Ansible as Ansible is legacy interoperability. The playbook's own
bytes never change and it never becomes a runbook. Convert Ansible into
native format is the Forge. The two can be used together during a
migration.

## The `pleiades forge` commands

| Command | What it does |
|---------|--------------|
| `forge new-collection` | Scaffolds a new Collection method inside this repository |
| `forge new-device` | Scaffolds a new inventory device type |
| `forge new-plugin` | Scaffolds a new inventory sync plugin |
| `forge new-external` | Scaffolds an external Collection program that builds against `pkg/` |
| `forge migrate-playbook` | Converts an Ansible playbook into a native runbook (not yet built) |
| `forge migrate-collection` | Converts a Galaxy collection into native source (not yet built) |

Two related surfaces are part of the Forge but keep their existing
commands:

- **`pleiades validate`** stays top level. It already exists, and moving
  it would break the established end-to-end test.
- **`pleiades-lsp`** is a separate process (a language server over
  JSON-RPC/stdio), not a `forge` subcommand.

See [the generated CLI reference](reference/cli.md#pleiades-forge) for
every flag each command accepts. See
[Extending The Pleiades](11-extending-pleiades.md) for the full walkthrough
of writing and testing an extension.

## The catalog naming convention

A native name is `<namespace>.<method>`. The namespace is the domain,
plus an implementation segment when a domain has more than one
implementation. The method is the action.

```
pkg.apt.install        namespace "pkg.apt",     method "install"
svc.systemd.restart    namespace "svc.systemd", method "restart"
net.ios.config         namespace "net.ios",     method "config"
exec.command           namespace "exec",        method "command"
```

Names are never bare. `pkg/collection` enforces this at registration
time.

The name states what capability it needs. `pkg.apt.install` requires
`AptCapable`. This is what makes a linter error readable, and it puts
the capability hierarchy in the runbook text itself.

### Generic and specific names

The naming convention carries the capability hierarchy directly:

```
pkg.install              requires PackageManagerCapable  (resolves down)
  pkg.apt.install        requires AptCapable             (explicit)
  pkg.dnf.install        requires DnfCapable             (explicit)

svc.restart              requires ServiceManagerCapable  (resolves down)
  svc.systemd.restart    requires SystemdCapable         (explicit)

net.cli.config           requires NetworkCLICapable      (resolves down)
  net.ios.config         requires CiscoIOSCapable        (explicit)
  net.junos.config       requires JunosCapable           (explicit)
```

Write the broadest name that works. A task calling `pkg.install` against
a device declaring `AptCapable` resolves to the apt implementation at
plan time. If nothing matches, planning fails with an error naming both
the task and the device, before anything runs.

### Why native names differ from Ansible names

A shared name is a promise of shared behavior. If this platform shipped
a module called `ansible.builtin.apt`, an author would reasonably expect
every parameter from the real module to work. A Go reimplementation will
not support all of them on day one. `pkg.apt.install` promises nothing
it does not deliver, and it sends nobody to the wrong documentation.

The concepts stay Ansible's: `when`, `block`, `rescue`, `always`,
`pre_tasks`, `tasks`, `post_tasks` are unchanged. Only the module names
change, and `forge migrate-playbook` absorbs that change mechanically.

### The Ansible-to-native mapping (summary)

The full table is in [design/hephaestus.md](../design/hephaestus.md).
The pattern:

| Ansible | Native | Capability |
|---------|--------|------------|
| `ansible.builtin.command` | `exec.command` | `CommandExecCapable` |
| `ansible.builtin.apt` | `pkg.apt.install`, `.remove`, `.upgrade` | `AptCapable` |
| `ansible.builtin.service` | `svc.start`, `.stop`, `.restart`, `.enable`, `.disable` | `ServiceManagerCapable` |
| `ansible.builtin.file` | `file.directory`, `.symlink`, `.remove`, `.touch`, `.permissions` | `POSIXFileSystemCapable` |
| `cisco.ios.ios_config` | `net.ios.config` | `CiscoIOSCapable` |
| `ansible.builtin.user` | `identity.user.create`, `.remove`, `.modify` | `PosixAccountCapable` |

Ansible's `file` module hides six unrelated operations behind a `state`
parameter. Splitting them into `file.directory`, `file.symlink`, and
`file.remove` is less to learn per operation.

## Declared is not implemented

The catalog today holds 71 registered method names. Most are declared
stubs, not working implementations. A stub returns an explicit "not
implemented" error; it never returns success. `pleiades validate` flags
any runbook calling a declared-but-unimplemented name at write time.

The manifest carries a `Status` field (`declared` or `implemented`), so
the gap is data the tooling reads rather than knowledge people carry.

## No AWX server importer

An AWX importer was previously specified and has been removed:

- **Inventory** comes from a sync plugin pointed at the real upstream
  source (NetBox, Nautobot, AWS, VMware). An AWX inventory was always a
  copy of something else. Copying the copy is the wrong migration.
- **Playbooks, roles, and collections** come through the Forge.
- **Secrets** are re-entered by a human who is authorized to hold them.

Nothing was left for a server importer to do.
