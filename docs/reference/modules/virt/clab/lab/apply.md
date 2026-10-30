---
status: beta
---

# virt.clab.lab.apply

Changes a running lab to match its on-disk topology, by the difference.

Asks the server to apply the topology file it holds for the lab: nodes and links added, removed, recreated or restarted, and the rest left alone. With dry_run it reports what it would do and changes nothing, which is also what a check does, so a check of this method is a real prediction. It cannot be undone: nodes it recreated are new containers. The target is the containerlab API server, a generic_http device that onboarding recognised (it declares ContainerlabAPICapable); its login is the device's stored credential, with http_auth basic, and neither the password nor the token it gets is logged, returned or journaled.

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
| `path` | `string` | no | - | A topology file on the server, when it is not the lab's own. |
| `dry_run` | `bool` | no | `false` | Report the difference and change nothing. |
| `skip_post_deploy` | `bool` | no | `false` | Skip the nodes' post-deploy steps. |

## Returns

| Name | Type | Returned | Description |
| --- | --- | --- | --- |
| `added_nodes` | `list of string` | always | Nodes the apply adds. |
| `deleted_nodes` | `list of string` | always | Nodes it removes. |
| `recreated_nodes` | `list of string` | always | Nodes it recreates. |
| `started_nodes` | `list of string` | always | Nodes it starts. |
| `restarted_nodes` | `list of string` | always | Nodes it restarts. |
| `added_links` | `list of string` | always | Links it adds. |
| `deleted_endpoints` | `list of string` | always | Link ends it removes. |
| `dry_run` | `bool` | always | Whether nothing was changed. |

## Undoing this

**Cannot be undone.** This method never records a reversing instruction, so a rollback reaching a task that used it stops rather than guessing.

Nodes it recreated are new containers, and a link it removed is not read first.

Note that no rollback engine reads this yet. What exists today is the recording, which has to happen during the forward run because the values an undo needs are gone once the change is applied.

## See also

- `virt.clab.lab.validate`
- `virt.clab.lab.redeploy`

## Examples

Plan, then apply:

```yaml
- name: What would change
  virt.clab.lab.apply:
    target: clab-api
    lab: nclab
    dry_run: true
- name: Apply it
  virt.clab.lab.apply:
    target: clab-api
    lab: nclab
```

