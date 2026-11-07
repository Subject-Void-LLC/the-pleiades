---
status: beta
---

# facts.gather

Gathers system facts from the target: OS, kernel, distribution, and on request its packages, services, listening ports, accounts and named files.

Reads every system fact it knows by default, over SSH or, from a Windows host, over WinRM (identity, kernel and system, distribution and its release, memory and swap, processors, uptime) and emits each one as a fact under this platform's own key (os.distribution, hardware.memory_mb) holding a native value (the os-release ID, a lowercase family, an integer major version), not under ansible.builtin.setup's names, which are not stored, so it is kept as drift data, which is also a stat a register, until or when reads as result.<register>.<name>. filter narrows it. A fact the device cannot answer for is left out entirely: there is no guessed default and no empty string, so a condition reading os.distribution can tell "this device does not say" from "this device says nothing". Nothing is changed and no command is elevated, so this always reports no change. gather_subset chooses what is read: min (identity, kernel, distribution) and hardware (memory, processors, uptime), the default; packages, one fact per installed package (package.<name>: version, arch and source, from dpkg, rpm, apk, FreeBSD's pkg, Homebrew, pkgutil's receipts, or a Windows host's Uninstall keys); services, two facts per service (service.<name>: enabled and status, and service_state.<name>: its state, which moves on its own and so is kept as its latest value only, never as drift), from systemd, OpenRC, FreeBSD's rc, launchd, or the Windows service control manager, leaving out a systemd unit that lasts only as long as a login, a connection or a process: a transient unit, a user@ or user-runtime-dir@ instance, or one connection's instance of an Accept=yes socket (a lingering user's user@<uid>.service is left out too, though it lasts as long as the host and not a login: that is a choice, so a user's own service manager is never listed as a service; and systemd before version 230 does not report a transient unit as transient, so there such a unit is still listed); ports, one fact per listening socket (port.<protocol>:<address>:<port>, from the kernel's socket tables on Linux, sockstat on FreeBSD, netstat on macOS, and the TCP/IP cmdlets on Windows); users, one fact per local account and group (user.<name>, group.<name>, from /etc/passwd and /etc/group read without their password field, a Mac's local directory, or a Windows host's local accounts), never a password or its hash; and files, one fact per path named in files (file.<path>: metadata and a SHA-256 digest, plain for a file anyone may read and keyed for any other, never the content), where a path the login cannot reach is unreadable, never absent. The sources each return names are Linux's: FreeBSD and macOS answer the same facts from sysctl (and macOS its distribution from sw_vers), and Windows from CIM, where host.name is the computer name, os.kernel.name, os.family and os.distribution are windows, the kernel release and os.version are the OS version, and os.edition is the edition's caption. A family is read or the task says it was not: a source on the device whose listing fails, or answers without the structure it always prints, fails the task, so no item reads as removed; a family no source on the device answers fails a task that named its subset, and is a warning for one that reached it through all. Two of ansible.builtin.setup's parameters are absent rather than accepted and ignored: gather_timeout is the task's own timeout here, and fact_path's local fact files are a separate feature this does not implement.

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
| `filter` | `list of string` | no | - | Shell-style patterns, as ansible.builtin.setup takes them, narrowing which facts are gathered. A fact is gathered when its key matches any pattern, so ["os.*"] gathers the operating system's facts (its distribution, family, version, codename and kernel) and nothing else. Leaving this out gathers everything the chosen subsets gather, and so does an empty list. A pattern set matching none of the facts this method knows about is refused rather than gathering nothing, since that is a typo far more often than an intention. Narrowing also skips the commands behind the facts it excludes, so it is a real saving rather than a filter on the way out. A service's state is service_state.<name>, which a pattern for service.<name> alone does not match. |
| `gather_subset` | `list of string` | no | - | The subsets to gather: min and hardware as ansible.builtin.setup names them, packages and services (Ansible's package_facts and service_facts), ports, users and files, or all, every one; a name with a leading ! leaves that subset out wherever it stands in the list, unless the task also names that subset itself, as Ansible reads it (files with paths in files and !files together are still refused), and a list of only such names starts from min and hardware. min is gathered unless !min is named. Leaving this out gathers min and hardware, what this method always read, so a task never starts listing a host's packages without asking. files is gathered whenever files names paths, all gathers it only then, and naming files with no paths is refused. An unknown name is refused. |
| `files` | `list of string` | no | - | Absolute paths to describe, at most 64: for each, whether it is present, absent or unreadable and, when present, its type, size, owner and group (uid and gid on a POSIX device), mode (attributes on Windows), the SHA-256 and size of a regular file anyone may read, and a link's target. Content is never read back. A file not world-readable (on a POSIX device, one its mode or a directory above it keeps from others; on Windows, one whose ACL does not let Everyone, Authenticated Users or Users read it) keeps no plain digest and no size, since a stored digest of a small secret can be guessed offline by anyone who may read the facts: its digest is kept only as hmac_sha256, HMAC-SHA-256 under a key the project (from its master key) or the Controller (PLEIADES_FACTS_DIGEST_KEY) holds, with hmac_key naming the key, so an unchanged file keeps its value and a change still shows. Where no such key is held the file is described without a digest. A path is absent only when the device says it does not exist; one inside a directory the login may not search, or that Windows will not open, is unreadable. A link is described as itself, not what it points to. A network path (a UNC path such as \\server\share) is refused. |
| `insecure_skip_host_key_verify` | `bool` | no | `false` | Skip SSH host key verification for this task. This removes protection against a machine in the middle answering for the device, so set it only for a target you have decided does not need it. |

## Returns

| Name | Type | Returned | Description |
| --- | --- | --- | --- |
| `host.name` | `string` | when the device reports one | The short host name, which is everything before the first dot of what uname -n reports; on Windows the computer name. |
| `os.kernel.release` | `string` | when the device reports one | The kernel release, as uname -r reports it; on Windows the OS version. |
| `hardware.architecture` | `string` | when the device reports one | The machine hardware name, as uname -m reports it, for example x86_64 or aarch64; on Windows the same names, read from the processor architecture Windows names (AMD64 is x86_64, ARM64 is aarch64, x86 is i686), and none for one it names otherwise. |
| `os.distribution` | `string` | when the device names one | The distribution, as a lowercase identifier: ID in /etc/os-release as the file writes it (debian, ubuntu, rhel, rocky, almalinux, ol, fedora, amzn, alpine, arch, opensuse-leap, sles and the rest), freebsd on FreeBSD, macos on macOS and windows on Windows. An /etc/os-release naming no ID reports none. |
| `os.version` | `string` | when the device names one | The distribution version: VERSION_ID in /etc/os-release, on Debian the point release /etc/debian_version holds when it is more precise (12.12 where VERSION_ID is 12, and forky/sid on testing, which has no VERSION_ID), and on the SUSE family VERSION_ID's major and minor parts; ProductVersion on macOS and the OS version on Windows. A rolling release that publishes no VERSION_ID reports no such fact. |
| `hardware.memory_mb` | `int` | when the device reports it | Total usable memory in whole megabytes, truncated: from MemTotal in /proc/meminfo, hw.physmem on FreeBSD, hw.memsize on macOS and TotalPhysicalMemory on Windows. |
| `hardware.cpu.sockets` | `int` | when /proc/cpuinfo reports it, or Windows does | The number of physical processor packages, counted as the distinct physical id values in /proc/cpuinfo (NumberOfProcessors on Windows). An architecture whose /proc/cpuinfo carries no physical id field reports no such fact rather than a guessed 1. |
| `host.uptime_seconds` | `int` | when the device reports it | How long the device has been up, in whole seconds, from the first field of /proc/uptime; from the boot time and the device's own clock on FreeBSD, macOS and Windows. |
| `host.nodename` | `string` | when the device reports one | The full node name uname -n reports, before host.name cuts it at the first dot; on Windows the DNS host name, with the domain when the host is joined to one. |
| `os.kernel.name` | `string` | when the device reports one | The kernel's name, lowercase: linux, freebsd or darwin, as uname -s reports it, and windows on Windows. |
| `os.version_major` | `int` | when the device names a version whose first part is a whole number | The distribution version up to its first dot, as a whole number, such as 24 for 24.04. A version whose first part is not one (Debian testing's forky/sid) reports none. |
| `os.family` | `string` | when the device names its distribution | The family the distribution belongs to, lowercase: debian, redhat, suse, arch, alpine, freebsd, macos or windows. A distribution whose os-release ID this method has not measured has a family only where Ansible's own family table lists its NAME (Linux Mint is debian), lowercased (gentoo, slackware): a family is never guessed. |
| `os.codename` | `string` | when the distribution names one | The release's codename: VERSION_CODENAME in /etc/os-release (bookworm, noble), then UBUNTU_CODENAME, then the name in parentheses in VERSION (Blue Onyx on Rocky Linux 9.6, Plow on Red Hat). A distribution that names none (Fedora, Alpine, Amazon Linux, Oracle Linux, CentOS Stream, the SUSE family) reports none, and so do FreeBSD, macOS and Windows. |
| `os.edition` | `string` | on Windows, when the host names it | The Windows edition's caption, such as Microsoft Windows Server 2022 Datacenter. |
| `hardware.swap_mb` | `int` | when the device reports it, never on macOS or Windows | Total swap in whole megabytes, truncated, from SwapTotal in /proc/meminfo (vm.swap_total on FreeBSD); 0 on a device with no swap. macOS and Windows report none: a Mac's swap files are made and removed as memory is needed, so their total moves on its own, and a Windows page file is not swap. |
| `hardware.cpu.threads` | `int` | when the device reports it | The number of logical processors, counted as the processor entries in /proc/cpuinfo (hw.ncpu on FreeBSD and macOS, NumberOfLogicalProcessors on Windows). |
| `hardware.cpu.cores` | `int` | when /proc/cpuinfo reports cpu cores, or Windows does | The cores per processor package, from the first cpu cores field in /proc/cpuinfo (the first package's NumberOfCores on Windows). |
| `package.<name>` | `map` | with the packages subset, for each installed package | One installed package: version, arch and source (dpkg, rpm, apk, pkg, brew, brew-cask, pkgutil or registry). A name a key cannot hold is percent-encoded; a name two packages share takes :arch, then @source, then #2 and on in the order the source lists them. A package its source gives no version has none. |
| `service.<name>` | `map` | with the services subset, for each service the service manager lists | One service by its manager's name for it: enabled, status and source; what it is doing now is service_state.<name>. From systemd (source systemd), status is the unit file's state (enabled, disabled, static, masked, generated, indirect and so on), enabled meaning enabled or enabled-runtime, and a unit that lasts only as long as a login, a connection or a process (a transient unit, a user@ or user-runtime-dir@ instance, one connection's instance of an Accept=yes socket) not listed, a lingering user's user@<uid>.service left out as well by choice though it lasts as long as the host, and a transient unit still listed on systemd before 230, which does not report it as transient; from OpenRC (openrc), status is the runlevels the script is in, or disabled, enabled meaning a runlevel other than shutdown; from FreeBSD's rc, status is enabled or disabled; from launchd, status is enabled or disabled, a daemon launchd has not loaded because it is disabled being listed all the same, and a job launchd starts once per connection or session (a label ending in a UUID, such as com.openssh.sshd.<UUID>) not listed, since a new one comes with every login; from the Windows service control manager (scm), status is the start mode (auto, delayed, manual, disabled, boot, system), enabled meaning it starts with Windows, and a per-user service is listed once by its template's name with per_user true, never by the instance Windows makes for each signed-in user (Template_<LUID>), whose name changes with every sign-in. |
| `service_state.<name>` | `string` | with the services subset, for each service its manager says the state of | What the service service.<name> names is doing, kept as its latest value only and never as drift, since services start and stop on their own (a timer's job, a service started when something asks for it). From systemd, the unit's sub-state (running, exited, dead, failed, inactive and the others systemd reports, such as activating or auto-restart); from OpenRC, running, stopped or OpenRC's own word (crashed, inactive), or unknown before OpenRC has booted; from FreeBSD's rc, running, stopped or unknown (an rc script that is not enabled, or whose status only root may read); from launchd, running or stopped; from Windows, the manager's own word lower-cased (running, stopped, start pending). A per-user service's template has none. |
| `file.<path>` | `map` | with the files subset, for each path in files | One named path: state (present, absent or unreadable); when present, type (file, directory, link, fifo, socket, char or block), size (not for a directory, nor a file not everyone may read), uid, gid, owner, group and mode as four octal digits on a POSIX device, attributes and owner on Windows, for a regular file world_readable and, when anyone may read it, sha256 and size, or when not, hmac_sha256 and hmac_key (a keyed digest the store can compare and nobody without the key can test guesses against; on a Runner the task's own result holds it sealed to the Controller, as sha256_sealed, and only the stored fact holds it keyed), readable, and link_target for a link. The path is percent-encoded in the key as any item name is. |
| `port.<protocol>:<address>:<port>` | `map` | with the ports subset, for each listening socket | One listening TCP socket, or UDP socket bound with no peer: protocol (tcp or udp), address (0.0.0.0 or :: for any; in brackets in the key when it is IPv6), port, uid on Linux, and process, the owning process's name, where the login may see it (on Linux its own processes, or every one as root; on FreeBSD every one unless security.bsd.see_other_uids is off; every one on macOS; on Windows every one an administrator sees). A socket taking both families is ::. |
| `user.<name>` | `map` | with the users subset, for each local account | One local account: uid, gid, home, shell and source (files from /etc/passwd, dslocal from a Mac's local directory), or on Windows sid, enabled and source sam. Accounts a directory service adds (LDAP, SSSD, a domain) are not local and not listed; the password field is never read. |
| `group.<name>` | `map` | with the users subset, for each local group | One local group: gid (sid on Windows), members as the group lists them (an account whose primary group it is is not repeated), and source. A Windows group whose members could not be listed has no members key. |

## Undoing this

**Cannot be undone.** This method never records a reversing instruction, so a rollback reaching a task that used it stops rather than guessing.

Reading is not changing. This method runs fixed reads (uname, /etc/os-release, /proc, sysctl, package and service listings, CIM and the registry on Windows), alters nothing on the device, and so has nothing an undo could restore.

`pleiades rollback` and the Controller's rollback route replay a recorded instruction. It is recorded during the forward run because the values an undo needs are gone once the change is applied.

## Facts it keeps

A real run stores each of these as the device's fact. A **stable** fact keeps every value it has had, and a change is drift; a **volatile** one keeps only its latest value and never counts as drift. A check stores none.

| Fact | Kind | Type | Description |
| --- | --- | --- | --- |
| `host.name` | stable | string | The short host name, which is everything before the first dot of what uname -n reports; on Windows the computer name. |
| `os.kernel.release` | stable | string | The kernel release, as uname -r reports it; on Windows the OS version. |
| `hardware.architecture` | stable | string | The machine hardware name, as uname -m reports it, for example x86_64 or aarch64; on Windows the same names, read from the processor architecture Windows names (AMD64 is x86_64, ARM64 is aarch64, x86 is i686), and none for one it names otherwise. |
| `os.distribution` | stable | string | The distribution, as a lowercase identifier: ID in /etc/os-release as the file writes it (debian, ubuntu, rhel, rocky, almalinux, ol, fedora, amzn, alpine, arch, opensuse-leap, sles and the rest), freebsd on FreeBSD, macos on macOS and windows on Windows. An /etc/os-release naming no ID reports none. |
| `os.version` | stable | string | The distribution version: VERSION_ID in /etc/os-release, on Debian the point release /etc/debian_version holds when it is more precise (12.12 where VERSION_ID is 12, and forky/sid on testing, which has no VERSION_ID), and on the SUSE family VERSION_ID's major and minor parts; ProductVersion on macOS and the OS version on Windows. A rolling release that publishes no VERSION_ID reports no such fact. |
| `hardware.memory_mb` | stable | integer | Total usable memory in whole megabytes, truncated: from MemTotal in /proc/meminfo, hw.physmem on FreeBSD, hw.memsize on macOS and TotalPhysicalMemory on Windows. |
| `hardware.cpu.sockets` | stable | integer | The number of physical processor packages, counted as the distinct physical id values in /proc/cpuinfo (NumberOfProcessors on Windows). An architecture whose /proc/cpuinfo carries no physical id field reports no such fact rather than a guessed 1. |
| `host.uptime_seconds` | volatile | integer | How long the device has been up, in whole seconds, from the first field of /proc/uptime; from the boot time and the device's own clock on FreeBSD, macOS and Windows. |
| `host.nodename` | stable | string | The full node name uname -n reports, before host.name cuts it at the first dot; on Windows the DNS host name, with the domain when the host is joined to one. |
| `os.kernel.name` | stable | string | The kernel's name, lowercase: linux, freebsd or darwin, as uname -s reports it, and windows on Windows. |
| `os.version_major` | stable | integer | The distribution version up to its first dot, as a whole number, such as 24 for 24.04. A version whose first part is not one (Debian testing's forky/sid) reports none. |
| `os.family` | stable | string | The family the distribution belongs to, lowercase: debian, redhat, suse, arch, alpine, freebsd, macos or windows. A distribution whose os-release ID this method has not measured has a family only where Ansible's own family table lists its NAME (Linux Mint is debian), lowercased (gentoo, slackware): a family is never guessed. |
| `os.codename` | stable | string | The release's codename: VERSION_CODENAME in /etc/os-release (bookworm, noble), then UBUNTU_CODENAME, then the name in parentheses in VERSION (Blue Onyx on Rocky Linux 9.6, Plow on Red Hat). A distribution that names none (Fedora, Alpine, Amazon Linux, Oracle Linux, CentOS Stream, the SUSE family) reports none, and so do FreeBSD, macOS and Windows. |
| `os.edition` | stable | string | The Windows edition's caption, such as Microsoft Windows Server 2022 Datacenter. |
| `hardware.swap_mb` | stable | integer | Total swap in whole megabytes, truncated, from SwapTotal in /proc/meminfo (vm.swap_total on FreeBSD); 0 on a device with no swap. macOS and Windows report none: a Mac's swap files are made and removed as memory is needed, so their total moves on its own, and a Windows page file is not swap. |
| `hardware.cpu.threads` | stable | integer | The number of logical processors, counted as the processor entries in /proc/cpuinfo (hw.ncpu on FreeBSD and macOS, NumberOfLogicalProcessors on Windows). |
| `hardware.cpu.cores` | stable | integer | The cores per processor package, from the first cpu cores field in /proc/cpuinfo (the first package's NumberOfCores on Windows). |
| `package.*` | stable | object | One installed package: version, arch and source (the database it was read from). |
| `service.*` | stable | object | One service: enabled, status (the manager's own word for its start setting) and source. |
| `service_state.*` | volatile | string | What one service is doing (running, stopped, the manager's own word), its latest value only. |
| `file.*` | stable | object | One path the task named: state (present, absent, unreadable) and, when present, type, size, owner, mode, sha256 and link target; never the content. |
| `port.*` | stable | object | One listening socket: protocol, address, port and, where the login may see it, the process. |
| `user.*` | stable | object | One local account: uid, gid, home and shell (sid and enabled on Windows); never a password or its hash. |
| `group.*` | stable | object | One local group: gid (sid on Windows) and its listed members. |

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

Record a configuration file's digest and who listens where:

```yaml
- name: Describe sshd's configuration and the listening sockets
  facts.gather:
    gather_subset:
      - ports
    files:
      - /etc/ssh/sshd_config
```

Gather only what a later condition reads:

```yaml
- name: Learn which distribution this is
  facts.gather:
    filter:
      - os.*
```

