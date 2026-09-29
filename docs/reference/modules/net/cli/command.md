---
status: beta
---

# net.cli.command

Runs one show/exec-mode command against a network device's CLI.

Opens an interactive PTY session over SSH and runs one command, matched against the device's own declared cli_prompt property (a generic Dialect, built by netcli.FromPrompt, with no vendor-specific paging, configuration-mode or error convention of its own). Reports the command's own output, with its echoed input line and the trailing prompt both stripped. A command cannot be inspected, so this reports changed every time it reaches the device without error, the same convention exec.command established: pair it with when/when_or/when_cel when idempotence matters. Refuses outright when the target device's cli_prompt property is unset, rather than guessing at a prompt shape. Because this method has no known paging convention for a generic device, a command whose output is longer than the device's own terminal length can pause on a pager prompt this method cannot answer (verified directly against a real device); each command is bounded to 30 seconds so that failure is a clear, timely error rather than an indefinite hang. Pipe a long-output command through the device's own output filter (e.g. "show running-config | include hostname") to avoid triggering it, or use net.ios.config, whose vendor-specific dialect disables paging for real.

## Attributes

|  |  |
| --- | --- |
| Capabilities | `NetworkCLICapable` |
| Transports | `ssh` |
| Requires elevation | no |
| Runs | on or against the target device; acts on its target device |
| Check mode | Not supported: a check run names this task as unchecked, since an arbitrary CLI line can be anything the device accepts, and what it changes, if anything, is known only once the device has run it |
| Engine version | `>=0.2.0` |

## Parameters

| Name | Type | Required | Default | Description |
| --- | --- | --- | --- | --- |
| `command` | `string` | yes | - | The single CLI line to run, e.g. "show version". |
| `insecure_skip_host_key_verify` | `bool` | no | `false` | Skip SSH host key verification for this task. This removes protection against a machine in the middle answering for the device, so set it only for a target you have decided does not need it. |

## Returns

| Name | Type | Returned | Description |
| --- | --- | --- | --- |
| `stdout` | `string` | always | Everything the device printed in response to the command, with the echoed command line and the trailing prompt removed. |

## Undoing this

**Cannot be undone.** This method never records a reversing instruction, so a rollback reaching a task that used it stops rather than guessing.

An arbitrary CLI line's effect on a device is unknown to this platform, so no undo can be derived from it.

Note that no rollback engine reads this yet. What exists today is the recording, which has to happen during the forward run because the values an undo needs are gone once the change is applied.

## See also

- `net.cli.config`
- `net.ios.config`

## Examples

Read a device's software version:

```yaml
- name: Check the running version
  net.cli.command:
    command: show version
  register: version
```

