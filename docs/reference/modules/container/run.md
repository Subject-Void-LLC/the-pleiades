---
status: beta
---

# container.run

Makes a container exist as declared, and running.

Makes the container named name exist from image as the task declares it, and run. An absent container is created and started; a stopped one that matches is started; a running one that matches is left alone. A container that differs from the declaration (its image by digest, command, entrypoint, the declared environment and labels, user, workdir, network, ports, mounts, devices, restart policy, privilege, added capabilities, read-only root, no-new-privileges) is refused, naming each difference and container.recreate, which replaces it: nothing is ever replaced here. A paused container is refused. Safe by default: no network, read-only bind mounts, restart no, no-new-privileges, and the image pulled and run by digest. A create whose answer was lost is found by a label that create alone set, never by name, so another run's container of the same name is left alone; a start that fails removes what the create made. A check makes every read and refusal a run makes, resolving the image's digest and reading the container, and pulls, creates and starts nothing. With wait, the container is a one-shot: a fresh container (a name in use is refused) runs to its exit, and the task returns its exit code and output (together at most 1 MiB, refused past it), removes it afterwards unless remove is false, and fails on an exit code accept_exit_codes does not name or on its timeout, removing it then whatever remove says, since a failed task records no undo; a one-shot runs its command, so it cannot be checked, and check_mode on one is refused at validation. The engine is reached the first way onboarding proved for the device (this machine's socket, an SSH channel to the engine's socket, the engine's own dial-stdio over SSH, or mutual TLS), its identity checked against the one onboarding recorded, and every request outside the operations this method declares is refused before it reaches the engine.

## Attributes

|  |  |
| --- | --- |
| Capabilities | `ContainerEngineCapable` |
| Transports | `engine_local`, `engine_socket`, `engine_stdio`, `engine_tls` |
| Requires elevation | no |
| Runs | on or against the target device; acts on its target device |
| Check mode | Supported for some calls, named in the description: those report what they would change and change nothing, a check run names any other as unchecked, and validation refuses check_mode on one |
| Engine version | `>=0.2.0` |

## Parameters

| Name | Type | Required | Default | Description |
| --- | --- | --- | --- | --- |
| `name` | `string` | yes | - | The container's name, which every container.* method addresses it by. |
| `image` | `string` | yes | - | The image to run. A tag is resolved through its registry to the digest it names now (a multi-platform image's index), and the container runs that digest, which the task returns; an image named by digest is run as named. An absent image is pulled by digest, with the registry credential bound for its host when there is one. |
| `pull` | `string` | no | `missing` | How the image is found. missing: a tag is resolved through its registry to a digest, pulled by that digest when absent. never: the image the engine already holds under the reference as written (a loaded or air-gapped image), refused when absent and never pulled; its ID is what runs, and the digest returned is the one the engine recorded from a registry, when it has one. |
| `command` | `list` | no | - | The command, as a list of arguments, replacing the image's. Never run through a shell unless the list names one ([/bin/sh, -c, "..."]). |
| `entrypoint` | `list` | no | - | The entrypoint, as a list of arguments, replacing the image's. |
| `env` | `dict` | no | - | Environment variables by name. Values are never reported (results, diffs and errors name the variable only), since one may come from a credential; anyone with access to the engine can still read them by inspecting the container. |
| `network` | `string` | no | `none` | **Host escape:** setting this lets the container act on its host. The network the container joins: none (the default: no network at all), bridge, host, or a network's name. Reaching a network is always a declared decision, and host shares the host's own network stack, its loopback services included. |
| `ports` | `list` | no | - | Published ports, [host_ip:]host_port:container_port[/tcp\|udp\|sctp]. Refused on network none or host, where nothing is published. |
| `volumes` | `list` | no | - | **Host escape:** setting this lets the container act on its host. Mounts, source:target[:ro\|rw]: a source beginning with / is a host path (absolute and clean, never the engine's own socket), anything else a named volume. Read-only unless the mount says rw. |
| `restart_policy` | `string` | no | `no` | The engine's restart policy for the container. |
| `labels` | `dict` | no | - | Labels on the container. org.pleiades.image, which records the image the task named, is this platform's own. |
| `workdir` | `string` | no | - | The process's working directory, an absolute path inside the container. |
| `user` | `string` | no | - | The user, or user:group, the process runs as. |
| `read_only` | `bool` | no | `false` | Give the container a read-only root filesystem. |
| `privileged` | `bool` | no | `false` | **Host escape:** setting this lets the container act on its host. Run the container privileged: every capability and every device of the host, which is root on the host for whoever controls the container. |
| `cap_add` | `list` | no | - | **Host escape:** setting this lets the container act on its host. Kernel capabilities to add, by name (NET_ADMIN, SYS_TIME). |
| `devices` | `list` | no | - | **Host escape:** setting this lets the container act on its host. Host devices to give the container, /dev/path[:/dev/path-inside]. |
| `engine` | `string` | no | - | Which container engine to use on a device that runs more than one. Needed only there: on a device with one engine, that engine is used. An engine the device does not run is refused. |
| `wait` | `bool` | no | `false` | Run the container to its exit, as a command: a one-shot. |
| `remove` | `bool` | no | `true` | With wait, remove the container once it exits. A failed one-shot is removed whatever this says, since a failed task records no undo to remove it by. |
| `wait_timeout` | `int` | no | `3600` | With wait, seconds the container may run before it is killed and the task fails, up to 86400. |
| `accept_exit_codes` | `list` | no | `[0]` | With wait, the exit codes that are success. |
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
| `exit_code` | `int` | with wait | The container's exit status. |
| `timed_out` | `bool` | with wait | Whether it ran past wait_timeout and was killed. |
| `stdout` | `string` | with wait | What it wrote to standard output. |
| `stderr` | `string` | with wait | What it wrote to standard error. |

## Undoing this

**Can be undone.** A run that changes something records the instruction that reverses it, as an `inverse` stat holding the method to call and the parameters to call it with, resolved from the state this run actually found. A run that changed nothing records no instruction, which is how it says that undoing it means doing nothing.

A run that created the container records its removal, by ID; a run that started an existing stopped one records stopping it, by ID; a run that found it running as declared records nothing.

Note that no rollback engine reads this yet. What exists today is the recording, which has to happen during the forward run because the values an undo needs are gone once the change is applied.

## See also

- `container.create`
- `container.recreate`
- `container.stop`
- `container.remove`

## Examples

Run a migration image to its exit:

```yaml
- name: Migrate
  container.run:
    name: migrate-{{ vars.release }}
    image: registry.example/app-migrate:2.4.1
    network: appnet
    wait: true
```

Run a web server on a network of its own:

```yaml
- name: Web
  container.run:
    name: web
    image: nginx:1.27
    network: appnet
    ports: ["127.0.0.1:8080:80"]
    volumes: ["/srv/web/conf:/etc/nginx/conf.d"]
    restart_policy: unless-stopped
```

