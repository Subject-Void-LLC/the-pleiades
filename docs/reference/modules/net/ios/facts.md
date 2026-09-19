---
status: beta
---

# net.ios.facts

Gathers structured facts from a Cisco IOS device over its CLI.

Closes a real gap: facts.gather requires FactGathererCapable, which no Cisco device type declares, so before this method a Cisco device could be commanded and configured but never described. Opens an interactive PTY session over SSH using netcli.IOS's own paging and prompt conventions, runs the read-only show commands the requested subsets need, and emits what it parses through EmitFact rather than SetStat, the same choice facts.gather and net.catalyst.device_facts both make: a fact is long-lived drift data worth comparing across weeks, and a software version recorded as a stat answers nothing next month. Every parser in this method was written against output captured from a real Cisco IOS XE device rather than from documentation or memory. A field the device does not report is left out entirely rather than emitted as an empty string, so a condition can tell "this device does not say" from "this device says nothing". Nothing is changed, so this always reports no change. Two of cisco.ios.ios_facts's own subsets are deliberately absent rather than accepted and ignored: config, because net.ios.config's own backup parameter already captures a running-config and doing it twice invites two answers, and hardware, because the memory and flash figures IOS reports vary enough by platform that parsing them generically would be a guess.

## Attributes

|  |  |
| --- | --- |
| Capabilities | `CiscoIOSCapable` |
| Transports | `ssh` |
| Requires elevation | no |
| Check mode | Supported: reports what it would change and changes nothing |
| Engine version | `>=1.0.0` |

## Parameters

| Name | Type | Required | Default | Description |
| --- | --- | --- | --- | --- |
| `gather_subset` | `list of string` | no | `["min"]` | Which subsets to gather: "min" (hostname, version, model, serial number, image, uptime, from "show version" and "show inventory"), "interfaces" (from "show ip interface brief"), or "all" for both. An unrecognized subset is refused rather than skipped. |
| `insecure_skip_host_key_verify` | `bool` | no | `false` | Skip SSH host key verification for this task. This removes protection against a machine in the middle answering for the device, so set it only for a target you have decided does not need it. |

## Returns

| Name | Type | Returned | Description |
| --- | --- | --- | --- |
| `ansible_net_hostname` | `string` | when the min subset is gathered and the device reports it | The device's own hostname, read from the "<hostname> uptime is ..." line of "show version". |
| `ansible_net_version` | `string` | when the min subset is gathered and the device reports it | The IOS XE version string, e.g. "17.15.04c". |
| `ansible_net_model` | `string` | when the min subset is gathered and the device reports it | The platform model, e.g. "C8000V". |
| `ansible_net_serialnum` | `string` | when the min subset is gathered and the device reports it | The chassis serial number, preferring "show inventory"'s Chassis SN and falling back to "show version"'s Processor board ID. |
| `ansible_net_image` | `string` | when the min subset is gathered and the device reports it | The running system image file, e.g. "bootflash:packages.conf". |
| `ansible_net_uptime` | `string` | when the min subset is gathered and the device reports it | Uptime exactly as the device words it, e.g. "1 hour, 32 minutes". Not converted to seconds: IOS reports a rounded phrase, and parsing it into a precise number would invent precision the device never gave. |
| `ansible_net_interfaces` | `list of map` | when the interfaces subset is gathered | One entry per interface: name, ip_address (omitted when the device says "unassigned"), status, and protocol. Status is taken whole, so "administratively down" is reported as written rather than truncated at the first space. |
| `ansible_net_gather_subset` | `list of string` | always | The subsets actually gathered, after "all" is expanded. |

## Undoing this

**Cannot be undone.** This method never records a reversing instruction, so a rollback reaching a task that used it stops rather than guessing.

A read-only fact gatherer changes nothing on the device, so there is nothing to undo.

Note that no rollback engine reads this yet. What exists today is the recording, which has to happen during the forward run because the values an undo needs are gone once the change is applied.

## See also

- `facts.gather`
- `net.catalyst.device_facts`
- `net.ios.config`

## Examples

Gather a device's identity before deciding anything:

```yaml
- name: Learn what this router is
  net.ios.facts:
  register: device
```

Gather interfaces as well:

```yaml
- name: Learn the interface list too
  net.ios.facts:
    gather_subset:
      - all
```

