---
status: beta
---

# wait.routes

Waits until a router has a route to every prefix given and its routing table has stopped changing.

A full OSPF adjacency is not convergence: shortest paths are computed after it, and a router can route the long way, or not at all, for a while after every neighbour is up. This waits for what a change needs: a selected route to each prefix (through the given next hops, when next_hops names any), and the routing table, uptimes aside, unchanged for stable_for seconds. At timeout it fails naming each route still missing, or saying the table never held still. It changes nothing.

## Attributes

|  |  |
| --- | --- |
| Capabilities | `FRRCapable`, `CommandExecCapable` |
| Transports | `ssh` |
| Requires elevation | no |
| Runs | on or against the target device; acts on its target device |
| Check mode | Not supported: a check run names this task as unchecked, since what a wait waits for is usually an earlier task's change, which a check never makes, so a check would wait out its timeout and fail where the real run succeeds |
| Engine version | `>=0.2.0` |

## Parameters

| Name | Type | Required | Default | Description |
| --- | --- | --- | --- | --- |
| `prefixes` | `list of string` | yes | - | The prefixes that must each have a selected route, e.g. 10.3.3.0/24. |
| `next_hops` | `dict` | no | - | Per prefix, the next-hop addresses its route must use, e.g. {"10.3.3.0/24": ["10.0.12.1"]}. |
| `stable_for` | `int` | no | `5` | Seconds the routing table must stay unchanged. |
| `timeout` | `int` | no | `120` | Seconds to wait in all. |
| `insecure_skip_host_key_verify` | `bool` | no | `false` | Skip SSH host key verification for this task. This removes protection against a machine in the middle answering for the device, so set it only for a target you have decided does not need it. |

## Returns

| Name | Type | Returned | Description |
| --- | --- | --- | --- |
| `waited` | `float` | always | Seconds waited. |
| `routes` | `dict` | always | Each prefix given, with the next hops its route used. |

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

