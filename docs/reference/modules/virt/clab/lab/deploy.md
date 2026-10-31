---
status: beta
---

# virt.clab.lab.deploy

Deploys a lab from a topology, unless it is already deployed.

Deploys the topology (given as a mapping, a project file or a repository URL) through the server. A lab named lab that is already deployed is left as it is and reported unchanged, so a runbook that deploys first can run again; reconfigure true deploys it over the running one. The topology's own name must be lab: a topology that names another lab would deploy that one and leave this one absent. Its undo destroys the lab, and is recorded only when this task created it. The target is the containerlab API server, a generic_http device that onboarding recognised (it declares ContainerlabAPICapable); its login is the device's stored credential, with http_auth basic, and neither the password nor the token it gets is logged, returned or journaled.

## Attributes

|  |  |
| --- | --- |
| Capabilities | `ContainerlabAPICapable` |
| Transports | `https` |
| Requires elevation | no |
| Runs | in the host process (the CLI or a Runner); acts on its target device |
| Check mode | Supported: reports what it would change and changes nothing |
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
| `reconfigure` | `bool` | no | `false` | Deploy over a lab that is already deployed, recreating it. Records no undo. |

## Returns

| Name | Type | Returned | Description |
| --- | --- | --- | --- |
| `exists` | `bool` | always | Whether the lab is deployed now. |
| `nodes` | `list of dict` | always | Its nodes, as lab.info reports them. |
| `created` | `bool` | always | Whether this task deployed it. |

## Undoing this

**Can be undone.** A run that changes something records the instruction that reverses it, as an `inverse` stat holding the method to call and the parameters to call it with, resolved from the state this run actually found. A run that changed nothing records no instruction, which is how it says that undoing it means doing nothing.

Its undo destroys the lab, and is recorded only when this task created it, before the deploy runs, since a deploy that fails partway leaves containers behind. A lab that was already deployed is left alone and records no undo.

Note that no rollback engine reads this yet. What exists today is the recording, which has to happen during the forward run because the values an undo needs are gone once the change is applied.

## See also

- `virt.clab.lab.destroy`
- `virt.clab.lab.redeploy`
- `virt.clab.lab.info`

## Examples

Deploy a two-node lab:

```yaml
- name: The lab is up
  virt.clab.lab.deploy:
    target: clab-api
    lab: nclab
    topology_file: lab/nclab.clab.yml
```

