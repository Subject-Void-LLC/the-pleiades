---
status: beta
---

# win.file.copy

Copies a file from the project to a Windows host over WinRM and keeps it only if its SHA-256 matches.

Makes sure path on the host holds the project file src, byte for byte. This is ansible.windows.win_copy for one file. The file is read from the project (at most 128 MiB; never from .pleiades, never through a link) and its SHA-256 worked out here. A file already at path with that digest reports no change and nothing is sent. Otherwise the file travels on the WinRM command's standard input as base64, never on a command line, into a file beside path, and is moved into place only once its digest matches; a mismatch leaves path as it was and fails. A file at path with another digest is replaced. Only the command line reads a project's files, so a Walk-tier Runner refuses src. A check reads path's digest and sends nothing.

## Attributes

|  |  |
| --- | --- |
| Capabilities | `WindowsShellCapable` |
| Transports | `winrm` |
| Requires elevation | no |
| Runs | on or against the target device; acts on its target device |
| Check mode | Supported: reports what it would change and changes nothing |
| Engine version | `>=0.2.0` |

## Parameters

| Name | Type | Required | Default | Description |
| --- | --- | --- | --- | --- |
| `src` | `string` | yes | - | The file to copy, a slash-separated path relative to the project directory, as dist/tree.tar.gz. |
| `path` | `string` | yes | - | The absolute path on the host to write, as C:\pleiades\tree.tar.gz. Its folder must exist. It may not hold a quote, a wildcard or a control character. |
| `timeout` | `int` | no | `1800` | How many seconds the copy may take. |

## Returns

| Name | Type | Returned | Description |
| --- | --- | --- | --- |
| `path` | `string` | always | The file this task made sure of. |
| `sha256` | `string` | always | Its SHA-256, lower case. |
| `size_bytes` | `int` | always | Its size. |
| `diff` | `dict` | always | The SHA-256 at path before this task and after it, empty where there was no file. |

## Undoing this

**Cannot be undone.** This method never records a reversing instruction, so a rollback reaching a task that used it stops rather than guessing.

A copy writes a file, and a file it replaces is not kept; nothing here removes a file yet, so neither a new file nor a replaced one can be undone.

Note that no rollback engine reads this yet. What exists today is the recording, which has to happen during the forward run because the values an undo needs are gone once the change is applied.

## See also

- `win.file.download`
- `file.copy`

## Examples

Send a build to a Windows host:

```yaml
- name: Copy the tools archive to the host
  win.file.copy:
    src: dist/tools.zip
    path: C:\ops\tools.zip
```

