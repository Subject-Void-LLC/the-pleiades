---
status: beta
---

# wait.path

Waits for a file path on the target to exist (or stop existing).

Polls a path on the device until it exists, or until it is gone when state is absent, and fails when the timeout runs out first. It changes nothing: state: absent waits for something else to remove the path, it does not remove it. Anything at the path counts as existing, including a directory and a symbolic link whose target is missing, since the link itself is a thing that is there; that differs from wait_for, which follows a link and calls a broken one absent. The delay is spent out of the timeout rather than added to it, matching wait_for, so delay must be shorter than timeout. A run that gives up returns an error, because a task that waited for something that never happened has failed.

## Attributes

|  |  |
| --- | --- |
| Capabilities | `POSIXFileSystemCapable` |
| Transports | `ssh` |
| Requires elevation | no |
| Engine version | `>=1.0.0` |

## Parameters

| Name | Type | Required | Default | Description |
| --- | --- | --- | --- | --- |
| `path` | `string` | yes | - | The path on the device to watch. |
| `state` | `string` | no | `present` | Whether to wait for the path to exist (present) or to stop existing (absent). started and stopped are accepted as wait_for's own spellings of the same two, so a converted playbook needs no edit. drained is refused, since it asks about a socket's send queue and means nothing for a file. |
| `timeout` | `int` | no | `300` | How many seconds to wait in total before giving up, counted from the start of the task, so the delay comes out of it. Must be more than 0. |
| `delay` | `int` | no | `0` | How many seconds to wait before looking the first time. Must be shorter than the timeout, which it is spent out of. |
| `sleep` | `int` | no | `1` | How many seconds to wait between looks. Must be more than 0: a sleep of zero would probe the device as fast as it can answer. |
| `insecure_skip_host_key_verify` | `bool` | no | `false` | Skip SSH host key verification for this task. This removes protection against a machine in the middle answering for the device, so set it only for a target you have decided does not need it. |

## Returns

| Name | Type | Returned | Description |
| --- | --- | --- | --- |
| `path` | `string` | always | The path this task watched. |
| `elapsed` | `int` | always | How many whole seconds the task waited, including the delay. |
| `diff` | `dict` | always | The state of the path when the wait ended, holding its path, kind, mode, owner, group, size and modification time. Both halves are identical, since a wait changes nothing. |

## Undoing this

**Cannot be undone.** This method never records a reversing instruction, so a rollback reaching a task that used it stops rather than guessing.

This method watches a path and changes nothing on the device, so there is nothing to undo. That is the same fact that makes it report changed: false. It still records a diff whose two halves are identical, so a journal can tell an observation from a task that was never recorded.

Note that no rollback engine reads this yet. What exists today is the recording, which has to happen during the forward run because the values an undo needs are gone once the change is applied.

## See also

- `wait.search`
- `pleiades.builtin.wait.port`

## Examples

Wait for a service to write its socket:

```yaml
- name: Wait for the socket to appear
  fqcn: wait.path
  params:
    path: /run/pleiades/api.sock
    timeout: 60
```

Wait for a lock file to be released:

```yaml
- name: Wait for the package manager to finish
  fqcn: wait.path
  params:
    path: /var/lib/dpkg/lock-frontend
    state: absent
    timeout: 300
    sleep: 5
```

Give a slow starter a head start:

```yaml
- name: Wait for the pid file, but not immediately
  fqcn: wait.path
  params:
    path: /run/app.pid
    delay: 10
    timeout: 120
```

