---
status: beta
---

# container.create

Makes a container exist as declared, without starting it.

Makes the container named name exist from image as the task declares it, without starting it: container.run's declaration, comparison and refusals, with no start. An existing container that matches is left as it is, running or not; one that differs is refused, naming container.recreate. A check pulls and creates nothing. The engine is reached the first way onboarding proved for the device (this machine's socket, an SSH channel to the engine's socket, the engine's own dial-stdio over SSH, or mutual TLS), its identity checked against the one onboarding recorded, and every request outside the operations this method declares is refused before it reaches the engine.

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
| `name` | `string` | yes | - | The container's name, which every container.* method addresses it by. |
| `image` | `string` | yes | - | The image to run. A tag is resolved through its registry to the digest it names now (a multi-platform image's index), and the container runs that digest, which the task returns; an image named by digest is run as named. An absent image is pulled by digest, with the registry credential bound for its host when there is one. |
| `pull` | `string` | no | `missing` | How the image is found. missing: a tag is resolved through its registry to a digest, pulled by that digest when absent. never: the image the engine already holds under the reference as written (a loaded or air-gapped image), refused when absent and never pulled; its ID is what runs, and the digest returned is the one the engine recorded from a registry, when it has one. **Choices:** `missing`, `never`. |
| `command` | `list` | no | - | The command, as a list of arguments, replacing the image's. Never run through a shell unless the list names one ([/bin/sh, -c, "..."]). |
| `entrypoint` | `list` | no | - | The entrypoint, as a list of arguments, replacing the image's. |
| `env` | `dict` | no | - | Environment variables by name. Values are never reported (results, diffs and errors name the variable only), since one may come from a credential; anyone with access to the engine can still read them by inspecting the container. |
| `network` | `string` | no | `none` | **Host escape:** setting this lets the container act on its host. The network the container joins: none (the default: no network at all), bridge, host, or a network's name. Reaching a network is always a declared decision, and host shares the host's own network stack, its loopback services included. |
| `ports` | `list` | no | - | Published ports, [host_ip:]host_port:container_port[/tcp\|udp\|sctp]. Refused on network none or host, where nothing is published. |
| `volumes` | `list` | no | - | **Host escape:** setting this lets the container act on its host. Mounts, source:target[:ro\|rw]: a source beginning with / is a host path (absolute and clean, never the engine's own socket), anything else a named volume. Read-only unless the mount says rw. |
| `restart_policy` | `string` | no | `no` | The engine's restart policy for the container. **Choices:** `no`, `on-failure`, `always`, `unless-stopped`. |
| `labels` | `dict` | no | - | Labels on the container. org.pleiades.image, which records the image the task named, is this platform's own. |
| `workdir` | `string` | no | - | The process's working directory, an absolute path inside the container. |
| `user` | `string` | no | - | The user, or user:group, the process runs as. |
| `read_only` | `bool` | no | `false` | Give the container a read-only root filesystem. |
| `memory` | `string` | no | - | The container's memory limit, a size such as 512m or 2g (b, k, m and g, binary units), at least 6m. Unset, the container has no limit. Compared with a container already there only when set. |
| `memory_swap` | `string` | no | - | Memory and swap together, a size at least memory's, or -1 for unlimited swap; needs memory. Unset, the engine decides (Docker allows as much swap as memory). |
| `privileged` | `bool` | no | `false` | **Host escape:** setting this lets the container act on its host. Run the container privileged: every capability and every device of the host, which is root on the host for whoever controls the container. |
| `cap_add` | `list` | no | - | **Host escape:** setting this lets the container act on its host. Kernel capabilities to add, by name (NET_ADMIN, SYS_TIME). |
| `devices` | `list` | no | - | **Host escape:** setting this lets the container act on its host. Host devices to give the container, /dev/path[:/dev/path-inside]. |
| `engine` | `string` | no | - | Which container engine to use on a device that runs more than one. Needed only there: on a device with one engine, that engine is used. An engine the device does not run is refused. **Choices:** `docker`, `podman`. |
| `insecure_skip_host_key_verify` | `bool` | no | `false` | Skip SSH host key verification for this task, when the engine is reached over SSH. This removes protection against a machine in the middle answering for the device, so set it only for a target you have decided does not need it. |

## Returns

| Name | Type | Returned | Description |
| --- | --- | --- | --- |
| `name` | `string` | always | The container's name. |
| `id` | `string` | when the container exists | The container's ID. |
| `state` | `string` | when the container exists or would | The container's state as the engine names it (created, running, exited, paused), after the task. |
| `image` | `string` | always | The image by digest: the one the container runs, or would. |
| `image_digest` | `string` | always | The image's digest. |
| `image_id` | `string` | when the image is on the engine | The engine's ID for the image. |
| `pulled` | `bool` | always | Whether the image had to be pulled (by a check, would be). |
| `env_names` | `list of string` | always | The environment variables the task set, by name only. |

## Undoing this

**Can be undone.** A run that changes something records the instruction that reverses it, as an `inverse` stat holding the method to call and the parameters to call it with, resolved from the state this run actually found. A run that changed nothing records no instruction, which is how it says that undoing it means doing nothing.

A run that created the container records its removal, by ID; one that found it as declared records nothing.

Note that no rollback engine reads this yet. What exists today is the recording, which has to happen during the forward run because the values an undo needs are gone once the change is applied.

## See also

- `container.run`
- `container.start`

## Examples

Create a job container to start later:

```yaml
- name: Backup job
  container.create:
    name: backup
    image: restic/restic:0.17.3
    command: [backup, /data]
    volumes: ["appdata:/data"]
```

