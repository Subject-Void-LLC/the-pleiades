---
status: beta
---

# container.registry.login

Keeps the bound registry credential in the engine's login file.

Logs the engine's machine in to registry with the registry credential bound to the run for that host: never a task parameter, and refused when none is bound. The registry is first asked, through the engine, whether it accepts the account; only then is the login written to the engine's login file, mode 0600 from the moment it exists, under a directory made 0700 when missing, keeping the file's other logins and settings. The password stays on that machine, readable by its owner and by root, so every run warns, changed or not. A file that sends the registry's login to a credential helper is refused. A login already holding this account is no change. A run that added the login records removing it as the undo. The password reaches no result, warning, error or journal entry. A check asks the registry and reads the file, and writes nothing. The login file is on the machine the engine runs on (this machine's own when the engine is reached through its local socket, otherwise the device's, over its SSH login): auth_file, or the engine's own default under the login's home, ~/.docker/config.json for Docker and ~/.config/containers/auth.json for Podman.

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
| `registry` | `string` | yes | - | The registry host, with its port when it has one (ghcr.io, registry.example.com:5000); docker.io for Docker Hub. |
| `auth_file` | `string` | no | - | The login file, an absolute path on the engine's machine. Absent means the engine's own default. |
| `insecure_skip_host_key_verify` | `bool` | no | `false` | Skip SSH host key verification for this task, when the engine or its machine is reached over SSH. This removes protection against a machine in the middle answering for the device, so set it only for a target you have decided does not need it. |

## Returns

| Name | Type | Returned | Description |
| --- | --- | --- | --- |
| `registry` | `string` | always | The registry host. |
| `username` | `string` | always | The account logged in as. |
| `auth_file` | `string` | always | The login file. |
| `warnings` | `list of string` | always | That the password is stored on the machine, and where. |

## Undoing this

**Can be undone.** A run that changes something records the instruction that reverses it, as an `inverse` stat holding the method to call and the parameters to call it with, resolved from the state this run actually found. A run that changed nothing records no instruction, which is how it says that undoing it means doing nothing.

A run that added the registry's login records removing it; one that replaced or found a login records nothing, since the password it replaced is not kept.

Note that no rollback engine reads this yet. What exists today is the recording, which has to happen during the forward run because the values an undo needs are gone once the change is applied.

## See also

- `container.registry.logout`
- `container.registry.info`
- `container.image.pull`

## Examples

Let the engine pull from a private registry:

```yaml
- name: Log in to the registry
  container.registry.login:
    registry: registry.example.com
```

