---
status: beta
---

# virt.vbox.vm.import_disk

Makes a VirtualBox VM from a disk image on the host, such as Microsoft's Windows Server evaluation VHDX.

Makes sure a VM of this name exists, creating it from a disk image already on the host: a VHDX, VHD, VMDK or VDI file, such as the evaluation VHDX Microsoft publishes for Windows Server. The image is copied into the VM's folder as a VDI that grows as the guest writes to it, and the image itself is left as it was, so one image can make many VMs. The copy is the VM's disk, and virt.vbox.vm.delete removes it with the VM. The VM gets os_type; a SATA controller holding the disk and an IDE controller for the DVD a clone's seed goes in; network cards Windows has a driver for, with nothing attached; and the firmware the disk boots with, read from its first sectors: EFI for a GUID partition table, BIOS for a master boot record. A VM already under the name reports no change and is not compared with the image. The VM is not started, and is meant as a base to snapshot and clone. For Windows the image must be generalized (by sysprep), and virt.vbox.vm.clone then gives each clone its own name, address and password on a DVD. Microsoft's Windows Server evaluation VHDX is generalized but never reads that DVD at its first boot (measured on the lab host), so its clones stop at the first-boot screens; virt.vbox.vm.install makes a Windows base whose clones read theirs. The task's target is the VirtualBox host (a device with virtualbox: true), not the VM, which is a resource on it. A check reads the host's VMs and sends nothing; it does not read the disk, so it reports the firmware only when firmware names one.

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
| `path` | `string` | yes | - | The disk image's absolute path on the host. It may not hold a quote, a wildcard or a control character. |
| `os_type` | `string` | yes | - | The VM's guest OS type, as VBoxManage list ostypes names it: Windows2025_64 for Windows Server 2025, Windows2022_64 for 2022, Ubuntu_64 for Ubuntu. VirtualBox tunes the VM to it, and virt.vbox.vm.clone seeds a clone of a Windows type with a Windows answer file. |
| `firmware` | `string` | no | `auto` | What the VM boots with. auto reads the disk's first sectors: EFI for a GUID partition table, BIOS for a master boot record. |
| `timeout` | `int` | no | `3600` | How many seconds the copy may take. A disk of many gigabytes takes minutes. |

## Returns

| Name | Type | Returned | Description |
| --- | --- | --- | --- |
| `uuid` | `string` | when not a check that would import | The VM's UUID. |
| `firmware` | `string` | when not a check with firmware auto | What the VM boots with: bios or efi. |
| `diff` | `dict` | always | Whether a VM of the name existed before this task and after it. |

## Undoing this

**Can be undone.** A run that changes something records the instruction that reverses it, as an `inverse` stat holding the method to call and the parameters to call it with, resolved from the state this run actually found. A run that changed nothing records no instruction, which is how it says that undoing it means doing nothing.

A run that made a VM emits virt.vbox.vm.delete naming it and pinning its UUID, which deletes the copied disk with it; the image copied from is never changed. One that found a VM under the name emits nothing.

Note that no rollback engine reads this yet. What exists today is the recording, which has to happen during the forward run because the values an undo needs are gone once the change is applied.

## See also

- `virt.vbox.vm.install`
- `virt.vbox.snapshot.take`
- `virt.vbox.vm.clone`
- `virt.vbox.vm.import_ova`

## Examples

Make a Windows Server base from Microsoft's VHDX:

```yaml
- name: Make a Windows Server 2025 base from the evaluation VHDX
  virt.vbox.vm.import_disk:
    name: ws2025-base
    path: G:\iso\server-2025-datacenter-eval.vhdx
    os_type: Windows2025_64

- name: Snapshot it, never booted
  virt.vbox.snapshot.take:
    vm: ws2025-base
    name: base
```

