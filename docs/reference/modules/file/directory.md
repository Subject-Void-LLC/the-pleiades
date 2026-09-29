---
status: beta
---

# file.directory

Makes sure a directory exists on the target, with the mode, owner and group the task asks for.

Ensures a directory exists at a path, creating any missing parents along the way exactly as mkdir -p does, and gives the directory, and every parent it creates, the mode, owner and group the task names, as Ansible does; a parent that already existed is never changed. This is ansible.builtin.file with state=directory; use file.remove to delete a directory and file.permissions to change attributes without creating anything. State is read before anything is written, so a run that finds the directory already correct reports no change, and only the attributes that actually differ are applied. If something that is not a directory already exists at the path, the task fails rather than replacing it: turning a file into a directory would destroy the file.

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
| `path` | `string` | yes | - | The directory to ensure exists. Missing parent directories are created too, the way mkdir -p does. Ansible creates parents implicitly and has no parameter for it, so neither does this. |
| `mode` | `string` | no | - | The permission bits in octal, written as a quoted string such as "0755". Quote it: an unquoted 0755 is read as a number by YAML and its leading zero is lost, so an unquoted value is refused rather than applied. A symbolic mode such as u+rwx is refused too, since it cannot be compared against the mode the device reports. Left unset, a new directory gets whatever the device's umask gives it and an existing one keeps the mode it has. Applies to the directory this task names and to every parent it creates on the way there, as ansible.builtin.file does, so a private tree is private all the way down; a parent that already existed keeps its mode. |
| `owner` | `string` | no | - | The user name that should own the directory. A name rather than a numeric id, because a name is what the device reports back and therefore the only form this method can compare against. Given to every parent this task creates too, as mode is. Left unset, ownership is not touched. |
| `group` | `string` | no | - | The group name that should own the directory, under the same rule as owner: a name, not a numeric id, and given to every parent this task creates. Left unset, the group is not touched. |
| `insecure_skip_host_key_verify` | `bool` | no | `false` | Skip SSH host key verification for this task. This removes protection against a machine in the middle answering for the device, so set it only for a target you have decided does not need it. |

## Returns

| Name | Type | Returned | Description |
| --- | --- | --- | --- |
| `path` | `string` | always | The directory this task acted on. |
| `diff` | `dict` | always | What the path looked like before this task and what it looks like after, each holding exists, kind, mode, owner and group. Recorded even on a run that changed nothing, because "it was already like this" is what tells a later rollback to do nothing. |

## Undoing this

**Can be undone.** A run that changes something records the instruction that reverses it, as an `inverse` stat holding the method to call and the parameters to call it with, resolved from the state this run actually found. A run that changed nothing records no instruction, which is how it says that undoing it means doing nothing.

A run that created the directory emits a file.remove naming it. That removal is deliberately not recursive, so it refuses rather than destroying anything put inside the directory after this task ran. A run that found the directory already there and only changed its mode, owner or group emits a file.permissions carrying the old ones, which puts the attributes back and never removes the directory. A converged run emits nothing at all. Parents a run created on the way to the directory are not removed by that undo.

Note that no rollback engine reads this yet. What exists today is the recording, which has to happen during the forward run because the values an undo needs are gone once the change is applied.

## See also

- `file.remove`
- `file.permissions`

## Examples

Create a directory tree:

```yaml
- name: Make sure the release directory is there
  file.directory:
    path: /opt/app/releases/current
```

Create it with a mode:

```yaml
- name: Make a private directory
  file.directory:
    path: /var/lib/app/secrets
    mode: "0700"
```

Hand it to a service account:

```yaml
- name: Own the data directory
  file.directory:
    path: /var/lib/app/data
    owner: app
    group: app
    mode: "0750"
```

