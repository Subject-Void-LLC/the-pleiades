---
status: beta
---

# win.cpu.topology

Reports a Windows host's logical processors: which are performance cores, which efficiency cores, and how they group.

Reads the host's CPU sets as its scheduler sees them, through the Windows API GetSystemCpuSetInformation, and changes nothing. Each logical processor is reported with its processor group, core, NUMA node, last-level cache and efficiency class. On a hybrid processor, such as Intel's with P-cores and E-cores, a higher efficiency class is a faster, less power-efficient core: the logical processors with the highest class are reported as performance processors and the rest as efficiency processors, with the affinity mask that selects the performance ones. On a processor whose cores are all alike, every logical processor is a performance one. No WMI class reports efficiency classes, so the host compiles a small call to the API with PowerShell's Add-Type; a host whose PowerShell runs in Constrained Language Mode refuses that, and the task fails saying so. A check is the same read.

## Attributes

|  |  |
| --- | --- |
| Capabilities | `WindowsShellCapable` |
| Transports | `winrm` |
| Requires elevation | no |
| Check mode | Supported: reports what it would change and changes nothing |
| Engine version | `>=0.2.0` |

## Parameters

| Name | Type | Required | Default | Description |
| --- | --- | --- | --- | --- |
| `timeout` | `int` | no | `120` | How many seconds the read may take. |

## Returns

| Name | Type | Returned | Description |
| --- | --- | --- | --- |
| `processors` | `list` | always | Each logical processor as index, group, core, numa_node, last_level_cache, efficiency_class and parked, in the order Windows lists them. |
| `processor_count` | `int` | always | How many logical processors the host has. |
| `core_count` | `int` | always | How many cores they are on. |
| `hybrid` | `bool` | always | Whether the cores are of more than one efficiency class. |
| `performance_processors` | `list` | always | The indexes of the logical processors of the highest efficiency class: every one, when the host is not hybrid. |
| `efficiency_processors` | `list` | always | The indexes of the rest, empty when the host is not hybrid. |
| `performance_affinity_mask` | `string` | when the host has one processor group | The affinity mask selecting the performance processors, as hexadecimal, such as 0xC03C3: what Windows' start /affinity and a process's ProcessorAffinity take. |

## Undoing this

**Cannot be undone.** This method never records a reversing instruction, so a rollback reaching a task that used it stops rather than guessing.

Reading is not changing: this reports the host's processors and alters nothing, so there is nothing an undo could restore.

Note that no rollback engine reads this yet. What exists today is the recording, which has to happen during the forward run because the values an undo needs are gone once the change is applied.

## See also

- `facts.gather`
- `virt.vbox.vm.start`

## Examples

Find a host's performance cores:

```yaml
- name: Which of the lab host's cores are P-cores
  win.cpu.topology: {}
  register: cpus
```

