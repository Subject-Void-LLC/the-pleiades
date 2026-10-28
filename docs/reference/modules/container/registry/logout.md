---
status: beta
---

# container.registry.logout

Removes a registry's login from the engine's login file.

Removes registry's login from the engine's login file, keeping its other logins and settings, and the file mode 0600. A registry with no login there is no change. A logout has no undo, since the password it removes is not kept. A check reads the file and writes nothing. The login file is on the machine the engine runs on (this machine's own when the engine is reached through its local socket, otherwise the device's, over its SSH login): auth_file, or the engine's own default under the login's home, ~/.docker/config.json for Docker and ~/.config/containers/auth.json for Podman.

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
| `registry` | `string` | yes | - | The registry host, as container.registry.login named it. |
| `username` | `string` | no | - | The account the login must be for; when it is another's, the task is refused and nothing is removed. The undo of a login sets it, so a rollback never removes a login someone has written since. |
| `auth_file` | `string` | no | - | The login file, an absolute path on the engine's machine. Absent means the engine's own default. |
| `insecure_skip_host_key_verify` | `bool` | no | `false` | Skip SSH host key verification for this task, when the engine or its machine is reached over SSH. This removes protection against a machine in the middle answering for the device, so set it only for a target you have decided does not need it. |

## Returns

| Name | Type | Returned | Description |
| --- | --- | --- | --- |
| `registry` | `string` | always | The registry host. |
| `auth_file` | `string` | always | The login file. |

## Undoing this

**Cannot be undone.** This method never records a reversing instruction, so a rollback reaching a task that used it stops rather than guessing.

The removed login's password is not kept, so it cannot be written back; container.registry.login writes the bound credential again.

Note that no rollback engine reads this yet. What exists today is the recording, which has to happen during the forward run because the values an undo needs are gone once the change is applied.

## See also

- `container.registry.login`

## Examples

Remove a login:

```yaml
- name: Log out of the registry
  container.registry.logout:
    registry: registry.example.com
```

