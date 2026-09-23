---
status: beta
---

# archive.create

Creates an archive (tar or tar.gz) from files on the target.

Creates path as a tar archive of src, entirely from files already on the target -- this is community.general.archive without a zip option. Idempotency here is existence-only: a run finding path already there reports no change and reads none of src, the same way file.copy's checksum comparison decides on bytes rather than a name but simpler still, since this does not even open the archive to compare. remove, when true, deletes src once the archive has been written; the archive itself is not touched a second time to verify it. A check reads what a real run reads and runs no tar. A src, or the directory the archive would go in, that is missing when a check runs makes the call unchecked rather than failed, since an earlier task in the same run may be what creates it.

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
| `path` | `string` | yes | - | The archive file to create. |
| `src` | `list` | yes | - | The paths on the target to include, at least one. |
| `format` | `string` | no | `tar.gz` | The archive format. tgz is accepted as a synonym for tar.gz. |
| `remove` | `bool` | no | `false` | Delete src once the archive has been written. |
| `insecure_skip_host_key_verify` | `bool` | no | `false` | Skip SSH host key verification for this task. This removes protection against a machine in the middle answering for the device, so set it only for a target you have decided does not need it. |

## Returns

| Name | Type | Returned | Description |
| --- | --- | --- | --- |
| `path` | `string` | always | The archive this task acted on. |
| `diff` | `dict` | always | What path looked like before this task and after it (exists, kind, mode, owner, group, size, mtime). Recorded even on a run that changed nothing. |

## Undoing this

**Can be undone.** A run that changes something records the instruction that reverses it, as an `inverse` stat holding the method to call and the parameters to call it with, resolved from the state this run actually found. A run that changed nothing records no instruction, which is how it says that undoing it means doing nothing.

A run that created an absent archive emits a file.remove naming it, which is a real, restorable inverse for the archive itself. A run that found the archive already present emits nothing. What no inverse here can restore is whatever remove=true deleted once the archive was written: those source paths are gone, and the archive holding their bytes is exactly what the inverse would just have deleted.

Note that no rollback engine reads this yet. What exists today is the recording, which has to happen during the forward run because the values an undo needs are gone once the change is applied.

## See also

- `archive.extract`
- `file.remove`

## Examples

Archive a directory:

```yaml
- name: Archive the release build
  archive.create:
    path: /tmp/release.tar.gz
    src:
      - /opt/app/dist
```

Archive and remove the originals:

```yaml
- name: Archive old logs and delete them
  archive.create:
    path: /var/backups/logs-2026-08.tar.gz
    src:
      - /var/log/app/2026-08
    remove: true
```

