---
status: beta
---

# Device types

Every registered inventory device type, its vendor package, and the capabilities every hydrated instance carries as its baseline.

| Type | Vendor | Capabilities | Origin |
| --- | --- | --- | --- |
| `cisco_router` | `cisco` | `SSHTransportCapable`, `CiscoIOSCapable`, `NetworkAddressableCapable` | hand-written, predates the Forge |
| `linux_server` | `linux` | `SSHTransportCapable`, `LinuxCapable`, `ShellExecCapable`, `POSIXFileSystemCapable`, `FactGathererCapable`, `SystemdCapable`, `NetworkAddressableCapable` | hand-written, predates the Forge |
| `windows_server` | `windows` | `WindowsCapable`, `WinRMCapable`, `WindowsServiceCapable`, `WindowsFeatureCapable`, `NetworkAddressableCapable` | generated |
| `aws_account` | `aws` | `AWSAPICapable` | generated |
| `catalyst_center` | `catalyst` | `CatalystAPICapable` | generated |
| `cisco_switch` | `cisco` | `SSHTransportCapable`, `CiscoIOSCapable`, `NetworkCLICapable`, `NetworkAddressableCapable` | generated |
| `container_host` | `container` | `SSHTransportCapable`, `DockerCapable`, `NetworkAddressableCapable` | generated |

7 device types registered.
