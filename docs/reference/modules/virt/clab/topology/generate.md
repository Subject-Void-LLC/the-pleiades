---
status: beta
---

# virt.clab.topology.generate

Generates a containerlab topology from a description of its tiers.

Asks the server to write a topology (a Clos fabric, say) from the tiers given, and returns its YAML. It never deploys: deploy is refused here, since virt.clab.lab.deploy is the call that changes anything. It changes nothing on the server, so a check is the same call. The target is the containerlab API server, a generic_http device that onboarding recognised (it declares ContainerlabAPICapable); its login is the device's stored credential, with http_auth basic, and neither the password nor the token it gets is logged, returned or journaled.

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
| `name` | `string` | yes | - | The topology's name. |
| `default_kind` | `string` | no | - | The node kind used where a tier names none. |
| `images` | `dict` | no | - | Image by kind. |
| `tiers` | `list of dict` | yes | - | The tiers, each a mapping of count, kind and type. |
| `management_network` | `string` | no | - | The management network's name. |
| `ipv4_subnet` | `string` | no | - | The management network's IPv4 subnet. |
| `node_prefix` | `string` | no | - | A prefix for node names. |
| `group_prefix` | `string` | no | - | A prefix for tier group names. |

## Returns

| Name | Type | Returned | Description |
| --- | --- | --- | --- |
| `topology` | `string` | always | The generated topology's YAML. |
| `message` | `string` | always | The server's message. |

## Undoing this

**Cannot be undone.** This method never records a reversing instruction, so a rollback reaching a task that used it stops rather than guessing.

It only reads the server, so there is nothing to undo.

Note that no rollback engine reads this yet. What exists today is the recording, which has to happen during the forward run because the values an undo needs are gone once the change is applied.

## See also

- `virt.clab.lab.deploy`

## Examples

Two linux nodes:

```yaml
- name: Generate
  virt.clab.topology.generate:
    target: clab-api
    name: g1
    default_kind: linux
    images: {linux: "alpine:3.20.10"}
    tiers:
      - {count: 2, kind: linux}
  register: g
```

