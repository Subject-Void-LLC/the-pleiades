---
status: beta
---

# net.frr.facts

Reads an FRRouting router's version, interfaces, OSPF neighbours, routes and BGP peers as data.

Asks vtysh for each in JSON and returns it as data, and by default also the running configuration with every secret in it (a password, an authentication or message-digest key, a neighbour's password) replaced by a placeholder that is the same for equal secrets throughout one command-line run, so a model can still compare two routers' keys without seeing one (a Runner reads each device apart, so there placeholders compare only within one router). The word after each secret word (password, secret, authentication-key, message-digest-key, key-string, community, key) is masked, past any encryption type, key id or algorithm between them. It changes nothing.

## Attributes

|  |  |
| --- | --- |
| Capabilities | `FRRCapable`, `CommandExecCapable` |
| Transports | `ssh` |
| Requires elevation | no |
| Runs | on or against the target device; acts on its target device |
| Check mode | Supported: reports what it would change and changes nothing |
| Absence | No exists return: it describes the device itself; a device not there is unreachable, not absent |
| Engine version | `>=0.2.0` |

## Parameters

| Name | Type | Required | Default | Description |
| --- | --- | --- | --- | --- |
| `include_config` | `bool` | no | `true` | Return the running configuration, its secrets replaced, the default; false leaves it out (config_sha256 is returned either way). |
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
| `config_sha256` | `string` | always | The SHA-256 of the running configuration as it runs (from its frr version line), in hex: equal to a candidate file's digest exactly when the router runs that file. |
| `config.running_digest` | `dict` | always | A digest of the running configuration, never its text, stored as a fact: scheme (frr-cli-text/v1), datastore (running) and hmac_sha256, HMAC-SHA-256 under the project's key (or the Controller's), with hmac_key naming the key. Taken over vtysh's running configuration with its banner lines removed. Keyed, since a plain digest of a short secret is a guessing oracle; where no key is held it is not kept. config_sha256 is unchanged and is not a fact. |
| `running_config` | `string` | unless include_config is false | The running configuration with its secrets replaced. |

## Undoing this

**Cannot be undone.** This method never records a reversing instruction, so a rollback reaching a task that used it stops rather than guessing.

Runs only show commands, which change nothing.

`pleiades rollback` and the Controller's rollback route replay a recorded instruction. It is recorded during the forward run because the values an undo needs are gone once the change is applied.

## Facts it keeps

A real run stores each of these as the device's fact. A **stable** fact keeps every value it has had, and a change is drift; a **volatile** one keeps only its latest value and never counts as drift. A check stores none.

| Fact | Kind | Type | Description |
| --- | --- | --- | --- |
| `config.running_digest` | stable | object | The running configuration's keyed digest, never its text. |

## See also

- `net.frr.command`
- `net.batfish.check`

## Examples

Read a router for a model:

```yaml
- name: Read r1
  net.frr.facts:
  register: r1
```

