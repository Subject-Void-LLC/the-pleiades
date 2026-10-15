---
status: beta
---

# container.docker.stop

Stops a running Docker container on the target.

Makes sure a container named name is not running, stopping it if it is. Container state is read from docker inspect before anything is sent, so a container already stopped, or absent entirely, reports no change and no command reaches the device. It acts only on the Docker engine onboarding pinned for the device, and refuses any other engine the login reaches. It stops the container by the id it inspected, and records container.docker.run, which starts it again, as its undo.

## Attributes

|  |  |
| --- | --- |
| Capabilities | `DockerCapable` |
| Transports | `ssh` |
| Requires elevation | yes |
| Runs | on or against the target device; acts on its target device |
| Check mode | Supported: reports what it would change and changes nothing |
| Engine version | `>=0.2.0` |

## Parameters

| Name | Type | Required | Default | Description |
| --- | --- | --- | --- | --- |
| `name` | `string` | yes | - | The container to stop. |
| `id` | `string` | no | - | When set, the container named name must be this one, by its full id, or the task is refused and nothing is stopped. The undo of container.docker.run's start sets it. |
| `insecure_skip_host_key_verify` | `bool` | no | `false` | Skip SSH host key verification for this task. This removes protection against a machine in the middle answering for the device, so set it only for a target you have decided does not need it. |

## Returns

| Name | Type | Returned | Description |
| --- | --- | --- | --- |
| `name` | `string` | always | The container this task acted on. |
| `diff` | `dict` | always | What docker inspect reported about the container before this task and after it (exists, status). |

## Undoing this

**Can be undone.** A run that changes something records the instruction that reverses it, as an `inverse` stat holding the method to call and the parameters to call it with, resolved from the state this run actually found. A run that changed nothing records no instruction, which is how it says that undoing it means doing nothing.

A stop that stopped a running container emits a container.docker.run naming it, from the image the container was created from, with start: true, since run starts an existing stopped container. A stop that found nothing running emits nothing.

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

