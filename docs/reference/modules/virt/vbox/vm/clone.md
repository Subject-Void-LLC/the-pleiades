---
status: beta
---

# virt.vbox.vm.clone

Makes a VM as a linked clone of another's snapshot, seeded by cloud-init with a device's login.

Makes sure a VM of this name exists, creating it as a linked clone of a snapshot of another VM, so it takes little space and starts from that snapshot's disk. A VM already under the name reports no change and is not reconfigured or reseeded; when its CPUs or memory are not those asked for, the task says so in a warning, and virt.vbox.vm.resize changes them. The new VM gets the size asked for, or the memory and CPUs, and one larger than the host is refused: more CPUs than it has processors online, or more memory than it has. It also gets a NAT adapter for the internet, a host-only adapter at a fixed address, a serial console written to console.log in its folder (virt.vbox.vm.host_keys reads its SSH host keys from there), and a cloud-init NoCloud seed on its DVD drive. The seed is built by Pleiades, not on the host, and reaches the host on the command's standard input, never on a command line. It carries login's user name, the public half of its key and a salted hash of its password, taken from the vault by pleiades add-credential login --generate; never the key or the password. SSH then admits the key only; the password is for the VM's console. A login of root may log in by key; any other user gets passwordless sudo. The seed ISO stays in the VM's folder, holding that hash, until virt.vbox.vm.delete removes both. The address and login are recorded on the VM as VirtualBox extradata (pleiades/address, pleiades/device), which virt.vbox.vm.list reports, since VirtualBox cannot know a guest's address without its Guest Additions. The VM is not started. The task's target is the VirtualBox host (a device with virtualbox: true), not the VM, which is a resource on it. A check reads the host's VMs and the snapshot, and sends nothing.

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
| `from` | `string` | yes | - | The VM to clone, named as name is. |
| `snapshot` | `string` | yes | - | The snapshot of from to clone. Exactly one of from's snapshots may have this name. |
| `login` | `string` | yes | - | The inventory device whose stored login the VM is seeded with: its user name, its key's public half and a hash of its password. Usually the device that stands for this VM. |
| `address` | `string` | yes | - | The host-only adapter's address with its prefix, as 192.168.56.10/24. Keep it out of the host-only DHCP server's range. |
| `hostname` | `string` | no | - | The VM's host name. Defaults to name, which must then be a valid host name. |
| `size` | `string` | no | - | The VM's size, a T-shirt size, which sets its CPUs and memory together as a cloud's instance type does: xsmall is 1 CPU and 1024 MB, small 1 CPU and 2048 MB, medium 2 CPUs and 4096 MB, large 4 CPUs and 8192 MB, and xlarge 8 CPUs and 16384 MB. Give size, or memory_mb and cpus, not both; with none of the three, the VM is xsmall. |
| `memory_mb` | `int` | no | `1024` | Its memory, in megabytes, when size is not given. |
| `cpus` | `int` | no | `1` | Its virtual CPU count, when size is not given. |
| `paravirt_provider` | `string` | no | - | The paravirtualization interface the guest is offered, as VBoxManage modifyvm --paravirt-provider takes it: default picks one by the guest's OS type (kvm for Linux, hyperv for Windows), and none offers nothing, so the guest keeps time by its own clocks. Left out, the VM keeps from's. |
| `host_only_adapter` | `string` | no | `VirtualBox Host-Only Ethernet Adapter` | The host's host-only adapter, as VBoxManage list hostonlyifs names it. |

## Returns

| Name | Type | Returned | Description |
| --- | --- | --- | --- |
| `uuid` | `string` | when not a check that would clone | The VM's UUID, which is also its cloud-init instance ID. |
| `address` | `string` | always | The host-only address the VM was given, without its prefix. |
| `size` | `string` | always | The VM's T-shirt size, read from its CPUs and memory (those asked for, in a check that would clone), or empty when they are not exactly one size's. |
| `diff` | `dict` | always | Whether a VM of the name existed before this task and after it. |

## Undoing this

**Can be undone.** A run that changes something records the instruction that reverses it, as an `inverse` stat holding the method to call and the parameters to call it with, resolved from the state this run actually found. A run that changed nothing records no instruction, which is how it says that undoing it means doing nothing.

A run that made a VM emits virt.vbox.vm.delete naming it; one that found a VM under the name emits nothing.

Note that no rollback engine reads this yet. What exists today is the recording, which has to happen during the forward run because the values an undo needs are gone once the change is applied.

## See also

- `virt.vbox.vm.import_ova`
- `virt.vbox.vm.start`
- `virt.vbox.vm.host_keys`
- `virt.vbox.vm.resize`
- `virt.vbox.vm.delete`
- `virt.vbox.vm.list`

## Examples

Clone and start a lab VM:

```yaml
- name: Make the lab VM from the base's clean snapshot
  virt.vbox.vm.clone:
    name: ubuntu-lab
    from: ubuntu-2404-base
    snapshot: base
    login: ubuntu-lab
    address: 192.168.56.10/24
    size: small

- name: Start it
  virt.vbox.vm.start:
    name: ubuntu-lab
```

