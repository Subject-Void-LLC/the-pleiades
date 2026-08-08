---
status: beta
---

# Device types

Every registered inventory device type, its vendor package, and the capabilities every hydrated instance carries as its baseline.

| Type | Vendor | Capabilities | Origin |
| --- | --- | --- | --- |
| `cisco_router` | `cisco` | `SSHTransportCapable`, `CiscoIOSCapable` | hand-written, predates the Forge |
| `linux_server` | `linux` | `SSHTransportCapable`, `LinuxCapable` | hand-written, predates the Forge |
| `windows_server` | `windows` | `WindowsCapable`, `WinRMCapable`, `WindowsServiceCapable`, `WindowsFeatureCapable` | generated |
| `aws_account` | `aws` | `AWSAPICapable` | generated |
| `catalyst_center` | `catalyst` | `CatalystAPICapable` | generated |
| `cisco_switch` | `cisco` | `SSHTransportCapable`, `CiscoIOSCapable`, `NetworkCLICapable` | generated |

6 device types registered.
