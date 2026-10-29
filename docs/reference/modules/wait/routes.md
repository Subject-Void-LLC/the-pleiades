---
status: beta
---

# wait.routes

Waits until a router has a route to every prefix given, or with state absent has none left, and its routing table has stopped changing.

A full OSPF adjacency is not convergence: shortest paths are computed after it, and a router can route the long way, or not at all, for a while after every neighbour is up. This waits for what a change needs: a selected route to each prefix (through the given next hops, when next_hops names any), and the routing table, uptimes aside, unchanged for stable_for seconds. With state absent it waits for the opposite, such as a withdrawn network or a failed link: no selected route to any prefix given (or, for a prefix next_hops names, a route through none of those next hops, so a failover away from one neighbour can be waited for), with the same stable_for. A table read with no selected route at all (not even a connected one, as a cut or refused read or output the parser does not recognize leaves it) is never taken as every route gone: the wait goes on. At timeout it fails naming each route still missing (or, with state absent, still there, or that the table read held no route), or saying the table never held still. It changes nothing. It reads an FRR router's table through vtysh (show ip route json, over SSH, the device granted FRRCapable and CommandExecCapable) and a Cisco IOS router's global table through its CLI (show ip route, over SSH, the controller's console or Telnet with insecure_telnet), and refuses any other device before contacting it. That refusal is made when the task runs: no single capability names both, so validate and a Controller's admission cannot hold the device to it beforehand.

## Attributes

|  |  |
| --- | --- |
| Capabilities | - |
| Transports | `ssh`, `telnet`, `console` |
| Requires elevation | no |
| Runs | on or against the target device; acts on its target device |
| Check mode | Not supported: a check run names this task as unchecked, since what a wait waits for is usually an earlier task's change, which a check never makes, so a check would wait out its timeout and fail where the real run succeeds |
| Engine version | `>=0.2.0` |

## Parameters

| Name | Type | Required | Default | Description |
| --- | --- | --- | --- | --- |
| `prefixes` | `list of string` | yes | - | The IPv4 prefixes that must each have a selected route (with state absent, none), e.g. 10.3.3.0/24; the IPv4 routing table is the one read. |
| `next_hops` | `dict` | no | - | Per prefix, the next-hop addresses its route must use, e.g. {"10.3.3.0/24": ["10.0.12.1"]}; with state absent, the next hops its route must no longer use. |
| `state` | `string` | no | `present` | present waits for a selected route to every prefix given; absent waits until none of them has one (or, for a prefix next_hops names, until its route uses none of those next hops). Case is ignored. **Choices:** `present`, `absent` (case ignored). |
| `stable_for` | `int` | no | `5` | Seconds the routing table must stay unchanged. |
| `timeout` | `int` | no | `120` | Seconds to wait in all. |
| `insecure_skip_host_key_verify` | `bool` | no | `false` | Skip SSH host key verification for this task. This removes protection against a machine in the middle answering for the device, so set it only for a target you have decided does not need it. |
| `insecure_telnet` | `bool` | no | `false` | Reach a Cisco IOS device that has no SSH over bare Telnet, answering its login with the task's credential. Telnet carries everything, the login included, in cleartext, so set this only for a device on a network you trust. |

## Returns

| Name | Type | Returned | Description |
| --- | --- | --- | --- |
| `waited` | `float` | always | Seconds waited. |
| `routes` | `dict` | always | Each prefix given, with the next hops its route used; with state absent, an empty list for a prefix with no route left. |

## Undoing this

**Cannot be undone.** This method never records a reversing instruction, so a rollback reaching a task that used it stops rather than guessing.

This method reads the routing table and changes nothing, so there is nothing to undo.

Note that no rollback engine reads this yet. What exists today is the recording, which has to happen during the forward run because the values an undo needs are gone once the change is applied.

## See also

- `net.frr.config`
- `net.trace`

## Examples

Wait for OSPF to settle:

```yaml
- name: Every router routes both LANs
  wait.routes:
    prefixes: [10.1.1.0/24, 10.3.3.0/24]
```

Wait for a withdrawn network to leave the table:

```yaml
- name: R1 no longer routes the retired LAN
  wait.routes:
    target: R1
    prefixes: [10.9.9.0/24]
    state: absent
```

Wait for a branch to learn the data center over its tunnel:

```yaml
- name: BR1 routes the data center through the hub
  wait.routes:
    target: BR1
    prefixes: [10.0.0.0/16]
    next_hops: {10.0.0.0/16: [10.255.0.1]}
    timeout: 180
```

