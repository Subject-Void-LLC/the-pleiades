---
status: beta
---

# pleiades.builtin.project.write

Writes text a run produced, such as a router's running configuration, to a file in the project.

Writes content to path, a file in the project directory, so a value a task read (a registered result's running_config) can be kept, edited and used as a candidate: net.frr.config's src and net.batfish.check's src read such a file. The path is held to the rules a method reading the project is held to: relative, never leaving the project, never through a symbolic link, never into the project's .pleiades directory, and a regular file or none yet. A missing directory on the way is created inside the project. The file is written whole to a temporary file beside it and renamed over it, so it is never seen half written, and the task reports changed only when the bytes or the mode differ. A check reads the file and predicts the same answer. It runs on the command line only (pleiades run and adhoc), which holds the project; a Walk-tier Runner refuses it. It acts on no device and needs no hosts. A configuration read by net.frr.facts or net.ios.* is masked: each secret in it is a pleiades-masked- placeholder, and net.frr.config refuses a candidate holding one, so put each secret back before applying the file. It cannot be undone by a rollback: the file's earlier content is never journaled, since the project is the author's working copy, kept under version control.

## Attributes

|  |  |
| --- | --- |
| Capabilities | - |
| Transports | - |
| Requires elevation | no |
| Runs | in the host process (the CLI or a Runner); acts on no device, so a task skips the runbook's hosts: and runs once |
| Check mode | Supported: reports what it would change and changes nothing |
| Engine version | `>=0.2.0` |

## Parameters

| Name | Type | Required | Default | Description |
| --- | --- | --- | --- | --- |
| `path` | `string` | yes | - | The file to write, a slash-separated path relative to the project directory, such as configs/r3.running.frr.conf. |
| `content` | `string` | yes | - | The text to write, usually rendered from a registered result, such as "{{ result.r3.running_config }}". An empty string writes an empty file. |
| `mode` | `string` | no | `0600` | The file's permission bits as a quoted octal string, such as "0640". Only the owner can read the default, since a configuration can hold more than its masked secrets. |

## Returns

| Name | Type | Returned | Description |
| --- | --- | --- | --- |
| `path` | `string` | always | The file written, as given. |
| `sha256` | `string` | always | The SHA-256 of the content, in hex: the digest net.batfish.check reports for the same bytes. |
| `size` | `int` | always | The content's length in bytes. |

## Undoing this

**Cannot be undone.** This method never records a reversing instruction, so a rollback reaching a task that used it stops rather than guessing.

It overwrites a file in the project, whose earlier content is never journaled: the journal keeps names and counts, never file content. The project is the author's own working copy, kept under version control, which is where an earlier version comes back from.

Note that no rollback engine reads this yet. What exists today is the recording, which has to happen during the forward run because the values an undo needs are gone once the change is applied.

## See also

- `net.frr.facts`
- `net.frr.config`
- `net.batfish.check`
- `file.copy`

## Examples

Keep r3's running configuration as a candidate file:

```yaml
- name: Read r3 as it runs
  net.frr.facts:
    target: r3
    include_config: true
  register: r3
- name: Keep it in the project, to edit into a candidate
  pleiades.builtin.project.write:
    path: configs/r3.running.frr.conf
    content: "{{ result.r3.running_config }}"
```

