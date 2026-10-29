---
status: beta
---

# net.ios.facts

Gathers structured facts from a Cisco IOS device over its CLI.

Closes a real gap: facts.gather requires FactGathererCapable, which no Cisco device type declares, so before this method a Cisco device could be commanded and configured but never described. Opens an interactive session in netcli.IOS's own paging and prompt conventions and sends only show commands: by default every subset, so one call hands back what the device is, each interface with its VRF and state, and the running configuration with every secret masked. Each value is recorded twice under one name: as a fact (EmitFact, long-lived drift data) and as a stat (SetStat), so register, until and when read it as result.<register>.<name>. The configuration is a stat only, since its placeholders are keyed per run and would read as a change on every run. Every parser was written against output captured from real devices (a Catalyst 8000V on IOS XE 17.15.04c and an IOL XE router on 17.15.1), not from documentation or memory. A field the device does not report is left out rather than set to an empty string, so a condition can tell "this device does not say" from "this device says nothing". Nothing is changed, so this always reports no change, and a check runs it for real. The config subset needs privileged mode: an account that lands in user mode needs its enable secret stored (pleiades add-credential --enable-password-stdin). When config came from all, the default, and the device refuses show running-config, it is left out with a warning and the rest is returned; asked for by name, that refusal fails the task. cisco.ios.ios_facts's hardware subset is absent on purpose: the memory and flash figures IOS reports vary enough by platform that parsing them generically would be a guess.

## Attributes

|  |  |
| --- | --- |
| Capabilities | `CiscoIOSCapable` |
| Transports | `ssh`, `telnet`, `console` |
| Requires elevation | no |
| Runs | on or against the target device; acts on its target device |
| Check mode | Supported: reports what it would change and changes nothing |
| Engine version | `>=0.2.0` |

## Parameters

| Name | Type | Required | Default | Description |
| --- | --- | --- | --- | --- |
| `gather_subset` | `list of string` | no | `all` | Which subsets to gather: "min" (hostname, version, model, serial number, image, uptime, from "show version" and "show inventory"), "interfaces" (from "show ip interface brief", "show interfaces" and "show vrf", or "show ip vrf brief" where show vrf is refused), "config" (the running configuration, masked), or "all" for every one, the default. An unrecognized subset is refused rather than skipped. **Choices:** `min`, `interfaces`, `config`, `all` (case ignored). |
| `insecure_skip_host_key_verify` | `bool` | no | `false` | Skip SSH host key verification for this task. This removes protection against a machine in the middle answering for the device, so set it only for a target you have decided does not need it. |
| `insecure_telnet` | `bool` | no | `false` | Reach a device that has no SSH over bare Telnet, answering its login with the task's credential. Telnet carries everything, the login included, in cleartext, so set this only for a device on a network you trust. |

## Returns

| Name | Type | Returned | Description |
| --- | --- | --- | --- |
| `ansible_net_hostname` | `string` | when the min subset is gathered and the device reports it | The device's own hostname, read from the "<hostname> uptime is ..." line of "show version". |
| `ansible_net_version` | `string` | when the min subset is gathered and the device reports it | The software version: the "Cisco IOS XE Software, Version" line's on IOS XE, e.g. "17.15.04c", and on a device that prints no such line (an IOL XE router) the "Cisco IOS Software ..., Version" line's, e.g. "17.15.1". |
| `ansible_net_model` | `string` | when the min subset is gathered and the device reports it | The platform model, e.g. "C8000V". An IOL router reports none. |
| `ansible_net_serialnum` | `string` | when the min subset is gathered and the device reports it | The chassis serial number, preferring "show inventory"'s Chassis SN and falling back to "show version"'s Processor board ID. |
| `ansible_net_image` | `string` | when the min subset is gathered and the device reports it | The running system image file, e.g. "bootflash:packages.conf". |
| `ansible_net_uptime` | `string` | when the min subset is gathered and the device reports it | Uptime exactly as the device words it, e.g. "1 hour, 32 minutes". Not converted to seconds: IOS reports a rounded phrase, and parsing it into a precise number would invent precision the device never gave. |
| `ansible_net_interfaces` | `list of map` | when the interfaces subset is gathered | One map per interface, in the order "show ip interface brief" lists them. From that command: name, ip_address (left out when the device says "unassigned"), status and protocol, the status taken whole so "administratively down" is not cut at its space. From "show interfaces": description (when one is set), mtu (a number), admin_state ("down" when the interface is shut down, else "up") and line_protocol (as written, e.g. "up" or "down (notconnect)"). And vrf, the VRF's name, only when the interface is in one: an interface with no vrf key is in the global table. A list, not the map keyed by name cisco.ios.ios_facts returns, because this method has returned a list since it was written, a list keeps the device's own order, and a condition finds one interface with exists(i, i.name == "Ethernet0/0"). |
| `ansible_net_config` | `string` | when the config subset is gathered | The running configuration as "show running-config" prints it, with every secret masked by pkg/netcfg, the masking net.batfish.check applies. A masked value reads pleiades-masked- and sixteen hexadecimal digits: equal secrets mask alike within one run, so two devices' keys still compare, and differently in the next. Output that does not end in "end", or a line that would still hold a second secret after masking, fails the subset rather than returning any of the text. net.ios.config refuses a line holding a placeholder, so this text cannot be applied back as it is. |
| `warnings` | `list of string` | when something was left out | What was left out and why: the config subset, when all asked for it and the device refused show running-config; a VRF member the device did not list as an interface; a device that answers neither VRF command. |
| `ansible_net_gather_subset` | `list of string` | always | The subsets actually gathered, after "all" is expanded; config is absent when it was left out. |

## Undoing this

**Cannot be undone.** This method never records a reversing instruction, so a rollback reaching a task that used it stops rather than guessing.

A read-only fact gatherer changes nothing on the device, so there is nothing to undo.

Note that no rollback engine reads this yet. What exists today is the recording, which has to happen during the forward run because the values an undo needs are gone once the change is applied.

## See also

- `facts.gather`
- `net.catalyst.device_facts`
- `net.ios.config`
- `net.batfish.check`

## Examples

Learn the whole device before deciding anything:

```yaml
- name: Learn what this router is
  net.ios.facts:
  register: device
```

Wait until an interface's line protocol is up:

```yaml
- name: Wait for Ethernet0/1
  net.ios.facts:
    gather_subset:
      - interfaces
  register: r1
  until: 'result.r1.ansible_net_interfaces.exists(i, i.name == "Ethernet0/1" && i.line_protocol == "up")'
  retries: 10
  delay: 6
```

Only the identity, without the configuration:

```yaml
- name: Read the version
  net.ios.facts:
    gather_subset:
      - min
```

