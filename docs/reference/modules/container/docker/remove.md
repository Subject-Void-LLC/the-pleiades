---
status: beta
---

# container.docker.remove

Removes a Docker container from the target.

Makes sure a container named name does not exist, removing it if present. Container state is read from docker inspect before anything is sent, so a container already absent reports no change and no command reaches the device.

## Attributes

|  |  |
| --- | --- |
| Capabilities | `DockerCapable` |
| Transports | `ssh` |
| Requires elevation | yes |
| Engine version | `>=1.0.0` |

## Parameters

| Name | Type | Required | Default | Description |
| --- | --- | --- | --- | --- |
| `name` | `string` | yes | - | The container to remove. |
| `force` | `bool` | no | `false` | Remove the container even if it is still running (docker rm -f). Left false, removing a running container fails rather than stopping it first. |
| `insecure_skip_host_key_verify` | `bool` | no | `false` | Skip SSH host key verification for this task. This removes protection against a machine in the middle answering for the device, so set it only for a target you have decided does not need it. |

## Returns

| Name | Type | Returned | Description |
| --- | --- | --- | --- |
| `name` | `string` | always | The container this task acted on. |
| `diff` | `dict` | always | What docker inspect reported about the container before this task and after it. After always reports exists: false on a successful run. |

## Undoing this

**Cannot be undone.** This method never records a reversing instruction, so a rollback reaching a task that used it stops rather than guessing.

docker inspect exposes a removed container's image and some of its configuration before deletion, but reconstructing ports, volumes, env and restart policy from that output well enough for a real container.docker.run to recreate it is parsing this pass does not take on. A partial inverse that silently dropped that configuration would be worse than refusing to record one, the same call pkg.upgrade makes for a package version a repository may no longer offer.

Note that no rollback engine reads this yet. What exists today is the recording, which has to happen during the forward run because the values an undo needs are gone once the change is applied.

## See also

- `container.docker.run`
- `container.docker.stop`

## Examples

Remove a stopped container:

```yaml
- name: Remove web
  container.docker.remove:
    name: web
```

Force-remove a running container:

```yaml
- name: Remove web even if it is still running
  container.docker.remove:
    name: web
    force: true
```

