---
status: beta
---

# win.firewall.rule

Makes, changes or removes a Windows Defender Firewall rule over WinRM, or reports one as it is.

A rule is named by its Name, its identity (OpenSSH-Server-In-TCP, not its display name), and read and written in the machine's own rule store (PersistentStore). With state present, a rule that does not exist is made, with Windows' defaults for what the task leaves out (inbound, allow, any protocol, every profile, enabled, its name as its display name); a rule that exists is compared on the settings the task gives and only those, and a setting that differs is changed, so a built-in rule's profiles can be widened without restating its ports or program. absent removes it; query only reports it. A rule that exists only through Group Policy is refused, since a change here would not be kept. Profiles, ports and addresses compare as sets, and an IPv4 network compares the same written as 10.0.0.0/8 or with the dotted mask Windows answers with. Ports need protocol tcp or udp, the task's or the rule's. The script is a constant and every value reaches it as JSON on standard input, held to its form first. A rule this run made undoes to its removal; a change, to the earlier value of each setting it changed; a removal, to making the rule again as it was, outside the group it belonged to. Lists may also be given as one comma-separated string, which is how an undo writes them; an undo restoring a display name or description is withheld from the journal. A check reads and reports what would change.

## Attributes

|  |  |
| --- | --- |
| Capabilities | `WinRMCapable` |
| Transports | `winrm` |
| Requires elevation | no |
| Runs | on or against the target device; acts on its target device |
| Check mode | Supported: reports what it would change and changes nothing |
| Engine version | `>=0.2.0` |

## Parameters

| Name | Type | Required | Default | Description |
| --- | --- | --- | --- | --- |
| `name` | `string` | yes | - | The rule's Name, its identity: letters, digits, space and ._{}()@,+-, at most 255. |
| `state` | `string` | no | `present` | present makes or changes it; absent removes it; query only reports it. |
| `display_name` | `string` | no | - | The name the firewall shows. A new rule takes its Name when this is left out. |
| `description` | `string` | no | - | Its description. |
| `enabled` | `bool` | no | - | Whether it is on. A new rule is. |
| `direction` | `string` | no | - | Which traffic it matches. A new rule's is inbound. |
| `action` | `string` | no | - | What it does with that traffic. A new rule allows it. |
| `profiles` | `scalar or list` | no | - | The network profiles it applies in: domain, private, public, or any (all three). A new rule's is any. |
| `protocol` | `string` | no | - | tcp, udp, icmpv4, icmpv6, any, or an IP protocol number 0-255. A new rule's is any. |
| `local_port` | `scalar or list` | no | - | The ports on this machine it matches: ports, ranges (8000-8100), any, or a keyword Windows knows (RPC, RPCEPMap, IPHTTPS, Teredo). Needs tcp or udp. |
| `remote_port` | `scalar or list` | no | - | The other side's ports it matches: ports, ranges or any. Needs tcp or udp. |
| `local_address` | `scalar or list` | no | - | This machine's addresses it matches: addresses, networks (10.0.0.0/8), ranges (10.0.0.1-10.0.0.9), any, or a keyword (LocalSubnet, Internet, Intranet, DNS, DHCP, WINS, DefaultGateway). |
| `remote_address` | `scalar or list` | no | - | The other side's addresses it matches, in the same forms. |
| `program` | `string` | no | - | The program whose traffic it matches, a path, or any. |
| `service` | `string` | no | - | The service whose traffic it matches, by its short name, or any. |

## Returns

| Name | Type | Returned | Description |
| --- | --- | --- | --- |
| `name` | `string` | always | The rule's Name. |
| `existed` | `bool` | always | Whether the rule existed before. |
| `group` | `string` | always | The group it belonged to before, as Windows names it, empty for none. |
| `rule` | `dict` | always | Its settings before, as this method's parameters name them, empty when it did not exist. |
| `differences` | `list of string` | always | How it differed from what was asked, empty when it matched. |

## Undoing this

**Can be undone.** A run that changes something records the instruction that reverses it, as an `inverse` stat holding the method to call and the parameters to call it with, resolved from the state this run actually found. A run that changed nothing records no instruction, which is how it says that undoing it means doing nothing.

A rule this run made is removed by its undo; a rule it changed gets back the earlier value of each setting it changed; a rule it removed is made again with every setting it had, though not in the group it belonged to, which a rule cannot be put back into. Its display name and description are text the device holds, withheld from the journal, so an undo restoring either replays only from the task's own rollback: list.

Note that no rollback engine reads this yet. What exists today is the recording, which has to happen during the forward run because the values an undo needs are gone once the change is applied.

## See also

- `win.schtask`
- `svc.windows.start`
- `fw.firewalld.allow`

## Examples

Let SSH in on a domain controller:

```yaml
- name: OpenSSH's rule covers the domain network too
  win.firewall.rule:
    name: OpenSSH-Server-In-TCP
    profiles: [domain, private]
```

An app's port from one network:

```yaml
- name: The app's port, from the office only
  win.firewall.rule:
    name: Pleiades-App-In
    display_name: App (TCP 8443)
    protocol: tcp
    local_port: [8443]
    remote_address: [10.20.0.0/16]
    profiles: [domain]
```

What a rule allows:

```yaml
- name: Remote Desktop's rule
  win.firewall.rule:
    name: RemoteDesktop-UserMode-In-TCP
    state: query
  register: rdp
```

