---
status: beta
---

# net.ios.config

Applies configuration lines to a Cisco IOS device, with an optional pre-change backup.

Opens an interactive PTY session over SSH, using netcli.IOS's own real paging, configuration-mode and error conventions (verified directly against a real Cisco IOS XE device, not assumed), and applies lines as a batch: "configure terminal", each line in order, then "end". Aborts on the first line the device rejects (a real IOS "% ..." error), still leaving configuration mode before returning that error. When backup is true, runs "show running-config" before applying anything and records it under the backup stat, giving an operator something to restore from by hand; this platform does not attempt an automatic rollback (see this method's own Reversibility notes for why). Reports changed whenever every line reaches the device without error: a configuration line's effect cannot be inspected before it runs, the same reasoning exec.command and net.cli.command both apply.

## Attributes

|  |  |
| --- | --- |
| Capabilities | `CiscoIOSCapable` |
| Transports | `ssh` |
| Requires elevation | no |
| Check mode | Not supported: a check run names this task as unchecked, since what a configuration line changes is decided by IOS's own parser as it applies the line, and IOS offers no way to ask without applying it |
| Engine version | `>=1.0.0` |

## Parameters

| Name | Type | Required | Default | Description |
| --- | --- | --- | --- | --- |
| `lines` | `list of string` | yes | - | The configuration lines to apply, in order, WITHOUT "configure terminal" or "end": this method supplies both itself. |
| `backup` | `bool` | no | `false` | Capture the device's running-config with "show running-config" before applying any line, recorded under the backup stat. |
| `insecure_skip_host_key_verify` | `bool` | no | `false` | Skip SSH host key verification for this task. This removes protection against a machine in the middle answering for the device, so set it only for a target you have decided does not need it. |

## Returns

| Name | Type | Returned | Description |
| --- | --- | --- | --- |
| `backup` | `string` | when backup is true | The device's full running-config, captured immediately before this task's own lines were applied. Not sanitized: it genuinely contains this device's own enable secret, enable password, local user password hashes, and any TACACS+/RADIUS shared key in whatever strength (or weakness -- IOS's own "type 7" is trivially reversible) encoding the device applies. Mask it with "register_mask: backup" on this task. |

## Undoing this

**Cannot be undone.** This method never records a reversing instruction, so a rollback reaching a task that used it stops rather than guessing.

IOS's "no <line>" negation convention does not reliably invert every configuration statement (a route-map or ACL entry, for instance), so this platform does not assert an inverse it cannot guarantee. Set backup: true to capture the prior running-config for a human-directed rollback.

Note that no rollback engine reads this yet. What exists today is the recording, which has to happen during the forward run because the values an undo needs are gone once the change is applied.

## See also

- `net.cli.command`
- `net.cli.config`

## Examples

Create a loopback interface with a backup:

```yaml
- name: Add a loopback interface
  net.ios.config:
    backup: true
    lines:
      - interface Loopback0
      - description managed by pleiades
```

