---
status: beta
---

# archive.extract

Extracts an archive (tar or tar.gz) on the target.

Extracts src into dest, where src is an archive already present on the target -- this is community.general.unarchive with remote_src implied true always; nothing in this platform can transfer a file from wherever a runbook runs to the target (file.copy explicitly refuses that too), so a src living anywhere else is out of scope. Compression is auto-detected by tar itself, so there is no format parameter here the way archive.create has one. Idempotency is opt-in: naming creates skips extraction when that path is already there, and naming none means every run extracts again, the same honesty exec.command already has for a command with no built-in idempotency of its own. A check reads what a real run reads and extracts nothing. A src missing when a check runs, or a dest that is there but is not a directory, makes the call unchecked rather than failed, since an earlier task in the same run may be what fixes it.

## Attributes

|  |  |
| --- | --- |
| Capabilities | `POSIXFileSystemCapable` |
| Transports | `ssh` |
| Requires elevation | no |
| Check mode | Supported: reports what it would change and changes nothing |
| Engine version | `>=0.2.0` |

## Parameters

| Name | Type | Required | Default | Description |
| --- | --- | --- | --- | --- |
| `src` | `string` | yes | - | The archive on the target to extract. Never a path on the machine running this task. |
| `dest` | `string` | yes | - | The directory to extract into, created if it does not exist. |
| `remove` | `bool` | no | `false` | Delete src once it has been extracted. |
| `creates` | `string` | no | - | A path whose existence means extraction already happened; when it is already there, this task reports no change and does not extract again. Left unset, every run extracts. |
| `insecure_skip_host_key_verify` | `bool` | no | `false` | Skip SSH host key verification for this task. This removes protection against a machine in the middle answering for the device, so set it only for a target you have decided does not need it. |

## Returns

| Name | Type | Returned | Description |
| --- | --- | --- | --- |
| `dest` | `string` | always | The directory this task extracted into. |
| `diff` | `dict` | always | What dest looked like before this task and after it (exists, kind, mode, owner, group, size, mtime). Recorded even on a run that skipped extraction because creates was already there. |

## Undoing this

**Cannot be undone.** This method never records a reversing instruction, so a rollback reaching a task that used it stops rather than guessing.

Extracting an archive can create any number of files and directories under dest, and this method does not enumerate them as it goes. A safe inverse would have to delete exactly what was created and nothing dest already held, which needs that enumeration; without it, the only honest choices are refusing to record an inverse or recording one that might delete more than this run added. This method refuses, the same call pkg.upgrade makes for an installed build a repository may no longer offer.

Note that no rollback engine reads this yet. What exists today is the recording, which has to happen during the forward run because the values an undo needs are gone once the change is applied.

## See also

- `archive.create`

## Examples

Extract a build already staged on the target:

```yaml
- name: Unpack the release
  archive.extract:
    src: /tmp/release.tar.gz
    dest: /opt/app
```

Extract only once:

```yaml
- name: Unpack the SDK if it is not already there
  archive.extract:
    src: /tmp/sdk.tar.gz
    dest: /opt/sdk
    creates: /opt/sdk/bin/sdk
```

