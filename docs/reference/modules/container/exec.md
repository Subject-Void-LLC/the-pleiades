---
status: beta
---

# container.exec

Runs one command inside a running container.

Runs cmd inside the running container named name, through the container's own /bin/sh -c, on whichever engine the device runs, and reports its real exit code, stdout and stderr (together at most 1 MiB, refused past it). A non-zero exit is an error, after the output is recorded, the line exec.command draws. The container must be running. It cannot be checked: what a command changes is known only once it has run. The engine is reached the first way onboarding proved for the device (this machine's socket, an SSH channel to the engine's socket, the engine's own dial-stdio over SSH, or mutual TLS), its identity checked against the one onboarding recorded, and every request outside the operations this method declares is refused before it reaches the engine.

## Attributes

|  |  |
| --- | --- |
| Capabilities | `ContainerEngineCapable` |
| Transports | `engine_local`, `engine_socket`, `engine_stdio`, `engine_tls` |
| Requires elevation | no |
| Runs | on or against the target device; acts on its target device |
| Check mode | Not supported: a check run names this task as unchecked, since a command run inside a container can change anything the container can reach, and what it changes is known only once it has run |
| Engine version | `>=0.2.0` |

## Parameters

| Name | Type | Required | Default | Description |
| --- | --- | --- | --- | --- |
| `engine` | `string` | no | - | Which container engine to use on a device that runs more than one. Needed only there: on a device with one engine, that engine is used. An engine the device does not run is refused. **Choices:** `docker`, `podman`. |
| `name` | `string` | yes | - | The container's name. |
| `cmd` | `string` | yes | - | The command line, run inside the container's /bin/sh -c. |
| `insecure_skip_host_key_verify` | `bool` | no | `false` | Skip SSH host key verification for this task, when the engine is reached over SSH. This removes protection against a machine in the middle answering for the device, so set it only for a target you have decided does not need it. |

## Returns

| Name | Type | Returned | Description |
| --- | --- | --- | --- |
| `name` | `string` | always | The container's name. |
| `exit_code` | `int` | always | The command's real exit status. |
| `stdout` | `string` | always | What it wrote to standard output. |
| `stderr` | `string` | always | What it wrote to standard error. |

## Undoing this

**Cannot be undone.** This method never records a reversing instruction, so a rollback reaching a task that used it stops rather than guessing.

A command's effect inside a container is unknown to this platform, as exec.command's is on a host.

Note that no rollback engine reads this yet. What exists today is the recording, which has to happen during the forward run because the values an undo needs are gone once the change is applied.

## See also

- `container.logs`
- `exec.command`

## Examples

Read the app's version inside its container:

```yaml
- name: App version
  container.exec:
    name: web
    cmd: cat /opt/app/VERSION
  register: version
```

