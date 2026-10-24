---
status: beta
---

# net.trace

Traces the path to an address from a Linux device or a container on a container host, and fails when it is not the path expected.

Runs tracepath, which needs no raw socket and so no privilege, and returns each hop's address. Without container it runs on the device, a Linux machine reached over SSH. With container it runs inside that container on the device, a container host, through the engine's own exec as container.exec does, so a lab host with no SSH of its own (a containerlab end host) can be traced from; the container needs tracepath (iputils) and /bin/sh. pleiades validate refuses a call whose device cannot take the shape it chose: container on a device that is not a container host, or none on one that is not a Linux SSH device. With expect, it fails the task when the destination was not reached, or a hop named in includes is missing, or one named in avoids is on the path. It changes nothing.

## Attributes

|  |  |
| --- | --- |
| Capabilities | `LinuxCapable`, `CommandExecCapable` |
| Transports | `ssh` |
| With `container`: capabilities | `ContainerEngineCapable` |
| With `container`: transports | `engine_local`, `engine_socket`, `engine_stdio`, `engine_tls` |
| Requires elevation | no |
| Runs | on or against the target device; acts on its target device |
| Check mode | Supported: reports what it would change and changes nothing |
| Engine version | `>=0.2.0` |

## Parameters

| Name | Type | Required | Default | Description |
| --- | --- | --- | --- | --- |
| `dest` | `string` | yes | - | The address to trace to. |
| `max_hops` | `int` | no | `30` | The most hops to try. |
| `expect` | `dict` | no | - | reached (bool), includes (list of addresses that must be hops) and avoids (list that must not be). |
| `container` | `string` | no | - | Trace from this container on the device, a container host, rather than from the device itself. The device then needs a container engine, not SSH and Linux. An empty value is refused rather than read as none: leave container out to trace from the device. |
| `engine` | `string` | no | - | With container, the engine on a device that runs both Docker and Podman: docker or podman. Never inferred. |
| `insecure_skip_host_key_verify` | `bool` | no | `false` | Skip SSH host key verification for this task. This removes protection against a machine in the middle answering for the device, so set it only for a target you have decided does not need it. |

## Returns

| Name | Type | Returned | Description |
| --- | --- | --- | --- |
| `hops` | `list of string` | always | Each hop's address, in order, the destination last when reached. |
| `reached` | `bool` | always | Whether the destination answered. |

## Undoing this

**Cannot be undone.** This method never records a reversing instruction, so a rollback reaching a task that used it stops rather than guessing.

It sends probes and reads their answers, changing nothing.

Note that no rollback engine reads this yet. What exists today is the recording, which has to happen during the forward run because the values an undo needs are gone once the change is applied.

## See also

- `wait.routes`
- `net.batfish.check`
- `container.exec`

## Examples

Expect a path through r2:

```yaml
- name: h1's LAN reaches h2 through r2
  net.trace:
    dest: 10.3.3.10
    expect:
      reached: true
      includes: [10.0.12.1]
```

Trace from a lab host container:

```yaml
- name: h1 reaches h2 through r1
  net.trace:
    target: dockerhost
    container: clab-routing-h1
    dest: 10.3.3.10
    expect:
      reached: true
      includes: [10.1.1.1]
```

