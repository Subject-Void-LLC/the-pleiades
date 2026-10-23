---
status: beta
---

# net.frr.facts

Reads an FRRouting router's version, interfaces, OSPF neighbours, routes and BGP peers as data.

Asks vtysh for each in JSON and returns it as data. With include_config, it also returns the running configuration with every secret in it (a password, an authentication or message-digest key, a neighbour's password) replaced by a placeholder that is the same for equal secrets throughout one command-line run, so a model can still compare two routers' keys without seeing one (a Runner reads each device apart, so there placeholders compare only within one router). The word after each secret word (password, secret, authentication-key, message-digest-key, key-string, community, key) is masked, past any encryption type, key id or algorithm between them. It changes nothing.

## Attributes

|  |  |
| --- | --- |
| Capabilities | `FRRCapable`, `CommandExecCapable` |
| Transports | `ssh` |
| Requires elevation | no |
| Runs | on or against the target device; acts on its target device |
| Check mode | Supported: reports what it would change and changes nothing |
| Engine version | `>=0.2.0` |

## Parameters

| Name | Type | Required | Default | Description |
| --- | --- | --- | --- | --- |
| `include_config` | `bool` | no | `false` | Also return the running configuration, its secrets replaced. |
| `insecure_skip_host_key_verify` | `bool` | no | `false` | Skip SSH host key verification for this task. This removes protection against a machine in the middle answering for the device, so set it only for a target you have decided does not need it. |

## Returns

| Name | Type | Returned | Description |
| --- | --- | --- | --- |
| `version` | `string` | always | The FRR version. |
| `hostname` | `string` | always | The router's hostname as FRR knows it. |
| `interfaces` | `dict` | always | Each interface, from show interface json. |
| `ospf_neighbors` | `dict` | always | show ip ospf neighbor json, empty when OSPF is not running. |
| `routes` | `dict` | always | show ip route json: every route by prefix. |
| `bgp_summary` | `dict` | always | show bgp summary json, empty when BGP is not running. |
| `running_config` | `string` | with include_config | The running configuration with its secrets replaced. |

## Undoing this

**Cannot be undone.** This method never records a reversing instruction, so a rollback reaching a task that used it stops rather than guessing.

Runs only show commands, which change nothing.

Note that no rollback engine reads this yet. What exists today is the recording, which has to happen during the forward run because the values an undo needs are gone once the change is applied.

## See also

- `net.frr.command`
- `net.batfish.check`

## Examples

Read a router for a model:

```yaml
- name: Read r1
  net.frr.facts:
    include_config: true
  register: r1
```

