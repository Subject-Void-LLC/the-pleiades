---
status: beta
---

# file.copy

Writes content, or a file from the project, to a file on the target, only when the bytes there differ.

Writes content to a path on the device, comparing a SHA-256 of the content against a SHA-256 the device computes of what is already there, so a run that finds the same bytes sends no write at all and reports no change. A write that does happen goes to a temporary file in the destination's own directory, which is given the mode, owner and group the file is to have and only then renamed over it, so a reader of the path sees either the whole old file or the whole new one, and a write that cannot be finished leaves the destination as it was. The bytes are content, or src: a file in the project directory, read by the command line (pleiades run and adhoc), which holds the project. A Walk-tier Runner is a per-task container that cannot see the project, so there src is refused, naming content as the way to send the bytes. src is a path relative to the project directory that stays inside it, passes through no symbolic link, never reaches the project's .pleiades directory (its credentials and master key), and names a regular file of at most 64 MiB; it is read before anything is sent. Anything at dest that is not a regular file is refused rather than replaced. An existing file keeps the mode, owner and group it had unless the task names them. When the connecting account may not give a new file that owner or group (a login that is not root replacing a file another account owns), the bytes are copied into the existing file instead, which keeps everything it was, and the run warns that the write was not atomic; a file the account may not write is refused before anything changes. Ansible's atomic move would leave such a file owned by the connecting account: this method never changes an owner or group the task did not name. An owner or group the task names that the connecting account may not give is refused before anything is written, and a check refuses the same. A file this method creates and the task gave no mode gets 0600, which is a fixed, documentable default rather than Ansible's umask-derived one.

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
| `dest` | `string` | yes | - | The path to write on the device. Its parent directory must already exist: this method writes a file and never creates the directories above it, which is file.directory's job. |
| `content` | `string` | no | - | The bytes to write, used verbatim (set content or src, not both): nothing is appended, so a file that should end in a newline needs one in the value. An empty string is a real request and truncates the file to nothing. Quote a value YAML would otherwise read as a number or a boolean, since a non-string is refused rather than rendered. |
| `src` | `string` | no | - | A file in the project directory whose bytes the file should hold, as a path relative to that directory: it must stay inside it, pass through no symbolic link, not be in .pleiades, and be a regular file of at most 64 MiB. Read by the command line, which holds the project; refused on a Walk-tier Runner, which cannot see it, where content is the way. Set content or src, not both. |
| `mode` | `string` | no | - | The permission bits as one to four octal digits, quoted, for example "0644". Quote it: an unquoted 0644 is a number in YAML, not text. A symbolic mode such as u+x is refused, because it cannot be compared against the mode the device reports and the task would then report changed on every run. Left unset, an existing file keeps its mode and a newly created one gets 0600. |
| `owner` | `string` | no | - | The user name to give the file. A numeric id is refused: chown reads an all-digit argument as an id while the device reports names, so the two could never compare equal and the task would report changed forever. Left unset, an existing file keeps its owner and a new one belongs to the connecting account. |
| `group` | `string` | no | - | The group name to give the file, refused as a numeric id for the same reason owner is. |
| `insecure_skip_host_key_verify` | `bool` | no | `false` | Skip SSH host key verification for this task. This removes protection against a machine in the middle answering for the device, so set it only for a target you have decided does not need it. |

## Returns

| Name | Type | Returned | Description |
| --- | --- | --- | --- |
| `dest` | `string` | always | The path this task wrote. |
| `checksum` | `string` | always | The SHA-256 of the file's contents in hex, read back from the device after any write rather than computed from what was asked for. Ansible's copy reports a SHA-1 here; this reports SHA-256, which is the one hash this platform computes. |
| `mode` | `string` | always | The permission bits the file carries now, as four octal digits. |
| `owner` | `string` | always | The user name owning the file now. |
| `group` | `string` | always | The group owning the file now. |
| `size` | `int` | always | The file's size in bytes now. |
| `diff` | `dict` | always | The before and after state of the path, each holding its kind, mode, owner, group, size, modification time and the checksum of its contents. Recorded even when nothing changed, in which case the two halves are identical. |
| `warnings` | `list of string` | when the write was not atomic | Says the new bytes were copied into the existing file rather than renamed over it, because a new file could not be given its owner and group; a reader at that moment could have seen the file part written. |
| `inverse` | `dict` | when something changed | The concrete task that undoes this run: a file.remove when this run created the file, or a file.permissions restoring the attributes it found when the file already existed. Absent from a run that changed nothing, which is how the record says undoing it means doing nothing. When the previous content was overwritten, the description says plainly that those bytes are not restored. |

## Undoing this

**Can be undone.** A run that changes something records the instruction that reverses it, as an `inverse` stat holding the method to call and the parameters to call it with, resolved from the state this run actually found. A run that changed nothing records no instruction, which is how it says that undoing it means doing nothing.

A run that created the file emits a file.remove that deletes it again, which is a complete undo. A run that OVERWROTE an existing file emits a file.permissions restoring the mode, owner and group it found, and the previous CONTENT is not restored by it: those bytes are not read before the write and are not journaled anywhere, so once the new content lands the old content is gone. The emitted instruction says so in its own description rather than leaving a rollback to discover it. A run that changed nothing emits nothing.

Note that no rollback engine reads this yet. What exists today is the recording, which has to happen during the forward run because the values an undo needs are gone once the change is applied.

## See also

- `file.template`
- `file.touch`
- `file.permissions`
- `file.remove`

## Examples

Write a configuration file:

```yaml
- name: Install the agent configuration
  file.copy:
    dest: /etc/pleiades/agent.yaml
    content: |
      endpoint: https://controller.internal:8443
      verify: true
    mode: "0644"
```

Write a private file:

```yaml
- name: Drop the deploy token
  file.copy:
    dest: /etc/pleiades/token
    content: "{{ vars.deploy_token }}"
    mode: "0600"
    owner: pleiades
    group: pleiades
```

Truncate a file to nothing:

```yaml
- name: Empty the local override file
  file.copy:
    dest: /etc/app/local.conf
    content: ""
```

