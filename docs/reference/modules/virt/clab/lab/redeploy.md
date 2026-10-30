---
status: beta
---

# virt.clab.lab.redeploy

Destroys and deploys a lab again from a topology.

Replaces the lab with a new one from the topology, through the server. Every node is recreated, so what was inside the containers is lost; it cannot be undone. Use virt.clab.lab.apply to change a running lab by the difference instead. The target is the containerlab API server, a generic_http device that onboarding recognised (it declares ContainerlabAPICapable); its login is the device's stored credential, with http_auth basic, and neither the password nor the token it gets is logged, returned or journaled.

## Attributes

|  |  |
| --- | --- |
| Capabilities | `ContainerlabAPICapable` |
| Transports | `https` |
| Requires elevation | no |
| Runs | in the host process (the CLI or a Runner); acts on its target device |
| Check mode | Not supported: a check run names this task as unchecked, since it recreates every node, and whether the new lab comes up is known only once it has run |
| Engine version | `>=0.3.0` |

## Parameters

| Name | Type | Required | Default | Description |
| --- | --- | --- | --- | --- |
| `lab` | `string` | yes | - | The lab's name, as containerlab deployed it. |
| `topology` | `dict` | no | - | The topology as a mapping, as containerlab's topology file is (name, topology: nodes, links). The three topology, topology_file and source_url are alternatives: give only one. |
| `topology_file` | `string` | no | - | A containerlab topology YAML file in the project directory, read by the command line; refused on a Walk-tier Runner, where topology is the way. The three topology, topology_file and source_url are alternatives: give only one. |
| `source_url` | `string` | no | - | A URL of a Git repository holding the topology, which the server fetches. The three topology, topology_file and source_url are alternatives: give only one. |
| `max_workers` | `int` | no | - | How many nodes the server starts at once; the server's own default when unset. |
| `network` | `string` | no | - | The management network's name, when it is not clab. |
| `ipv4_subnet` | `string` | no | - | The management network's IPv4 subnet. |
| `ipv6_subnet` | `string` | no | - | The management network's IPv6 subnet. |
| `skip_post_deploy` | `bool` | no | `false` | Skip the nodes' post-deploy steps. |
| `cleanup` | `bool` | no | `false` | Also remove the old lab's directory and configuration state. |
| `graceful` | `bool` | no | `false` | Stop the old nodes gracefully. |

## Returns

| Name | Type | Returned | Description |
| --- | --- | --- | --- |
| `nodes` | `list of dict` | always | The new lab's nodes, as lab.info reports them. |

## Undoing this

**Cannot be undone.** This method never records a reversing instruction, so a rollback reaching a task that used it stops rather than guessing.

Every node is recreated, so what was inside the containers is gone.

Note that no rollback engine reads this yet. What exists today is the recording, which has to happen during the forward run because the values an undo needs are gone once the change is applied.

## See also

- `virt.clab.lab.deploy`
- `virt.clab.lab.apply`

## Examples

Start over:

```yaml
- name: Fresh lab
  virt.clab.lab.redeploy:
    target: clab-api
    lab: nclab
    topology_file: lab/nclab.clab.yml
```

