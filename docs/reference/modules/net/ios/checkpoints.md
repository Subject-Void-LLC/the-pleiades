---
status: beta
---

# net.ios.checkpoints

Lists the checkpoints net.ios.config left on a Cisco IOS device, and with prune deletes the older ones.

Reads the file system net.ios.config keeps its checkpoints on (the device's default writable disk or flash, such as unix: or flash:) with dir, and reports each change's checkpoints, newest first: its id, the configuration before it and, for a change that completed, after it, with the time and size the device gives. Only files named as net.ios.config names them (pleiades-<12 hex digits>.cfg and -after.cfg) are ever listed or deleted, so a startup configuration or an image is never touched. Each checkpoint holds the whole configuration, its secrets included, and stays on the device until its change's undo runs. With prune, the checkpoints of every change but the newest keep, or of every change older than older_than, or both (a change goes when either says so), are deleted with delete /force; a check reports what would go. A deleted checkpoint's undo (the net.ios.replace net.ios.config recorded) then refuses, so prune only changes no rollback will need, such as those of a lab about to be deleted.

## Attributes

|  |  |
| --- | --- |
| Capabilities | `CiscoIOSCapable` |
| Transports | `ssh`, `telnet`, `console` |
| Requires elevation | no |
| Runs | on or against the target device; acts on its target device |
| Check mode | Supported: reports what it would change and changes nothing |
| Engine version | `>=0.2.0` |

## Parameters

| Name | Type | Required | Default | Description |
| --- | --- | --- | --- | --- |
| `prune` | `bool` | no | `false` | Delete the checkpoints keep and older_than select. Without it the method only lists. |
| `keep` | `int` | no | - | With prune, keep the checkpoints of this many of the newest changes and delete the rest; 0 deletes every one. |
| `older_than` | `string` | no | - | With prune, delete the checkpoints of every change older than this, a Go duration such as 72h, by the time the device gives its files. |
| `insecure_skip_host_key_verify` | `bool` | no | `false` | Skip SSH host key verification for this task. This removes protection against a machine in the middle answering for the device, so set it only for a target you have decided does not need it. |
| `insecure_telnet` | `bool` | no | `false` | Reach a device that has no SSH over bare Telnet, answering its login with the task's credential. Telnet carries everything, the login included, in cleartext, so set this only for a device on a network you trust. |

## Returns

| Name | Type | Returned | Description |
| --- | --- | --- | --- |
| `checkpoints` | `list of dict` | always | Each change's checkpoints, newest first: id, before, after (empty when the change did not complete), modified (RFC 3339, empty when the device's time did not read) and bytes. |
| `deleted` | `list of string` | when prune is true | The files deleted, or in a check the files that would be. |

## Undoing this

**Cannot be undone.** This method never records a reversing instruction, so a rollback reaching a task that used it stops rather than guessing.

A checkpoint deleted is gone, and the undo net.ios.config recorded for its change (net.ios.replace naming it) then refuses: prune only what no rollback will need. Listing changes nothing.

Note that no rollback engine reads this yet. What exists today is the recording, which has to happen during the forward run because the values an undo needs are gone once the change is applied.

## See also

- `net.ios.config`
- `net.ios.replace`

## Examples

Keep the checkpoints of the last five changes:

```yaml
- name: Prune old checkpoints
  net.ios.checkpoints:
    prune: true
    keep: 5
```

