---
status: beta
---

# Device types

Every registered inventory device type, its vendor package, and the capabilities that type can carry.

| Type | Vendor | Capabilities | Origin |
| --- | --- | --- | --- |
| `cisco_router` | `cisco` | `SSHTransportCapable`, `CiscoIOSCapable`, `NetworkAddressableCapable`, `NetconfCapable` \* | hand-written, predates the Forge |
| `linux_server` | `linux` | `SSHTransportCapable`, `LinuxCapable`, `ShellExecCapable`, `POSIXFileSystemCapable`, `FactGathererCapable`, `SystemdCapable`, `NetworkAddressableCapable`, `FileTransferCapable`, `FirewalldCapable` \* | hand-written, predates the Forge |
| `generic_grpc` | `generic` | `NetworkAddressableCapable`; discovered: `GRPCCapable` | generic, discovered at onboarding |
| `generic_http` | `generic` | `NetworkAddressableCapable`; discovered: `HTTPAPICapable` | generic, discovered at onboarding |
| `generic_netconf` | `generic` | `NetworkAddressableCapable`, `SSHTransportCapable`; discovered: `NetconfCapable` | generic, discovered at onboarding |
| `generic_ssh` | `generic` | `CommandExecCapable`, `NetworkAddressableCapable`, `SSHTransportCapable`; discovered: `ShellExecCapable`, `LinuxCapable`, `POSIXFileSystemCapable`, `FactGathererCapable`, `SystemdCapable`, `FirewalldCapable`, `AptCapable`, `DnfCapable`, `PosixAccountCapable` | generic, discovered at onboarding |
| `windows_server` | `windows` | `WindowsCapable`, `WinRMCapable`, `WindowsServiceCapable`, `WindowsFeatureCapable`, `NetworkAddressableCapable` | generated |
| `aws_account` | `aws` | `AWSAPICapable` | generated |
| `catalyst_center` | `catalyst` | `CatalystAPICapable` | generated |
| `cisco_switch` | `cisco` | `SSHTransportCapable`, `CiscoIOSCapable`, `NetworkCLICapable`, `NetworkAddressableCapable` | generated |
| `container_host` | `container` | `SSHTransportCapable`, `DockerCapable`, `NetworkAddressableCapable` | generated |
| `console_device` | `console` | `SerialCapable`, `RawPassthroughCapable`, `RFC2217Capable`, `TelnetCapable` \* | generated |

12 device types registered.

A capability list marked with an asterisk is what that type CAN carry, not what every instance declares: the type decides per device, from that device's own properties. `console_device` is the case this exists for, because a local serial line, a console server port and a bare Telnet session are alternative ways to reach one device rather than three facts about it, so a device configured for one must not claim the others. `cisco_router` declares `NetconfCapable` only when `netconf_enabled` is true, and `linux_server` declares `FileTransferCapable` only when `file_transfer_root` names a directory and `FirewalldCapable` only when `firewalld` is true. A `linux_server` gains `AptCapable` (or `DnfCapable`) and `PosixAccountCapable` from its classification: add it with `--classify linux_server,debian_family` (or `rhel_family`) and the package and account methods can reach it. See each type's package documentation for which property enables which capability.

A `generic` type's capabilities after "discovered:" are granted only by `pleiades onboard`, which probes the device over its protocol and records what the device's own answers prove; no inventory value, classification or sync plugin can grant one. Such a device starts `discovered`, which runs nothing, and becomes `active` when onboarding succeeds.
