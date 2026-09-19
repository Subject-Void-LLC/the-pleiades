---
status: beta
---

# container.docker.stop

Stops a running Docker container on the target.

Makes sure a container named name is not running, stopping it if it is. Container state is read from docker inspect before anything is sent, so a container already stopped, or absent entirely, reports no change and no command reaches the device.

## Attributes

|  |  |
| --- | --- |
| Capabilities | `DockerCapable` |
| Transports | `ssh` |
| Requires elevation | yes |
| Check mode | Supported: reports what it would change and changes nothing |
| Engine version | `>=1.0.0` |

## Parameters

| Name | Type | Required | Default | Description |
| --- | --- | --- | --- | --- |
| `name` | `string` | yes | - | The container to stop. |
| `insecure_skip_host_key_verify` | `bool` | no | `false` | Skip SSH host key verification for this task. This removes protection against a machine in the middle answering for the device, so set it only for a target you have decided does not need it. |

## Returns

| Name | Type | Returned | Description |
| --- | --- | --- | --- |
| `name` | `string` | always | The container this task acted on. |
| `diff` | `dict` | always | What docker inspect reported about the container before this task and after it (exists, status). |

## Undoing this

**Cannot be undone.** This method never records a reversing instruction, so a rollback reaching a task that used it stops rather than guessing.

This catalog declares no container.docker.start, and recording container.docker.run as this method's inverse would be dishonest: run's own idempotency means it would just no-op against the existing name rather than actually restart the container this stopped. The container's own state is still recorded under diff, so a rollback reaching this task can see it was running before and decide for itself, but this method emits no instruction because none would be true.

Note that no rollback engine reads this yet. What exists today is the recording, which has to happen during the forward run because the values an undo needs are gone once the change is applied.

## See also

- `container.docker.run`
- `container.docker.remove`

## Examples

Stop a container:

```yaml
- name: Stop web
  container.docker.stop:
    name: web
```

