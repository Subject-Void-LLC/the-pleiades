---
status: beta
---

# container.docker.run

Runs a Docker container on the target.

Makes sure a container named name is running, starting one from image if no container by that name exists. This is a narrow slice of community.docker.docker_container: idempotency here is existence of the NAME only, not a comparison of the running container's configuration against what this task asked for. A container already present under name is left exactly as it is, regardless of whether its image, ports, volumes, env or restart policy match; this method never recreates. Always runs detached (-d), since this platform has no interactive session to attach one to.

## Attributes

|  |  |
| --- | --- |
| Capabilities | `DockerCapable` |
| Transports | `ssh` |
| Requires elevation | yes |
| Check mode | Supported: reports what it would change and changes nothing |
| Engine version | `>=0.2.0` |

## Parameters

| Name | Type | Required | Default | Description |
| --- | --- | --- | --- | --- |
| `name` | `string` | yes | - | The container name to create or leave alone. |
| `image` | `string` | yes | - | The image to run. Ignored when a container already exists under name. |
| `command` | `list` | no | - | The command and its arguments, as separate list elements rather than one shell string. Overrides the image's own default command. |
| `ports` | `list` | no | - | Port mappings, each "host:container" (docker run's own -p syntax). |
| `volumes` | `list` | no | - | Volume mappings, each "host:container" (docker run's own -v syntax). |
| `env` | `dict` | no | - | Environment variables to set in the container, as a map of name to value. |
| `restart_policy` | `string` | no | - | The restart policy (docker run's own --restart value, e.g. unless-stopped). |
| `insecure_skip_host_key_verify` | `bool` | no | `false` | Skip SSH host key verification for this task. This removes protection against a machine in the middle answering for the device, so set it only for a target you have decided does not need it. |

## Returns

| Name | Type | Returned | Description |
| --- | --- | --- | --- |
| `name` | `string` | always | The container this task acted on. |
| `diff` | `dict` | always | What docker inspect reported about the container before this task and after it (exists, status). Recorded even on a run that changed nothing. |

## Undoing this

**Can be undone.** A run that changes something records the instruction that reverses it, as an `inverse` stat holding the method to call and the parameters to call it with, resolved from the state this run actually found. A run that changed nothing records no instruction, which is how it says that undoing it means doing nothing.

A run that created an absent container emits a container.docker.remove naming it, with force: true since a freshly started container is very likely still running. A run that found a container already present under that name emits nothing, the same as every other converged run in this catalog, even though this method does not compare that existing container's configuration against what was requested.

Note that no rollback engine reads this yet. What exists today is the recording, which has to happen during the forward run because the values an undo needs are gone once the change is applied.

## See also

- `container.docker.stop`
- `container.docker.remove`

## Examples

Run a container:

```yaml
- name: Run nginx
  container.docker.run:
    name: web
    image: nginx:1.27
    ports:
      - "8080:80"
```

