---
status: beta
---

# fs.mount

Mounts a filesystem on the target, and optionally persists it to fstab.

Makes sure path is mounted from src, creating the mount if it is not already there. This is close to ansible.builtin.mount with state=mounted, split so that persisting to fstab (see the persist parameter) is independent of mounting: either can be true without the other. Mount state is read from findmnt before anything is sent, so a path already mounted from src with a matching fstype reports no change; a path already mounted from a different src or fstype is refused rather than silently remounted, since that is not something this method can do without first unmounting it. opts is compared only when the task actually names it: a path already mounted with different options than an unspecified opts is left alone rather than treated as drift.

## Attributes

|  |  |
| --- | --- |
| Capabilities | `LinuxCapable` |
| Transports | `ssh` |
| Requires elevation | yes |
| Engine version | `>=1.0.0` |

## Parameters

| Name | Type | Required | Default | Description |
| --- | --- | --- | --- | --- |
| `path` | `string` | yes | - | The mountpoint to mount onto. |
| `src` | `string` | yes | - | The device, share or filesystem source to mount. |
| `fstype` | `string` | yes | - | The filesystem type, as mount's own -t takes it (e.g. ext4, nfs, xfs). |
| `opts` | `string` | no | - | Mount options, as mount's own -o takes them. Defaults to "defaults" for a fresh mount and a fresh fstab entry; an existing mount or fstab entry is only compared or rewritten against this when the task actually sets it. |
| `persist` | `bool` | no | `true` | Also make sure path has a matching entry in fstab, so it mounts again on the next boot. |
| `fstab` | `string` | no | `/etc/fstab` | The fstab-format file to read and, if persist is true, write. |
| `insecure_skip_host_key_verify` | `bool` | no | `false` | Skip SSH host key verification for this task. This removes protection against a machine in the middle answering for the device, so set it only for a target you have decided does not need it. |

## Returns

| Name | Type | Returned | Description |
| --- | --- | --- | --- |
| `path` | `string` | always | The mountpoint this task acted on. |
| `diff` | `dict` | always | What findmnt and the fstab file reported about path before this task and after it, each holding mounted, source, fstype, options and persisted. Recorded even on a run that changed nothing. |

## Undoing this

**Can be undone.** A run that changes something records the instruction that reverses it, as an `inverse` stat holding the method to call and the parameters to call it with, resolved from the state this run actually found. A run that changed nothing records no instruction, which is how it says that undoing it means doing nothing.

A run that mounted an absent path emits an fs.unmount naming it, which also strips the fstab entry this run added, if any. A run that found the path already mounted and only added or updated its fstab entry emits no inverse: undoing that without also unmounting a mount this task did not create needs a capability this namespace does not expose. A run that found everything already as requested emits nothing.

Note that no rollback engine reads this yet. What exists today is the recording, which has to happen during the forward run because the values an undo needs are gone once the change is applied.

## See also

- `fs.unmount`

## Examples

Mount and persist a data volume:

```yaml
- name: Mount the data volume
  fs.mount:
    path: /data
    src: /dev/sdb1
    fstype: ext4
```

Mount without touching fstab:

```yaml
- name: Mount a scratch volume for this run only
  fs.mount:
    path: /mnt/scratch
    src: /dev/sdb2
    fstype: ext4
    persist: false
```

