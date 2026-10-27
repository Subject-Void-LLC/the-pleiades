---
status: beta
---

# file.template

Renders a template with the run's variables and results and writes the result to a file on the target, only when the bytes there differ.

Renders a template and writes the result to a path on the device exactly as file.copy writes content: a SHA-256 of the rendered bytes is compared with one the device computes of what is already there, so a run that finds the same bytes sends no write and reports no change; a write goes to a temporary file in the destination's directory with the final mode, owner and group and is renamed over it; an existing file keeps its mode, owner and group unless the task names them; a new file without a mode gets 0600; anything at dest that is not a regular file is refused. The template is rendered by the platform's one template engine on the machine running the work, never on the device, with the same syntax as every other parameter: {{ vars.x }} reads a run variable, {{ result.name }} and {{ nodes.name }} read earlier tasks' registered results, and {{ device.name }} the device the task runs on. A variable or result that does not exist is an error, not an empty string, so a misspelled name never writes a half-filled file. The template is either content, written in the runbook, or src, a file in the project directory (relative, inside it, through no symbolic link, never .pleiades, a regular file of at most 64 MiB) that the command line reads; a Walk-tier Runner holds no project, so there src is refused and content is the way. The rendered text can hold a value a bound credential or a variable carried: it is sent to the device and is never written to the journal, the diff or the result, which hold the file's checksum and size only. In check mode it makes the same reads and refusals and predicts the write and its diff without sending one. Writing a file this way is undone like file.copy's: a created file is removed, and an overwritten file gets its former mode, owner and group back, while its former content is not restored.

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
| `content` | `string` | no | - | The template, written in the runbook (set content or src, not both). It renders like any parameter holding {{ }}; text without one is written verbatim. Nothing is appended, so a file that should end in a newline needs one in the value. |
| `src` | `string` | no | - | A template file in the project directory, as a path relative to it that is written out in the runbook, not computed: it must stay inside the project, pass through no symbolic link, not be in .pleiades, and be a regular file of at most 64 MiB. The command line reads and renders it; a Walk-tier Runner cannot see the project, so use content there. Set content or src, not both. |
| `mode` | `string` | no | - | The permission bits as one to four octal digits, quoted, for example "0644". A symbolic mode such as u+x is refused. Left unset, an existing file keeps its mode and a newly created one gets 0600. |
| `owner` | `string` | no | - | The user name to give the file. A numeric id is refused. Left unset, an existing file keeps its owner and a new one belongs to the connecting account. |
| `group` | `string` | no | - | The group name to give the file, refused as a numeric id for the same reason owner is. |
| `insecure_skip_host_key_verify` | `bool` | no | `false` | Skip SSH host key verification for this task. This removes protection against a machine in the middle answering for the device, so set it only for a target you have decided does not need it. |

## Returns

| Name | Type | Returned | Description |
| --- | --- | --- | --- |
| `dest` | `string` | always | The path this task wrote. |
| `checksum` | `string` | always | The SHA-256 of the file's contents in hex, read back from the device after any write. |
| `mode` | `string` | always | The permission bits the file carries now, as four octal digits. |
| `owner` | `string` | always | The user name owning the file now. |
| `group` | `string` | always | The group owning the file now. |
| `size` | `int` | always | The file's size in bytes now. |
| `diff` | `dict` | always | The before and after state of the path, each holding its kind, mode, owner, group, size, modification time and the checksum of its contents, never the content itself. |
| `warnings` | `list of string` | when the write was not atomic | Says the new bytes were copied into the existing file rather than renamed over it, because a new file could not be given its owner and group. |
| `inverse` | `dict` | when something changed | The concrete task that undoes this run: a file.remove when this run created the file, or a file.permissions restoring the attributes it found when the file already existed. Absent from a run that changed nothing. |

## Undoing this

**Can be undone.** A run that changes something records the instruction that reverses it, as an `inverse` stat holding the method to call and the parameters to call it with, resolved from the state this run actually found. A run that changed nothing records no instruction, which is how it says that undoing it means doing nothing.

A run that created the file emits a file.remove that deletes it again, which is a complete undo. A run that OVERWROTE an existing file emits a file.permissions restoring the mode, owner and group it found, and the previous CONTENT is not restored by it: those bytes are not read before the write and are not journaled anywhere, so once the new content lands the old content is gone. The emitted instruction says so in its own description rather than leaving a rollback to discover it. A run that changed nothing emits nothing.

Note that no rollback engine reads this yet. What exists today is the recording, which has to happen during the forward run because the values an undo needs are gone once the change is applied.

## See also

- `file.copy`
- `file.touch`
- `file.permissions`
- `file.remove`

## Examples

Render a configuration file from a template in the project:

```yaml
- name: Install the NTP configuration
  file.template:
    src: templates/chrony.conf
    dest: /etc/chrony/chrony.conf
    mode: "0644"
```

Render a short template written in the runbook:

```yaml
- name: Write the node label
  file.template:
    dest: /etc/node-label
    content: "role={{ vars.role }}\nhost={{ device.name }}\n"
    mode: "0644"
```

