---
status: beta
---

# Capability vocabulary

What a device *can do*, not what it *is*. A Collection method declares which capabilities it requires; a device advertises one by structurally implementing the matching Go interface.

**Nothing compares these to your inventory before a run yet.** `pleiades validate` checks a target device's capabilities for exactly two legacy action names, `ssh_exec` and `ios_backup`. For every catalog FQCN the required capability is documentation only: a mismatch surfaces during the run, not at plan time. See [Implementation status](../01-start-here.md#implementation-status).

| Capability | Parent | Children |
| --- | --- | --- |
| `AWSAPICapable` | - | - |
| `AptCapable` | `PackageManagerCapable` | - |
| `AristaEOSCapable` | `NetworkCLICapable` | - |
| `CatalystAPICapable` | - | - |
| `CiscoIOSCapable` | `NetworkCLICapable` | - |
| `CommandExecCapable` | - | `ShellExecCapable` |
| `DnfCapable` | `PackageManagerCapable` | - |
| `DockerCapable` | - | - |
| `FactGathererCapable` | - | - |
| `FileTransferCapable` | - | - |
| `FirewalldCapable` | `SystemdCapable` | - |
| `JunosCapable` | `NetworkCLICapable` | - |
| `LinuxCapable` | - | - |
| `NetconfCapable` | - | - |
| `NetworkAddressableCapable` | - | - |
| `NetworkCLICapable` | - | `AristaEOSCapable`, `CiscoIOSCapable`, `JunosCapable` |
| `POSIXFileSystemCapable` | - | - |
| `PackageManagerCapable` | - | `AptCapable`, `DnfCapable` |
| `PosixAccountCapable` | - | - |
| `RFC2217Capable` | - | - |
| `RawPassthroughCapable` | - | - |
| `SSHTransportCapable` | - | - |
| `SerialCapable` | - | - |
| `ServiceManagerCapable` | - | `SystemdCapable`, `WindowsServiceCapable` |
| `ShellExecCapable` | `CommandExecCapable` | - |
| `SystemdCapable` | `ServiceManagerCapable` | `FirewalldCapable` |
| `TelnetCapable` | - | - |
| `WinRMCapable` | - | - |
| `WindowsCapable` | - | - |
| `WindowsFeatureCapable` | - | - |
| `WindowsServiceCapable` | `ServiceManagerCapable` | - |

31 capabilities registered.
