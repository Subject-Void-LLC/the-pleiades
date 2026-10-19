---
status: beta
---

# win.archive.extract

Extracts a .zip, .tar or .tar.gz already on a Windows host into a folder there, over WinRM.

Extracts src into dest, making dest when it does not exist, with the tar.exe Windows ships, which reads zip as well as tar and refuses an entry whose path is absolute or climbs out with "..". This is community.windows.win_unzip, and archive.extract for a Windows host: src must already be on the host. Idempotency is opt-in, as archive.extract's: naming creates skips extraction when that path is already there; naming none means every run extracts again and reports a change. remove deletes src once it is extracted. Every path is absolute and held to a pattern with no quote, wildcard or ".." before WinRM is reached. A check reads creates, src and dest and extracts nothing; a src not there yet, or a dest that is not a folder, makes the call unchecked rather than failed, since an earlier task may change either.

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
| `src` | `string` | yes | - | The archive on the host, an absolute path ending .zip, .tar, .tar.gz or .tgz. |
| `dest` | `string` | yes | - | The folder to extract into, an absolute path or a drive's root (C:\), made when it does not exist. |
| `creates` | `string` | no | - | An absolute path whose existence means the extraction already happened; when it is there, nothing is extracted and the task reports no change. |
| `remove` | `bool` | no | `false` | Delete src once it has been extracted. |
| `timeout` | `int` | no | `600` | How many seconds the extraction may take. |

## Returns

| Name | Type | Returned | Description |
| --- | --- | --- | --- |
| `dest` | `string` | always | The folder extracted into. |
| `extracted` | `bool` | always | Whether this task extracted (or, in a check, would). |
| `diff` | `dict` | always | Whether dest existed before this task and after it. |

## Undoing this

**Cannot be undone.** This method never records a reversing instruction, so a rollback reaching a task that used it stops rather than guessing.

Extracting can write any number of files under dest, some replacing files dest already held, and this method does not list them as it goes; an undo would have to delete exactly what was added and restore what was replaced, which it cannot know, as archive.extract cannot.

Note that no rollback engine reads this yet. What exists today is the recording, which has to happen during the forward run because the values an undo needs are gone once the change is applied.

## See also

- `win.file.download`
- `archive.extract`

## Examples

Unpack Go, once:

```yaml
- name: Unpack Go into C:\go
  win.archive.extract:
    src: C:\Windows\Temp\go1.26.9.windows-amd64.zip
    dest: C:\
    creates: C:\go\bin\go.exe
```

