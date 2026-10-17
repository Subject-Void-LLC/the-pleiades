---
status: beta
---

# container.registry.info

Reports the registries the engine's login file holds a login for.

Reads the engine's login file and reports each registry it holds a login for, with the account name; with registry, also whether that one is logged in and as whom. A password is never read out. It changes nothing; a check is the same read. The login file is on the machine the engine runs on (this machine's own when the engine is reached through its local socket, otherwise the device's, over its SSH login): auth_file, or the engine's own default under the login's home, ~/.docker/config.json for Docker and ~/.config/containers/auth.json for Podman.

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
| `registry` | `string` | no | - | A registry host to report on. |
| `auth_file` | `string` | no | - | The login file, an absolute path on the engine's machine. Absent means the engine's own default. |
| `insecure_skip_host_key_verify` | `bool` | no | `false` | Skip SSH host key verification for this task, when the engine or its machine is reached over SSH. This removes protection against a machine in the middle answering for the device, so set it only for a target you have decided does not need it. |

## Returns

| Name | Type | Returned | Description |
| --- | --- | --- | --- |
| `auth_file` | `string` | always | The login file. |
| `registries` | `list of dict` | always | One entry per login: registry and username. |
| `logged_in` | `bool` | with registry | Whether registry has a login. |
| `username` | `string` | with registry | The account registry is logged in as, empty when none. |

## Undoing this

**Cannot be undone.** This method never records a reversing instruction, so a rollback reaching a task that used it stops rather than guessing.

It reads the login file and changes nothing.

Note that no rollback engine reads this yet. What exists today is the recording, which has to happen during the forward run because the values an undo needs are gone once the change is applied.

## See also

- `container.registry.login`

## Examples

Is the engine logged in:

```yaml
- name: Read the login
  container.registry.info:
    registry: registry.example.com
  register: login
```

