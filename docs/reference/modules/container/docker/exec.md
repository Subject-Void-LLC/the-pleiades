---
status: beta
---

# container.docker.exec

Runs one command inside a running Docker container, reached directly through the daemon socket.

Runs cmd inside the container named name, through /bin/sh -c on the daemon's own exec endpoints (pkg/dockerexec), never over SSH. The container must already be running; this method does not start one (see container.docker.run). Reports the command's real exit code, stdout and stderr. A non-zero exit is an error, not a result to inspect, the same line exec.command draws.

## Attributes

|  |  |
| --- | --- |
| Capabilities | `DockerCapable` |
| Transports | `docker` |
| Requires elevation | no |
| Engine version | `>=1.0.0` |

## Parameters

| Name | Type | Required | Default | Description |
| --- | --- | --- | --- | --- |
| `name` | `string` | yes | - | The running container to exec into. |
| `cmd` | `string` | yes | - | The command line, run inside the container's own /bin/sh -c. Pipes, redirects and quoting all work, because the container's shell sees them. |

## Returns

| Name | Type | Returned | Description |
| --- | --- | --- | --- |
| `name` | `string` | always | The container this task acted on. |
| `exit_code` | `int` | always | The command's real exit status, reported by the Docker daemon. |
| `stdout` | `string` | always | Everything the command wrote to standard output. |
| `stderr` | `string` | always | Everything the command wrote to standard error. |

## Undoing this

**Cannot be undone.** This method never records a reversing instruction, so a rollback reaching a task that used it stops rather than guessing.

An arbitrary command's effect inside a container is unknown to this platform, the same reasoning exec.command and exec.shell already record for the identical shape over SSH.

Note that no rollback engine reads this yet. What exists today is the recording, which has to happen during the forward run because the values an undo needs are gone once the change is applied.

## See also

- `container.docker.run`
- `container.docker.stop`
- `container.docker.remove`
- `exec.shell`

## Examples

Check a running container's own view of a file:

```yaml
- name: Read the app's version file
  container.docker.exec:
    name: web
    cmd: cat /opt/app/VERSION
  register: version
```

