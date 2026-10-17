---
status: beta
---

# container.info

Reports one container as the engine sees it.

Reports the container named name: whether it exists, and if so its ID, state, image, command, labels, network, restart policy, privilege, mounts, last exit code and start time, and its environment variables by name only, since a value may be a credential (anyone with access to the engine can still read them by inspecting the container). An absent container is reported as not existing, not as a failure. It changes nothing, so a check is the same read. The engine is reached the first way onboarding proved for the device (this machine's socket, an SSH channel to the engine's socket, the engine's own dial-stdio over SSH, or mutual TLS), its identity checked against the one onboarding recorded, and every request outside the operations this method declares is refused before it reaches the engine.

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
| `engine` | `string` | no | - | Which container engine to use on a device that runs more than one. Needed only there: on a device with one engine, that engine is used. An engine the device does not run is refused. |
| `name` | `string` | yes | - | The container's name. |
| `insecure_skip_host_key_verify` | `bool` | no | `false` | Skip SSH host key verification for this task, when the engine is reached over SSH. This removes protection against a machine in the middle answering for the device, so set it only for a target you have decided does not need it. |

## Returns

| Name | Type | Returned | Description |
| --- | --- | --- | --- |
| `exists` | `bool` | always | Whether a container by that name exists. |
| `id` | `string` | when it exists | The container's ID. |
| `state` | `string` | when it exists | Its state as the engine names it. |
| `env_names` | `list of string` | when it exists | Its environment variables, by name only. |

## Undoing this

**Cannot be undone.** This method never records a reversing instruction, so a rollback reaching a task that used it stops rather than guessing.

It reads what the engine reports, and changes nothing.

Note that no rollback engine reads this yet. What exists today is the recording, which has to happen during the forward run because the values an undo needs are gone once the change is applied.

## See also

- `container.list`
- `container.logs`

## Examples

Is the web server running?:

```yaml
- name: Web state
  container.info:
    name: web
  register: web
```

