---
status: beta
---

# file.remove

Removes a file or directory from the target.

Ensures nothing exists at a path, which is ansible.builtin.file with state=absent. It reads the path first, so a path that is already gone reports no change and sends no removal command at all, and a symbolic link is removed as the link rather than followed to whatever it points at. It diverges from Ansible in exactly one place, deliberately: Ansible deletes a non-empty directory without being asked, and this refuses unless the task sets recurse, because a recursive delete is the most destructive thing in this namespace and should be readable in the runbook that asks for it. Nothing this method removes can be restored by this platform.

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
| `path` | `string` | yes | - | The path that must not exist when this task finishes. A symbolic link is removed as the link, so whatever it points at is left alone. |
| `recurse` | `bool` | no | `false` | Permit removing a directory that is not empty, and everything inside it. Without it, a non-empty directory is refused rather than emptied. Ansible's state=absent recurses without asking; this does not. |
| `insecure_skip_host_key_verify` | `bool` | no | `false` | Skip SSH host key verification for this task. This removes protection against a machine in the middle answering for the device, so set it only for a target you have decided does not need it. |

## Returns

| Name | Type | Returned | Description |
| --- | --- | --- | --- |
| `diff` | `object` | always | What was at the path before this task and what is there after, under before and after. The before half carries exists, kind, mode, owner, group and size, which is everything a rollback can report about what it cannot restore. On a path that was already absent the two halves are identical, which is how a reader tells "nothing to do" from "never ran". |

## Undoing this

**Cannot be undone.** This method never records a reversing instruction, so a rollback reaching a task that used it stops rather than guessing.

What this removes is gone. The content is not journaled anywhere, not the bytes of a file, not the tree under a directory, not what a symbolic link pointed at, so nothing on this platform can put it back and no run of this method emits an inverse. The recorded diff still describes what was there, so a rollback can report precisely what it cannot restore, which is the honest answer. Emitting an inverse that recreated the path would be worse than emitting none: it would leave an empty file where a full one had been and call that a restore.

Note that no rollback engine reads this yet. What exists today is the recording, which has to happen during the forward run because the values an undo needs are gone once the change is applied.

## See also

- `file.directory`
- `file.touch`

## Examples

Remove a file:

```yaml
- name: Drop the leftover lock file
  file.remove:
    path: /var/run/deploy.lock
```

Remove an empty directory:

```yaml
- name: Drop the empty spool directory
  file.remove:
    path: /var/spool/old-queue
```

Remove a directory and everything in it:

```yaml
- name: Drop the previous release
  file.remove:
    path: /opt/app/releases/2024-11-02
    recurse: true
```

