---
status: beta
---

# file.touch

Creates an empty file on the target, or updates its modification time.

Makes sure a regular file exists at a path, creating it empty when nothing is there and updating its modification time when it already is. This is ansible.builtin.file with state: touch, and it reports changed on every run for the same reason that module does: moving a modification time is a real change to the device, so a run that claimed otherwise would be wrong rather than tidy. The two kinds of change are told apart in the recorded diff, where a created file reads exists false then true and a re-stamped one reads true then true. Anything at the path that is not a regular file is refused rather than replaced.

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
| `path` | `string` | yes | - | The full path to the file. It is created empty when nothing is there, and left alone apart from its modification time when a regular file already is. A directory, a symbolic link or anything else at the path is refused. |
| `mode` | `string` | no | - | The permission bits, written the way chmod takes them, for example 0644. Sent to the device only when it differs from what is already there, so a converged file is not re-chmodded. Left alone when not set. |
| `owner` | `string` | no | - | The user that should own the file, by name rather than numeric id, since a numeric id is not portable between devices. Sent only when it differs. Left alone when not set. |
| `group` | `string` | no | - | The group that should own the file, by name rather than numeric id. Sent only when it differs, and in one chown alongside owner when both are set. Left alone when not set. |
| `insecure_skip_host_key_verify` | `bool` | no | `false` | Skip SSH host key verification for this task. This removes protection against a machine in the middle answering for the device, so set it only for a target you have decided does not need it. |

## Returns

| Name | Type | Returned | Description |
| --- | --- | --- | --- |
| `dest` | `string` | always | The path this touched, exactly as the task named it. |
| `created` | `bool` | always | True when nothing was at the path and an empty file was made. False when a file was already there and only its modification time moved. |
| `diff` | `dict` | always | The before and after state of the path, each holding exists, kind, and for a path that is there its mode, owner, group, size and mtime. This is the record a rollback reads, so it is written even though this method always reports changed. |

## Undoing this

**Can be undone.** A run that changes something records the instruction that reverses it, as an `inverse` stat holding the method to call and the parameters to call it with, resolved from the state this run actually found. A run that changed nothing records no instruction, which is how it says that undoing it means doing nothing.

A run that created the file emits a file.remove naming it. A run that found the file already there and also changed its mode, owner or group emits a file.permissions carrying the old ones, which is a partial undo: the modification time this method moves on every run is NOT restored, because no method in this catalog can set one. A run that found the file already there and only re-stamped it emits nothing at all, since the modification time is then the single thing that moved and nothing here can put it back.

Note that no rollback engine reads this yet. What exists today is the recording, which has to happen during the forward run because the values an undo needs are gone once the change is applied.

## See also

- `file.remove`
- `file.permissions`
- `file.copy`

## Examples

Create a marker file:

```yaml
- name: Mark the host as provisioned
  fqcn: file.touch
  params:
    path: /var/lib/pleiades/provisioned
```

Create a log file with an owner and a mode:

```yaml
- name: Make the log file the service will append to
  fqcn: file.touch
  params:
    path: /var/log/app/app.log
    owner: app
    group: app
    mode: "0640"
```

Act only when the file had to be created:

```yaml
- name: Create the seed file
  fqcn: file.touch
  params:
    path: /opt/app/seed
  register: seed

- name: Seed the database the first time only
  fqcn: exec.command
  params:
    cmd: /opt/app/bin/seed
  when_cel: seed.created
```

