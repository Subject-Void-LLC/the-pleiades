---
status: beta
---

# container.engine.info

Reads the container engine's own report of itself.

Reads the device's container engine as it reports itself, for a later task's when: Docker's info and version, or Podman's libpod info, which says what Docker's form does not. Both are reported under the same names: the engine and its version, the newest API version it speaks, its storage driver, whether it runs rootless, the cgroup version and driver, the OS and architecture (in Go's names, linux and amd64), the kernel, the host's operating system, its container and image counts, CPUs and memory. On a device running both engines, engine chooses which. Reading changes nothing, and a check reads the same way. The engine is reached the first way onboarding proved for the device (this machine's socket, an SSH channel to the engine's socket, the engine's own dial-stdio over SSH, or mutual TLS), its identity checked against the one onboarding recorded, and every request outside the operations this method declares is refused before it reaches the engine.

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
| `insecure_skip_host_key_verify` | `bool` | no | `false` | Skip SSH host key verification for this task, when the engine is reached over SSH. This removes protection against a machine in the middle answering for the device, so set it only for a target you have decided does not need it. |

## Returns

| Name | Type | Returned | Description |
| --- | --- | --- | --- |
| `engine` | `string` | always | The engine: docker or podman. |
| `carrier` | `string` | always | How the engine was reached: local, socket, stdio or tls. |
| `version` | `string` | always | The engine's release. |
| `api_version` | `string` | always | The newest API version the engine speaks. |
| `storage_driver` | `string` | always | The storage driver (overlay2, overlay, btrfs, ...). |
| `rootless` | `bool` | always | Whether the engine runs without root. |
| `cgroup_version` | `string` | always | The cgroup version, 1 or 2. |
| `cgroup_driver` | `string` | always | The cgroup driver: systemd or cgroupfs. |
| `os` | `string` | always | The engine's OS, in Go's name (linux). |
| `arch` | `string` | always | The engine's architecture, in Go's name (amd64, arm64). |
| `kernel_version` | `string` | always | The host's kernel release. |
| `operating_system` | `string` | always | The host's operating system, as the engine names it. |
| `containers` | `int` | always | How many containers the engine has, running or not. |
| `containers_running` | `int` | always | How many of them are running. |
| `images` | `int` | always | How many images the engine has. |
| `cpus` | `int` | always | The host's CPUs, as the engine sees them. |
| `memory_bytes` | `int` | always | The host's memory in bytes, as the engine sees it. |

## Undoing this

**Cannot be undone.** This method never records a reversing instruction, so a rollback reaching a task that used it stops rather than guessing.

Reading is not changing: the method reads the engine's own report and changes nothing.

Note that no rollback engine reads this yet. What exists today is the recording, which has to happen during the forward run because the values an undo needs are gone once the change is applied.

## See also

- `container.network.info`
- `container.volume.info`
- `facts.gather`

## Examples

Run a container only on a rootless engine:

```yaml
- name: Read the engine
  container.engine.info:
  register: engine

- name: Run the build cache
  container.run:
    name: cache
    image: redis:7.4
  when:
    - result.engine.rootless
```

