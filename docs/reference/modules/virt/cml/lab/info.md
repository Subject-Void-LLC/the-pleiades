---
status: beta
---

# virt.cml.lab.info

Reports one CML lab: its nodes, their interfaces and links, and how many nodes the controller runs.

Reads one lab and changes nothing: each node's label, definition and state, each of its interfaces with the link it is on (the link's state, and the node and interface at its other end), every link, and how many nodes run on the controller, so a plan can see how full a shared controller is before it stops or starts a node. A lab that does not exist is not a failure: exists is false and only the controller's count is reported, so a later task can decide what to do. The count is of what this login can see (every lab GET /labs?show_all=true lists it), which may be fewer nodes than the controller runs, and it is not the controller's limit, which CML does not show an ordinary user. The task's target is a cml_controller device (its api_base, TLS settings and its own credential, stored with add-credential --password-stdin); the method runs on this machine and talks to the controller's REST API. A check is the same read, a missing lab included.

## Attributes

|  |  |
| --- | --- |
| Capabilities | `CMLAPICapable` |
| Transports | `https` |
| Requires elevation | no |
| Runs | in the host process (the CLI or a Runner); acts on its target device |
| Check mode | Supported: reports what it would change and changes nothing |
| Engine version | `>=0.2.0` |

## Parameters

| Name | Type | Required | Default | Description |
| --- | --- | --- | --- | --- |
| `lab` | `string` | yes | - | The lab, by id or by title. A title two labs share is refused, naming both ids; an undo always names the id. |

## Returns

| Name | Type | Returned | Description |
| --- | --- | --- | --- |
| `exists` | `bool` | always | Whether the controller holds the lab. |
| `id` | `string` | when exists | The lab's id. |
| `title` | `string` | when exists | The lab's title. |
| `state` | `string` | when exists | DEFINED_ON_CORE, STARTED or STOPPED. |
| `nodes` | `list` | when exists | Each node as id, label, definition, state (DEFINED_ON_CORE, STOPPED, QUEUED, STARTED or BOOTED) and interfaces. Each interface is label and connected and, when it is on a link, link (the link's id), link_state, peer and peer_interface (the node and interface at the link's other end, by label). |
| `links` | `list` | when exists | Each link as id, a, a_interface, b, b_interface (by node label and interface label) and state: STARTED carries traffic, STOPPED is cut as if unplugged, DEFINED_ON_CORE has never run. |
| `controller_nodes_running` | `int` | always | How many nodes are queued, started or booted across every lab this login can see, this one's included. It may be fewer than the controller runs, and it is not the controller's limit. |
| `controller_labs` | `int` | always | How many labs controller_nodes_running counts. |

## Undoing this

**Cannot be undone.** This method never records a reversing instruction, so a rollback reaching a task that used it stops rather than guessing.

It only reads the controller.

Note that no rollback engine reads this yet. What exists today is the recording, which has to happen during the forward run because the values an undo needs are gone once the change is applied.

## See also

- `virt.cml.lab.list`
- `virt.cml.lab.create`

## Examples

Read a lab:

```yaml
- name: Read the CVD lab
  virt.cml.lab.info:
    lab: cvd
  register: lab
```

