---
status: beta
---

# facts.gather

Gathers baseline system facts from the target (OS, kernel, distribution).

Reads a small, fixed set of system facts over SSH and emits each one as a fact, so it is kept as drift data rather than as a value that expires at the end of the run. A fact the device cannot answer for is left out entirely: there is no guessed default and no empty string, so a condition reading ansible_distribution can tell "this device does not say" from "this device says nothing". Nothing is changed and no command is elevated, so this always reports no change. Three of ansible.builtin.setup's parameters are absent rather than accepted and ignored: gather_subset has nothing to select between at this size, gather_timeout is the task's own timeout here, and fact_path's local fact files are a separate feature this does not implement.

## Attributes

|  |  |
| --- | --- |
| Capabilities | `FactGathererCapable` |
| Transports | `ssh` |
| Requires elevation | no |
| Runs | on or against the target device; acts on its target device |
| Check mode | Supported: reports what it would change and changes nothing |
| Engine version | `>=0.2.0` |

## Parameters

| Name | Type | Required | Default | Description |
| --- | --- | --- | --- | --- |
| `filter` | `list of string` | no | - | Shell-style patterns, as ansible.builtin.setup takes them, narrowing which facts are gathered. A fact is gathered when its name matches any pattern, so ["ansible_distribution*"] gathers the distribution and its version and nothing else. Leaving this out gathers everything, and so does an empty list. A pattern set matching none of the facts this method knows about is refused rather than gathering nothing, since that is a typo far more often than an intention. Narrowing also skips the commands behind the facts it excludes, so it is a real saving rather than a filter on the way out. |
| `insecure_skip_host_key_verify` | `bool` | no | `false` | Skip SSH host key verification for this task. This removes protection against a machine in the middle answering for the device, so set it only for a target you have decided does not need it. |

## Returns

| Name | Type | Returned | Description |
| --- | --- | --- | --- |
| `ansible_hostname` | `string` | when the device reports one | The short host name, which is everything before the first dot of what uname reports. |
| `ansible_kernel` | `string` | when the device reports one | The kernel release, as uname -r reports it. |
| `ansible_architecture` | `string` | when the device reports one | The machine hardware name, as uname -m reports it, for example x86_64. |
| `ansible_distribution` | `string` | when /etc/os-release names one | The distribution name, read from NAME in /etc/os-release, for example Ubuntu. |
| `ansible_distribution_version` | `string` | when /etc/os-release names one | The distribution version, read from VERSION_ID in /etc/os-release. A rolling release that publishes no VERSION_ID reports no such fact. |
| `ansible_memtotal_mb` | `int` | when /proc/meminfo reports it | Total usable memory in megabytes, from MemTotal in /proc/meminfo, truncated the way Ansible truncates it. |
| `ansible_processor_count` | `int` | when /proc/cpuinfo reports it | The number of physical processor packages, counted as the distinct physical id values in /proc/cpuinfo. An architecture whose /proc/cpuinfo carries no physical id field reports no such fact rather than a guessed 1. |
| `ansible_uptime_seconds` | `int` | when /proc/uptime reports it | How long the device has been up, in whole seconds, from the first field of /proc/uptime. |

## Undoing this

**Cannot be undone.** This method never records a reversing instruction, so a rollback reaching a task that used it stops rather than guessing.

Reading is not changing. This method runs uname and reads /etc/os-release and files under /proc, alters nothing on the device, and so has nothing an undo could restore.

Note that no rollback engine reads this yet. What exists today is the recording, which has to happen during the forward run because the values an undo needs are gone once the change is applied.

## See also

- `exec.command`
- `net.catalyst.device_facts`

## Examples

Gather everything before deciding what to do:

```yaml
- name: Learn what this device is
  facts.gather:
```

Gather only what a later condition reads:

```yaml
- name: Learn which distribution this is
  facts.gather:
    filter:
      - ansible_distribution*
```

