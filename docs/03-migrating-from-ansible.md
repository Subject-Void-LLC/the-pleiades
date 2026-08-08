---
status: beta
---

# Migrating from Ansible, AWX, and AAP

Ansible support is Pleiades' migration on-ramp, not its destination: a workload lands
unchanged, then converts to native typed collections at its own pace, one playbook at
a time. This book covers what carries over directly, what does not exist yet, and
what the exact vocabulary mapping is, so a migration decision can be made from facts
rather than guesses.

Read [Start here](01-start-here.md) first if you have not: its Implementation status
and Limitations sections are the honest baseline everything below builds on.

## What transfers, and what does not

**Transfers directly, same meaning:** `hosts:`, `block:`/`rescue:`, `register:`,
`when:` (list form ANDs, same as Ansible), `pretasks:`/`tasks:`/`posttasks:` (Ansible's
`pre_tasks:`/`tasks:`/`post_tasks:`, same three-phase shape and ordering).

**Genuinely new, no Ansible equivalent:** `when_or:` (list ORed instead of ANDed),
`when_cel:` (one raw CEL expression), `register_mask:` and `secret_mask:` (per-field
secrecy on a registered result, not just a whole-task `no_log: true`),
`lock_acquisition:` (per-device distributed locking, not a job-level lock), `parallel:`
(native fan-out/join).

**Does not exist yet, and a runbook cannot express it:** Jinja templating anywhere in
`params:` (a runbook's params map is always a literal value), `loop:`/`with_items:`,
`handlers:`/`notify:`, `tags:`, `become:`, `serial:`, `roles:`, `ignore_errors:`,
`changed_when:`/`failed_when:`, `group_vars:`/`host_vars:`, and inventory-level `vars:`
of any kind. See the keyword map below for the complete, itemized list.

None of these are secret gaps. They are the honest distance between "what Ansible
does today" and "what Pleiades does today," and closing them is most of the open
roadmap.

## Worked example: a real playbook and runbook, side by side

[`examples/upgrade_ios/`](../examples/upgrade_ios/) has the same Cisco IOS-XE upgrade
written twice: once as a real Ansible playbook, once as a Pleiades runbook (in both
its explicit and module-as-key-sugar forms). It is the best single artifact for
seeing every point in this book applied to one real scenario at once, including the
exact `pleiades validate` output the runbook produces today (declared, not yet
implemented, the same honest refusal [Start here](01-start-here.md) describes).

## Playbook to runbook keyword map

| Ansible playbook key | Pleiades runbook key | Notes |
|---|---|---|
| `hosts:` | `hosts:` | Same meaning: a default target. A task's own `params.target` (or module-as-key sugar's bare `target:`) still wins when set. |
| `pre_tasks:` | `pretasks:` | Same phase, no underscore. |
| `tasks:` | `tasks:` | Same. |
| `post_tasks:` | `posttasks:` | Same phase, no underscore. |
| `block:` | `block:` | Same grouping. |
| `rescue:` | `rescue:` | Accepted and validated; the Walk-tier executor does not run rescue handlers yet (see [Start here](01-start-here.md)). |
| `always:` | `always:` | Same status as `rescue:` above: accepted, not yet executed. |
| `register:` | `register:` | Same idea: name a result for a later task to read. Addressed as `stat.<name>[<deviceID>].<field>` in `when_cel`, not as a bare Jinja variable. |
| `when:` (single or list) | `when:` | A list ANDs, same as Ansible. Pleiades evaluates CEL underneath, not Jinja, but a plain comparison reads identically in both. |
| — | `when_or:` | New. A list ORed instead of ANDed. |
| — | `when_cel:` | New. One raw CEL expression, for a condition `when`/`when_or` cannot express. |
| — | `register_mask:` | New. Masks a field of this task's own registered result the instant it registers. |
| — | `secret_mask:` | New. Retroactively masks a named, already-registered result's fields. |
| — | `lock_acquisition:` | New. `per_device_as_reached` (default) or `all_at_plan_time`. |
| — | `parallel:` | New. Native fan-out/join; mutually exclusive with `fqcn:`/`block:`. |
| `name:` | `name:` | Same, free-form label. |
| module name as a task key (e.g. `ansible.builtin.copy:`) | `fqcn:` + `params:`, or module-as-key sugar | See [Two ways to write a task](../examples/upgrade_ios/README.md#two-ways-to-write-a-pleiades-task). |
| `vars:` (play or task level) | *(not supported)* | No template rendering exists; see [What transfers](#what-transfers-and-what-does-not). |
| `{{ jinja }}` anywhere in a module's args | *(not supported)* | `params:` is always a literal value. |
| `loop:` / `with_items:` | *(not supported)* | A task runs once per its target device, never once per list item. |
| `handlers:` / `notify:` | *(not supported)* | No handler mechanism exists. |
| `tags:` | *(not supported)* | No tag-based task selection. |
| `become:` / `become_user:` | *(not supported)* | No privilege-escalation directive; a Collection method's own `ExecutionContext.RequiresElevation` states this instead, as data, not as a runbook key. |
| `serial:` | *(not supported)* | No batched-rollout control. |
| `roles:` | *(not supported)* | No role mechanism yet; it is on the open roadmap. |
| `ignore_errors:` | *(not supported)* | A failed task fails its run; no per-task override. |
| `changed_when:` / `failed_when:` | *(not supported)* | A Collection method's own `Result.Changed` is the only changed signal; no runbook-level override. Read the change forward with `when_cel` instead, the way [the worked example](../examples/upgrade_ios/README.md) does for its checksum gate. |
| `group_vars/` / `host_vars/` directories | *(not supported)* | No inventory-level variable layering of any kind. |

## Borrowed vocabulary: exact semantics

A term reading the same in both files does not always mean the same guarantee
underneath. Where it matters:

- **`when:`** is CEL, not Jinja, underneath. A simple equality or substring check
  reads identically; anything relying on a Jinja filter does not port and needs
  rewriting against CEL (or `when_cel:` for anything past a bare comparison).
- **`register:`** in Ansible is read back with `{{ result.stdout }}` templating. In
  Pleiades there is no templating at all: a later task's `when_cel:` reads it
  directly (`stat.result['deviceID'].stdout`), and nothing else can reference it.
- **`block:` conditions.** Ansible's block-level `when:` gates every task inside at
  once. Pleiades evaluates a task's own condition once per task; a block task's own
  condition is never walked by the executor. The equivalent "skip everything" effect
  today means repeating the same `when_cel:` on every task inside the block.
- **Credentials.** Ansible reads `{{ vault_* }}` variables from an `ansible-vault`
  file referenced from `vars:`. Pleiades has no vars or vault mechanism: `pleiades
  add-credential <device> --username <user>` prompts for a secret once and writes it
  to a local AES-256-GCM encrypted store. Neither a runbook nor `inventory.yaml` ever
  contains a password.
- **`no_log: true`** is whole-task secrecy: nothing about that task appears in output.
  `register_mask:`/`secret_mask:` are field-level and apply to the registered
  *result*, not the task's own invocation; a password field is scrubbed from every
  later printed line of the run, not just the task that read it.

## Module to FQCN map

Sourced from the real catalog: every `<namespace>.<method>` name below is registered
today, capability-checked, and reachable through the real dispatcher. The
authoritative, always-current version of just the Pleiades side is
[the generated module catalog](reference/modules/index.md); this table adds the
Ansible-side name for migration purposes and is maintained by hand alongside it, not
yet emitted by `tools/gendocs` itself (a real gap: the `Doc` metadata contract has no
field for an Ansible-equivalent name yet, so this table can drift from the catalog in
a way the generated pages cannot; treat a mismatch as a documentation bug in this
file, not in the catalog).

Only the four `net.catalyst.*` rows are `implemented` today; every other FQCN below is
`declared`: registered, validated, and refused at call time with an explicit "not
implemented yet" error rather than a silent no-op. See
[Implementation status](reference/implementation-status.md) for the exact list.

**Execution**

| Ansible | Pleiades FQCN | Capability |
|---|---|---|
| `ansible.builtin.command` | `exec.command` | `CommandExecCapable` |
| `ansible.builtin.shell` | `exec.shell` | `ShellExecCapable` |

**Packages**

| Ansible | Pleiades FQCN | Capability |
|---|---|---|
| `ansible.builtin.package` | `pkg.install`, `pkg.remove`, `pkg.upgrade` | `PackageManagerCapable` |
| `ansible.builtin.apt` | `pkg.apt.install`, `pkg.apt.remove`, `pkg.apt.upgrade` | `AptCapable` |
| `ansible.builtin.dnf` | `pkg.dnf.install`, `pkg.dnf.remove`, `pkg.dnf.upgrade` | `DnfCapable` |

**Services**

| Ansible | Pleiades FQCN | Capability |
|---|---|---|
| `ansible.builtin.service` | `svc.start`, `svc.stop`, `svc.restart`, `svc.enable`, `svc.disable` | `ServiceManagerCapable` |
| `ansible.builtin.systemd` | `svc.systemd.start`/`.stop`/`.restart`/`.enable`/`.disable`/`.daemon_reload` | `SystemdCapable` |
| `ansible.windows.win_service` | `svc.windows.start`/`.stop`/`.restart`/`.enable`/`.disable` | `WindowsServiceCapable` |

**Identity**

| Ansible | Pleiades FQCN | Capability |
|---|---|---|
| `ansible.builtin.user` | `identity.user.create`, `.modify`, `.remove` | `PosixAccountCapable` |
| `ansible.builtin.group` | `identity.group.create`, `.modify`, `.remove` | `PosixAccountCapable` |

**Files.** Ansible's `file` module takes one `state` parameter covering five unrelated
operations. Here they are five separate methods.

| Ansible | Pleiades FQCN | Capability |
|---|---|---|
| `ansible.builtin.copy` | `file.copy` | `POSIXFileSystemCapable` |
| `ansible.builtin.template` | `file.template` | `POSIXFileSystemCapable` |
| `ansible.builtin.file` | `file.directory`, `file.symlink`, `file.remove`, `file.touch`, `file.permissions` | `POSIXFileSystemCapable` |
| `ansible.builtin.lineinfile` | `file.line.set`, `file.line.remove` | `POSIXFileSystemCapable` |
| `ansible.builtin.blockinfile` | `file.block.set`, `file.block.remove` | `POSIXFileSystemCapable` |

**Network devices**

| Ansible | Pleiades FQCN | Capability |
|---|---|---|
| `ansible.netcommon.cli_command` | `net.cli.command` | `NetworkCLICapable` |
| `ansible.netcommon.cli_config` | `net.cli.config` | `NetworkCLICapable` |
| `ansible.netcommon.netconf_config` | `net.netconf.config` | `NetconfCapable` |
| `cisco.ios.ios_config` | `net.ios.config` | `CiscoIOSCapable` |
| `junipernetworks.junos.junos_config` | `net.junos.config` | `JunosCapable` |
| `arista.eos.eos_config` | `net.eos.config` | `AristaEOSCapable` |
| `cisco.dnac.*_info` | `net.catalyst.device_facts`, `.reachability`, `.site_facts`, `.tag_facts` | `CatalystAPICapable` |

The four `net.catalyst.*` methods are controller-side and read-only, against Cisco
Catalyst Center's REST API. They are the only `implemented` rows in this whole table,
verified against Cisco's public DevNet sandbox.

**Extended infrastructure**

| Ansible | Pleiades FQCN | Capability | Context |
|---|---|---|---|
| `ansible.posix.firewalld` | `fw.firewalld.allow`, `.deny`, `.reload` | `FirewalldCapable` | target side |
| `ansible.posix.mount` | `fs.mount`, `fs.unmount` | `LinuxCapable` | target side |
| `ansible.windows.win_feature` | `win.feature.install`, `.remove` | `WindowsFeatureCapable` | target side |
| `community.general.archive` | `archive.create` | `POSIXFileSystemCapable` | target side |
| `community.general.unarchive` | `archive.extract` | `FileTransferCapable` | hybrid |
| `community.docker.docker_container` | `container.docker.run`, `.stop`, `.remove` | `DockerCapable` | hybrid |
| `amazon.aws.ec2_instance` | `cloud.aws.ec2.create`, `.terminate` | `AWSAPICapable` | controller side |
| `amazon.aws.s3_bucket` | `cloud.aws.s3.create_bucket`, `.delete_bucket` | `AWSAPICapable` | controller side |

The two cloud entries are the clearest controller-side cases: they call an API, not a
device over SSH. Ansible needs `delegate_to: localhost` to express this; Pleiades
makes execution context a first-class manifest field, so no such hack is needed.

**Gating and facts**

| Ansible | Pleiades FQCN | Capability | Context |
|---|---|---|---|
| `ansible.builtin.uri` | `http.request` | none | controller side |
| `ansible.builtin.wait_for` | `pleiades.builtin.wait.port`, `wait.path`, `wait.search` | `NetworkAddressableCapable` | hybrid |
| `ansible.builtin.setup` | `facts.gather` | `FactGathererCapable` | target side |
| `ansible.builtin.set_stats` | `set_metadata` (an engine builtin, not a Collection method) | none | controller side |

`pleiades.builtin.wait.port` and `set_metadata` are not 1:1 ports of an Ansible
module; they are native to Pleiades, which is why the first lives under a reserved
`pleiades.builtin.` namespace and the second is an engine builtin reachable by its
bare name rather than a Collection FQCN at all. `wait.path`/`wait.search` have not
been renamed under `pleiades.builtin.` yet, an intentional inconsistency rather than
an oversight.

**Not yet in the catalog at all:** `ansible.builtin.debug`, `ansible.builtin.assert`,
`ansible.builtin.fail`, `ansible.builtin.pause`, `ansible.builtin.git`, and anything
from a Galaxy collection not listed above. A missing row here is either a gap to fill
in a future phase, or a case for `forge new-collection` to add it yourself; see
[Extending Pleiades](reference/index.md).

## AWX / AAP object map

Pleiades' Crawl tier (a Controller, a Runner, and NATS; see
[Start here](01-start-here.md#the-walk-crawl-and-run-tiers)) is the layer that
corresponds to AWX at all. The dispatcher, RBAC, and job model are real and tested;
several AWX concepts below have no Pleiades equivalent yet, which this table states
plainly rather than implying a rough match exists.

| AWX / AAP object | Pleiades equivalent | Status |
|---|---|---|
| Job template | A runbook, dispatched via `POST /api/v1/jobs/dispatch` | `beta`: the API and dispatcher are real; the job does not yet reach a real device (see [Start here](01-start-here.md)) |
| Inventory | `inventory.yaml`, or a synced inventory via a sync plugin | `beta` (static), `experimental` (sync plugins; only `catalyst_center`, itself `declared`, exists beyond the built-in `static_yaml` plugin) |
| Credential | A `pleiades add-credential` entry in the local encrypted store | `beta`, Walk tier only. No credential *types* (only username+password/key), no injector engine |
| Workflow (a DAG of job templates) | A single runbook's own `block`/`rescue`/`parallel` DAG | `experimental`: a runbook is itself a DAG, but chaining multiple independent runbooks the way an AWX workflow chains job templates does not exist |
| Survey | — | `design`, not built |
| Approval node | — | `design`, not built |
| Schedule (RRULE) | — | `design`, not built |
| Notification template | — | `design`, not built |
| Execution environment | — | `design`, not built. The static binary is the point; see [Start here](01-start-here.md)'s FAQ |
| Instance group | — | `design`, not built. No capacity/admission control exists yet |
| RBAC (organizations, teams, roles) | The control plane's own RBAC | `beta`, real and tested, but the object model has not been checked against AWX's own for parity |

## Inventory migration

`pleiades init` scaffolds an empty `inventory.yaml`. `pleiades add-host <name> --type
<type> [--set key=value ...] [--tags ...]` adds one device at a time by hand;
`pleiades inventory sync --plugin <name>` pulls devices from an external source (today,
only `catalyst_center`, which is itself `declared`). Nothing reads an Ansible dynamic
inventory script or a Galaxy inventory plugin directly. There is no bulk import path
from an existing AWX inventory today: migrating one means walking its host list and
issuing one `add-host` per device, or writing a new sync plugin
(`pleiades forge new-plugin`) against wherever AWX's inventory source actually is.

## Try it yourself

```console
$ cd examples/upgrade_ios/pleiades
$ pleiades validate runbooks/upgrade_ios_xe.yaml
$ pleiades validate runbooks/upgrade_ios_xe_sugar.yaml   # same findings, different syntax
```

```console
$ cd examples/upgrade_ios/ansible
$ ansible-galaxy collection install cisco.ios ansible.netcommon   # once
$ ansible-playbook -i inventory.ini upgrade_ios_xe.yml --syntax-check
```
