---
status: beta
---

# file.permissions

Sets a file's owner, group, and mode on the target.

Sets the mode, owner and group of a path that already exists, changing only the ones that differ from what the device reports. A path that is absent is a refusal rather than a create, since making a file exist is file.touch's job and making a directory exist is file.directory's; that keeps a typo in a path from silently producing an empty file. A symbolic link is refused too, because chmod and chown follow a link while a stat of the link reports the link itself, so such a task would change one path while comparing against another and could never report converged. At least one of mode, owner and group must be given: a task that asks for nothing is a runbook mistake, not a no-op.

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
| `path` | `string` | yes | - | The path to change. It must already exist: this method changes a path rather than creating one. |
| `mode` | `string` | no | - | The permission bits as one to four octal digits, quoted, for example "0644". Quote it: an unquoted 0644 is a number in YAML, not text. A symbolic mode such as u+x is refused, because it cannot be compared against the mode the device reports and the task would then report changed on every run. |
| `owner` | `string` | no | - | The user name to give the path. A numeric id is refused: chown reads an all-digit argument as an id while the device reports names, so the two could never compare equal and the task would report changed forever. |
| `group` | `string` | no | - | The group name to give the path, refused as a numeric id for the same reason owner is. |
| `insecure_skip_host_key_verify` | `bool` | no | `false` | Skip SSH host key verification for this task. This removes protection against a machine in the middle answering for the device, so set it only for a target you have decided does not need it. |

## Returns

| Name | Type | Returned | Description |
| --- | --- | --- | --- |
| `path` | `string` | always | The path this task acted on. |
| `mode` | `string` | always | The permission bits the path carries now, as four octal digits. |
| `owner` | `string` | always | The user name owning the path now. |
| `group` | `string` | always | The group owning the path now. |
| `diff` | `dict` | always | The before and after state of the path, each holding its kind, mode, owner, group, size and modification time. Recorded even when nothing changed, in which case the two halves are identical. |

## Undoing this

**Can be undone.** A run that changes something records the instruction that reverses it, as an `inverse` stat holding the method to call and the parameters to call it with, resolved from the state this run actually found. A run that changed nothing records no instruction, which is how it says that undoing it means doing nothing.

A run that changed something emits a file.permissions carrying the mode, owner and group the path had before, which restores it exactly. A converged run emits nothing, because undoing a change that was never made means doing nothing. A run that fails partway emits nothing either, so whatever it applied before failing stays applied and has to be re-run rather than undone.

Note that no rollback engine reads this yet. What exists today is the recording, which has to happen during the forward run because the values an undo needs are gone once the change is applied.

## See also

- `file.touch`
- `file.directory`
- `file.copy`

## Examples

Lock down a key file:

```yaml
- name: Keep the deploy key private
  file.permissions:
    path: /etc/pleiades/deploy.key
    mode: "0600"
    owner: pleiades
    group: pleiades
```

Make a script executable:

```yaml
- name: Allow the rotation script to run
  file.permissions:
    path: /usr/local/bin/rotate-logs
    mode: "0755"
```

Hand a directory to a service account:

```yaml
- name: Give the cache directory to the service
  file.permissions:
    path: /var/cache/pleiades
    owner: pleiades
```

