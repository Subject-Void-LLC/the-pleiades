---
status: beta
---

# facts.gather

Gathers system facts from the target: OS, kernel, distribution, and on request its packages and services.

Reads every system fact it knows by default, over SSH or, from a Windows host, over WinRM (identity, kernel and system, distribution and its release, memory and swap, processors, uptime) and emits each one as a fact, so it is kept as drift data, which is also a stat a register, until or when reads as result.<register>.<name>. filter narrows it. A fact the device cannot answer for is left out entirely: there is no guessed default and no empty string, so a condition reading ansible_distribution can tell "this device does not say" from "this device says nothing". Nothing is changed and no command is elevated, so this always reports no change. gather_subset chooses what is read: min (identity, kernel, distribution) and hardware (memory, processors, uptime), the default; packages, one fact per installed package (package.<name>: version, arch and source, from dpkg, rpm, apk, FreeBSD's pkg, Homebrew, pkgutil's receipts, or a Windows host's Uninstall keys); and services, one fact per service (service.<name>: state, enabled and status, from systemd, FreeBSD's rc, launchd, or the Windows service control manager). The sources each return names are Linux's: FreeBSD and macOS answer the same facts from sysctl (and macOS its distribution from sw_vers), and Windows from CIM, where ansible_hostname is the computer name, ansible_system is Win32NT, the kernel and the distribution version are the OS version, and the distribution is the edition's caption, as Ansible reports them. Two of ansible.builtin.setup's parameters are absent rather than accepted and ignored: gather_timeout is the task's own timeout here, and fact_path's local fact files are a separate feature this does not implement.

## Attributes

|  |  |
| --- | --- |
| Capabilities | `FactGathererCapable` |
| Transports | `ssh`, `winrm` |
| Requires elevation | no |
| Runs | on or against the target device; acts on its target device |
| Check mode | Supported: reports what it would change and changes nothing |
| Engine version | `>=0.2.0` |

## Parameters

| Name | Type | Required | Default | Description |
| --- | --- | --- | --- | --- |
| `filter` | `list of string` | no | - | Shell-style patterns, as ansible.builtin.setup takes them, narrowing which facts are gathered. A fact is gathered when its name matches any pattern, so ["ansible_distribution*"] gathers the distribution and its version and nothing else. Leaving this out gathers everything, and so does an empty list. A pattern set matching none of the facts this method knows about is refused rather than gathering nothing, since that is a typo far more often than an intention. Narrowing also skips the commands behind the facts it excludes, so it is a real saving rather than a filter on the way out. |
| `gather_subset` | `list of string` | no | - | The subsets to gather, as ansible.builtin.setup names them: min, hardware, packages, services, or all; a name with a leading ! leaves that subset out, and a list of only such names starts from all. min is gathered unless !min is named. Leaving this out gathers min and hardware, what this method always read, so a task never starts listing a host's packages without asking. An unknown name is refused. |
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
| `ansible_nodename` | `string` | when the device reports one | The full node name uname -n reports, before ansible_hostname cuts it at the first dot. |
| `ansible_system` | `string` | when the device reports one | The kernel's name, as uname -s reports it: Linux, FreeBSD, Darwin. |
| `ansible_distribution_major_version` | `string` | when /etc/os-release names a version | VERSION_ID up to its first dot, such as 24 for 24.04. |
| `ansible_distribution_release` | `string` | when /etc/os-release names a codename | VERSION_CODENAME from /etc/os-release, such as noble. |
| `ansible_swaptotal_mb` | `int` | when /proc/meminfo reports it | Total swap in megabytes, from SwapTotal in /proc/meminfo; 0 on a device with no swap. |
| `ansible_processor_vcpus` | `int` | when /proc/cpuinfo lists processors | The number of logical processors, counted as the processor entries in /proc/cpuinfo. |
| `ansible_processor_cores` | `int` | when /proc/cpuinfo reports cpu cores | The cores per processor package, from the first cpu cores field in /proc/cpuinfo. |
| `package.<name>` | `map` | with the packages subset, for each installed package | One installed package: version, arch and source (dpkg, rpm, apk, pkg, brew, brew-cask, pkgutil or registry). A name a key cannot hold is percent-encoded; a name two packages share takes :arch, then @source, then #2 and on in the order the source lists them. A package its source gives no version has none. |
| `service.<name>` | `map` | with the services subset, for each service the service manager lists | One service by its manager's name for it: state, enabled, status and source. From systemd (source systemd), state is running, exited, dead, failed or inactive and status the unit file's state (enabled, disabled, static, masked); from FreeBSD's rc, state is running, stopped or unknown (an rc script that is not enabled, or whose status only root may read) and status enabled or disabled; from launchd, state is running or stopped and status the last exit status launchctl prints; from the Windows service control manager (scm), state is the manager's own word lower-cased (running, stopped, start pending) and status the start mode (auto, delayed, manual, disabled, boot, system), enabled meaning it starts with Windows. |

## Undoing this

**Cannot be undone.** This method never records a reversing instruction, so a rollback reaching a task that used it stops rather than guessing.

Reading is not changing. This method runs fixed reads (uname, /etc/os-release, /proc, sysctl, package and service listings, CIM and the registry on Windows), alters nothing on the device, and so has nothing an undo could restore.

`pleiades rollback` and the Controller's rollback route replay a recorded instruction. It is recorded during the forward run because the values an undo needs are gone once the change is applied.

## Facts it keeps

A real run stores each of these as the device's fact. A **stable** fact keeps every value it has had, and a change is drift; a **volatile** one keeps only its latest value and never counts as drift. A check stores none.

| Fact | Kind | Type | Description |
| --- | --- | --- | --- |
| `ansible_hostname` | stable | string | The short host name, which is everything before the first dot of what uname reports. |
| `ansible_kernel` | stable | string | The kernel release, as uname -r reports it. |
| `ansible_architecture` | stable | string | The machine hardware name, as uname -m reports it, for example x86_64. |
| `ansible_distribution` | stable | string | The distribution name, read from NAME in /etc/os-release, for example Ubuntu. |
| `ansible_distribution_version` | stable | string | The distribution version, read from VERSION_ID in /etc/os-release. A rolling release that publishes no VERSION_ID reports no such fact. |
| `ansible_memtotal_mb` | stable | integer | Total usable memory in megabytes, from MemTotal in /proc/meminfo, truncated the way Ansible truncates it. |
| `ansible_processor_count` | stable | integer | The number of physical processor packages, counted as the distinct physical id values in /proc/cpuinfo. An architecture whose /proc/cpuinfo carries no physical id field reports no such fact rather than a guessed 1. |
| `ansible_uptime_seconds` | volatile | integer | How long the device has been up, in whole seconds, from the first field of /proc/uptime. |
| `ansible_nodename` | stable | string | The full node name uname -n reports, before ansible_hostname cuts it at the first dot. |
| `ansible_system` | stable | string | The kernel's name, as uname -s reports it: Linux, FreeBSD, Darwin. |
| `ansible_distribution_major_version` | stable | string | VERSION_ID up to its first dot, such as 24 for 24.04. |
| `ansible_distribution_release` | stable | string | VERSION_CODENAME from /etc/os-release, such as noble. |
| `ansible_swaptotal_mb` | stable | integer | Total swap in megabytes, from SwapTotal in /proc/meminfo; 0 on a device with no swap. |
| `ansible_processor_vcpus` | stable | integer | The number of logical processors, counted as the processor entries in /proc/cpuinfo. |
| `ansible_processor_cores` | stable | integer | The cores per processor package, from the first cpu cores field in /proc/cpuinfo. |
| `package.*` | stable | object | One installed package: version, arch and source (the database it was read from). |
| `service.*` | stable | object | One service: state, enabled, status (the manager's own word for it) and source. |

## See also

- `exec.command`
- `net.catalyst.device_facts`

## Examples

Gather everything before deciding what to do:

```yaml
- name: Learn what this device is
  facts.gather:
```

Gather the installed packages and services:

```yaml
- name: Learn what is installed and running
  facts.gather:
    gather_subset:
      - packages
      - services
```

Gather only what a later condition reads:

```yaml
- name: Learn which distribution this is
  facts.gather:
    filter:
      - ansible_distribution*
```

