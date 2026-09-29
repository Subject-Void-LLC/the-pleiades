---
status: beta
---

# virt.vbox.vm.send_keys

Types into a running VM's console, as its keyboard would.

Types keys into a running VM's console, written as Packer's boot_command writes them: text as it is, a named key between angle brackets (<enter>, <tab>, <esc>, <bs>, <del>, <spacebar>, <up>, <down>, <left>, <right>, <home>, <end>, <pageup>, <pagedown>, <insert>, <f1> to <f12>), and <wait> or <wait5> for a pause of one or five seconds (at most 60). Text is typed with a US keyboard layout and may hold only printable ASCII. It is for a VM with no other way in yet, such as one at a first-boot screen; see what it shows with virt.vbox.vm.screenshot. Never type a secret: text travels on the host's VBoxManage command line, where any process on the host can read it. This cannot be undone. The task's target is the VirtualBox host (a device with virtualbox: true), not the VM, which is a resource on it. A check reads the VM and the keys, and types nothing.

## Attributes

|  |  |
| --- | --- |
| Capabilities | `VirtualBoxCapable` |
| Transports | `winrm` |
| Requires elevation | no |
| Runs | on or against the target device; acts on its target device |
| Check mode | Supported: reports what it would change and changes nothing |
| Engine version | `>=0.2.0` |

## Parameters

| Name | Type | Required | Default | Description |
| --- | --- | --- | --- | --- |
| `name` | `string` | yes | - | The VM's name on the host. It must start with a letter or digit and hold only letters, digits, '.', '_' and '-', at most 63 characters; any other name is refused rather than quoted. |
| `keys` | `string` | yes | - | What to type, as Packer's boot_command writes it, such as <tab><tab><enter>. |

## Returns

| Name | Type | Returned | Description |
| --- | --- | --- | --- |
| `strokes` | `int` | always | How many steps were typed, or would be, in a check: each run of text, named key and pause is one. |

## Undoing this

**Cannot be undone.** This method never records a reversing instruction, so a rollback reaching a task that used it stops rather than guessing.

What was typed was acted on by the guest, and no key undoes that in general.

Note that no rollback engine reads this yet. What exists today is the recording, which has to happen during the forward run because the values an undo needs are gone once the change is applied.

## See also

- `virt.vbox.vm.screenshot`

## Examples

Accept a first-boot screen:

```yaml
- name: Move to the Accept button and press it
  virt.vbox.vm.send_keys:
    name: win-lab
    keys: <tab><tab><enter>
```

