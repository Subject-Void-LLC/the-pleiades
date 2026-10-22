---
status: beta
---

# file.stat

Reports what is at a path on the target, and changes nothing.

Reads what is at a path and reports it: whether anything is there, what it is (a file, a directory, a symlink or something else), its mode, owner and group, a symlink's target, its size and its modification time. This is ansible.builtin.stat. It only reads, so it never reports a change, records no undo, and its check is the same read. An absent path is an answer (exists is false), not a failure. Use it to look at a path before or after a change instead of a shell command, which reports changed on every run because it cannot tell what its command did.

## Attributes

|  |  |
| --- | --- |
| Capabilities | `POSIXFileSystemCapable` |
| Transports | `ssh` |
| Requires elevation | no |
| Runs | on or against the target device; acts on its target device |
| Check mode | Supported: reports what it would change and changes nothing |
| Engine version | `>=0.2.0` |

## Parameters

| Name | Type | Required | Default | Description |
| --- | --- | --- | --- | --- |
| `path` | `string` | yes | - | The path to look at. A symlink is reported as the link itself, with its target, not as what it points to. |
| `insecure_skip_host_key_verify` | `bool` | no | `false` | Skip SSH host key verification for this task. This removes protection against a machine in the middle answering for the device, so set it only for a target you have decided does not need it. |

## Returns

| Name | Type | Returned | Description |
| --- | --- | --- | --- |
| `path` | `string` | always | The path this task looked at. |
| `exists` | `bool` | always | Whether anything is at the path. |
| `kind` | `string` | always | What is there: file, directory, symlink, other, or absent. |
| `mode` | `string` | when it exists | The permission bits in octal, such as 0755, with the setuid, setgid and sticky digit when one is set. |
| `owner` | `string` | when it exists | The owning user's name. |
| `group` | `string` | when it exists | The owning group's name. |
| `size` | `int` | when it exists | The size in bytes. |
| `mtime` | `int` | when it exists | The modification time, as a Unix timestamp. |
| `target` | `string` | when it is a symlink | Where the symlink points. |

## Undoing this

**Cannot be undone.** This method never records a reversing instruction, so a rollback reaching a task that used it stops rather than guessing.

It only reads what is at a path; nothing on the device changes, so there is nothing to undo.

Note that no rollback engine reads this yet. What exists today is the recording, which has to happen during the forward run because the values an undo needs are gone once the change is applied.

## See also

- `file.directory`
- `file.permissions`
- `facts.gather`

## Examples

Look at a directory before changing it:

```yaml
- name: See what is at the app directory
  file.stat:
    path: /srv/app
  register: app_dir
```

