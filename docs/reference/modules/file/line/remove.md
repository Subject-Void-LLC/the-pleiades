---
status: beta
---

# file.line.remove

Ensures no line matching a pattern remains in a file.

Makes sure no line matching a pattern remains in a text file, which is ansible.builtin.lineinfile with state: absent. The whole file is read, the surviving lines are worked out locally, and the file is written back only when the bytes really differ, so a run that finds nothing to remove sends no write at all. Every matching line goes, not only the first. A file that is not there is already in the wanted state, so the task reports no change instead of failing, which is what lets one runbook strip a setting from a fleet where not every host has the file. Three differences from Ansible are deliberate: setting both regexp and line is refused rather than silently preferring regexp, insertafter and insertbefore are refused rather than accepted and ignored, and a file that ends without a newline keeps ending without one. Patterns are Go's RE2, which has no backreferences and no lookaround; search_string, backup and validate are not implemented. Lines are split on the newline byte alone, so a file with Windows endings carries its carriage return as part of each line's text and a pattern meant to match one has to say so.

## Attributes

|  |  |
| --- | --- |
| Capabilities | `POSIXFileSystemCapable` |
| Transports | `ssh` |
| Requires elevation | no |
| Check mode | Supported: reports what it would change and changes nothing |
| Engine version | `>=1.0.0` |

## Parameters

| Name | Type | Required | Default | Description |
| --- | --- | --- | --- | --- |
| `path` | `string` | yes | - | The text file to edit. A file that is not there is reported as no change rather than as an error, since it holds no lines to remove. A symbolic link is refused rather than followed, because writing replaces the path and would leave a regular file where the link was. |
| `regexp` | `string` | no | - | Remove every line this pattern matches. Matched anywhere in a line unless anchored with ^ or $. Cannot be combined with line, and one of the two is required. |
| `line` | `string` | no | - | Remove every line whose text is exactly this. It may not contain a newline, since a line is matched whole. Cannot be combined with regexp, and one of the two is required. |
| `insecure_skip_host_key_verify` | `bool` | no | `false` | Skip SSH host key verification for this task. This removes protection against a machine in the middle answering for the device, so set it only for a target you have decided does not need it. |

## Returns

| Name | Type | Returned | Description |
| --- | --- | --- | --- |
| `path` | `string` | always | The file this task acted on. |
| `found` | `int` | always | How many lines were removed. Zero when the file held none matching, and zero when the file was not there at all. |
| `msg` | `string` | always | What happened, in lineinfile's own words: "3 line(s) removed", "file not present", or empty when the file held nothing to remove. |
| `diff` | `dict` | always | The file's state before and after, each holding exists and kind, plus mode, owner, group, size, mtime and the file's whole text when it is there. Recorded even when nothing changed, in which case the two halves are identical. |

## Undoing this

**Can be undone.** A run that changes something records the instruction that reverses it, as an `inverse` stat holding the method to call and the parameters to call it with, resolved from the state this run actually found. A run that changed nothing records no instruction, which is how it says that undoing it means doing nothing.

A run that removed something emits a file.copy carrying the whole text the file held beforehand, with its mode, owner and group. The whole file, because this can take several lines from several places and putting them back with file.line.set would pile them all at the end instead. That makes the record as large as the file. A run that found nothing to remove emits nothing, and so does a run against a file that was not there. The modification time is not restored, and file.copy itself is declared rather than implemented today.

Note that no rollback engine reads this yet. What exists today is the recording, which has to happen during the forward run because the values an undo needs are gone once the change is applied.

## See also

- `file.line.set`
- `file.block.remove`
- `file.remove`

## Examples

Drop a package source by pattern:

```yaml
- name: Drop the retired package mirror
  file.line.remove:
    path: /etc/apt/sources.list
    regexp: '^deb .*mirror\.old\.example\.com'
```

Drop one exact entry:

```yaml
- name: Remove the decommissioned host entry
  file.line.remove:
    path: /etc/hosts
    line: 10.0.4.9 registry.internal
```

Act only when something was really removed:

```yaml
- name: Strip every commented out override
  file.line.remove:
    path: /etc/app/app.conf
    regexp: '^#\s*override'
  register: overrides

- name: Reload the service that read them
  exec.command:
    cmd: systemctl reload app
  when_cel: overrides.found > 0
```

