---
status: beta
---

# virt.clab.node.exec

Runs one command in a lab node, with no SSH to the node.

Runs the command inside the node's container through the server's exec endpoint, so a node that has no SSH, or no address, is reached. The server reports exit code 0 for a command that failed, so the method wraps the command in a small shell that prints its own exit status (marked with a random value no output can forge) and reads it back: a failing command fails the task, with its output recorded first, unless allow_nonzero is set. That needs a shell (sh) in the node; raw sends the command exactly as given, with the server's exit code, which is unreliable. A check is not possible: what a command changes is known only after it runs. The target is the containerlab API server, a generic_http device that onboarding recognised (it declares ContainerlabAPICapable); its login is the device's stored credential, with http_auth basic, and neither the password nor the token it gets is logged, returned or journaled.

## Attributes

|  |  |
| --- | --- |
| Capabilities | `ContainerlabAPICapable` |
| Transports | `https` |
| Requires elevation | no |
| Runs | in the host process (the CLI or a Runner); acts on its target device |
| Check mode | Not supported: a check run names this task as unchecked, since a command run in a lab node can change anything the node can reach, and what it changes is known only once it has run |
| Engine version | `>=0.3.0` |

## Parameters

| Name | Type | Required | Default | Description |
| --- | --- | --- | --- | --- |
| `lab` | `string` | yes | - | The lab's name, as containerlab deployed it. |
| `node` | `string` | yes | - | The node's name in the lab (n1), or its container's name (clab-<lab>-n1). |
| `command` | `string` | no | - | The command line, split into words by the server as a shell would, so quote what must stay one word. Set command or argv, not both. |
| `argv` | `list of string` | no | - | The command as separate words, each quoted so the server cannot split or expand one. Set command or argv, not both. |
| `allow_nonzero` | `bool` | no | `false` | Do not fail on a non-zero exit code; the code is still returned. |
| `raw` | `bool` | no | `false` | Send the command as given, with no wrapper and no reliable exit code, for a node with no shell. |
| `timeout` | `int` | no | `60` | Seconds the login and the command may take together. |

## Returns

| Name | Type | Returned | Description |
| --- | --- | --- | --- |
| `exit_code` | `int` | always | The command's exit code. |
| `stdout` | `string` | always | Its standard output. |
| `stderr` | `string` | always | Its standard error. |
| `name` | `string` | always | The node's container name. |

## Undoing this

**Cannot be undone.** This method never records a reversing instruction, so a rollback reaching a task that used it stops rather than guessing.

A command's effect inside a lab node is unknown to this platform, as exec.command's is on a host.

Note that no rollback engine reads this yet. What exists today is the recording, which has to happen during the forward run because the values an undo needs are gone once the change is applied.

## See also

- `container.exec`
- `exec.command`
- `net.frr.config`

## Examples

Give a lab router an address with no SSH:

```yaml
- name: r1 eth1 gets its address
  virt.clab.node.exec:
    target: clab-api
    lab: nclab
    node: r1
    argv: [ip, address, add, 10.1.1.1/24, dev, eth1]
```

