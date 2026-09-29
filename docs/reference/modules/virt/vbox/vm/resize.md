---
status: beta
---

# virt.vbox.vm.resize

Changes a stopped VirtualBox VM's CPUs and memory, by T-shirt size or by count.

Makes sure a VM has the CPUs and memory asked for: a size, or memory_mb and cpus, either of which alone leaves the other as it is. A VM that has them already reports no change. VirtualBox changes them only while a VM is powered off, so a running, paused or saved VM is refused: stop it first with virt.vbox.vm.stop. A size larger than the host is refused: more CPUs than it has processors online, or more memory than it has. The guest sees the change at its next boot. A run that changed the VM emits virt.vbox.vm.resize back to the CPUs and memory it had. The task's target is the VirtualBox host (a device with virtualbox: true), not the VM, which is a resource on it. A check reads the VM and the host, and sends nothing.

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
| `size` | `string` | no | - | The size to give the VM, a T-shirt size, which sets its CPUs and memory together as a cloud's instance type does: xsmall is 1 CPU and 1024 MB, small 1 CPU and 2048 MB, medium 2 CPUs and 4096 MB, large 4 CPUs and 8192 MB, and xlarge 8 CPUs and 16384 MB. Give size, or memory_mb, cpus or both, not size with either. |
| `memory_mb` | `int` | no | - | The memory to give it, in megabytes. |
| `cpus` | `int` | no | - | The virtual CPU count to give it. |

## Returns

| Name | Type | Returned | Description |
| --- | --- | --- | --- |
| `memory_mb` | `int` | always | Its memory after this task, in megabytes (predicted, in a check). |
| `cpus` | `int` | always | Its virtual CPU count after this task (predicted, in a check). |
| `size` | `string` | always | Its T-shirt size after this task, or empty when its CPUs and memory are not exactly one size's. |
| `diff` | `dict` | always | Its memory_mb, cpus and size before this task and after it. |

## Undoing this

**Can be undone.** A run that changes something records the instruction that reverses it, as an `inverse` stat holding the method to call and the parameters to call it with, resolved from the state this run actually found. A run that changed nothing records no instruction, which is how it says that undoing it means doing nothing.

A run that changed the VM emits virt.vbox.vm.resize back to the memory and CPUs it had; one that found them already right emits nothing.

Note that no rollback engine reads this yet. What exists today is the recording, which has to happen during the forward run because the values an undo needs are gone once the change is applied.

## See also

- `virt.vbox.vm.clone`
- `virt.vbox.vm.stop`
- `virt.vbox.vm.info`

## Examples

Make a lab VM bigger:

```yaml
- name: Shut the lab VM down
  virt.vbox.vm.stop:
    name: ubuntu-lab

- name: Make it medium
  virt.vbox.vm.resize:
    name: ubuntu-lab
    size: medium

- name: Start it again
  virt.vbox.vm.start:
    name: ubuntu-lab
```

Give a VM more memory only:

```yaml
- name: Double the build VM's memory
  virt.vbox.vm.resize:
    name: build-vm
    memory_mb: 8192
```

