---
status: beta
---

# container.info

Reports one container as the engine sees it.

Reports the container named name: whether it exists, and if so everything the engine's inspect answer says that a runbook decides on: its ID, state, image, command and entrypoint (each as its program and how many arguments follow it, never an argument, since a command line often carries a secret such as a --password flag), user, working directory, hostname, labels, network mode and each attached network with its address, published ports, restart policy and count, privilege, read-only root, added capabilities, memory limit, mounts, last exit code, start and finish times, whether it was killed for memory, process ID, health and the engine's last start error, and its environment variables by name only, since a value may be a credential (anyone with access to the engine can still read them by inspecting the container). An absent container is reported as exists false, not as a failure, so a runbook can branch on it. It changes nothing, so a check is the same read. The engine is reached the first way onboarding proved for the device (this machine's socket, an SSH channel to the engine's socket, the engine's own dial-stdio over SSH, or mutual TLS), its identity checked against the one onboarding recorded, and every request outside the operations this method declares is refused before it reaches the engine.

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
| `insecure_skip_host_key_verify` | `bool` | no | `false` | Skip SSH host key verification for this task, when the engine is reached over SSH. This removes protection against a machine in the middle answering for the device, so set it only for a target you have decided does not need it. |

## Returns

| Name | Type | Returned | Description |
| --- | --- | --- | --- |
| `exists` | `bool` | always | Whether a container by that name exists. |
| `name` | `string` | when it exists | The container's name. |
| `id` | `string` | when it exists | The container's ID. |
| `state` | `string` | when it exists | Its state as the engine names it (created, running, paused, restarting, exited, dead). |
| `image_id` | `string` | when it exists | The engine's ID for the image it runs. |
| `image` | `string` | when it exists | The image reference a container.* method recorded on it, or empty for a container something else made. |
| `created` | `string` | when it exists | When it was created, as the engine writes the time. |
| `platform` | `string` | when it exists | The platform the engine reports for it, such as linux. |
| `restart_count` | `int` | when it exists | How many times the engine has restarted it. |
| `labels` | `dict` | when it exists | Its labels. |
| `command` | `dict` | when it exists | Its command as program (the first word) and arg_count (how many arguments follow). The arguments are withheld: a command line often carries a secret, such as a --password flag. |
| `entrypoint` | `dict` | when it exists | Its entrypoint as program and arg_count, the arguments withheld as the command's are. |
| `user` | `string` | when it exists | The user it runs as, or empty for the image's. |
| `working_dir` | `string` | when it exists | Its working directory, or empty for the image's. |
| `hostname` | `string` | when it exists | Its hostname. |
| `env_names` | `list of string` | when it exists | Its environment variables, by name only. |
| `network` | `string` | when it exists | Its network mode: none, bridge, host, or a network's name. |
| `networks` | `list of dict` | when it exists | Each network it is attached to, sorted by name: name and network_id, and ip_address, gateway, mac_address and aliases when the engine gives them (a stopped container has no address). |
| `ports` | `list of string` | when it exists | Each port it publishes, as docker run writes one (127.0.0.1:8080:80/tcp, the host address left out when it is every address), sorted. |
| `restart_policy` | `string` | when it exists | Its restart policy, or empty for none. |
| `privileged` | `bool` | when it exists | Whether it runs privileged. |
| `read_only` | `bool` | when it exists | Whether its root filesystem is read-only. |
| `cap_add` | `list of string` | when it exists | The capabilities added beyond the engine's default set. |
| `memory` | `int` | when it exists | Its memory limit in bytes, 0 for none. |
| `volumes` | `list of string` | when it exists | Each mount as source:destination:mode, a volume by its name and a bind by its host path. |
| `exit_code` | `int` | when it exists | Its last exit status, 0 when it has never exited. |
| `started_at` | `string` | when it exists | When it last started, as the engine writes the time. |
| `finished_at` | `string` | when it exists | When it last exited, as the engine writes the time. |
| `oom_killed` | `bool` | when it exists | Whether the kernel killed it for running out of memory. |
| `pid` | `int` | when it exists | Its main process's ID on the engine's host, 0 when it is not running. |
| `health` | `string` | when the container has a health check | Its health as the engine names it: starting, healthy or unhealthy. |
| `state_error` | `string` | when the engine recorded why it last failed to start | The engine's own words for it, cut to 1 KiB. |

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

