---
status: beta
---

# net.ios.config

Applies configuration lines to a Cisco IOS device, keeping a checkpoint of the configuration before them so the change can be undone exactly.

Opens an interactive session over SSH (or over Telnet, with insecure_telnet), using netcli.IOS's own paging, configuration-mode and error conventions, and applies lines as a batch: "configure terminal", each line in order, then "end". Aborts on the first line the device rejects (an IOS "% ..." error), still leaving configuration mode before returning that error. Before the lines, it copies the running configuration to a checkpoint file on the device (its default writable disk or flash, such as unix: or flash:), and after them the device's own show archive config differences says what changed: no difference reports unchanged and deletes the checkpoint, so a second run of the same lines changes nothing. A change copies the configuration it left to a second checkpoint and records its undo as net.ios.replace naming both files, which returns the device exactly to the first (configure replace) and refuses if the device no longer matches the second. The files stay on the device until that undo runs. A device with no writable file system, or no configure replace, records no undo, with a warning: the configuration lines that would undo the change, written from its configuration read before and after it (the negation of each added line, each removed line put back), are reported under undo_lines, masked, for the task's rollback: list, since lines read off a device never reach the journal. A change inside an access list, a banner or a certificate has no such lines. checkpoint: false does the same on any device. The differences this reports are masked as net.batfish.check masks a candidate.

## Attributes

|  |  |
| --- | --- |
| Capabilities | `CiscoIOSCapable` |
| Transports | `ssh`, `telnet` |
| Requires elevation | no |
| Runs | on or against the target device; acts on its target device |
| Check mode | Not supported: a check run names this task as unchecked, since what a configuration line changes is decided by IOS's own parser as it applies the line, and IOS offers no way to ask without applying it |
| Engine version | `>=0.2.0` |

## Parameters

| Name | Type | Required | Default | Description |
| --- | --- | --- | --- | --- |
| `lines` | `list of string` | yes | - | The configuration lines to apply, in order, WITHOUT "configure terminal" or "end": this method supplies both itself. |
| `checkpoint` | `bool` | no | `true` | Keep a checkpoint of the running configuration on the device before the change, which its undo returns to exactly. false records no undo, and reports the lines that would undo the change for the task's rollback: list. |
| `backup` | `bool` | no | `false` | Capture the device's running-config with "show running-config" before applying any line, recorded under the backup stat. |
| `insecure_skip_host_key_verify` | `bool` | no | `false` | Skip SSH host key verification for this task. This removes protection against a machine in the middle answering for the device, so set it only for a target you have decided does not need it. |
| `insecure_telnet` | `bool` | no | `false` | Reach a device that has no SSH over bare Telnet, answering its login with the task's credential. Telnet carries everything, the login included, in cleartext, so set this only for a device on a network you trust. |

## Returns

| Name | Type | Returned | Description |
| --- | --- | --- | --- |
| `checkpoint` | `string` | when a checkpoint was kept and the lines changed the configuration | The checkpoint file on the device holding the configuration before the change, which the undo returns to. |
| `differences` | `list of string` | when a checkpoint was kept | The device's own contextual differences the change made (+ added, - removed), masked. |
| `undo_lines` | `list of string` | when no checkpoint was kept and the lines changed the configuration | The configuration lines that would undo the change, masked; copy them into the task's rollback: list, putting each masked secret back. |
| `backup` | `string` | when backup is true | The device's full running-config, captured immediately before this task's own lines were applied. Not sanitized: it genuinely contains this device's own enable secret, enable password, local user password hashes, and any TACACS+/RADIUS shared key in whatever strength (or weakness -- IOS's own "type 7" is trivially reversible) encoding the device applies. Mask it with "register_mask: backup" on this task. |

## Undoing this

**Can be undone.** A run that changes something records the instruction that reverses it, as an `inverse` stat holding the method to call and the parameters to call it with, resolved from the state this run actually found. A run that changed nothing records no instruction, which is how it says that undoing it means doing nothing.

Its undo returns the device to a checkpoint of its running configuration kept on the device (net.ios.replace, named by the file's path alone, so no configuration text reaches the journal), refusing when the device has changed since. A device with nowhere to keep one records no undo: the configuration lines that would undo the change are shown, masked, for the task's rollback: list, since lines read off the device never reach the journal.

Note that no rollback engine reads this yet. What exists today is the recording, which has to happen during the forward run because the values an undo needs are gone once the change is applied.

## See also

- `net.ios.replace`
- `net.ios.parse`
- `net.cli.command`
- `net.cli.config`

## Examples

Create a loopback interface, undoable by pleiades rollback:

```yaml
- name: Add a loopback interface
  net.ios.config:
    lines:
      - interface Loopback0
      - description managed by pleiades
```

