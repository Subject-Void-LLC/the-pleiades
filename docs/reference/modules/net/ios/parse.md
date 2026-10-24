---
status: beta
---

# net.ios.parse

Runs a show command on a Cisco IOS or IOS XE device and returns its output parsed into Genie's schema.

Runs one show command over the same session net.ios.command opens and parses its output with a parser ported from Cisco's Genie (genieparser, Apache License 2.0), returning the same nested structure Genie returns for that command, so Genie's own schema documentation describes the result and a later task can compare two reads field by field, such as the state before a change and after its rollback. Each parser is held to every one of genieparser's own recorded device outputs and expected results for the command forms it covers. Output in which the parser finds nothing (no OSPF neighbors yet) is an empty result with empty true, not a failure, so a before and after can compare it; a command the device refuses (% BGP not active) fails the task. A command no parser covers is refused before the device is reached, naming the commands that are covered. It only reads, so it reports unchanged, and a check runs it as a real run does.

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
| `command` | `string` | yes | - | The show command to run and parse, one of the commands a parser covers (pleiades doc net.ios.parse lists them), for example "show ip interface brief" or "show ip route". IOS's abbreviations are not accepted: write the command in full. |
| `insecure_skip_host_key_verify` | `bool` | no | `false` | Skip SSH host key verification for this task. This removes protection against a machine in the middle answering for the device, so set it only for a target you have decided does not need it. |
| `insecure_telnet` | `bool` | no | `false` | Reach a device that has no SSH over bare Telnet, answering its login with the task's credential. Telnet carries everything, the login included, in cleartext, so set this only for a device on a network you trust. |

## Returns

| Name | Type | Returned | Description |
| --- | --- | --- | --- |
| `parsed` | `dict` | always | The command's output in Genie's schema for it: for show ip interface brief, interface, keyed by interface name, each with ip_address, interface_is_ok, method, status and protocol. |
| `parser` | `string` | always | The Genie parser class whose port parsed it, such as ShowIpInterfaceBrief. |
| `empty` | `bool` | always | Whether the parser found nothing in the output, parsed then being empty. |
| `stdout` | `string` | always | The command's output as the device printed it, except that the secrets in show archive config differences (a password, secret, key or SNMP community) are masked, as net.ios.replace masks them, before it is parsed or recorded. |

## Undoing this

**Cannot be undone.** This method never records a reversing instruction, so a rollback reaching a task that used it stops rather than guessing.

It runs one show command and changes nothing on the device, so there is nothing to undo.

Note that no rollback engine reads this yet. What exists today is the recording, which has to happen during the forward run because the values an undo needs are gone once the change is applied.

## See also

- `net.ios.command`
- `net.ios.facts`
- `net.ios.replace`

## Examples

Read the interfaces before a change, to compare after its rollback:

```yaml
- name: Read the interfaces
  net.ios.parse:
    command: show ip interface brief
  register: interfaces_before
```

