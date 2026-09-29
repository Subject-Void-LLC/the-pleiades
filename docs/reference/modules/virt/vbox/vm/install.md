---
status: beta
---

# virt.vbox.vm.install

Makes a Windows or FreeBSD VM by installing it from its installation ISO, unattended, as a base to clone.

Makes sure a VM of this name exists, creating it by installing an operating system from an installation ISO already on the host, with no one at the keyboard. installer says which: windows or freebsd, declared by the task rather than read from os_type. The VM gets a new disk of disk_gb, BIOS firmware, the size asked for, os_type, and no network adapter, so the install downloads nothing. Its DVD drives hold the ISO and an answer medium Pleiades builds. For windows it is an answer file (Autounattend.xml), which partitions the disk, installs image and accepts its license, then takes the new Windows through audit mode, where it deletes the two copies of itself Windows cached, points the registry (HKLM\SYSTEM\Setup, UnattendFile) at D:\Autounattend.xml, and runs sysprep to generalize the installation and shut it down. A generalized Windows looks for its answer file at first boot only there and in its own folders, never on a DVD, so the pointer is how a clone reads its seed. The answer file holds a password only for audit mode's sign-in, made at random for this install and kept nowhere by Pleiades; Windows keeps it, not blanked, in one of the two copies it caches, which is why audit mode deletes both, and a clone's seed sets a password of its own. For freebsd it is bsdinstall's script (installerconfig), which a released DVD cannot hold, so Pleiades watches the VM's screen for the installer's Welcome dialog, chooses its shell, and types the command that mounts the script's DVD and runs it. The command first says so on the VM's serial console, which is written to console.log in its folder, then copies the installer's log there, so the console shows how far an install got; running the task again resumes an install that stopped before or after the command, and never types it twice. The script installs the kernel and base system from the DVD onto the first disk with bsdinstall's default partitioning, enables sshd, and prepares the system for its clones' first boot: nuageinit reads each clone's seed, the serial console stays on, and the SSH host keys are printed on it between the lines cloud-init uses, which virt.vbox.vm.host_keys reads. Then the VM powers off; a script that fails leaves it running at a shell. The task waits for the power-off: about six to eight minutes for Windows on the lab host. When the VM is off, both DVDs are taken out, the answer medium is deleted, and the VM is marked installed (the extradata pleiades/installed). The VM is meant as a base to snapshot and clone: virt.vbox.vm.clone gives each clone its own name, address and login. A VM already under the name reports no change when it is marked installed, and is refused when it is not, since an install that did not finish is no base: delete it with virt.vbox.vm.delete and run again. An install still running at the timeout is left running, with a picture of its screen saved in its folder to show where it stopped. The task's target is the VirtualBox host (a device with virtualbox: true), not the VM, which is a resource on it. A check reads the host's VMs and memory, and sends nothing.

## Attributes

|  |  |
| --- | --- |
| Capabilities | `VirtualBoxCapable` |
| Transports | `winrm` |
| Requires elevation | no |
| Check mode | Supported: reports what it would change and changes nothing |
| Engine version | `>=0.2.0` |

## Parameters

| Name | Type | Required | Default | Description |
| --- | --- | --- | --- | --- |
| `name` | `string` | yes | - | The VM's name on the host. It must start with a letter or digit and hold only letters, digits, '.', '_' and '-', at most 63 characters; any other name is refused rather than quoted. |
| `installer` | `string` | yes | - | The operating system the ISO installs, which sets how: windows reads an answer file by itself, and freebsd is started from its installer's Welcome dialog. Declared here, never inferred from os_type. |
| `iso` | `string` | yes | - | The installation ISO's absolute path on the host, such as Microsoft's Windows Server evaluation ISO or FreeBSD's dvd1 ISO. It is attached and then taken out again, never changed or deleted. |
| `image` | `string` | no | - | windows only, and required with it. The edition to install: its name in the ISO's install.wim, such as Windows Server 2025 Standard Evaluation, which is Server Core, or Windows Server 2025 Datacenter Evaluation (Desktop Experience); or its index there, 1 for the first. |
| `os_type` | `string` | yes | - | The VM's guest OS type, as VBoxManage list ostypes names it: Windows2025_64 for Windows Server 2025, Windows2022_64 for 2022, Ubuntu_64 for Ubuntu. VirtualBox tunes the VM to it, and virt.vbox.vm.clone seeds a clone of a Windows type with a Windows answer file. |
| `disk_gb` | `int` | no | `64` | The size of the VM's new disk, in gigabytes, at least 32 for windows and 8 for freebsd. It grows on the host as the guest writes to it. |
| `size` | `string` | no | `medium` | The VM's size while it installs, a T-shirt size, which sets its CPUs and memory together as a cloud's instance type does: xsmall is 1 CPU and 1024 MB, small 1 CPU and 2048 MB, medium 2 CPUs and 4096 MB, large 4 CPUs and 8192 MB, and xlarge 8 CPUs and 16384 MB. Windows Server needs at least small. |
| `language` | `string` | no | `en-US` | windows only. The installation's language and locale, as a tag such as en-US. The ISO must hold it. |
| `timeout` | `int` | no | `7200` | How many seconds to wait for the install to finish and the VM to power itself off. |

## Returns

| Name | Type | Returned | Description |
| --- | --- | --- | --- |
| `uuid` | `string` | when not a check that would install | The VM's UUID. |
| `size` | `string` | always | The VM's T-shirt size, or empty when its CPUs and memory are not exactly one size's. |
| `diff` | `dict` | always | Whether a VM of the name existed before this task and after it. |

## Undoing this

**Can be undone.** A run that changes something records the instruction that reverses it, as an `inverse` stat holding the method to call and the parameters to call it with, resolved from the state this run actually found. A run that changed nothing records no instruction, which is how it says that undoing it means doing nothing.

A run that installed a VM emits virt.vbox.vm.delete naming it and pinning its UUID, which deletes its disk with it; the ISO is never changed. One that found an installed VM under the name emits nothing.

Note that no rollback engine reads this yet. What exists today is the recording, which has to happen during the forward run because the values an undo needs are gone once the change is applied.

## See also

- `virt.vbox.vm.import_disk`
- `virt.vbox.snapshot.take`
- `virt.vbox.vm.clone`
- `virt.vbox.vm.delete`

## Examples

Install Windows Server Core as a base:

```yaml
- name: Install Windows Server 2025 Core from the evaluation ISO
  virt.vbox.vm.install:
    name: ws2025-core-base
    installer: windows
    iso: G:\iso\server-2025-eval.iso
    image: Windows Server 2025 Standard Evaluation
    os_type: Windows2025_64

- name: Snapshot the generalized install
  virt.vbox.snapshot.take:
    vm: ws2025-core-base
    name: base
```

Install FreeBSD as a base:

```yaml
- name: Install FreeBSD 15.1 from its DVD
  virt.vbox.vm.install:
    name: freebsd-base
    installer: freebsd
    iso: G:\iso\FreeBSD-15.1-RELEASE-amd64-dvd1.iso
    os_type: FreeBSD_64
    size: small
    disk_gb: 16

- name: Snapshot the installed base
  virt.vbox.snapshot.take:
    vm: freebsd-base
    name: base
```

