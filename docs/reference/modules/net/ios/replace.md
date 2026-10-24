---
status: beta
---

# net.ios.replace

Replaces a Cisco IOS device's running configuration with a checkpoint file on the device.

Runs IOS's configure replace with a checkpoint file the device holds (net.ios.config writes one before each change and records this method as its undo), so the device itself puts back exactly the configuration the checkpoint holds: lines a change added are removed and lines it replaced or removed come back, with no inverse line written by anyone. When expect names a second checkpoint (the one net.ios.config writes after its change), the device must still match it, by the device's own show archive config differences, or this refuses before replacing anything, unless force is true: configure replace returns the whole configuration to the checkpoint, so a change someone made since would be undone with it. After the replace, the device must report no differences from the checkpoint, or the task fails; a line in IOS's "% " form the replace prints fails it only then, and when the device matches it is reported as a warning, masked. The checkpoint files net.ios.config made (named pleiades-<id>.cfg) are deleted afterwards unless keep is true; any other file, such as nvram:startup-config or a configuration kept on flash:, is never deleted. A check reports what the replace would change, from the same comparison, and changes nothing. The configuration lines this reports are masked as net.batfish.check masks a candidate: a password, secret or key is never shown.

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
| `checkpoint` | `string` | yes | - | The checkpoint to return to, a file on the device such as unix:pleiades-3f2a.cfg or flash:pleiades-3f2a.cfg. |
| `expect` | `string` | no | - | A checkpoint the running configuration must still match before anything is replaced, refusing otherwise: the configuration as the change left it. |
| `force` | `bool` | no | `false` | Replace even when the running configuration no longer matches expect, undoing whatever changed since along with the change. |
| `keep` | `bool` | no | `false` | Keep the checkpoint files net.ios.config made on the device after a replace that leaves no differences, rather than deleting them. A file it did not make is never deleted. |
| `insecure_skip_host_key_verify` | `bool` | no | `false` | Skip SSH host key verification for this task. This removes protection against a machine in the middle answering for the device, so set it only for a target you have decided does not need it. |
| `insecure_telnet` | `bool` | no | `false` | Reach a device that has no SSH over bare Telnet, answering its login with the task's credential. Telnet carries everything, the login included, in cleartext, so set this only for a device on a network you trust. |

## Returns

| Name | Type | Returned | Description |
| --- | --- | --- | --- |
| `differences` | `list of string` | always | The device's own contextual differences between the running configuration and the checkpoint before the replace, masked: what the replace changes, or would change in a check. |

## Undoing this

**Cannot be undone.** This method never records a reversing instruction, so a rollback reaching a task that used it stops rather than guessing.

It is the undo net.ios.config records; what it removes is the change that checkpoint was kept before, which running that change again puts back.

Note that no rollback engine reads this yet. What exists today is the recording, which has to happen during the forward run because the values an undo needs are gone once the change is applied.

## See also

- `net.ios.config`
- `net.ios.parse`

## Examples

Return a device to a checkpoint a change left:

```yaml
- name: Undo the loopback change
  net.ios.replace:
    checkpoint: unix:pleiades-3f2a.cfg
    expect: unix:pleiades-3f2a-after.cfg
```

