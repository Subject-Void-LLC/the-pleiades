---
status: beta
---

# Capability vocabulary

What a device *can do*, not what it *is*. A Collection method declares which capabilities it requires; a device advertises one by structurally implementing the matching Go interface.

**When these are checked.** A method's required capabilities are checked against its device before the method runs, on the CLI and on a Runner, so a mismatch stops the task rather than reaching the device. `pleiades validate` does not check them yet, except for the legacy action names `ssh_exec` and `ios_backup`. A method's transports (below) are checked at plan time: `pleiades validate` refuses a task whose device reaches none of them. See [Implementation status](../01-start-here.md#implementation-status).

| Capability | Parent | Children |
| --- | --- | --- |
| `AIProviderCapable` | - | - |
| `AWSAPICapable` | - | - |
| `AptCapable` | `PackageManagerCapable` | - |
| `AristaEOSCapable` | `NetworkCLICapable` | - |
| `BatfishAPICapable` | - | - |
| `CMLAPICapable` | - | - |
| `CatalystAPICapable` | - | - |
| `CiscoIOSCapable` | `NetworkCLICapable` | - |
| `CloudflareAPICapable` | - | - |
| `CommandExecCapable` | - | `ShellExecCapable`, `WindowsShellCapable` |
| `ConsoleServedCapable` | - | - |
| `ContainerEngineCapable` | - | `DockerCapable`, `PodmanCapable` |
| `DnfCapable` | `PackageManagerCapable` | - |
| `DockerCapable` | `ContainerEngineCapable` | - |
| `EngineLocalCapable` | - | - |
| `EngineSocketCapable` | - | - |
| `EngineStdioCapable` | - | - |
| `EngineTLSCapable` | - | - |
| `FRRCapable` | - | - |
| `FactGathererCapable` | - | - |
| `FileTransferCapable` | - | - |
| `FirewalldCapable` | `SystemdCapable` | - |
| `GRPCCapable` | - | - |
| `HTTPAPICapable` | - | - |
| `JunosCapable` | `NetworkCLICapable` | - |
| `KubernetesCapable` | - | - |
| `LinuxCapable` | - | - |
| `NetconfCapable` | - | - |
| `NetworkAddressableCapable` | - | - |
| `NetworkCLICapable` | - | `AristaEOSCapable`, `CiscoIOSCapable`, `JunosCapable` |
| `OpenVSwitchCapable` | - | - |
| `POSIXFileSystemCapable` | - | - |
| `PackageManagerCapable` | - | `AptCapable`, `DnfCapable` |
| `PodmanCapable` | `ContainerEngineCapable` | - |
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

49 capabilities registered.

## Transports

How a method's work reaches its device. A method lists the transports it uses; a device reaches a transport when it has any one of the capabilities beside it. `pleiades validate`, and the engine again before the method runs, refuse a task whose device reaches none of its method's transports. A method that calls an API rather than its device lists none.

| Transport | Reached by any of |
| --- | --- |
| `console` | `ConsoleServedCapable` |
| `engine_local` | `EngineLocalCapable` |
| `engine_socket` | `EngineSocketCapable` |
| `engine_stdio` | `EngineStdioCapable` |
| `engine_tls` | `EngineTLSCapable` |
| `https` | `HTTPAPICapable`, `CatalystAPICapable`, `AIProviderCapable`, `CloudflareAPICapable`, `BatfishAPICapable`, `CMLAPICapable` |
| `kubernetes` | `KubernetesCapable` |
| `netconf` | `NetconfCapable` |
| `ssh` | `SSHTransportCapable` |
| `telnet` | `TelnetCapable` |
| `winrm` | `WinRMCapable` |
