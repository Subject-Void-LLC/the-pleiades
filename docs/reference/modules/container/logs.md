---
status: beta
---

# container.logs

Returns a container's last lines of output.

Returns the last tail lines the container named name wrote to standard output and standard error, optionally only since a timestamp or a duration (10m). More than 1 MiB together is refused rather than cut; ask for fewer lines. A container run with a terminal has one stream, returned as stdout. A container that does not exist is not a failure: exists is false and no output is reported, so a later task can decide what to do. It changes nothing, so a check is the same read, an absent container included. The engine is reached the first way onboarding proved for the device (this machine's socket, an SSH channel to the engine's socket, the engine's own dial-stdio over SSH, or mutual TLS), its identity checked against the one onboarding recorded, and every request outside the operations this method declares is refused before it reaches the engine.

## Attributes

|  |  |
| --- | --- |
| Capabilities | `ContainerEngineCapable` |
| Transports | `engine_local`, `engine_socket`, `engine_stdio`, `engine_tls` |
| Requires elevation | no |
| Runs | on or against the target device; acts on its target device |
| Check mode | Supported: reports what it would change and changes nothing |
| Engine version | `>=0.2.0` |

## Parameters

| Name | Type | Required | Default | Description |
| --- | --- | --- | --- | --- |
| `engine` | `string` | no | - | Which container engine to use on a device that runs more than one. Needed only there: on a device with one engine, that engine is used. An engine the device does not run is refused. **Choices:** `docker`, `podman`. |
| `name` | `string` | yes | - | The container's name. |
| `tail` | `int` | no | `100` | How many of the last lines to return, from 1 to 10000. |
| `since` | `string` | no | - | Only lines since this RFC 3339 timestamp, Unix time, or duration (10m). |
| `insecure_skip_host_key_verify` | `bool` | no | `false` | Skip SSH host key verification for this task, when the engine is reached over SSH. This removes protection against a machine in the middle answering for the device, so set it only for a target you have decided does not need it. |

## Returns

| Name | Type | Returned | Description |
| --- | --- | --- | --- |
| `name` | `string` | always | The container's name. |
| `exists` | `bool` | always | Whether a container by that name exists. |
| `id` | `string` | when the container exists | The container's ID. |
| `stdout` | `string` | when the container exists | What it wrote to standard output. |
| `stderr` | `string` | when the container exists | What it wrote to standard error. |

## Undoing this

**Cannot be undone.** This method never records a reversing instruction, so a rollback reaching a task that used it stops rather than guessing.

It reads what the engine reports, and changes nothing.

Note that no rollback engine reads this yet. What exists today is the recording, which has to happen during the forward run because the values an undo needs are gone once the change is applied.

## See also

- `container.info`
- `container.exec`

## Examples

Why did the job fail?:

```yaml
- name: Job output
  container.logs:
    name: backup
    tail: 50
  register: out
```

