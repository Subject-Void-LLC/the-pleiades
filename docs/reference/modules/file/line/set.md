---
status: beta
---

# file.line.set

Ensures one line matching a pattern is present in a file, replacing or appending it.

Makes sure one line is present in a text file, which is ansible.builtin.lineinfile with state: present. The whole file is read, the new text is worked out locally, and the file is written back only when the bytes really differ, so a run that finds the line already in place sends no write at all. When regexp is set, the last line it matches is replaced; when it is not set, a line already exactly equal to line means the task is done. A line that has to be added goes at the end of the file unless insertafter or insertbefore names a pattern to place it against. The file has to exist already, since Ansible's create is not implemented, so file.touch or file.copy is what makes one. Two differences from Ansible are deliberate and worth knowing. A regexp that does not match the line being placed is refused, because such a task adds the line again on every single run and never settles. And a file that ends without a newline keeps ending without one, where Ansible would add it. Patterns are Go's RE2, which has no backreferences and no lookaround; backrefs, search_string, firstmatch, create, backup and validate are not implemented. Lines are split on the newline byte alone, so a file with Windows endings carries its carriage return as part of each line's text and a pattern meant to match one has to say so.

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
| `path` | `string` | yes | - | The text file to edit. It has to exist already and it has to be a regular file. A symbolic link is refused rather than followed, because writing replaces the path and would leave a regular file where the link was. |
| `line` | `string` | yes | - | The exact text of the line to make sure is present, written verbatim, so leading whitespace is part of the line. It may not contain a newline: this method places one line, and file.block.set is what places several. |
| `regexp` | `string` | no | - | A pattern naming the line to replace. The last line it matches is replaced by line, and if nothing matches then line is added. It has to match line itself, and is refused when it does not, because otherwise no run would ever find the line it just added and the file would grow forever. Matched anywhere in a line unless anchored with ^ or $. |
| `insertafter` | `string` | no | - | Where to put the line when it has to be added: a pattern, and the line goes after the last line matching it. The value EOF means the end of the file, which is also what happens when this is left out and when the pattern matches nothing. Cannot be combined with insertbefore. |
| `insertbefore` | `string` | no | - | Where to put the line when it has to be added: a pattern, and the line goes before the last line matching it. The value BOF means the start of the file. A pattern matching nothing puts the line at the end, which is Ansible's own behavior. Cannot be combined with insertafter. |
| `insecure_skip_host_key_verify` | `bool` | no | `false` | Skip SSH host key verification for this task. This removes protection against a machine in the middle answering for the device, so set it only for a target you have decided does not need it. |

## Returns

| Name | Type | Returned | Description |
| --- | --- | --- | --- |
| `path` | `string` | always | The file this task edited. |
| `msg` | `string` | always | What happened, in lineinfile's own words: "line added", "line replaced", or empty when the file was already correct. |
| `diff` | `dict` | always | The file's state before and after, each holding exists, kind, mode, owner, group, size, mtime and the file's whole text. Recorded even when nothing changed, in which case the two halves are identical. |

## Undoing this

**Can be undone.** A run that changes something records the instruction that reverses it, as an `inverse` stat holding the method to call and the parameters to call it with, resolved from the state this run actually found. A run that changed nothing records no instruction, which is how it says that undoing it means doing nothing.

A run that changed the file emits a file.copy carrying the whole text the file held beforehand, with its mode, owner and group. The whole file, because a line edit is not reliably undone by another line edit: a replacement has already thrown the old line away, and an anchored insert cannot be located again. That makes the record as large as the file. A converged run emits nothing, since undoing a change that was never made means doing nothing, and a run that fails partway emits nothing either. The modification time is not restored, and file.copy itself is declared rather than implemented today.

Note that no rollback engine reads this yet. What exists today is the recording, which has to happen during the forward run because the values an undo needs are gone once the change is applied.

## See also

- `file.line.remove`
- `file.block.set`
- `file.copy`

## Examples

Correct a setting whether or not it is commented out:

```yaml
- name: Turn off root login over SSH
  file.line.set:
    path: /etc/ssh/sshd_config
    regexp: '^#?PermitRootLogin'
    line: PermitRootLogin no
```

Append an entry that is either there or not:

```yaml
- name: Add the internal registry to the hosts file
  file.line.set:
    path: /etc/hosts
    line: 10.0.4.12 registry.internal
```

Place a line against an anchor:

```yaml
- name: Put the include ahead of the defaults section
  file.line.set:
    path: /etc/app/app.conf
    line: include /etc/app/conf.d/all.conf
    insertbefore: '^\[defaults\]'
```

