---
status: beta
---

# virt.vbox.vm.import_ova

Imports an OVA appliance on a VirtualBox host as a VM, with no network adapter.

Makes sure a VM of this name exists, importing it from an OVA file already on the host (win.file.download fetches one) into the host's vm_folder. A VM already under the name reports no change, and is not compared with the file. The imported VM is left with no network adapter, whatever the appliance asked for: Ubuntu's cloud image asks for a bridged one, which would put the VM on the host's own network. A VM made from it (virt.vbox.vm.clone) is given the networks it is meant to have. The VM is not started, and is meant as a base to snapshot and clone rather than to boot. The task's target is the VirtualBox host (a device with virtualbox: true), not the VM, which is a resource on it. A check reads the host's VMs and sends nothing.

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
| `path` | `string` | yes | - | The OVA file's absolute path on the host. It may not hold a quote, a wildcard or a control character. |

## Returns

| Name | Type | Returned | Description |
| --- | --- | --- | --- |
| `uuid` | `string` | when not a check that would import | The VM's UUID. |
| `diff` | `dict` | always | Whether a VM of the name existed before this task and after it. |

## Undoing this

**Can be undone.** A run that changes something records the instruction that reverses it, as an `inverse` stat holding the method to call and the parameters to call it with, resolved from the state this run actually found. A run that changed nothing records no instruction, which is how it says that undoing it means doing nothing.

A run that imported a VM emits virt.vbox.vm.delete naming it and pinning its UUID, so a VM made later under the name is refused rather than deleted; one that found a VM under the name emits nothing.

Note that no rollback engine reads this yet. What exists today is the recording, which has to happen during the forward run because the values an undo needs are gone once the change is applied.

## See also

- `win.file.download`
- `virt.vbox.snapshot.take`
- `virt.vbox.vm.clone`

## Examples

Import the Ubuntu cloud image:

```yaml
- name: Import the Ubuntu 24.04 cloud image as a base
  virt.vbox.vm.import_ova:
    name: ubuntu-2404-base
    path: G:\PleiadesLab\ubuntu-24.04-server-cloudimg-amd64.ova
```

