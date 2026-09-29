---
status: beta
---

# fs.unmount

Unmounts a filesystem on the target, and optionally removes it from fstab.

Makes sure path is not mounted, unmounting it if it is. This is close to ansible.builtin.mount with state=unmounted, split so that removing the fstab entry (see the persist parameter) is independent of unmounting: either can happen without the other. Mount state is read from findmnt before anything is sent, so a path already unmounted reports no change from the unmount itself.

## Attributes

|  |  |
| --- | --- |
| Capabilities | `LinuxCapable` |
| Transports | `ssh` |
| Requires elevation | yes |
| Runs | on or against the target device; acts on its target device |
| Check mode | Supported: reports what it would change and changes nothing |
| Engine version | `>=0.2.0` |

## Parameters

| Name | Type | Required | Default | Description |
| --- | --- | --- | --- | --- |
| `path` | `string` | yes | - | The mountpoint to unmount. |
| `persist` | `bool` | no | `true` | Also remove any matching entry from fstab, so it does not mount again on the next boot. |
| `fstab` | `string` | no | `/etc/fstab` | The fstab-format file to read and, if persist is true, write. |
| `insecure_skip_host_key_verify` | `bool` | no | `false` | Skip SSH host key verification for this task. This removes protection against a machine in the middle answering for the device, so set it only for a target you have decided does not need it. |

## Returns

| Name | Type | Returned | Description |
| --- | --- | --- | --- |
| `path` | `string` | always | The mountpoint this task acted on. |
| `diff` | `dict` | always | What findmnt and the fstab file reported about path before this task and after it, each holding mounted, source, fstype, options and persisted. After always reports mounted: false on a successful unmount. |

## Undoing this

**Can be undone.** A run that changes something records the instruction that reverses it, as an `inverse` stat holding the method to call and the parameters to call it with, resolved from the state this run actually found. A run that changed nothing records no instruction, which is how it says that undoing it means doing nothing.

A run that unmounted a mounted path emits an fs.mount pinned to the exact src, fstype and opts this run captured before unmounting, which is a real, restorable inverse. If this run also removed the path's fstab entry, the inverse asks fs.mount to persist it again. A run that found the path already unmounted and only removed a stale fstab entry emits no inverse: undoing that without also mounting something this task did not mount needs a capability this namespace does not expose. A run that found everything already absent emits nothing.

Note that no rollback engine reads this yet. What exists today is the recording, which has to happen during the forward run because the values an undo needs are gone once the change is applied.

## See also

- `fs.mount`

## Examples

Unmount and forget a volume:

```yaml
- name: Unmount the old data volume
  fs.unmount:
    path: /data
```

Unmount but leave the fstab entry:

```yaml
- name: Unmount temporarily for maintenance
  fs.unmount:
    path: /data
    persist: false
```

