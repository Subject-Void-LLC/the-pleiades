---
status: beta
---

# net.ios.save

Saves a Cisco IOS device's running configuration to startup.

Runs IOS's "write memory", copying running-config over startup-config so the current configuration survives a reload. This is the step that makes every earlier net.ios.config task permanent, and it is deliberately a separate method rather than a parameter on net.ios.config: persisting configuration is a decision about blast radius, not a detail of applying a line, and a runbook that applies several changes should be able to decide once, at the end, whether any of them should outlive the next reload. Reports changed whenever the save completes, since IOS gives no way to know whether startup-config already matched. Aborts on a real IOS "% ..." error rather than reporting a save that did not happen.

## Attributes

|  |  |
| --- | --- |
| Capabilities | `CiscoIOSCapable` |
| Transports | `ssh` |
| Requires elevation | no |
| Engine version | `>=1.0.0` |

## Parameters

| Name | Type | Required | Default | Description |
| --- | --- | --- | --- | --- |
| `insecure_skip_host_key_verify` | `bool` | no | `false` | Skip SSH host key verification for this task. This removes protection against a machine in the middle answering for the device, so set it only for a target you have decided does not need it. |

## Returns

| Name | Type | Returned | Description |
| --- | --- | --- | --- |
| `stdout` | `string` | always | Whatever the device printed in response to "write memory", typically a "Building configuration..." line followed by "[OK]". |

## Undoing this

**Cannot be undone.** This method never records a reversing instruction, so a rollback reaching a task that used it stops rather than guessing.

Saving overwrites startup-config with running-config, and the device keeps no copy of what startup-config held beforehand, so this platform cannot reconstruct an inverse. Capture the prior configuration first (net.ios.config's backup parameter) if a rollback target is needed.

Note that no rollback engine reads this yet. What exists today is the recording, which has to happen during the forward run because the values an undo needs are gone once the change is applied.

## See also

- `net.ios.config`

## Examples

Persist a change after verifying it:

```yaml
- name: Save the running configuration
  net.ios.save:
```

