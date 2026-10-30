---
status: beta
---

# virt.clab.capture.packetflix

Builds Packetflix capture links for a lab's interfaces.

Asks the server for the packetflix:// URI that opens a capture of each interface named, for Wireshark with the Edgeshark plugin. It starts no capture itself. It only reads, so a check is the same call. The target is the containerlab API server, a generic_http device that onboarding recognised (it declares ContainerlabAPICapable); its login is the device's stored credential, with http_auth basic, and neither the password nor the token it gets is logged, returned or journaled.

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
| `targets` | `list of dict` | yes | - | What to capture: each a mapping of container_name (the node's container), interface_name, and node_namespace (true to capture in the node's namespace). |
| `remote_hostname` | `string` | no | - | The server's name as the capturing machine reaches it. |

## Returns

| Name | Type | Returned | Description |
| --- | --- | --- | --- |
| `captures` | `list of dict` | always | Each: container_name, interface_names and packetflix_uri. |

## Undoing this

**Cannot be undone.** This method never records a reversing instruction, so a rollback reaching a task that used it stops rather than guessing.

It only reads the server, so there is nothing to undo.

Note that no rollback engine reads this yet. What exists today is the recording, which has to happen during the forward run because the values an undo needs are gone once the change is applied.

## See also

- `virt.clab.edgeshark.status`

## Examples

Builds Packetflix capture links for a lab's interfaces.:

```yaml
- name: Capture links
  virt.clab.capture.packetflix:
    target: clab-api
    lab: nclab
    targets:
      - {container_name: clab-nclab-r1, interface_name: eth1}
```

