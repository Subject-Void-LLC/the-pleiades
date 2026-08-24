---
status: beta
---

# net.cli.config

Applies configuration lines to a network device over its CLI.

Splits config into non-blank lines and sends each one through the same generic session net.cli.command uses, one line at a time, matched against the device's own declared cli_prompt property. It carries no vendor-specific configuration-mode knowledge of its own: it never enters or leaves a configuration mode on the caller's behalf, so a device that needs one (Cisco IOS's "configure terminal"/"end", for instance) must have those lines included in config itself. net.ios.config is the Cisco-specific sibling that does drive configuration mode, and is what a runbook targeting Cisco IOS should use instead. Reports changed every time every line reaches the device without error, the same convention net.cli.command and exec.command both establish: a CLI line's effect cannot be inspected, so this platform does not guess at one. Each line is bounded to 30 seconds for the same reason net.cli.command's own line is: a generic device has no known paging convention, so an unexpectedly long response can pause on a pager prompt this method cannot answer, and a bounded, clear error is preferable to an indefinite hang.

## Attributes

|  |  |
| --- | --- |
| Capabilities | `NetworkCLICapable` |
| Transports | `ssh` |
| Requires elevation | no |
| Engine version | `>=1.0.0` |

## Parameters

| Name | Type | Required | Default | Description |
| --- | --- | --- | --- | --- |
| `config` | `string` | yes | - | The configuration lines to send, one per line. Blank lines are skipped. Include any vendor-specific mode commands (e.g. "configure terminal" and "end") this device needs, since this method sends none on its own. |
| `insecure_skip_host_key_verify` | `bool` | no | `false` | Skip SSH host key verification for this task. This removes protection against a machine in the middle answering for the device, so set it only for a target you have decided does not need it. |

## Undoing this

**Cannot be undone.** This method never records a reversing instruction, so a rollback reaching a task that used it stops rather than guessing.

An arbitrary CLI line's effect on a device is unknown to this platform, so no undo can be derived from it.

Note that no rollback engine reads this yet. What exists today is the recording, which has to happen during the forward run because the values an undo needs are gone once the change is applied.

## See also

- `net.cli.command`
- `net.ios.config`

## Examples

Apply configuration on a device with no vendor-specific method yet:

```yaml
- name: Set a banner
  net.cli.config:
    config: |
      configure terminal
      banner motd ^Cauthorized access only^C
      end
```

