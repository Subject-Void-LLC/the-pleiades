---
status: beta
---

# Capability vocabulary

What a device *can do*, not what it *is*. A Collection method declares which capabilities it requires; a device advertises one by structurally implementing the matching Go interface.

**When these are checked.** A method's required capabilities are checked against its device before the method runs, on the CLI and on a Runner, so a mismatch stops the task rather than reaching the device. `pleiades validate` does not check them yet, except for the legacy action names `ssh_exec` and `ios_backup`. A method's transports (below) are checked at plan time: `pleiades validate` refuses a task whose device reaches none of them. See [Implementation status](../01-start-here.md#implementation-status).

| Capability | Parent | Children |
| --- | --- | --- |
| `AWSAPICapable` | - | - |
| `AptCapable` | `PackageManagerCapable` | - |
| `AristaEOSCapable` | `NetworkCLICapable` | - |
| `CatalystAPICapable` | - | - |
| `CiscoIOSCapable` | `NetworkCLICapable` | - |
| `CommandExecCapable` | - | `ShellExecCapable`, `WindowsShellCapable` |
| `DnfCapable` | `PackageManagerCapable` | - |
| `DockerCapable` | - | - |
| `FactGathererCapable` | - | - |
| `FileTransferCapable` | - | - |
| `FirewalldCapable` | `SystemdCapable` | - |
| `GRPCCapable` | - | - |
| `HTTPAPICapable` | - | - |
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
| `VirtualBoxCapable` | - | - |
| `WinRMCapable` | - | - |
| `WindowsCapable` | - | - |
| `WindowsFeatureCapable` | - | - |
| `WindowsServiceCapable` | `ServiceManagerCapable` | - |
| `WindowsShellCapable` | `CommandExecCapable` | - |

35 capabilities registered.

## Transports

How a method's work reaches its device. A method lists the transports it uses; a device reaches a transport when it has any one of the capabilities beside it. `pleiades validate`, and the engine again before the method runs, refuse a task whose device reaches none of its method's transports. A method that calls an API rather than its device lists none.

| Transport | Reached by any of |
| --- | --- |
| `docker` | `DockerCapable` |
| `https` | `HTTPAPICapable`, `CatalystAPICapable` |
| `netconf` | `NetconfCapable` |
| `ssh` | `SSHTransportCapable` |
| `winrm` | `WinRMCapable` |
