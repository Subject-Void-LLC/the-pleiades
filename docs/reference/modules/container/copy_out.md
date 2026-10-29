---
status: beta
---

# container.copy_out

Reads one text file out of a container.

Returns the text of the file at src inside the container named name, running or stopped, with its size and SHA-256. A file over max_bytes, or one that is not valid UTF-8 text, is refused rather than cut or mangled. A file that is not there, in a container or because the container is not there, is not a failure: exists is false and no text is reported, so a later task can decide what to do (container.info says which). It changes nothing, so a check is the same read, an absent file included. The engine is reached the first way onboarding proved for the device (this machine's socket, an SSH channel to the engine's socket, the engine's own dial-stdio over SSH, or mutual TLS), its identity checked against the one onboarding recorded, and every request outside the operations this method declares is refused before it reaches the engine.

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
| `src` | `string` | yes | - | The file's absolute path inside the container. |
| `max_bytes` | `int` | no | `1048576` | The largest file read, in bytes, up to 16 MiB. |
| `insecure_skip_host_key_verify` | `bool` | no | `false` | Skip SSH host key verification for this task, when the engine is reached over SSH. This removes protection against a machine in the middle answering for the device, so set it only for a target you have decided does not need it. |

## Returns

| Name | Type | Returned | Description |
| --- | --- | --- | --- |
| `name` | `string` | always | The container's name. |
| `src` | `string` | always | The path read, as src gave it. |
| `exists` | `bool` | always | Whether the file is there to read. |
| `content` | `string` | when exists | The file's text. |
| `size` | `int` | when exists | Its size in bytes. |
| `checksum` | `string` | when exists | Its SHA-256. |

## Undoing this

**Cannot be undone.** This method never records a reversing instruction, so a rollback reaching a task that used it stops rather than guessing.

It reads what the engine reports, and changes nothing.

Note that no rollback engine reads this yet. What exists today is the recording, which has to happen during the forward run because the values an undo needs are gone once the change is applied.

## See also

- `container.copy_in`
- `container.exec`

## Examples

Read the app's version file:

```yaml
- name: Version
  container.copy_out:
    name: web
    src: /opt/app/VERSION
  register: version
```

