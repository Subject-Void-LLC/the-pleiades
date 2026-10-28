---
status: beta
---

# gate.list

Validates a project's gate manifest on the device and lists how each check is classified, running nothing.

Reads and validates the same manifest gate.run reads, under the same rules, and reports each check's name, kind, whether it is heavy or serial, its timeout, where it would run (a directory on the device or an image), its preconditions, and whether the task's selection would run it, with the reason when it would not. A check that needs a container engine on a device with none onboarded is reported as not runnable. Nothing in the manifest runs, so a check of this task is the task itself.

## Attributes

|  |  |
| --- | --- |
| Capabilities | `CommandExecCapable` |
| Transports | `ssh` |
| Requires elevation | no |
| Runs | on or against the target device; acts on its target device |
| Check mode | Supported: reports what it would change and changes nothing |
| Engine version | `>=0.2.0` |

## Parameters

| Name | Type | Required | Default | Description |
| --- | --- | --- | --- | --- |
| `manifest` | `string` | yes | - | The gate manifest's path on the device. |
| `kinds` | `list of string` | no | - | Mark as selected only checks of these kinds, as gate.run would select them. |
| `checks` | `list of string` | no | - | Mark as selected only these checks, by name. |
| `heavy` | `string` | no | `include` | include, exclude or only, as for gate.run. **Choices:** `include`, `exclude`, `only`. |
| `insecure_skip_host_key_verify` | `bool` | no | `false` | Skip SSH host key verification for this task. This removes protection against a machine in the middle answering for the device, so set it only for a target you have decided does not need it. |

## Returns

| Name | Type | Returned | Description |
| --- | --- | --- | --- |
| `checks` | `list of dict` | always | One entry per check: name, kind, heavy, serial, timeout_s, runs ("dir <path>" or "image <reference>"), requires (the preconditions, as written), selected, reason (why it is not selected), and runnable (false, with the reason in reason, for a check the device cannot run). |
| `kinds` | `list of dict` | always | Every kind the manifest may use: name, builtin and description. |
| `selected` | `int` | always | How many checks the selection would run. |

## Undoing this

**Cannot be undone.** This method never records a reversing instruction, so a rollback reaching a task that used it stops rather than guessing.

It reads the manifest and runs nothing, so there is nothing to undo.

Note that no rollback engine reads this yet. What exists today is the recording, which has to happen during the forward run because the values an undo needs are gone once the change is applied.

## See also

- `gate.run`

## Examples

Show how a project's checks are classified:

```yaml
- name: List the gate
  gate.list:
    manifest: /srv/app/gates.yaml
  register: gates
```

