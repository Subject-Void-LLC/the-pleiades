---
status: beta
---

# container.copy_in

Puts one file into a container.

Makes the file at dest inside the container named name hold content, or the bytes of src, a file in the project directory, with mode. The file is read first and written only when its bytes or mode differ, so a second run with the same bytes writes nothing. src is read by the command line, which holds the project (relative, inside it, through no symbolic link, never .pleiades, at most 64 MiB); a Walk-tier Runner cannot see the project, so there src is refused and content is the way. The container may be running or stopped. The bytes the file held before are not kept, so the write has no undo. A check reads and compares, and writes nothing. The engine is reached the first way onboarding proved for the device (this machine's socket, an SSH channel to the engine's socket, the engine's own dial-stdio over SSH, or mutual TLS), its identity checked against the one onboarding recorded, and every request outside the operations this method declares is refused before it reaches the engine.

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
| `dest` | `string` | yes | - | The file's absolute path inside the container; its directory must exist. |
| `content` | `string` | no | - | The bytes the file should hold. Set content or src, not both. |
| `src` | `string` | no | - | A file in the project directory whose bytes the file should hold, relative to it. |
| `mode` | `string` | no | `0644` | The file's mode, in octal. |
| `insecure_skip_host_key_verify` | `bool` | no | `false` | Skip SSH host key verification for this task, when the engine is reached over SSH. This removes protection against a machine in the middle answering for the device, so set it only for a target you have decided does not need it. |

## Returns

| Name | Type | Returned | Description |
| --- | --- | --- | --- |
| `name` | `string` | always | The container's name. |
| `dest` | `string` | always | The file's path inside the container. |
| `checksum` | `string` | always | The SHA-256 of the bytes it holds. |
| `size` | `int` | always | Their size. |

## Undoing this

**Cannot be undone.** This method never records a reversing instruction, so a rollback reaching a task that used it stops rather than guessing.

The bytes a file held before are not kept, so the write cannot be undone; a container recreated from its image has the image's file again.

Note that no rollback engine reads this yet. What exists today is the recording, which has to happen during the forward run because the values an undo needs are gone once the change is applied.

## See also

- `container.copy_out`
- `file.copy`

## Examples

Drop a configuration file into a running container:

```yaml
- name: Web config
  container.copy_in:
    name: web
    dest: /etc/nginx/conf.d/site.conf
    src: files/site.conf
```

